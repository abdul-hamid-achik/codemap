package app

import "fmt"

// MaxImpactBatchPositions is the public request bound shared by the service and
// CLI. Callers should cap before expensive resolution; the service enforces it
// again so non-CLI consumers cannot bypass the contract.
const MaxImpactBatchPositions = 25

// ImpactBatchReport resolves impact analysis for several source positions in
// one call — a multi-frame stack trace or a diff's changed-line list, without
// one round-trip per frame. Each Results[i] is a full ImpactReport; callers
// inspect per-position Found/Resolution to detect misses.
type ImpactBatchReport struct {
	Project   string          `json:"project"`
	Indexed   bool            `json:"indexed"`
	Requested int             `json:"requested"`
	Processed int             `json:"processed"`
	Truncated int             `json:"truncated,omitempty"`
	Results   []*ImpactReport `json:"results"`
	Note      string          `json:"note,omitempty"`
}

// ImpactPositions resolves raw source positions and computes impact for every
// match. Misses are returned in-order as item-level errors so one native,
// generated, or unindexed frame cannot discard valid application frames.
func (svc *Service) ImpactPositions(cwd string, positions []FilePosition, depth int) (*ImpactBatchReport, error) {
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ImpactBatchReport{
		Project: name, Indexed: found, Requested: len(positions), Results: []*ImpactReport{},
	}
	if !found {
		return rep, nil
	}
	if len(positions) > MaxImpactBatchPositions {
		rep.Truncated = len(positions) - MaxImpactBatchPositions
		rep.Note = fmt.Sprintf("requested %d positions — resolved the first %d", len(positions), MaxImpactBatchPositions)
		positions = positions[:MaxImpactBatchPositions]
	}
	rep.Processed = len(positions)
	resolved := make([]*SymbolAtReport, len(positions))
	names := make([]string, 0, len(positions))
	for i, position := range positions {
		pos := position
		at, err := svc.SymbolAt(cwd, pos.File, pos.Line)
		if err != nil {
			return nil, err
		}
		resolved[i] = at
		if at.Resolution != "none" && at.Selector != nil {
			names = append(names, at.Symbol)
		}
	}
	shared := svc.impactSharedForNames(cwd, names)
	for i, position := range positions {
		pos := position
		at := resolved[i]
		if at.Resolution == "none" || at.Selector == nil {
			miss := emptyImpactReport(name, "", depth, nil)
			miss.Position = &pos
			miss.PositionMatch = "none"
			miss.Error = &ImpactItemError{
				Code: "symbol_not_found", Message: fmt.Sprintf("no indexed symbol at %s:%d", pos.File, pos.Line),
			}
			rep.Results = append(rep.Results, miss)
			continue
		}
		impact, err := svc.impactBySelectorShared(cwd, *at.Selector, depth, shared)
		if err != nil {
			return nil, err
		}
		impact.Position = &pos
		impact.PositionMatch = at.Resolution
		if !impact.Found {
			impact.Error = &ImpactItemError{
				Code: "symbol_not_found", Message: fmt.Sprintf("selected definition at %s:%d is no longer indexed", pos.File, pos.Line),
			}
		}
		rep.Results = append(rep.Results, impact)
	}
	return rep, nil
}

// ImpactBatch computes impact for each selector (resolved from --at positions)
// in one call. It fans out to ImpactBySelector with a shared impact context so
// project-wide state loads once instead of once per position.
func (svc *Service) ImpactBatch(cwd string, selectors []SymbolSelector, depth int) (*ImpactBatchReport, error) {
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ImpactBatchReport{Project: name, Indexed: found, Requested: len(selectors), Results: []*ImpactReport{}}
	if !found {
		return rep, nil
	}
	if len(selectors) > MaxImpactBatchPositions {
		rep.Truncated = len(selectors) - MaxImpactBatchPositions
		rep.Note = fmt.Sprintf("requested %d positions — resolved the first %d", len(selectors), MaxImpactBatchPositions)
		selectors = selectors[:MaxImpactBatchPositions]
	}
	rep.Processed = len(selectors)
	// Build a shared impact context so the per-position loop doesn't re-load
	// project-wide state for each call (same pattern as reviewImpactAnalyzer).
	names := make([]string, 0, len(selectors))
	for _, sel := range selectors {
		resolved, resolveErr := svc.resolveSourceSelector(cwd, sel)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved.found {
			names = append(names, resolved.node.Symbol)
		}
	}
	shared := svc.impactSharedForNames(cwd, names)
	for _, sel := range selectors {
		r, err := svc.impactBySelectorShared(cwd, sel, depth, shared)
		if err != nil {
			return nil, err
		}
		r.Position = &FilePosition{File: sel.File, Line: sel.StartLine}
		r.PositionMatch = "exact"
		rep.Results = append(rep.Results, r)
	}
	return rep, nil
}
