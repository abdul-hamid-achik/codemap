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
	Project  string       `json:"project"`
	Hotspots []HotspotRef `json:"hotspots"`
}

// OrphansReport is returned by Orphans. Resolution carries the same
// call-graph-unavailable signal as ImpactReport/ContextReport (B71), so
// an agent never sees "every function is dead" on a TS/JS/Python project
// without --precise (where the lack of call edges is a *lack of data*,
// not a positive signal that the code is unreachable).
type OrphansReport struct {
	Project    string      `json:"project"`
	Orphans    []SymbolRef `json:"orphans"`
	Resolution string      `json:"resolution,omitempty"` // P1-08: "none" when no call graph is available; "name" otherwise
	Note       string      `json:"note,omitempty"`       // P1-08: shown when the orphan list is unreliable (no call graph yet)
}

// PathReport is returned by Path. Resolution is the same call-graph-
// unavailable signal as ImpactReport (B71): when the project has no
// call graph (TS/JS/Python without --precise), Path assertions like
// "no call path" are not "these two are not connected" — they are
// "we don't know whether they're connected" and must be flagged as
// such so the agent doesn't act on a confidently-empty answer.
type PathReport struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	Project     string             `json:"project"`
	Found       bool               `json:"found"`
	Resolution  string             `json:"resolution,omitempty"` // P1-08: "none" when no call graph is available
	Note        string             `json:"note,omitempty"`       // set when an endpoint isn't a symbol in the project
	Path        []SymbolRef        `json:"path"`
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes pinned to this from→to path
	Stale       bool               `json:"stale,omitempty"`       // P1-08 (O92): index drifted from disk; reindex before trusting the path
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
	Symbol      string             `json:"symbol"`
	Project     string             `json:"project"`
	Matches     []SourceMatch      `json:"matches"`
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to this symbol
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
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			continue
		}
		src, _ := readLineRange(filepath.Join(p.Path, n.FilePath), n.StartLine, n.EndLine)
		rep.Matches = append(rep.Matches, SourceMatch{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
			StartLine: n.StartLine, EndLine: n.EndLine,
			Signature: n.Signature, Doc: n.Docstring, Source: src,
		})
	}
	rep.Annotations = symbolAnnotations(g, pid, name)
	return rep, nil
}

// SymbolAtReport resolves a file:line position to its enclosing symbol (C2).
// Resolution is "exact" (line is the definition line), "enclosing" (line falls
// inside the symbol's body), or "none" (no symbol there).
type SymbolAtReport struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Symbol     string `json:"symbol,omitempty"`
	FQN        string `json:"fqn,omitempty"`
	Kind       string `json:"kind,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Resolution string `json:"resolution"`
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
	rep := &SymbolAtReport{File: file, Line: line, Resolution: "none"}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	n, ok, err := g.NodeAtLine(p.ID, file, line)
	if err != nil {
		return nil, err
	}
	if !ok {
		return rep, nil
	}
	rep.Symbol, rep.FQN, rep.Kind = n.Symbol, n.FQN, n.Kind
	rep.StartLine, rep.EndLine = n.StartLine, n.EndLine
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
	rep := &HotspotsReport{Project: name, Hotspots: []HotspotRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
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
	rep := &OrphansReport{Project: name, Orphans: []SymbolRef{}}
	if !found {
		return rep, nil
	}
	// P1-08 (B71): tag with the call-graph-unavailable signal so an agent
	// never sees a confident "everything is dead" on a project that
	// has no call graph (TS/JS/Python without --precise).
	g, _ := svc.s.Graph()
	if g != nil {
		rep.Resolution, _ = svc.callGraphUnavailable(g, pid, nil)
		if rep.Resolution != "" {
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
	// P1-08 (B71): the Path guard runs early so the Resolution/Note are
	// always populated, even when one endpoint is missing or no path
	// exists. (The full path-finding logic lives below.)
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &PathReport{From: from, To: to, Project: name, Path: []SymbolRef{}}
	if g, _ := svc.s.Graph(); g != nil {
		rep.Resolution, _ = svc.callGraphUnavailable(g, pid, nil)
		if st, _ := svc.Staleness(cwd); st != nil && st.Any() {
			rep.Stale = true
		}
	}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()

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
	for _, n := range nodes {
		rep.Path = append(rep.Path, nodeToRef(n))
	}
	rep.Found = len(nodes) > 0
	// Surface notes pinned to this from→to path (annotate <from> <to>), so a path
	// annotation shows up where it's relevant — not only in `annotations`.
	if anns, _ := g.AnnotationsByTarget(pid, graph.AnnotationPath, pathTarget(from, to)); len(anns) > 0 {
		rep.Annotations = anns
	}
	return rep, nil
}
