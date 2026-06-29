package app

import (
	"fmt"
	"sort"
)

// contextBatchMax bounds a batch so one call can't blow an agent's context window
// (each ContextReport carries source bodies). Extra symbols are noted, not silently
// dropped.
const contextBatchMax = 25

// ContextBatchReport bundles the one-call context for several symbols at once,
// plus cross-symbol analysis — so an agent building a mental model of a component
// fetches it in ONE round-trip instead of N. CommonCallers (callers that reach two
// or more of the queried symbols) reveal shared entrypoints / coupling between them.
type ContextBatchReport struct {
	Project             string           `json:"project"`
	Indexed             bool             `json:"indexed"`
	Requested           int              `json:"requested"`
	Results             []*ContextReport `json:"results"`
	NotFound            []string         `json:"not_found,omitempty"`
	CombinedBlastRadius int              `json:"combined_blast_radius"` // sum of per-symbol blast (upper bound — shared dependents double-count)
	CommonCallers       []SymbolRef      `json:"common_callers,omitempty"`
	Note                string           `json:"note,omitempty"`
}

// ContextBatch fetches Context for each symbol and aggregates them. Reuses the
// flagship Context bundle per symbol; never errors on a missing symbol (it lands in
// NotFound). Dedups and bounds the input so the response stays usable.
func (svc *Service) ContextBatch(cwd string, symbols []string, depth int) (*ContextBatchReport, error) {
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ContextBatchReport{Project: name, Indexed: found, Requested: len(symbols), Results: []*ContextReport{}}
	if !found {
		return rep, nil
	}

	uniq := dedupStrings(symbols)
	if len(uniq) > contextBatchMax {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("requested %d symbols — analyzed the first %d", len(uniq), contextBatchMax))
		uniq = uniq[:contextBatchMax]
	}

	callerCount := map[string]int{}     // caller key → how many queried symbols it calls
	callerRef := map[string]SymbolRef{} // caller key → a representative ref
	for _, sym := range uniq {
		cr, cerr := svc.Context(cwd, sym, depth)
		if cerr != nil {
			return nil, cerr
		}
		rep.Results = append(rep.Results, cr)
		if !cr.Found {
			rep.NotFound = append(rep.NotFound, sym)
			continue
		}
		rep.CombinedBlastRadius += cr.BlastRadius
		// cr.Callers is capped (for display); count common callers over the FULL set
		// so coupling on hub symbols (>25 callers) isn't undercounted. Callers() is
		// uncapped; fall back to the capped list if it errors.
		callers := cr.Callers
		if full, ferr := svc.Callers(cwd, sym); ferr == nil && full != nil && full.Found {
			callers = full.Results
		}
		seen := map[string]bool{} // a symbol's own caller counts once even if it appears twice in its list
		for _, c := range callers {
			key := refKey(c)
			if seen[key] {
				continue
			}
			seen[key] = true
			callerCount[key]++
			callerRef[key] = c
		}
	}

	for key, n := range callerCount {
		if n >= 2 {
			rep.CommonCallers = append(rep.CommonCallers, callerRef[key])
		}
	}
	sort.SliceStable(rep.CommonCallers, func(i, j int) bool {
		ki, kj := refKey(rep.CommonCallers[i]), refKey(rep.CommonCallers[j])
		if callerCount[ki] != callerCount[kj] {
			return callerCount[ki] > callerCount[kj]
		}
		return ki < kj
	})
	if len(rep.CommonCallers) > 0 {
		rep.Note = joinNote(rep.Note, "common_callers reach two or more of the queried symbols — a likely shared entrypoint or coupling point")
	}
	return rep, nil
}

// refKey identifies a symbol ref for cross-symbol dedup (qualified name first).
func refKey(r SymbolRef) string {
	if r.FQN != "" {
		return r.FQN
	}
	return r.Symbol
}

// dedupStrings returns the input with blanks and duplicates removed, order preserved.
func dedupStrings(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
