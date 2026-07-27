package app

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const MaxAnnotationExternalIDBytes = 512

// AnnotationsReport is returned by the annotation read methods.
type AnnotationsReport struct {
	Project     string             `json:"project"`
	Annotations []graph.Annotation `json:"annotations"`
	// Dangling lists annotation ids whose target no longer matches an indexed
	// symbol (e.g. it was renamed or removed since). They persist but won't surface
	// in queries — so they can be pruned or re-targeted (codemap_unannotate).
	Dangling []int64 `json:"dangling,omitempty"`
}

// pathTarget is the canonical key for a path annotation.
func pathTarget(from, to string) string { return from + " -> " + to }

// annotateProject resolves cwd to a project, auto-registering it (so you can
// annotate before indexing), and returns its id + the graph store.
func (svc *Service) annotateProject(cwd string) (int64, *graph.Store, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return 0, nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return 0, nil, err
	}
	pid, err := g.UpsertProject(name, root, detectLanguage(root))
	if err != nil {
		return 0, nil, err
	}
	return pid, g, nil
}

// AnnotateNode pins a note/data to a symbol. matched reports whether the target
// currently matches an indexed symbol (by name or FQN); annotations are kept even
// when it doesn't (they're reindex-durable and may predate the code), but callers
// warn so a typo isn't silently saved to a name that can never surface.
func (svc *Service) AnnotateNode(cwd, symbol, source, note, data string) (id int64, matched bool, err error) {
	id, _, matched, err = svc.AnnotateNodeIdempotent(cwd, symbol, source, note, data, "")
	return id, matched, err
}

// AnnotateNodeIdempotent pins or upserts a symbol annotation. externalID is
// caller-owned and unique within (project, source); empty keeps append-only
// behavior for humans and existing integrations.
func (svc *Service) AnnotateNodeIdempotent(cwd, symbol, source, note, data, externalID string) (id int64, action string, matched bool, err error) {
	externalID, err = validateAnnotationExternalID(externalID)
	if err != nil {
		return 0, "", false, err
	}
	pid, g, err := svc.annotateProject(cwd)
	if err != nil {
		return 0, "", false, err
	}
	id, action, err = g.UpsertAnnotation(pid, graph.Annotation{
		Kind: graph.AnnotationNode, Target: symbol, Source: source, ExternalID: externalID, Note: note, Data: data,
	})
	if err != nil {
		return 0, "", false, err
	}
	matched, _ = g.NodeExistsByName(pid, symbol)
	return id, action, matched, nil
}

// AnnotatePath pins a note / external data to a call path from→to. Returns the
// new id and the canonical path target.
// AnnotatePath pins a note/data to a from→to call path. matched reports whether
// BOTH endpoints currently match an indexed symbol (the annotation surfaces on the
// path only if they do); like AnnotateNode it's saved either way so callers warn
// rather than block.
func (svc *Service) AnnotatePath(cwd, from, to, source, note, data string) (id int64, target string, matched bool, err error) {
	id, target, _, matched, err = svc.AnnotatePathIdempotent(cwd, from, to, source, note, data, "")
	return id, target, matched, err
}

// AnnotatePathIdempotent is the path counterpart of AnnotateNodeIdempotent.
func (svc *Service) AnnotatePathIdempotent(cwd, from, to, source, note, data, externalID string) (id int64, target, action string, matched bool, err error) {
	externalID, err = validateAnnotationExternalID(externalID)
	if err != nil {
		return 0, "", "", false, err
	}
	pid, g, err := svc.annotateProject(cwd)
	if err != nil {
		return 0, "", "", false, err
	}
	target = pathTarget(from, to)
	id, action, err = g.UpsertAnnotation(pid, graph.Annotation{
		Kind: graph.AnnotationPath, Target: target, Source: source, ExternalID: externalID, Note: note, Data: data,
	})
	if err != nil {
		return 0, target, "", false, err
	}
	fromOK, _ := g.NodeExistsByName(pid, from)
	toOK, _ := g.NodeExistsByName(pid, to)
	return id, target, action, fromOK && toOK, nil
}

func validateAnnotationExternalID(externalID string) (string, error) {
	externalID = strings.TrimSpace(externalID)
	if len(externalID) > MaxAnnotationExternalIDBytes {
		return "", fmt.Errorf("annotation external id is %d bytes; maximum is %d", len(externalID), MaxAnnotationExternalIDBytes)
	}
	return externalID, nil
}

// AllAnnotations lists every annotation in the project.
func (svc *Service) AllAnnotations(cwd string) (*AnnotationsReport, error) {
	return svc.annotations(cwd, "", "")
}

// NodeAnnotations lists annotations attached to a symbol.
func (svc *Service) NodeAnnotations(cwd, symbol string) (*AnnotationsReport, error) {
	if !validSymbol(symbol) {
		return nil, fmt.Errorf("symbol name is required (a blank symbol matches every file node)")
	}
	return svc.annotations(cwd, graph.AnnotationNode, symbol)
}

// PathAnnotations lists annotations attached to a call path from→to.
func (svc *Service) PathAnnotations(cwd, from, to string) (*AnnotationsReport, error) {
	return svc.annotations(cwd, graph.AnnotationPath, pathTarget(from, to))
}

// annotations lists annotations: all for the project (kind==""), or those on a
// specific node/path target.
func (svc *Service) annotations(cwd, kind, target string) (*AnnotationsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &AnnotationsReport{Project: name, Annotations: []graph.Annotation{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	var anns []graph.Annotation
	if kind == "" {
		anns, err = g.AllAnnotations(pid)
	} else {
		anns, err = g.AnnotationsByTarget(pid, kind, target)
	}
	if err != nil {
		return nil, err
	}
	if anns != nil {
		rep.Annotations = anns
	}
	// Flag annotations whose target no longer resolves to an indexed symbol, so a
	// refactor's stale notes can be pruned/re-targeted rather than silently kept.
	for _, a := range rep.Annotations {
		if !annotationResolves(g, pid, a) {
			rep.Dangling = append(rep.Dangling, a.ID)
		}
	}
	return rep, nil
}

// annotationResolves reports whether an annotation's target currently matches an
// indexed symbol: the symbol itself for a node annotation, BOTH endpoints for a
// path annotation. Unknown formats are treated as resolved (never falsely flagged).
func annotationResolves(g *graph.Store, pid int64, a graph.Annotation) bool {
	switch a.Kind {
	case graph.AnnotationNode:
		ok, _ := g.NodeExistsByName(pid, a.Target)
		return ok
	case graph.AnnotationPath:
		from, to, found := strings.Cut(a.Target, " -> ")
		if !found {
			return true
		}
		f, _ := g.NodeExistsByName(pid, from)
		t, _ := g.NodeExistsByName(pid, to)
		return f && t
	}
	return true
}

// RemoveAnnotation deletes one annotation by id; reports whether it existed.
func (svc *Service) RemoveAnnotation(cwd string, id int64) (bool, error) {
	pid, _, found, err := svc.project(cwd)
	if err != nil || !found {
		return false, err
	}
	g, _ := svc.s.Graph()
	return g.DeleteAnnotation(pid, id)
}
