package app

import "fmt"

const impactBatchMax = 25

// ImpactBatchReport resolves impact analysis for several source positions in
// one call — a multi-frame stack trace or a diff's changed-line list, without
// one round-trip per frame. Each Results[i] is a full ImpactReport; callers
// inspect per-position Found/Resolution to detect misses.
type ImpactBatchReport struct {
	Project   string          `json:"project"`
	Indexed   bool            `json:"indexed"`
	Requested int             `json:"requested"`
	Results   []*ImpactReport `json:"results"`
	Note      string          `json:"note,omitempty"`
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
	if len(selectors) > impactBatchMax {
		rep.Note = fmt.Sprintf("requested %d positions — resolved the first %d", len(selectors), impactBatchMax)
		selectors = selectors[:impactBatchMax]
	}
	// Build a shared impact context so the per-position loop doesn't re-load
	// project-wide state for each call (same pattern as reviewImpactAnalyzer).
	shared := &impactShared{}
	for _, sel := range selectors {
		r, err := svc.impactBySelectorShared(cwd, sel, depth, shared)
		if err != nil {
			return nil, err
		}
		rep.Results = append(rep.Results, r)
	}
	return rep, nil
}