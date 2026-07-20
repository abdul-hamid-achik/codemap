package app

import "context"

// RefactorPlanReport answers "if I rename or move this symbol, what do I need to
// touch?" — a rename/move impact plan built from the context bundle plus the
// files that depend on the symbol's definition file. It is the refactor-oriented
// peer of codemap_context: the same structural data, framed as the set of sites
// a rename/move would have to update (E3.3).
type RefactorPlanReport struct {
	SchemaVersion int             `json:"schema_version"`
	Project       string          `json:"project"`
	Symbol        string          `json:"symbol"`
	Selector      *SymbolSelector `json:"selector,omitempty"`
	Found         bool            `json:"found"`
	CallGraph     string          `json:"call_graph,omitempty"`
	// Definitions: the symbol's definition site(s) — the rename source.
	Definitions []SourceMatch `json:"definitions"`
	// CallSites: functions that call the symbol — call sites a rename updates.
	CallSites      []SymbolRef `json:"call_sites"`
	CallSitesTotal int         `json:"call_sites_total"`
	// ValueReferences: enclosing scopes that use the symbol as a value
	// (callbacks, handlers) — rename sites reached via references edges.
	ValueReferences      []ReferenceSite `json:"value_references"`
	ValueReferencesTotal int             `json:"value_references_total"`
	// MoveSites: files that depend on (import/call into) the definition file —
	// the import statements a move or cross-file rename would have to update.
	MoveSites []string `json:"move_sites"`
	// CoveringTests: tests exercising the symbol — must be re-run after a rename.
	CoveringTests []ImpactNode `json:"covering_tests"`
	TestsTotal    int          `json:"tests_total"`
	// BlastRadius: transitive impact count — rename risk awareness.
	BlastRadius int                  `json:"blast_radius"`
	Candidates  []AmbiguityCandidate `json:"candidates,omitempty"`
	Resolution  string               `json:"resolution,omitempty"`
	Note        string               `json:"note,omitempty"`
	Next        []NextAction         `json:"next,omitempty"`
}

// RefactorPlan builds a rename/move impact plan for a symbol by composing the
// context bundle (definitions, call sites, value references, covering tests,
// blast radius) with the files that depend on the symbol's definition file (the
// imports a move would update). Source bodies are dropped (brief) — a plan needs
// sites, not bodies.
func (svc *Service) RefactorPlan(ctx context.Context, cwd, symbol string, depth int) (*RefactorPlanReport, error) {
	ctxRep, err := svc.ContextWithContext(ctx, cwd, symbol, depth, true)
	if err != nil {
		return nil, err
	}
	return svc.buildRefactorPlan(cwd, ctxRep), nil
}

// RefactorPlanBySelector is RefactorPlan for one exact definition (no same-named
// union), mirroring codemap_context's selector form.
func (svc *Service) RefactorPlanBySelector(ctx context.Context, cwd string, selector SymbolSelector, depth int) (*RefactorPlanReport, error) {
	ctxRep, err := svc.ContextBySelectorWithContext(ctx, cwd, selector, depth, true)
	if err != nil {
		return nil, err
	}
	return svc.buildRefactorPlan(cwd, ctxRep), nil
}

func (svc *Service) buildRefactorPlan(cwd string, ctxRep *ContextReport) *RefactorPlanReport {
	rep := &RefactorPlanReport{
		SchemaVersion:        1,
		Project:              ctxRep.Project,
		Symbol:               ctxRep.Symbol,
		Selector:             ctxRep.Selector,
		Found:                ctxRep.Found,
		CallGraph:            ctxRep.CallGraph,
		Definitions:          ctxRep.Definitions,
		CallSites:            ctxRep.Callers,
		CallSitesTotal:       ctxRep.CallersTotal,
		ValueReferences:      ctxRep.References,
		ValueReferencesTotal: ctxRep.ReferencesTotal,
		CoveringTests:        ctxRep.Tests,
		TestsTotal:           ctxRep.TestsTotal,
		BlastRadius:          ctxRep.BlastRadius,
		Candidates:           ctxRep.Candidates,
		Resolution:           ctxRep.Resolution,
		Note:                 ctxRep.Note,
	}
	if !ctxRep.Found {
		return rep
	}
	// Move sites: files that depend on the definition file (import/call into it).
	if len(ctxRep.Definitions) > 0 {
		if deps, derr := svc.Dependencies(cwd, ctxRep.Definitions[0].File); derr == nil && deps != nil {
			rep.MoveSites = deps.allDependentFiles
		}
	}
	rep.Next = append(rep.Next, nextAction("codemap_context",
		"drill into the definition and its callers before renaming",
		map[string]any{"path": cwd, "symbol": rep.Symbol}))
	if len(rep.MoveSites) > 0 && len(ctxRep.Definitions) > 0 {
		rep.Next = append(rep.Next, nextAction("codemap_dependencies",
			"inspect the dependent files whose imports a move would update",
			map[string]any{"path": cwd, "file": ctxRep.Definitions[0].File}))
	}
	return rep
}
