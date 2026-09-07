package app

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// InconsistenciesSchemaVersion is the major version of the contradictions
// report. Additive optional fields stay compatible within v1; removing or
// re-typing a field needs v2 and a consumer dual-read window.
const InconsistenciesSchemaVersion = 1

// DanglingAnnotation is user/agent knowledge whose target no longer matches an
// indexed symbol — the compiled structure moved (rename/removal) and the note
// did not follow.
type DanglingAnnotation struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

// InconsistenciesReport surfaces where codemap's compiled knowledge disagrees
// with itself or with the world, so a consumer can gate trust or trigger repair
// (reindex, retarget, unannotate). An empty report means no KNOWN
// contradictions — it is evidence of internal coherence, not of correctness.
type InconsistenciesReport struct {
	SchemaVersion int    `json:"schema_version"`
	Project       string `json:"project"`
	// Dangling lists annotations whose target resolves to no indexed symbol.
	// Repair: `codemap annotate --retarget <id> <new-target>` or `codemap
	// annotations --rm <id>`.
	Dangling []DanglingAnnotation `json:"dangling_annotations"`
	// NameEdges lists precise-resolved files that still emit name-based call
	// edges — the coverage row and the edge set contradict each other.
	// Repair: re-run the precise pass (`codemap index --precise`).
	NameEdges []graph.NameEdgeFile `json:"name_call_edges_on_resolved_files"`
	// OrphanedCoverage lists call-graph coverage recorded for files that no
	// longer have indexed nodes. Repair: reindex.
	OrphanedCoverage []graph.CoverageWithoutNode `json:"coverage_without_nodes"`
	// Stale reports working-tree drift, since drifted files make every other
	// claim here provisional.
	Stale bool   `json:"stale"`
	Note  string `json:"note,omitempty"`
}

// Inconsistencies aggregates the three known contradiction classes across the
// compiled store. CLI-only by contract (like structural-manifest): peers consume
// it through versioned CLI JSON, never through codemap's database or packages.
func (svc *Service) Inconsistencies(cwd string) (*InconsistenciesReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, coded(CodeMissing, "run: codemap index",
			fmt.Errorf("project %s is not indexed", name))
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	rep := &InconsistenciesReport{
		SchemaVersion:    InconsistenciesSchemaVersion,
		Project:          name,
		Dangling:         []DanglingAnnotation{},
		NameEdges:        []graph.NameEdgeFile{},
		OrphanedCoverage: []graph.CoverageWithoutNode{},
	}

	// 1. Knowledge that lost its target.
	anns, err := g.AllAnnotations(pid)
	if err != nil {
		return nil, err
	}
	for _, a := range anns {
		if annotationResolves(g, pid, a) {
			continue
		}
		rep.Dangling = append(rep.Dangling, DanglingAnnotation{
			ID: a.ID, Kind: a.Kind, Target: a.Target, Source: a.Source, Note: a.Note,
		})
	}

	// 2. Confidence that contradicts the edge set.
	if rep.NameEdges, err = g.NameCallEdgesOnResolvedFiles(pid); err != nil {
		return nil, err
	}

	// 3. Confidence recorded for knowledge that no longer exists.
	if rep.OrphanedCoverage, err = g.CoverageWithoutNodes(pid); err != nil {
		return nil, err
	}
	// The store queries return nil when empty; the schema promises arrays.
	if rep.NameEdges == nil {
		rep.NameEdges = []graph.NameEdgeFile{}
	}
	if rep.OrphanedCoverage == nil {
		rep.OrphanedCoverage = []graph.CoverageWithoutNode{}
	}

	// Drift context: a stale working tree makes every statement here
	// provisional, so the report says so instead of implying finality.
	if st, _ := svc.Staleness(cwd); st != nil && st.Any() {
		rep.Stale = true
		rep.Note = "index has drifted from the working tree — repair inconsistencies after re-running 'codemap index'"
	}
	return rep, nil
}
