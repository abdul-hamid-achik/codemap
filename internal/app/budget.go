package app

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Response token budgets.
//
// Agents pay context for every report, so the report-heavy tools (context,
// explore, impact, task-context) accept an optional max_tokens. The budget is a
// deterministic, approximate bound on the compact-JSON payload:
//
//	estimated_tokens = ceil(len(compact JSON bytes) / 4)
//
// The estimate is deliberately simple — it is NOT a tokenizer count, it only has
// to be stable and roughly proportional to what the payload costs. The JSON used
// is compact with HTML escaping disabled, i.e. what the MCP server returns.
//
// When a marshaled report exceeds the budget, the least important material is
// dropped first, in a fixed per-report priority order (see the *BudgetSteps
// builders): source bodies, then advisory/recall material, then list tails
// (shrinking from the end, so the best-ranked items survive longest), then
// whole trailing sections. Identity and honesty fields — schema_version,
// query/symbol, selectors, call_graph/confidence enums, freshness, notes,
// partial_errors and every *_total count — are never removed. If the identity
// fields alone exceed the budget, trimming stops at that floor and
// estimated_tokens simply stays above max_tokens.

// budgetBytesPerToken is the documented bytes-per-token ratio of the estimate.
const budgetBytesPerToken = 4

// TokenBudget is the additive `budget` object attached to a report when the
// caller requested max_tokens. Dropped counts items removed per list/section
// (summed across nested reports, e.g. every explore context's callers); it is
// an empty object when nothing was trimmed.
type TokenBudget struct {
	MaxTokens       int            `json:"max_tokens"`
	EstimatedTokens int            `json:"estimated_tokens"`
	Truncated       bool           `json:"truncated"`
	Dropped         map[string]int `json:"dropped"`
}

// EstimateTokens returns the approximate token cost of v: the compact JSON byte
// length (no HTML escaping, no indentation) divided by four, rounded up. It is
// deterministic for a given value. A value that cannot be marshaled costs 0.
func EstimateTokens(v any) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0
	}
	n := len(bytes.TrimRight(buf.Bytes(), "\n"))
	return (n + budgetBytesPerToken - 1) / budgetBytesPerToken
}

// ValidateMaxTokens rejects a negative budget with the same coded error on every
// surface. 0 means "no budget".
func ValidateMaxTokens(maxTokens int) error {
	if maxTokens < 0 {
		return coded(CodeInvalidInput, "pass max_tokens >= 0 (0 or omitted = no budget)",
			fmt.Errorf("max_tokens must not be negative"))
	}
	return nil
}

// budgetStep is one trimmable list: count items currently, of which at least
// floor must stay. keep(k) re-slices the list to its first k items (idempotent
// and callable in any order — it always slices the original), also adjusting
// any sibling truncation field.
type budgetStep struct {
	key   string
	floor int
	count int
	keep  func(k int)
}

// sliceStep builds a tail-shrinking step over *ptr. onKeep (optional) runs after
// each re-slice so sibling counters (e.g. references_truncated) stay consistent.
func sliceStep[T any](key string, floor int, ptr *[]T, onKeep func(k int)) budgetStep {
	orig := *ptr
	return budgetStep{
		key: key, floor: floor, count: len(orig),
		keep: func(k int) {
			*ptr = orig[:k]
			if onKeep != nil {
				onKeep(k)
			}
		},
	}
}

// enforceBudget attaches a budget to rep (via attach) and, when the estimate
// exceeds max, applies steps in order until the report fits or nothing more can
// be trimmed. Each step is fully exhausted (down to its floor) before the next
// is touched; inside a step the largest retained prefix that still fits is
// found by bisection, so trimming is both minimal and O(steps * log n).
func enforceBudget(max int, rep any, attach func(*TokenBudget), steps []budgetStep) {
	b := &TokenBudget{MaxTokens: max, Dropped: map[string]int{}}
	attach(b)
	committed := map[string]int{}

	// measure publishes the budget metadata for the hypothetical state "committed
	// drops + extra for key" and returns the payload estimate. The estimate
	// includes the budget object itself, so it is iterated to a fixed point.
	measure := func(key string, extra int) int {
		dropped := make(map[string]int, len(committed)+1)
		for k, v := range committed {
			dropped[k] = v
		}
		if extra > 0 {
			dropped[key] += extra
		}
		b.Dropped = dropped
		b.Truncated = len(dropped) > 0
		b.EstimatedTokens = EstimateTokens(rep)
		for i := 0; i < 4; i++ {
			next := EstimateTokens(rep)
			if next == b.EstimatedTokens {
				break
			}
			b.EstimatedTokens = next
		}
		return b.EstimatedTokens
	}

	if measure("", 0) <= max {
		return
	}
	for _, s := range steps {
		if s.count <= s.floor {
			continue
		}
		s.keep(s.floor)
		if measure(s.key, s.count-s.floor) > max {
			committed[s.key] += s.count - s.floor // fully spent; keep going
			continue
		}
		lo, hi := s.floor, s.count-1 // lo is known to fit
		for lo < hi {
			mid := (lo + hi + 1) / 2
			s.keep(mid)
			if measure(s.key, s.count-mid) <= max {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		s.keep(lo)
		committed[s.key] += s.count - lo
		measure("", 0)
		return
	}
	measure("", 0)
}

// contextBudgetSteps is the trim order for one ContextReport, least important
// first: source bodies (structural evidence outranks them), recalled memories,
// advisory next actions, value references, callees, callers, test commands,
// covering tests, and finally surplus merged definitions (the first definition
// always stays). *_total counts are the true pre-cap numbers and never change;
// references_truncated is recomputed.
func contextBudgetSteps(rep *ContextReport) []budgetStep {
	if rep == nil {
		return nil
	}
	var steps []budgetStep

	var withBody []int
	var origSource []string
	var origOmitted []bool
	for i, d := range rep.Definitions {
		if d.Source != "" {
			withBody = append(withBody, i)
			origSource = append(origSource, d.Source)
			origOmitted = append(origOmitted, d.SourceOmitted)
		}
	}
	steps = append(steps, budgetStep{
		key: "source", count: len(withBody),
		keep: func(k int) {
			for j, di := range withBody {
				if j < k {
					rep.Definitions[di].Source = origSource[j]
					rep.Definitions[di].SourceOmitted = origOmitted[j]
				} else {
					rep.Definitions[di].Source = ""
					rep.Definitions[di].SourceOmitted = true
				}
			}
		},
	})
	steps = append(steps,
		sliceStep("memories", 0, &rep.Memories, nil),
		sliceStep("next", 0, &rep.Next, nil),
		sliceStep("references", 0, &rep.References, func(k int) {
			rep.ReferencesTruncated = rep.ReferencesTotal - k
			if rep.ReferencesTruncated < 0 {
				rep.ReferencesTruncated = 0
			}
		}),
		sliceStep("callees", 0, &rep.Callees, nil),
		sliceStep("callers", 0, &rep.Callers, nil),
		sliceStep("test_commands", 0, &rep.TestCommands, nil),
		sliceStep("tests", 0, &rep.Tests, nil),
		sliceStep("definitions", 1, &rep.Definitions, nil),
	)
	return steps
}

// impactBudgetSteps is the trim order for one ImpactReport: advisory next
// actions, blast radius (the largest and most speculative list), direct callers,
// test commands, then covering tests. Definition locations, candidates and the
// untested/call_graph signals are never trimmed.
func impactBudgetSteps(rep *ImpactReport) []budgetStep {
	if rep == nil {
		return nil
	}
	return []budgetStep{
		sliceStep("next", 0, &rep.Next, nil),
		sliceStep("blast_radius", 0, &rep.BlastRadius, nil),
		sliceStep("direct_callers", 0, &rep.DirectCallers, nil),
		sliceStep("test_commands", 0, &rep.TestCommands, nil),
		sliceStep("tests", 0, &rep.Tests, nil),
	}
}

// reverseContextSteps concatenates contextBudgetSteps for each report from the
// last (lowest-ranked) to the first, so a later item is fully stripped before an
// earlier one loses anything.
func reverseContextSteps(reports []*ContextReport) []budgetStep {
	var steps []budgetStep
	for i := len(reports) - 1; i >= 0; i-- {
		steps = append(steps, contextBudgetSteps(reports[i])...)
	}
	return steps
}

func reverseImpactSteps(reports []*ImpactReport) []budgetStep {
	var steps []budgetStep
	for i := len(reports) - 1; i >= 0; i-- {
		steps = append(steps, impactBudgetSteps(reports[i])...)
	}
	return steps
}

// ApplyContextBudget bounds a ContextReport to maxTokens (0 = no budget) and
// attaches the additive budget object. Callers (CLI, MCP) invoke it on the
// report returned by Context*; it is a no-op without a budget.
func ApplyContextBudget(rep *ContextReport, maxTokens int) error {
	if err := ValidateMaxTokens(maxTokens); err != nil || maxTokens == 0 || rep == nil {
		return err
	}
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, contextBudgetSteps(rep))
	return nil
}

// ApplyContextBatchBudget is ApplyContextBudget for a ContextBatchReport.
func ApplyContextBatchBudget(rep *ContextBatchReport, maxTokens int) error {
	if err := ValidateMaxTokens(maxTokens); err != nil || maxTokens == 0 || rep == nil {
		return err
	}
	steps := reverseContextSteps(rep.Results)
	steps = append(steps,
		sliceStep("common_callers", 0, &rep.CommonCallers, nil),
		sliceStep("results", 1, &rep.Results, nil))
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, steps)
	return nil
}

// ApplyImpactBudget bounds an ImpactReport to maxTokens (0 = no budget).
func ApplyImpactBudget(rep *ImpactReport, maxTokens int) error {
	if err := ValidateMaxTokens(maxTokens); err != nil || maxTokens == 0 || rep == nil {
		return err
	}
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, impactBudgetSteps(rep))
	return nil
}

// ApplyImpactBatchBudget is ApplyImpactBudget for an ImpactBatchReport.
func ApplyImpactBatchBudget(rep *ImpactBatchReport, maxTokens int) error {
	if err := ValidateMaxTokens(maxTokens); err != nil || maxTokens == 0 || rep == nil {
		return err
	}
	steps := reverseImpactSteps(rep.Results)
	steps = append(steps, sliceStep("results", 1, &rep.Results, nil))
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, steps)
	return nil
}

// exploreBudgetSteps: every context's lists (last context first), then whole
// trailing contexts, then trailing seeds (the top seed always stays). not_joined
// is recomputed so it never exceeds the retained unjoined seeds.
func exploreBudgetSteps(rep *ExploreReport) []budgetStep {
	if rep == nil {
		return nil
	}
	steps := reverseContextSteps(rep.Contexts)
	steps = append(steps, sliceStep("contexts", 0, &rep.Contexts, nil))
	origNotJoined := rep.NotJoined
	steps = append(steps, sliceStep("seeds", 1, &rep.Seeds, nil))
	// sliceStep captured the original seeds; wrap keep to fix not_joined too.
	seeds := steps[len(steps)-1]
	allSeeds := append([]ExploreSeed(nil), rep.Seeds...)
	steps[len(steps)-1].keep = func(k int) {
		seeds.keep(k)
		if k >= len(allSeeds) {
			rep.NotJoined = origNotJoined
			return
		}
		n := 0
		for _, s := range allSeeds[:k] {
			if s.Selector == nil {
				n++
			}
		}
		rep.NotJoined = n
	}
	return steps
}

// applyExploreBudget is the unexported core shared by Explore.
func applyExploreBudget(rep *ExploreReport, maxTokens int) {
	if maxTokens <= 0 || rep == nil {
		return
	}
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, exploreBudgetSteps(rep))
}

// taskContextBudgetSteps: advisory next actions, related files (lists, then
// groups), impact drill-downs (lists, then entries), the explore section
// (contexts, then seeds — it duplicates the contexts section in debug mode, so
// it goes first), then the batch contexts section. targets, freshness,
// call_graph and partial_errors are never trimmed.
func taskContextBudgetSteps(rep *TaskContextReport) []budgetStep {
	if rep == nil {
		return nil
	}
	steps := []budgetStep{sliceStep("next", 0, &rep.Next, nil)}
	for i := len(rep.RelatedFiles) - 1; i >= 0; i-- {
		steps = append(steps, sliceStep("related", 0, &rep.RelatedFiles[i].Related, nil))
	}
	steps = append(steps, sliceStep("related_files", 0, &rep.RelatedFiles, nil))
	for i := len(rep.Impacts) - 1; i >= 0; i-- {
		steps = append(steps, impactBudgetSteps(rep.Impacts[i].Impact)...)
	}
	steps = append(steps, sliceStep("impacts", 0, &rep.Impacts, nil))
	var seedStep *budgetStep
	if rep.Explore != nil {
		es := exploreBudgetSteps(rep.Explore)
		// Drop the explore seeds last of all, after the batch contexts.
		seedStep = &es[len(es)-1]
		steps = append(steps, es[:len(es)-1]...)
	}
	if rep.Contexts != nil {
		steps = append(steps, reverseContextSteps(rep.Contexts.Results)...)
		steps = append(steps,
			sliceStep("common_callers", 0, &rep.Contexts.CommonCallers, nil),
			sliceStep("results", 0, &rep.Contexts.Results, nil))
	}
	if seedStep != nil {
		steps = append(steps, *seedStep)
	}
	return steps
}

func applyTaskContextBudget(rep *TaskContextReport, maxTokens int) {
	if maxTokens <= 0 || rep == nil {
		return
	}
	enforceBudget(maxTokens, rep, func(b *TokenBudget) { rep.Budget = b }, taskContextBudgetSteps(rep))
}
