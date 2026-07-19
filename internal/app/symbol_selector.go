package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// SymbolSelector is a durable, project-scoped source identity for one symbol
// definition. It is safe to carry between codemap calls and across reindexes:
// file+FQN+kind is preferred when present, while start_line disambiguates and
// remains the fallback for languages that do not provide an FQN. It is not an
// immutable ID across arbitrary moves or renames, and intentionally does not
// expose the graph's volatile database node id.
type SymbolSelector struct {
	File      string `json:"file" jsonschema:"canonical project-relative file path"`
	StartLine int    `json:"start_line" jsonschema:"1-based declaration line; used as a disambiguator and fallback"`
	FQN       string `json:"fqn,omitempty" jsonschema:"fully-qualified symbol name; preferred identity within file when available"`
	Kind      string `json:"kind,omitempty" jsonschema:"optional symbol-kind guard (function, method, type, test, etc.)"`
}

func selectorForNode(n graph.Node) *SymbolSelector {
	return &SymbolSelector{File: n.FilePath, StartLine: n.StartLine, FQN: n.FQN, Kind: n.Kind}
}

// AmbiguityCandidate is one distinct definition merged into a name-union
// answer (impact/context/callers/callees/risk/source), so an agent can
// re-issue the query with candidates[i].selector instead of parsing free-form
// Note prose and hand-building a selector via find/symbols.
type AmbiguityCandidate struct {
	Selector  *SymbolSelector `json:"selector"`
	Signature string          `json:"signature,omitempty"`
	File      string          `json:"file"`
	StartLine int             `json:"start_line"`
}

// candidatesFromNodes builds the candidate list from a merged node set. Returns
// nil (→ omitted, not an empty array) when there is nothing ambiguous to report.
func candidatesFromNodes(nodes []graph.Node) []AmbiguityCandidate {
	if len(nodes) < 2 {
		return nil
	}
	out := make([]AmbiguityCandidate, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, AmbiguityCandidate{
			Selector: selectorForNode(n), Signature: n.Signature,
			File: n.FilePath, StartLine: n.StartLine,
		})
	}
	return out
}

type resolvedSelector struct {
	graph       *graph.Store
	project     *graph.Project
	projectName string
	node        graph.Node
	found       bool
}

// resolveSourceSelector resolves an external source selector to a current graph
// node. FQN+file survives line shifts; line is used to break duplicate-FQN ties.
// A selector that is still ambiguous fails rather than silently unioning nodes.
func (svc *Service) resolveSourceSelector(cwd string, selector SymbolSelector) (*resolvedSelector, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	res := &resolvedSelector{graph: g, projectName: name}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.project = p
	if strings.TrimSpace(selector.File) == "" {
		return nil, fmt.Errorf("selector file must not be empty")
	}
	if selector.StartLine <= 0 && strings.TrimSpace(selector.FQN) == "" {
		return nil, fmt.Errorf("selector needs a positive start_line or an fqn")
	}

	paths, err := selectorPaths(p.Path, cwd, selector.File)
	if err != nil {
		return nil, err
	}
	var matches []graph.Node
	for _, file := range paths {
		matches, err = g.NodesAtSource(p.ID, file, selector.StartLine, selector.FQN, selector.Kind)
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			break
		}
	}
	if len(matches) == 0 {
		return res, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("selector for %s matches %d definitions; add fqn and kind", selector.File, len(matches))
	}
	res.node, res.found = matches[0], true
	return res, nil
}

// selectorPaths accepts the canonical project-relative contract first, while
// retaining the CLI's historical relative-to-cwd convenience as a fallback.
func selectorPaths(root, cwd, file string) ([]string, error) {
	if filepath.IsAbs(file) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return nil, fmt.Errorf("resolve selector file: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("selector file is outside the project")
		}
		return []string{filepath.Clean(rel)}, nil
	}
	canonical := filepath.Clean(filepath.FromSlash(file))
	if canonical == ".." || strings.HasPrefix(canonical, ".."+string(filepath.Separator)) {
		// This may still be a legitimate cwd-relative path into the project; only
		// accept it after resolving against cwd and checking the project boundary.
		canonical = ""
	}
	fromCWD := projectRel(root, cwd, file)
	if fromCWD == ".." || strings.HasPrefix(fromCWD, ".."+string(filepath.Separator)) {
		fromCWD = ""
	}
	paths := make([]string, 0, 2)
	for _, candidate := range []string{canonical, fromCWD} {
		if candidate == "" {
			continue
		}
		seen := false
		for _, existing := range paths {
			if existing == candidate {
				seen = true
				break
			}
		}
		if !seen {
			paths = append(paths, candidate)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("selector file is outside the project")
	}
	return paths, nil
}

// SourceBySelector returns exactly one definition body when the selector still
// resolves. This closes the find/symbols -> source chain without reintroducing
// name-based merging. brief drops the Source body (keeping signature/doc/
// location) and sets SourceOmitted, skipping the disk read entirely.
func (svc *Service) SourceBySelector(cwd string, selector SymbolSelector, brief bool) (*SourceReport, error) {
	res, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	rep := &SourceReport{Project: res.projectName, Matches: []SourceMatch{}, Selector: &selector}
	if res.project != nil {
		rep.Project = res.project.Name
	}
	if !res.found {
		return rep, nil
	}
	n := res.node
	rep.Symbol = n.Symbol
	rep.Selector = selectorForNode(n)
	var src string
	if !brief {
		src, _ = readLineRange(filepath.Join(res.project.Path, n.FilePath), n.StartLine, n.EndLine)
	}
	rep.Matches = append(rep.Matches, sourceMatchForNode(n, src, brief))
	rep.Annotations = nodeAnnotationsFor(res.graph, res.project.ID, n.FQN, n.Symbol)
	return rep, nil
}

func sourceMatchForNode(n graph.Node, source string, brief bool) SourceMatch {
	m := SourceMatch{
		Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
		StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature,
		Doc: n.Docstring,
	}
	if brief {
		m.SourceOmitted = true
	} else {
		m.Source = source
	}
	return m
}

type nodeRelationQuery func(*graph.Store, int64, int64) ([]graph.Node, error)

func (svc *Service) relationBySelector(cwd string, selector SymbolSelector, query nodeRelationQuery) (*RelationReport, error) {
	res, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	rep := &RelationReport{Project: res.projectName, Results: []SymbolRef{}, CallGraph: CallGraphNone, Selector: &selector}
	if res.project != nil {
		rep.Project = res.project.Name
	}
	if !res.found {
		return rep, nil
	}
	n := res.node
	rep.Symbol, rep.Found, rep.Selector = n.Symbol, true, selectorForNode(n)
	nodes, err := query(res.graph, res.project.ID, n.ID)
	if err != nil {
		return nil, err
	}
	for _, result := range nodes {
		rep.Results = append(rep.Results, nodeToRef(result))
	}
	resolvedFiles, _ := res.graph.CallGraphResolvedFiles(res.project.ID)
	rep.CallGraph = callGraphEnum(resolvedFiles, []graph.Node{n})
	if lang, unavailable := callGraphUnavailableResolved(resolvedFiles, []graph.Node{n}); unavailable {
		rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — callers/callees are unresolved (not absent); run 'codemap index --precise'", lang) + svc.coverageHintResolved(res.graph, res.project.ID, resolvedFiles)
	}
	rep.Annotations = nodeAnnotationsFor(res.graph, res.project.ID, n.FQN, n.Symbol)
	return rep, nil
}

// CallersBySelector and CalleesBySelector query one definition instead of the
// union of all same-named definitions. Like their name-based counterparts they
// may resolve an otherwise-unavailable LSP graph on demand.
func (svc *Service) CallersBySelector(cwd string, selector SymbolSelector) (*RelationReport, error) {
	rep, err := svc.relationBySelector(cwd, selector, (*graph.Store).CallersOfNode)
	if err != nil {
		return nil, err
	}
	return svc.autoUpgradeSelectorRelation(rep, cwd, true), nil
}

func (svc *Service) CalleesBySelector(cwd string, selector SymbolSelector) (*RelationReport, error) {
	rep, err := svc.relationBySelector(cwd, selector, (*graph.Store).CalleesOfNode)
	if err != nil {
		return nil, err
	}
	return svc.autoUpgradeSelectorRelation(rep, cwd, false), nil
}

// autoUpgradeSelectorRelation is the selector-scoped twin of autoUpgradeRelation
// (see its doc comment for the P1-06 / B1 three-way resolution contract):
// preciseRelations' errPreciseUnresolved sentinel already makes a soft miss
// indistinguishable from a hard failure here, so this `err != nil` check keeps
// the honest note on both, and only a genuine nil-error result (which may still
// be legitimately empty) claims resolution below.
func (svc *Service) autoUpgradeSelectorRelation(base *RelationReport, cwd string, wantCallers bool) *RelationReport {
	if base == nil || base.Resolution == "" || base.Selector == nil || !base.Found {
		return base
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	callers, callees, _, err := svc.preciseRelations(ctx, cwd, base.Symbol, base.Selector.File, base.Selector.StartLine)
	if err != nil {
		return base // soft miss (errPreciseUnresolved) or hard failure — keep the honest "unresolved" note
	}
	results := callees
	if wantCallers {
		results = callers
	}
	base.Results = nonNil(results)
	base.Resolution = ""
	base.CallGraph = CallGraphResolved
	base.Note = "resolved on demand via the language server's callHierarchy (no --precise needed)"
	return base
}

func (svc *Service) preciseRelationBySelector(ctx context.Context, cwd string, selector SymbolSelector, wantCallers bool) (*RelationReport, error) {
	res, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	rep := &RelationReport{Project: res.projectName, Results: []SymbolRef{}, CallGraph: CallGraphNone, Selector: &selector}
	if res.project != nil {
		rep.Project = res.project.Name
	}
	if !res.found {
		return rep, nil
	}
	n := res.node
	rep.Symbol, rep.Found, rep.Selector = n.Symbol, true, selectorForNode(n)
	callers, callees, project, err := svc.preciseRelations(ctx, cwd, n.Symbol, n.FilePath, n.StartLine)
	if err != nil {
		fallback, fallbackErr := svc.relationBySelector(cwd, *rep.Selector, map[bool]nodeRelationQuery{
			true: (*graph.Store).CallersOfNode, false: (*graph.Store).CalleesOfNode,
		}[wantCallers])
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		fallback.Note = fmt.Sprintf("precise resolution unavailable (%v) — showing indexed-graph results for the selected definition", err)
		return fallback, nil
	}
	results := callees
	if wantCallers {
		results = callers
	}
	rep.Project, rep.Results, rep.CallGraph = project, nonNil(results), CallGraphResolved
	rep.Annotations = nodeAnnotationsFor(res.graph, res.project.ID, n.FQN, n.Symbol)
	return rep, nil
}

func (svc *Service) PreciseCallersBySelector(ctx context.Context, cwd string, selector SymbolSelector) (*RelationReport, error) {
	return svc.preciseRelationBySelector(ctx, cwd, selector, true)
}

func (svc *Service) PreciseCalleesBySelector(ctx context.Context, cwd string, selector SymbolSelector) (*RelationReport, error) {
	return svc.preciseRelationBySelector(ctx, cwd, selector, false)
}

// PathBySelectors finds a path between two exact definitions. It is primarily
// used by Studio and agent drill-downs that already hold SymbolRef projections.
func (svc *Service) PathBySelectors(cwd string, from, to SymbolSelector) (*PathReport, error) {
	fromRes, err := svc.resolveSourceSelector(cwd, from)
	if err != nil {
		return nil, err
	}
	toRes, err := svc.resolveSourceSelector(cwd, to)
	if err != nil {
		return nil, err
	}
	project := fromRes.projectName
	rep := &PathReport{
		Project: project, Path: []SymbolRef{}, CallGraph: CallGraphNone,
		FromSelector: &from, ToSelector: &to,
	}
	if st, _ := svc.Staleness(cwd); st != nil && st.Any() {
		rep.Stale = true
	}
	switch {
	case !fromRes.found && !toRes.found:
		rep.Note = "neither source selector resolves in the current index"
		return rep, nil
	case !fromRes.found:
		rep.Note = "the from_selector does not resolve in the current index"
		return rep, nil
	case !toRes.found:
		rep.Note = "the to_selector does not resolve in the current index"
		return rep, nil
	}
	if fromRes.project.ID != toRes.project.ID {
		return nil, fmt.Errorf("path selectors must belong to the same project")
	}
	rep.From, rep.To = fromRes.node.Symbol, toRes.node.Symbol
	rep.FromSelector, rep.ToSelector = selectorForNode(fromRes.node), selectorForNode(toRes.node)
	nodes, err := fromRes.graph.PathFromNodes(fromRes.project.ID, fromRes.node.ID, toRes.node.ID, 0)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Path = append(rep.Path, nodeToRef(n))
	}
	rep.Found = len(nodes) > 0
	confidenceNodes := nodes
	if !rep.Found {
		projectNodes, projectErr := fromRes.graph.ProjectNodes(fromRes.project.ID)
		if projectErr != nil {
			return nil, projectErr
		}
		confidenceNodes = callableNodes(projectNodes)
	}
	resolvedFiles, _ := fromRes.graph.CallGraphResolvedFiles(fromRes.project.ID)
	rep.CallGraph = callGraphEnum(resolvedFiles, confidenceNodes)
	if lang, unavailable := callGraphUnavailableResolved(resolvedFiles, confidenceNodes); unavailable {
		if rep.Found {
			rep.Resolution = fmt.Sprintf("a path was found, but the %s call graph is not available without precise indexing — path completeness is unresolved", lang)
		} else {
			rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — whether this path exists is unresolved", lang)
		}
		rep.Resolution += svc.coverageHintResolved(fromRes.graph, fromRes.project.ID, resolvedFiles)
	}
	if anns, annErr := fromRes.graph.AnnotationsByTarget(fromRes.project.ID, graph.AnnotationPath, pathTarget(rep.From, rep.To)); annErr == nil {
		rep.Annotations = anns
	}
	return rep, nil
}
