package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// contextBatchItem is one unit of context_batch work — either a plain name
// (unions same-named definitions, like a solo Context call) or an exact
// selector (scoped to one definition). Both forms are unioned into a single
// ordered list of items so the existing cap/elision accounting applies to the
// combined input, not just the name half.
type contextBatchItem struct {
	name     string
	selector *SymbolSelector
}

// label identifies the item for not_found/partial_errors — the plain name for
// a name item (unchanged from today), or a "file:line (fqn)" form for a
// selector item, since a selector has no Symbol string of its own.
func (i contextBatchItem) label() string {
	if i.selector != nil {
		l := fmt.Sprintf("%s:%d", i.selector.File, i.selector.StartLine)
		if i.selector.FQN != "" {
			l += " (" + i.selector.FQN + ")"
		}
		return l
	}
	return i.name
}

func itemsFromNames(names []string) []contextBatchItem {
	items := make([]contextBatchItem, 0, len(names))
	for _, n := range names {
		items = append(items, contextBatchItem{name: n})
	}
	return items
}

func itemsFromSelectors(selectors []SymbolSelector) []contextBatchItem {
	items := make([]contextBatchItem, 0, len(selectors))
	for i := range selectors {
		sel := selectors[i]
		items = append(items, contextBatchItem{selector: &sel})
	}
	return items
}

// dedupSelectors returns the input with blanks (empty File) and duplicates
// removed, order preserved — the selector counterpart of dedupStrings. Two
// selectors are the same item when file, start_line, fqn, and kind all match;
// this does NOT cross-dedup against the symbols list (a name and a selector
// pointing at one of its definitions are intentionally treated as distinct
// batch items — see the context_batch package docs).
func dedupSelectors(xs []SymbolSelector) []SymbolSelector {
	seen := map[string]bool{}
	out := make([]SymbolSelector, 0, len(xs))
	for _, x := range xs {
		if strings.TrimSpace(x.File) == "" {
			continue
		}
		key := x.File + "\x00" + strconv.Itoa(x.StartLine) + "\x00" + x.FQN + "\x00" + x.Kind
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, x)
	}
	return out
}

// ContextBatch fetches Context for each symbol and aggregates them. Reuses the
// flagship Context bundle per symbol; never errors on a missing symbol (it lands in
// NotFound). Dedups and bounds the input so the response stays usable.
func (svc *Service) ContextBatch(cwd string, symbols []string, selectors []SymbolSelector, depth int) (*ContextBatchReport, error) {
	return svc.ContextBatchWithContext(context.Background(), cwd, symbols, selectors, depth)
}

// ContextBatchWithContext is the cancellable form of ContextBatch. It reuses
// each symbol's already-fetched, uncapped graph callers for common-caller
// aggregation, so a 25-symbol unresolved batch performs zero one-off LSP
// upgrades and zero duplicate caller queries. selectors is unioned with
// symbols (not cross-deduped against it) so an agent can pass exact
// definitions — e.g. from a prior ambiguous call's candidates — alongside
// plain names.
func (svc *Service) ContextBatchWithContext(ctx context.Context, cwd string, symbols []string, selectors []SymbolSelector, depth int) (*ContextBatchReport, error) {
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
	items := append(itemsFromNames(dedupStrings(symbols)), itemsFromSelectors(dedupSelectors(selectors))...)
	rep := &ContextBatchReport{
		Project: name, Indexed: found, Requested: len(items), Results: []*ContextReport{},
		SourceBudget: ContextSourceBudget{LimitBytes: contextBatchSourceBudgetBytes},
	}
	if !found {
		return rep, nil
	}

	if len(items) > contextBatchMax {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("requested %d symbols — analyzed the first %d", len(items), contextBatchMax))
		items = items[:contextBatchMax]
	}

	callerCount := map[string]int{}     // caller key → how many queried symbols it calls
	callerRef := map[string]SymbolRef{} // caller key → a representative ref
	// Optional vecgrep recall gets one aggregate timeout for the whole batch,
	// instead of up to 25 independent three-second tails.
	memoryCtx, cancelMemory := context.WithTimeout(ctx, contextMemoryTimeout)
	defer cancelMemory()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cr, callers, cerr := svc.contextForTarget(ctx, memoryCtx, cwd, item.name, item.selector, depth)
		if cerr != nil {
			// A selector's own validation (blank file, ambiguous selector, no
			// start_line/fqn) is a bad individual input, not a broken batch —
			// record it and move on, exactly like an unresolvable name already
			// lands in NotFound rather than aborting. A real cancellation/backend
			// failure (ctx.Err() != nil) still aborts the whole batch, unchanged.
			if item.selector != nil && ctx.Err() == nil {
				rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{Component: "selector", Error: boundedErrorText(cerr)})
				rep.NotFound = append(rep.NotFound, item.label())
				continue
			}
			return nil, cerr
		}
		applyContextBatchSourceBudget(cr, &rep.SourceBudget, &rep.SourceTruncations)
		rep.Results = append(rep.Results, cr)
		rep.PartialErrors = append(rep.PartialErrors, cr.PartialErrors...)
		if !cr.Found {
			rep.NotFound = append(rep.NotFound, item.label())
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
