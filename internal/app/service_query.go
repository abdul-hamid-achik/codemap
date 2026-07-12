package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// HotspotRef is a hub node with its incoming-usage count.
type HotspotRef struct {
	Symbol     string `json:"symbol"`
	FQN        string `json:"fqn,omitempty"`
	Kind       string `json:"kind"`
	File       string `json:"file"`
	StartLine  int    `json:"start_line"`
	InDegree   int    `json:"in_degree"`
	SharedName int    `json:"shared_name,omitempty"` // defs sharing this name (>1 ⇒ in-degree inflated)
}

// HotspotsReport is returned by Hotspots.
type HotspotsReport struct {
	Project    string       `json:"project"`
	Hotspots   []HotspotRef `json:"hotspots"`
	CallGraph  string       `json:"call_graph"`           // resolved|name|unresolved|none across project callable definitions
	Resolution string       `json:"resolution,omitempty"` // human explanation when some callable graph is unavailable
	Note       string       `json:"note,omitempty"`
}

// OrphansReport is returned by Orphans. Resolution carries the same
// call-graph-unavailable signal as ImpactReport/ContextReport (B71), so
// an agent never sees "every function is dead" on a TS/JS/Python project
// without --precise (where the lack of call edges is a *lack of data*,
// not a positive signal that the code is unreachable).
type OrphansReport struct {
	Project    string      `json:"project"`
	Orphans    []SymbolRef `json:"orphans"`
	CallGraph  string      `json:"call_graph"`           // resolved|name|unresolved|none across project callable definitions
	Resolution string      `json:"resolution,omitempty"` // human explanation when the orphan evidence is incomplete
	Note       string      `json:"note,omitempty"`       // shown when the orphan list is unreliable (no call graph yet)
}

// PathReport is returned by Path. Resolution is the same call-graph-
// unavailable signal as ImpactReport (B71): when the project has no
// call graph (TS/JS/Python without --precise), Path assertions like
// "no call path" are not "these two are not connected" — they are
// "we don't know whether they're connected" and must be flagged as
// such so the agent doesn't act on a confidently-empty answer.
type PathReport struct {
	From         string             `json:"from"`
	To           string             `json:"to"`
	FromSelector *SymbolSelector    `json:"from_selector,omitempty"`
	ToSelector   *SymbolSelector    `json:"to_selector,omitempty"`
	Project      string             `json:"project"`
	Found        bool               `json:"found"`
	CallGraph    string             `json:"call_graph"`           // found: across every path node; not found: across all project callables
	Resolution   string             `json:"resolution,omitempty"` // human explanation when path evidence is incomplete
	Note         string             `json:"note,omitempty"`       // set when an endpoint isn't a symbol in the project
	Path         []SymbolRef        `json:"path"`
	Annotations  []graph.Annotation `json:"annotations,omitempty"` // notes pinned to this from→to path
	Stale        bool               `json:"stale,omitempty"`       // P1-08 (O92): index drifted from disk; reindex before trusting the path
}

// SymbolsReport is returned by Symbols.
type SymbolsReport struct {
	Project string      `json:"project"`
	File    string      `json:"file"`
	Symbols []SymbolRef `json:"symbols"`
}

// Symbols lists the symbols defined in a file (functions, types, methods, tests)
// straight from the index — no file read needed. file may be relative to cwd or
// absolute; it is resolved to a project-relative path.
func (svc *Service) Symbols(cwd, file string) (*SymbolsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SymbolsReport{Project: name, File: file, Symbols: []SymbolRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}
	rel := projectRel(p.Path, cwd, file)
	rep.File = rel
	nodes, err := g.NodesInFile(pid, rel)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			continue
		}
		rep.Symbols = append(rep.Symbols, nodeToRef(n))
	}
	return rep, nil
}

func projectRel(root, cwd, file string) string {
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, file)
	}
	if rel, err := filepath.Rel(root, abs); err == nil {
		return rel
	}
	return file
}

// SourceMatch is a symbol's definition with its source text read back from disk.
type SourceMatch struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Source    string `json:"source"`
}

// SourceReport is returned by Source.
type SourceReport struct {
	Symbol      string               `json:"symbol"`
	Selector    *SymbolSelector      `json:"selector,omitempty"` // exact selected definition; absent on a name-union query
	Project     string               `json:"project"`
	Matches     []SourceMatch        `json:"matches"`
	Candidates  []AmbiguityCandidate `json:"candidates,omitempty"`  // the merged definition set behind Matches (redundant with Matches by design — kept for uniformity with impact/context/callers/callees/risk)
	Annotations []graph.Annotation   `json:"annotations,omitempty"` // notes/data pinned to this symbol
}

// Source returns the source code of every symbol matching name, read from the
// indexed file at its recorded line range — the implementation behind the
// signature/docstring, without the caller having to open the file. The graph
// only stores line ranges (not source), so this reads from disk; reindex if a
// file changed since indexing.
func (svc *Service) Source(cwd, name string) (*SourceReport, error) {
	pid, projName, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SourceReport{Symbol: name, Project: projName, Matches: []SourceMatch{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	p, err := g.GetProjectByName(projName)
	if err != nil {
		return nil, err
	}
	name = canonicalSymbol(g, pid, name) // accept the qualified form hotspots/orphans print
	rep.Symbol = name
	nodes, err := g.FindNodesBySymbol(pid, name)
	if err != nil {
		return nil, err
	}
	var kept []graph.Node
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			continue
		}
		src, _ := readLineRange(filepath.Join(p.Path, n.FilePath), n.StartLine, n.EndLine)
		rep.Matches = append(rep.Matches, sourceMatchForNode(n, src))
		kept = append(kept, n)
	}
	rep.Candidates = candidatesFromNodes(kept)
	rep.Annotations = symbolAnnotations(g, pid, name)
	return rep, nil
}

// SymbolAtReport resolves a file:line position to its enclosing symbol (C2).
// Resolution is "exact" (line is the definition line), "enclosing" (line falls
// inside the symbol's body), or "none" (no symbol there).
type SymbolAtReport struct {
	File       string          `json:"file"`
	Line       int             `json:"line"`
	Symbol     string          `json:"symbol,omitempty"`
	FQN        string          `json:"fqn,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	StartLine  int             `json:"start_line,omitempty"`
	EndLine    int             `json:"end_line,omitempty"`
	Selector   *SymbolSelector `json:"selector,omitempty"`
	Resolution string          `json:"resolution"`
	// Indexed is false when the project has not been indexed yet, so
	// Resolution="none" is a "run codemap index" signal, not a real miss.
	Indexed bool `json:"indexed"`
}

// SymbolAt resolves a file:line to the enclosing symbol node — the entry point
// that lets a sibling tool's file:line result join onto the graph. Never errors on
// a miss: an unresolved position returns Resolution="none".
func (svc *Service) SymbolAt(cwd, file string, line int) (*SymbolAtReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SymbolAtReport{File: file, Line: line, Resolution: "none", Indexed: true}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		rep.Indexed = false
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	paths, pathErr := selectorPaths(p.Path, cwd, file)
	if pathErr == nil && len(paths) > 0 {
		file = paths[0]
		rep.File = file
	}
	n, ok, err := g.NodeAtLine(p.ID, file, line)
	if !ok && err == nil && pathErr == nil && len(paths) > 1 {
		file = paths[1]
		rep.File = file
		n, ok, err = g.NodeAtLine(p.ID, file, line)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return rep, nil
	}
	rep.Symbol, rep.FQN, rep.Kind = n.Symbol, n.FQN, n.Kind
	rep.StartLine, rep.EndLine = n.StartLine, n.EndLine
	rep.Selector = selectorForNode(n)
	if n.StartLine == line {
		rep.Resolution = "exact"
	} else {
		rep.Resolution = "enclosing"
	}
	return rep, nil
}

// readLineRange returns lines [start, end] (1-based, inclusive) of a file.
func readLineRange(path string, start, end int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if start < 1 {
		start = 1
	}
	lines := strings.Split(string(data), "\n")
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", nil
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

// Hotspots returns the most-referenced nodes (hubs).
func (svc *Service) Hotspots(cwd string, limit int) (*HotspotsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &HotspotsReport{Project: name, Hotspots: []HotspotRef{}, CallGraph: CallGraphNone}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	projectNodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	callables := callableNodes(projectNodes)
	rep.CallGraph = svc.callGraphStatus(g, pid, callables)
	if lang, unavailable := svc.callGraphUnavailable(g, pid, callables); unavailable {
		rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — hotspot rankings are incomplete; run 'codemap index --precise'", lang)
		rep.Note = "hotspot ranking is unreliable while some callable files are unresolved"
	}
	hs, err := g.Hotspots(pid, limit)
	if err != nil {
		return nil, err
	}
	// Flag entries whose in-degree is inflated by name-based fan-out (e.g. six
	// Close() methods each credited with every Close call). The flag is
	// provenance-aware: it fires only when the name is shared (>1 def) AND the node
	// still has name-based in-edges — so on a `--precise` index, where those edges
	// were resolved exactly, an accurate count is no longer mislabeled "inflated".
	shared, _ := g.SymbolDefCounts(pid)
	ids := make([]int64, len(hs))
	for i, h := range hs {
		ids[i] = h.Node.ID
	}
	nameInflated, _ := g.HasNameInEdges(ids)
	for _, h := range hs {
		ref := HotspotRef{
			Symbol: h.Node.Symbol, FQN: h.Node.FQN, Kind: h.Node.Kind,
			File: h.Node.FilePath, StartLine: h.Node.StartLine, InDegree: h.InDegree,
		}
		if n := shared[h.Node.Symbol]; n > 1 && nameInflated[h.Node.ID] {
			ref.SharedName = n
		}
		rep.Hotspots = append(rep.Hotspots, ref)
	}
	return rep, nil
}

// Orphans returns function/method nodes with no callers (dead-code candidates).
func (svc *Service) Orphans(cwd string, limit int) (*OrphansReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &OrphansReport{Project: name, Orphans: []SymbolRef{}, CallGraph: CallGraphNone}
	if !found {
		return rep, nil
	}
	// P1-08 (B71): tag with the call-graph-unavailable signal so an agent
	// never sees a confident "everything is dead" on a project that
	// has no call graph (TS/JS/Python without --precise).
	g, _ := svc.s.Graph()
	if g != nil {
		projectNodes, _ := g.ProjectNodes(pid)
		callables := callableNodes(projectNodes)
		rep.CallGraph = svc.callGraphStatus(g, pid, callables)
		if lang, unavailable := svc.callGraphUnavailable(g, pid, callables); unavailable {
			rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — orphan candidates are incomplete", lang)
			rep.Note = "orphan list is unreliable — run 'codemap index --precise' to resolve the call graph, then re-check"
		}
	}
	nodes, err := g.Orphans(pid, limit)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Orphans = append(rep.Orphans, nodeToRef(n))
	}
	return rep, nil
}

// Path returns the shortest call path between two symbols.
func (svc *Service) Path(cwd, from, to string) (*PathReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &PathReport{From: from, To: to, Project: name, Path: []SymbolRef{}, CallGraph: CallGraphNone}
	if g, _ := svc.s.Graph(); g != nil {
		if st, _ := svc.Staleness(cwd); st != nil && st.Any() {
			rep.Stale = true
		}
	}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()

	// Any endpoint with exactly one candidate is an exact definition; an explicit
	// unique FQN is preserved before canonicalization. Keep both node identities
	// through traversal. If either side is ambiguous, the name-union path below
	// remains backward-compatible.
	fromExact, fromExactErr := pathEndpointNodes(g, pid, from)
	if fromExactErr != nil {
		return nil, fromExactErr
	}
	toExact, toExactErr := pathEndpointNodes(g, pid, to)
	if toExactErr != nil {
		return nil, toExactErr
	}
	if len(fromExact) == 1 && len(toExact) == 1 {
		rep.FromSelector, rep.ToSelector = selectorForNode(fromExact[0]), selectorForNode(toExact[0])
		nodes, pathErr := g.PathFromNodes(pid, fromExact[0].ID, toExact[0].ID, 0)
		if pathErr != nil {
			return nil, pathErr
		}
		return svc.finishPathReport(g, pid, rep, nodes, from, to)
	}

	// Accept the qualified form hotspots/orphans/find print for either endpoint.
	from = canonicalSymbol(g, pid, from)
	to = canonicalSymbol(g, pid, to)
	rep.From, rep.To = from, to

	// Distinguish "this endpoint isn't a symbol here" from "no path between two
	// real symbols" — otherwise a typo'd name reads as an unconnected pair.
	fromDefs, _ := g.FindNodesBySymbol(pid, from)
	toDefs, _ := g.FindNodesBySymbol(pid, to)
	switch {
	case len(fromDefs) == 0 && len(toDefs) == 0:
		rep.Note = fmt.Sprintf("neither %q nor %q is a symbol in %s", from, to, name)
		return rep, nil
	case len(fromDefs) == 0:
		rep.Note = fmt.Sprintf("%q is not a symbol in %s", from, name)
		return rep, nil
	case len(toDefs) == 0:
		rep.Note = fmt.Sprintf("%q is not a symbol in %s", to, name)
		return rep, nil
	}
	nodes, err := g.Path(pid, from, to, 0)
	if err != nil {
		return nil, err
	}
	return svc.finishPathReport(g, pid, rep, nodes, from, to)
}

// pathEndpointNodes preserves a unique exact FQN when supplied; otherwise it
// resolves through the existing qualified/bare-name rules. A single candidate
// is safe to traverse exactly, while multiple candidates deliberately fall
// back to Path's backward-compatible name union.
func pathEndpointNodes(g *graph.Store, pid int64, input string) ([]graph.Node, error) {
	byFQN, err := g.FindNodesByFQN(pid, input)
	if err != nil {
		return nil, err
	}
	if len(byFQN) == 1 && byFQN[0].FQN != byFQN[0].Symbol {
		return byFQN, nil
	}
	return g.FindNodesBySymbol(pid, canonicalSymbol(g, pid, input))
}

func (svc *Service) finishPathReport(g *graph.Store, pid int64, rep *PathReport, nodes []graph.Node, annotationFrom, annotationTo string) (*PathReport, error) {
	for _, n := range nodes {
		rep.Path = append(rep.Path, nodeToRef(n))
	}
	rep.Found = len(nodes) > 0

	// A found path is only as exact as every node it traverses, not merely its
	// endpoints. Conversely, a negative assertion must account for every callable
	// in the project: an uncovered intermediate could be the missing connection.
	confidenceNodes := nodes
	if !rep.Found {
		projectNodes, projectErr := g.ProjectNodes(pid)
		if projectErr != nil {
			return nil, projectErr
		}
		confidenceNodes = callableNodes(projectNodes)
	}
	rep.CallGraph = svc.callGraphStatus(g, pid, confidenceNodes)
	if lang, unavailable := svc.callGraphUnavailable(g, pid, confidenceNodes); unavailable {
		if rep.Found {
			rep.Resolution = fmt.Sprintf("a path was found, but the %s call graph is not available without precise indexing — path completeness is unresolved", lang)
		} else {
			rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — whether this path exists is unresolved", lang)
		}
	}
	// Surface notes pinned to this from→to path (annotate <from> <to>), so a path
	// annotation shows up where it's relevant — not only in `annotations`.
	if anns, _ := g.AnnotationsByTarget(pid, graph.AnnotationPath, pathTarget(annotationFrom, annotationTo)); len(anns) > 0 {
		rep.Annotations = anns
	}
	return rep, nil
}
