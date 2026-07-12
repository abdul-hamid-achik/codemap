package app

import (
	"context"
	"fmt"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// ContextReport is the one-call bundle for a symbol: its definition(s) with
// source, who calls it, what it calls, where it is stored/passed as a value, the
// tests that cover it, the blast-radius
// size, and any pinned annotations. It exists so a person or agent gets a
// complete picture in a single query instead of stitching together source +
// callers + callees + references + impact (five round-trips for a harness).
type ContextReport struct {
	Symbol      string          `json:"symbol"`
	Selector    *SymbolSelector `json:"selector,omitempty"` // exact selected definition; absent on a name-union query
	Project     string          `json:"project"`
	Found       bool            `json:"found"`
	Definitions []SourceMatch   `json:"definitions"` // signature, doc, file:line, and source body per matching def
	Callers     []SymbolRef     `json:"callers"`     // who calls it (capped — see callers_total)
	Callees     []SymbolRef     `json:"callees"`     // what it calls (capped — see callees_total)
	References  []ReferenceSite `json:"references"`  // enclosing scopes that use it as a value (capped)
	Tests       []ImpactNode    `json:"tests"`       // tests covering it (capped — see tests_total)
	// *Total are the true counts before capping, so an agent knows when a list was
	// truncated and can call codemap_callers/codemap_callees/codemap_references/
	// codemap_impact for the
	// complete set. The bundle stays bounded so one orientation call can't blow an
	// agent's context window.
	CallersTotal        int `json:"callers_total"`
	CalleesTotal        int `json:"callees_total"`
	ReferencesTotal     int `json:"references_total"`
	ReferencesTruncated int `json:"references_truncated,omitempty"`
	TestsTotal          int `json:"tests_total"`
	// Reference-specific honesty is independent of CallGraph: precise call
	// resolution does not upgrade stored callback/value-reference edges.
	ReferencesCoverage   string             `json:"references_coverage"`             // partial|unavailable|none
	ReferencesStale      bool               `json:"references_stale"`                // stale sites are candidates
	ReferencesConfidence string             `json:"references_confidence"`           // confirmed|candidate|mixed|none
	ReferencesResolution string             `json:"references_resolution,omitempty"` // coverage/lexical-location caveat
	BlastRadius          int                `json:"blast_radius"`                    // count of transitively-affected nodes
	BlastDepth           int                `json:"blast_depth"`                     // depth the blast radius was traversed to (it's bounded, not the full closure)
	Note                 string             `json:"note,omitempty"`                  // set when the name is ambiguous (merges same-named defs)
	Resolution           string             `json:"resolution,omitempty"`            // human sentence set when the call graph is unresolved (TS/JS/Python without --precise) — callers/callees/tests/blast are unavailable, not absent
	CallGraph            string             `json:"call_graph"`                      // stable machine enum: resolved|name|unresolved|none (carried from the bundled Impact)
	Annotations          []graph.Annotation `json:"annotations,omitempty"`           // pinned notes/data on the symbol
	// Memories are TRANSIENT agent notes recalled by meaning from vecgrep's global
	// memory store, scoped to this project via codemap's project_key (G2) — distinct
	// from Annotations (codemap's own durable, symbol-pinned layer). Empty when
	// vecgrep is absent/disabled or nothing matches.
	Memories []MemoryNote `json:"memories,omitempty"`
	// PartialErrors names optional bundle components that could not be computed.
	// Source is the hard prerequisite; relation/impact failures leave the rest of
	// the report usable and are surfaced here instead of being silently dropped.
	PartialErrors []ContextPartialError `json:"partial_errors,omitempty"`
	Next          []NextAction          `json:"next,omitempty"`
}

// ContextPartialError is one non-fatal failure while assembling a context
// bundle. Component is a stable, small enum-like label (callers, callees,
// references, impact, memory_recall); Error is bounded so a backend failure cannot itself
// bloat an agent response. Symbol is useful on an aggregated context_batch.
type ContextPartialError struct {
	Symbol    string `json:"symbol,omitempty"`
	Component string `json:"component"`
	Error     string `json:"error"`
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
// away (codemap_callers / codemap_callees / codemap_references / codemap_impact).
const (
	contextListCap       = 25
	contextMemoryTimeout = 3 * time.Second
	contextErrorMaxRunes = 240
)

func capSlice[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

// Context assembles the one-call bundle from Source, indexed graph relations,
// and Impact so relationship logic, caps, and ambiguity notes stay in one place
// without public relation auto-upgrades. depth bounds the blast-radius count
// (defaults to 3, like Impact). Returns Found=false (not an error) for an
// unknown symbol.
func (svc *Service) Context(cwd, symbol string, depth int) (*ContextReport, error) {
	return svc.ContextWithContext(context.Background(), cwd, symbol, depth)
}

// ContextWithContext is the cancellable form of Context. Relationship lookups
// deliberately use the already-indexed graph directly: Context is an orientation
// bundle, not an implicit request to spawn language servers. An unresolved LSP-
// language index therefore stays honestly unresolved and recommends a precise
// reindex. Callers that explicitly want one-off callHierarchy can still use
// PreciseCallers/PreciseCallees.
func (svc *Service) ContextWithContext(ctx context.Context, cwd, symbol string, depth int) (*ContextReport, error) {
	rep, _, err := svc.contextWithContexts(ctx, ctx, cwd, symbol, depth)
	return rep, err
}

// ContextBySelectorWithContext assembles the same bounded bundle for one exact
// definition. It is the selector-safe counterpart of ContextWithContext.
func (svc *Service) ContextBySelectorWithContext(ctx context.Context, cwd string, selector SymbolSelector, depth int) (*ContextReport, error) {
	rep, _, err := svc.contextForTarget(ctx, ctx, cwd, "", &selector, depth)
	return rep, err
}

// contextWithContexts also returns the uncapped graph callers so ContextBatch can
// compute common_callers without issuing another query (and without invoking the
// public auto-upgrading Callers path).
// contextWithContexts separates required-work cancellation from the optional
// memory-recall budget. ContextBatch supplies one shared, bounded memory context
// across all symbols, preventing N independent 3-second sidecar tails.
func (svc *Service) contextWithContexts(ctx, memoryCtx context.Context, cwd, symbol string, depth int) (*ContextReport, []SymbolRef, error) {
	return svc.contextForTarget(ctx, memoryCtx, cwd, symbol, nil, depth)
}

func (svc *Service) contextForTarget(ctx, memoryCtx context.Context, cwd, symbol string, selector *SymbolSelector, depth int) (*ContextReport, []SymbolRef, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if memoryCtx == nil {
		memoryCtx = ctx
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if depth <= 0 {
		depth = 3
	}
	rep := &ContextReport{
		Symbol: symbol, Selector: selector, Definitions: []SourceMatch{},
		Callers: []SymbolRef{}, Callees: []SymbolRef{}, References: []ReferenceSite{}, Tests: []ImpactNode{},
		ReferencesCoverage: ReferenceCoverageNone, ReferencesConfidence: ReferenceConfidenceNone,
		BlastDepth: depth, CallGraph: CallGraphNone, // refined from the bundled Impact below
	}
	var src *SourceReport
	var err error
	if selector != nil {
		src, err = svc.SourceBySelector(cwd, *selector)
	} else {
		src, err = svc.Source(cwd, symbol)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	rep.Project = src.Project
	rep.Symbol = src.Symbol
	rep.Selector = src.Selector
	rep.Definitions = src.Matches
	rep.Annotations = src.Annotations
	rep.Found = len(src.Matches) > 0
	if !rep.Found {
		return rep, nil, nil // unknown symbol: empty bundle, no point querying relations
	}
	var ca *RelationReport
	var cErr error
	if selector != nil {
		ca, cErr = svc.relationBySelector(cwd, *selector, (*graph.Store).CallersOfNode)
	} else {
		ca, cErr = svc.relation(cwd, symbol, (*graph.Store).Callers)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	fullCallers := applyContextRelation(rep, "callers", ca, cErr)

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var ce *RelationReport
	var ceErr error
	if selector != nil {
		ce, ceErr = svc.relationBySelector(cwd, *selector, (*graph.Store).CalleesOfNode)
	} else {
		ce, ceErr = svc.relation(cwd, symbol, (*graph.Store).Callees)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	_ = applyContextRelation(rep, "callees", ce, ceErr)

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var refs *ReferencesReport
	var refsErr error
	if selector != nil {
		refs, refsErr = svc.ReferencesBySelector(cwd, *selector)
	} else {
		refs, refsErr = svc.References(cwd, symbol)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	applyContextReferences(rep, refs, refsErr)

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var imp *ImpactReport
	var iErr error
	if selector != nil {
		imp, iErr = svc.ImpactBySelector(cwd, *selector, depth)
	} else {
		imp, iErr = svc.Impact(cwd, symbol, depth)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	applyContextImpact(rep, imp, iErr)

	// G2: surface relevant agent memories from vecgrep's global store, scoped to
	// this project by codemap's project_key (the leak-free recall convention).
	// Best-effort sidecar: bounded and derived from the caller's context, so an MCP
	// cancellation/deadline terminates the child process rather than leaving a 3s
	// background-context tail behind.
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if svc.s.Config.Vecgrep.Enabled {
		if root, _, rerr := svc.resolveProject(cwd); rerr == nil {
			mctx, cancel := context.WithTimeout(memoryCtx, contextMemoryTimeout)
			memories, recallErr := vecgrepMemoryRecall(mctx, svc.s.Config.Vecgrep, cwd, rep.Symbol,
				[]string{"codemap", git.RepoHash(root)}, 5)
			rep.Memories = memories
			memoryErr := mctx.Err()
			cancel()
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if memoryErr != nil {
				rep.addPartialError("memory_recall", memoryErr)
			} else if recallErr != nil {
				rep.addPartialError("memory_recall", recallErr)
			}
		} else {
			rep.addPartialError("memory_recall", rerr)
		}
	}
	if rep.CallGraph == CallGraphUnresolved {
		rep.Next = append(rep.Next, nextAction("codemap_index",
			"the call graph is unresolved; precise indexing is required before trusting callers, blast radius, or coverage",
			map[string]any{"path": cwd, "precise": true}))
	}
	if rep.BlastRadius >= 20 || (rep.CallersTotal >= 10 && rep.TestsTotal == 0) {
		args := map[string]any{"path": cwd, "symbol": rep.Symbol, "depth": depth}
		if rep.Selector != nil {
			args = map[string]any{"path": cwd, "selector": rep.Selector, "depth": depth}
		}
		rep.Next = append(rep.Next, nextAction("codemap_risk",
			"this symbol is broadly depended on or appears untested; score change risk before editing",
			args))
	}
	if len(rep.Annotations) > 0 && len(rep.Next) < 2 {
		rep.Next = append(rep.Next, nextAction("codemap_annotations",
			"pinned knowledge exists for this symbol; read it before changing behavior",
			map[string]any{"path": cwd, "symbol": rep.Symbol}))
	}
	if len(rep.Next) > 2 {
		rep.Next = rep.Next[:2]
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return rep, fullCallers, nil
}

func applyContextReferences(rep *ContextReport, refs *ReferencesReport, err error) {
	if err != nil {
		rep.addPartialError("references", err)
		return
	}
	if refs == nil {
		rep.addPartialError("references", fmt.Errorf("empty references report"))
		return
	}
	rep.ReferencesTotal = refs.ReferencesTotal
	sites := refs.References
	if sites == nil {
		sites = []ReferenceSite{}
	}
	rep.References = capSlice(sites, contextListCap)
	rep.ReferencesTruncated = refs.ReferencesTotal - len(rep.References)
	rep.ReferencesCoverage = refs.Coverage
	rep.ReferencesStale = refs.Stale
	rep.ReferencesConfidence = refs.Confidence
	rep.ReferencesResolution = refs.Resolution
	if rep.ReferencesTruncated > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"showing %d of %d enclosing value-reference scopes — call codemap_references for the bounded drill-down",
			len(rep.References), rep.ReferencesTotal))
	}
}

// applyContextRelation adds one graph-relation component and returns its full,
// uncapped result for batch aggregation. Errors are non-fatal and explicit.
func applyContextRelation(rep *ContextReport, component string, rel *RelationReport, err error) []SymbolRef {
	if err != nil {
		rep.addPartialError(component, err)
		return nil
	}
	if rel == nil {
		rep.addPartialError(component, fmt.Errorf("empty relation report"))
		return nil
	}
	if rel.CallGraph != CallGraphNone {
		rep.CallGraph = weakerContextCallGraph(rep.CallGraph, rel.CallGraph)
	}
	if rep.Resolution == "" && rel.Resolution != "" {
		rep.Resolution = rel.Resolution
	}
	if rep.Note == "" && rel.Note != "" {
		rep.Note = rel.Note
	}
	results := nonNil(rel.Results)
	switch component {
	case "callers":
		rep.CallersTotal = len(results)
		rep.Callers = capSlice(results, contextListCap)
		if rep.CallersTotal > contextListCap {
			rep.Note = joinNote(rep.Note, fmt.Sprintf(
				"showing top %d of %d callers, ranked by fan-in (hubs first) — call codemap_callers for the complete list",
				contextListCap, rep.CallersTotal))
		}
	case "callees":
		rep.CalleesTotal = len(results)
		rep.Callees = capSlice(results, contextListCap)
		if rep.CalleesTotal > contextListCap {
			rep.Note = joinNote(rep.Note, fmt.Sprintf(
				"showing top %d of %d callees, ranked by fan-in — call codemap_callees for the complete list",
				contextListCap, rep.CalleesTotal))
		}
	}
	return results
}

func applyContextImpact(rep *ContextReport, imp *ImpactReport, err error) {
	if err != nil {
		rep.addPartialError("impact", err)
		return
	}
	if imp == nil {
		rep.addPartialError("impact", fmt.Errorf("empty impact report"))
		return
	}
	rep.TestsTotal = len(imp.Tests)
	rep.Tests = capSlice(imp.Tests, contextListCap)
	rep.BlastRadius = len(imp.BlastRadius)
	if rep.Note == "" {
		rep.Note = imp.Note
	}
	if imp.Resolution != "" {
		rep.Resolution = imp.Resolution
	}
	if imp.CallGraph != CallGraphNone {
		rep.CallGraph = weakerContextCallGraph(rep.CallGraph, imp.CallGraph)
	}
}

func weakerContextCallGraph(current, incoming string) string {
	if current == CallGraphNone {
		return incoming
	}
	if incoming == CallGraphNone {
		return current
	}
	rank := map[string]int{CallGraphUnresolved: 1, CallGraphName: 2, CallGraphResolved: 3}
	if rank[incoming] < rank[current] {
		return incoming
	}
	return current
}

func (rep *ContextReport) addPartialError(component string, err error) {
	if rep == nil || err == nil {
		return
	}
	msg := []rune(err.Error())
	if len(msg) > contextErrorMaxRunes {
		msg = append(msg[:contextErrorMaxRunes-1], '…')
	}
	rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{
		Symbol: rep.Symbol, Component: component, Error: string(msg),
	})
}
