package app

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"
)

// contextBatchMax bounds a batch so one call can't blow an agent's context window
// (each ContextReport carries source bodies). Extra symbols are noted, not silently
// dropped.
const (
	contextBatchMax               = 25
	contextBatchSourceBudgetBytes = 64 * 1024
)

// ContextSourceBudget describes the aggregate source-body budget applied to a
// context_batch response. Signatures/docs/locations are always retained; only
// SourceMatch.Source bodies are shortened when the batch would exceed LimitBytes.
type ContextSourceBudget struct {
	LimitBytes           int `json:"limit_bytes"`
	OriginalBytes        int `json:"original_bytes"`
	IncludedBytes        int `json:"included_bytes"`
	TruncatedDefinitions int `json:"truncated_definitions"`
}

// ContextSourceTruncation identifies one definition whose source body was
// shortened by the aggregate batch budget.
type ContextSourceTruncation struct {
	Symbol        string `json:"symbol"`
	File          string `json:"file"`
	StartLine     int    `json:"start_line"`
	OriginalBytes int    `json:"original_bytes"`
	IncludedBytes int    `json:"included_bytes"`
}

// ContextBatchReport bundles the one-call context for several symbols at once,
// plus cross-symbol analysis — so an agent building a mental model of a component
// fetches it in ONE round-trip instead of N. CommonCallers (callers that reach two
// or more of the queried symbols) reveal shared entrypoints / coupling between them.
type ContextBatchReport struct {
	Project             string                    `json:"project"`
	Indexed             bool                      `json:"indexed"`
	Requested           int                       `json:"requested"`
	Results             []*ContextReport          `json:"results"`
	NotFound            []string                  `json:"not_found,omitempty"`
	CombinedBlastRadius int                       `json:"combined_blast_radius"` // sum of per-symbol blast (upper bound — shared dependents double-count)
	CommonCallers       []SymbolRef               `json:"common_callers,omitempty"`
	PartialErrors       []ContextPartialError     `json:"partial_errors,omitempty"`
	SourceBudget        ContextSourceBudget       `json:"source_budget"`
	SourceTruncations   []ContextSourceTruncation `json:"source_truncations,omitempty"`
	Note                string                    `json:"note,omitempty"`
}

// ContextBatch fetches Context for each symbol and aggregates them. Reuses the
// flagship Context bundle per symbol; never errors on a missing symbol (it lands in
// NotFound). Dedups and bounds the input so the response stays usable.
func (svc *Service) ContextBatch(cwd string, symbols []string, depth int) (*ContextBatchReport, error) {
	return svc.ContextBatchWithContext(context.Background(), cwd, symbols, depth)
}

// ContextBatchWithContext is the cancellable form of ContextBatch. It reuses
// each symbol's already-fetched, uncapped graph callers for common-caller
// aggregation, so a 25-symbol unresolved batch performs zero one-off LSP
// upgrades and zero duplicate caller queries.
func (svc *Service) ContextBatchWithContext(ctx context.Context, cwd string, symbols []string, depth int) (*ContextBatchReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ContextBatchReport{
		Project: name, Indexed: found, Requested: len(symbols), Results: []*ContextReport{},
		SourceBudget: ContextSourceBudget{LimitBytes: contextBatchSourceBudgetBytes},
	}
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
	// Optional vecgrep recall gets one aggregate timeout for the whole batch,
	// instead of up to 25 independent three-second tails.
	memoryCtx, cancelMemory := context.WithTimeout(ctx, contextMemoryTimeout)
	defer cancelMemory()
	for _, sym := range uniq {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cr, callers, cerr := svc.contextWithContexts(ctx, memoryCtx, cwd, sym, depth)
		if cerr != nil {
			return nil, cerr
		}
		applyContextBatchSourceBudget(cr, &rep.SourceBudget, &rep.SourceTruncations)
		rep.Results = append(rep.Results, cr)
		rep.PartialErrors = append(rep.PartialErrors, cr.PartialErrors...)
		if !cr.Found {
			rep.NotFound = append(rep.NotFound, sym)
			continue
		}
		rep.CombinedBlastRadius += cr.BlastRadius
		seen := map[string]bool{} // a symbol's own caller counts once even if it appears twice in its list
		for _, c := range callers {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	if rep.SourceBudget.TruncatedDefinitions > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"source bodies exceeded the %d-byte context_batch budget — %d definition(s) were truncated; signatures, docs, and locations remain complete",
			rep.SourceBudget.LimitBytes, rep.SourceBudget.TruncatedDefinitions))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rep, nil
}

func applyContextBatchSourceBudget(rep *ContextReport, budget *ContextSourceBudget, truncations *[]ContextSourceTruncation) {
	if rep == nil || budget == nil {
		return
	}
	for i := range rep.Definitions {
		def := &rep.Definitions[i]
		original := len(def.Source)
		budget.OriginalBytes += original
		remaining := budget.LimitBytes - budget.IncludedBytes
		if remaining < 0 {
			remaining = 0
		}
		included := original
		if included > remaining {
			def.Source = utf8Prefix(def.Source, remaining)
			included = len(def.Source)
			budget.TruncatedDefinitions++
			*truncations = append(*truncations, ContextSourceTruncation{
				Symbol: rep.Symbol, File: def.File, StartLine: def.StartLine,
				OriginalBytes: original, IncludedBytes: included,
			})
		}
		budget.IncludedBytes += included
	}
}

func utf8Prefix(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
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
