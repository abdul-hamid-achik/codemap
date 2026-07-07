package app

import (
	"context"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// ContextReport is the one-call bundle for a symbol: its definition(s) with
// source, who calls it, what it calls, the tests that cover it, the blast-radius
// size, and any pinned annotations. It exists so a person or agent gets a
// complete picture in a single query instead of stitching together source +
// callers + callees + impact (four round-trips for a harness).
type ContextReport struct {
	Symbol      string        `json:"symbol"`
	Project     string        `json:"project"`
	Found       bool          `json:"found"`
	Definitions []SourceMatch `json:"definitions"` // signature, doc, file:line, and source body per matching def
	Callers     []SymbolRef   `json:"callers"`     // who calls it (capped — see callers_total)
	Callees     []SymbolRef   `json:"callees"`     // what it calls (capped — see callees_total)
	Tests       []ImpactNode  `json:"tests"`       // tests covering it (capped — see tests_total)
	// *Total are the true counts before capping, so an agent knows when a list was
	// truncated and can call codemap_callers/codemap_callees/codemap_impact for the
	// complete set. The bundle stays bounded so one orientation call can't blow an
	// agent's context window.
	CallersTotal int                `json:"callers_total"`
	CalleesTotal int                `json:"callees_total"`
	TestsTotal   int                `json:"tests_total"`
	BlastRadius  int                `json:"blast_radius"`          // count of transitively-affected nodes
	BlastDepth   int                `json:"blast_depth"`           // depth the blast radius was traversed to (it's bounded, not the full closure)
	Note         string             `json:"note,omitempty"`        // set when the name is ambiguous (merges same-named defs)
	Resolution   string             `json:"resolution,omitempty"`  // human sentence set when the call graph is unresolved (TS/JS/Python without --precise) — callers/callees/tests/blast are unavailable, not absent
	CallGraph    string             `json:"call_graph"`            // stable machine enum: resolved|name|unresolved|none (carried from the bundled Impact)
	Annotations  []graph.Annotation `json:"annotations,omitempty"` // pinned notes/data on the symbol
	// Memories are TRANSIENT agent notes recalled by meaning from vecgrep's global
	// memory store, scoped to this project via codemap's project_key (G2) — distinct
	// from Annotations (codemap's own durable, symbol-pinned layer). Empty when
	// vecgrep is absent/disabled or nothing matches.
	Memories []MemoryNote `json:"memories,omitempty"`
}

// MemoryNote is one recalled agent memory (vecgrep's store) attached to a context
// bundle — a shared scratchpad surfaced by meaning, never authoritative.
type MemoryNote struct {
	Content    string   `json:"content"`
	Importance float64  `json:"importance"`
	Tags       []string `json:"tags,omitempty"`
	Score      float32  `json:"score"`
}

// contextListCap bounds each relationship list in a context bundle so one
// orientation call stays small even for a hub. The full lists are a drill-down
// away (codemap_callers / codemap_callees / codemap_impact).
const contextListCap = 25

func capSlice[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

// Context assembles the one-call bundle for a symbol by composing the existing
// queries (Source, Callers, Callees, Impact) so the relationship logic, caps,
// and ambiguity notes stay in one place. depth bounds the blast-radius count
// (defaults to 3, like Impact). Returns Found=false (not an error) for an
// unknown symbol.
func (svc *Service) Context(cwd, symbol string, depth int) (*ContextReport, error) {
	if depth <= 0 {
		depth = 3
	}
	rep := &ContextReport{
		Symbol: symbol, Definitions: []SourceMatch{},
		Callers: []SymbolRef{}, Callees: []SymbolRef{}, Tests: []ImpactNode{},
		BlastDepth: depth, CallGraph: CallGraphNone, // refined from the bundled Impact below
	}
	src, err := svc.Source(cwd, symbol)
	if err != nil {
		return nil, err
	}
	rep.Project = src.Project
	rep.Definitions = src.Matches
	rep.Annotations = src.Annotations
	rep.Found = len(src.Matches) > 0
	if !rep.Found {
		return rep, nil // unknown symbol: empty bundle, no point querying relations
	}
	if ca, cErr := svc.Callers(cwd, symbol); cErr == nil {
		rep.CallersTotal = len(ca.Results)
		rep.Callers = capSlice(ca.Results, contextListCap)
		rep.Note = ca.Note
	}
	if ce, cErr := svc.Callees(cwd, symbol); cErr == nil {
		rep.CalleesTotal = len(ce.Results)
		rep.Callees = capSlice(ce.Results, contextListCap)
	}
	if imp, iErr := svc.Impact(cwd, symbol, depth); iErr == nil {
		rep.TestsTotal = len(imp.Tests)
		rep.Tests = capSlice(imp.Tests, contextListCap)
		rep.BlastRadius = len(imp.BlastRadius)
		if rep.Note == "" {
			rep.Note = imp.Note
		}
		rep.Resolution = imp.Resolution // carry the "call graph unavailable without --precise" honesty note
		rep.CallGraph = imp.CallGraph   // carry the stable machine enum too
	}
	// G2: surface relevant agent memories from vecgrep's global store, scoped to
	// this project by codemap's project_key (the leak-free recall convention).
	// Best-effort sidecar: bounded, swallows failures, never blocks the bundle.
	if root, _, rerr := svc.resolveProject(cwd); rerr == nil {
		mctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		rep.Memories = vecgrepMemoryRecall(mctx, svc.s.Config.Vecgrep, cwd, symbol,
			[]string{"codemap", git.RepoHash(root)}, 5)
		cancel()
	}
	return rep, nil
}
