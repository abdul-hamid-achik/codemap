package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TaskContextSchemaVersion is the stable major version of the task-context
// orientation contract (schemas/codemap.task-context.v1.schema.json).
const TaskContextSchemaVersion = 1

// Mode names for TaskContext. "review" is deliberately absent: diff-scoped
// post-edit analysis is codemap_review's shipped contract.
const (
	TaskModeUnderstand = "understand"
	TaskModeChange     = "change"
	TaskModeDebug      = "debug"
)

// Bounds for TaskContext. Every number reuses an existing cap where one exists;
// the three task-specific ones follow the disclosure convention of
// context_batch.go's const block.
const (
	// taskImpactMax bounds the per-target impact drill-downs (change mode) —
	// aligned with DefaultExploreSeeds, the same orientation scale explore uses.
	taskImpactMax = 5
	// taskRelatedFilesTargets bounds how many definition files get a related-file
	// group (change mode); the first found targets, in deterministic order.
	taskRelatedFilesTargets = 3
	// taskRelatedFilesCap bounds each related-file group; RelatedFiles itself is
	// uncapped, so the wrapper must keep one hub file from flooding the bundle.
	taskRelatedFilesCap = 10
	// taskMaxPartialErrors caps the composite's own partial_errors list (the same
	// payload budget reviewMaxPartialErrors applies); the omitted tail is counted
	// in partial_errors_truncated, never silently dropped.
	taskMaxPartialErrors = 20
)

// TaskContextOptions is the caller-scoped input for TaskContext. Mode and
// Selectors come from the caller's own plan — codemap never infers them from
// the task text, so the composition stays deterministic and model-independent.
type TaskContextOptions struct {
	Mode      string           // understand|change|debug; "" → understand
	Selectors []SymbolSelector // change/debug only; exact definitions the caller already holds
	Depth     int              // contexts/impact depth; <= 0 → 3 (the Context default)
}

// TaskFreshness is the always-assemble-and-flag freshness header. Checked is
// false when no staleness comparison was possible (unindexed project, or the
// walk itself failed — see partial_errors); stale:false must then never be read
// as "fresh".
type TaskFreshness struct {
	Checked   bool             `json:"checked"`
	Stale     bool             `json:"stale"`
	Staleness *index.Staleness `json:"staleness,omitempty"` // {changed,new,deleted} — same shape as review/status
}

// TaskTarget is one resolved anchor the bundle is scoped to. Source says where
// it came from: "selector" (caller-supplied) or "explore" (joined from the task
// query's seeds).
type TaskTarget struct {
	Selector *SymbolSelector `json:"selector"`
	Symbol   string          `json:"symbol,omitempty"`
	Source   string          `json:"source,omitempty"` // "selector" | "explore"
	Found    bool            `json:"found"`
}

// TaskImpact is one bounded impact drill-down (change mode). The wrapper caps
// the embedded lists at contextListCap and discloses the true counts, because
// ImpactReport's own lists are uncapped.
type TaskImpact struct {
	Selector           *SymbolSelector `json:"selector"`
	Symbol             string          `json:"symbol,omitempty"`
	Impact             *ImpactReport   `json:"impact"`
	DirectCallersTotal int             `json:"direct_callers_total"`
	BlastRadiusTotal   int             `json:"blast_radius_total"`
	TestsTotal         int             `json:"tests_total"`
}

// TaskRelatedFiles is one definition file's bounded related-file group.
type TaskRelatedFiles struct {
	File         string        `json:"file"`
	Related      []RelatedFile `json:"related"`
	RelatedTotal int           `json:"related_total"`
}

// TaskContextReport is the mode-scoped orientation bundle: one retrieval query,
// composed over the existing deterministic reports (explore, context_batch,
// impact, related_files) plus a single freshness pass. Sections sit side by
// side with their own caps and honesty signals — the composite adds no
// cross-component selection or ranking, and never decides whether to reindex:
// that policy stays with the consumer (see Next).
type TaskContextReport struct {
	SchemaVersion          int                   `json:"schema_version"`
	Task                   string                `json:"task"`
	Mode                   string                `json:"mode"` // understand|change|debug
	Project                string                `json:"project"`
	Indexed                bool                  `json:"indexed"`
	Freshness              TaskFreshness         `json:"freshness"`
	Targets                []TaskTarget          `json:"targets,omitempty"`        // change/debug only
	Explore                *ExploreReport        `json:"explore,omitempty"`        // embedded verbatim (its own schema_version rides along)
	Contexts               *ContextBatchReport   `json:"contexts,omitempty"`       // embedded verbatim; brief bodies, no memory recall
	Impacts                []TaskImpact          `json:"impacts,omitempty"`        // change only; first taskImpactMax found targets
	RelatedFiles           []TaskRelatedFiles    `json:"related_files,omitempty"`  // change only; first taskRelatedFilesTargets found targets
	CallGraph              string                `json:"call_graph"`               // weakest across the assembled sections
	PartialErrors          []ContextPartialError `json:"partial_errors,omitempty"` // staleness|explore|contexts|impact|related_files|selector (+ labels flowing through from embedded reports)
	PartialErrorsTruncated int                   `json:"partial_errors_truncated,omitempty"`
	Next                   []NextAction          `json:"next,omitempty"` // max 2, advisory only
	Note                   string                `json:"note,omitempty"`
}

// addPartialError appends one bounded component failure up to the payload cap,
// then counts the omitted tail (the review.v1 mechanism).
func (rep *TaskContextReport) addPartialError(component string, err error) {
	if rep == nil || err == nil {
		return
	}
	if len(rep.PartialErrors) < taskMaxPartialErrors {
		rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{
			Component: component, Error: boundedErrorText(err),
		})
		return
	}
	rep.PartialErrorsTruncated++
}

// emptyIfNil is the generic twin of nonNil: a nil slice becomes an empty one so
// capped lists marshal as [] rather than null.
func emptyIfNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// ValidateTaskContext checks the caller-controlled shape before any I/O, with
// the same codes and hints on both surfaces: the MCP handler delegates to
// TaskContext (which calls this), and the CLI calls it directly so a bad
// mode/--at combination is rejected before --at positions are resolved.
func ValidateTaskContext(task string, opts TaskContextOptions) error {
	if strings.TrimSpace(task) == "" {
		return coded(CodeInvalidInput, "pass a task or question to orient on", fmt.Errorf("task must not be empty"))
	}
	mode := opts.Mode
	if mode == "" {
		mode = TaskModeUnderstand
	}
	switch mode {
	case TaskModeUnderstand, TaskModeChange, TaskModeDebug:
	default:
		if mode == "review" {
			return coded(CodeInvalidInput,
				"use codemap_review for diff-scoped post-edit analysis",
				fmt.Errorf("mode review is not a task-context mode"))
		}
		return coded(CodeInvalidInput,
			"pass mode understand, change, or debug",
			fmt.Errorf("unknown mode %q", mode))
	}
	if mode == TaskModeUnderstand && len(opts.Selectors) > 0 {
		return coded(CodeInvalidInput,
			"pass mode change or debug when supplying selectors",
			fmt.Errorf("selectors require mode change or debug"))
	}
	return nil
}

// attachTaskFreshness folds one staleness pass into the report. err (or a nil
// result on the unindexed nil,nil path) leaves Checked:false — stale:false must
// then never be read as "fresh" — and records the failure as a partial error.
func attachTaskFreshness(rep *TaskContextReport, st *index.Staleness, serr error) {
	if serr != nil {
		rep.addPartialError("staleness", serr)
		return
	}
	if st != nil {
		rep.Freshness = TaskFreshness{Checked: true, Stale: st.Any(), Staleness: st}
	}
}

// TaskContext assembles the mode-scoped orientation bundle. Determinism: the
// same task+mode+selectors+path against a byte-identical index and unchanged
// working tree produces byte-identical JSON; memory recall is disabled so no
// in-process sidecar can vary between calls. Sections degrade independently
// into partial_errors; only cancellation and storage failures abort the call.
func (svc *Service) TaskContext(ctx context.Context, cwd, task string, opts TaskContextOptions) (*TaskContextReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateTaskContext(task, opts); err != nil {
		return nil, err
	}
	mode := opts.Mode
	if mode == "" {
		mode = TaskModeUnderstand
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 3
	}

	_, name, indexed, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &TaskContextReport{
		SchemaVersion: TaskContextSchemaVersion,
		// indexed here means "registered"; both surfaces gate with the stricter
		// svc.Indexed (registered AND nodes>0) before calling.
		Task: task, Mode: mode, Project: name, Indexed: indexed,
		CallGraph: CallGraphNone,
	}
	if !indexed {
		return rep, nil // surfaces gate this; the report stays graceful (indexed:false)
	}

	// One staleness pass for the whole bundle — never recomputed per section.
	// A failed walk degrades to Checked:false + partial_errors; it never aborts
	// and never renders stale:false as an assertion of freshness.
	st, serr := svc.Staleness(cwd)
	attachTaskFreshness(rep, st, serr)

	selectors := dedupSelectors(opts.Selectors)
	if len(selectors) > contextBatchMax {
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"requested %d selectors — analyzed the first %d", len(selectors), contextBatchMax))
		selectors = selectors[:contextBatchMax]
	}

	var exploreOpts ExploreOptions
	if mode == TaskModeDebug {
		exploreOpts = ExploreOptions{Edges: MaxExploreEdges} // caller/callee emphasis via the higher edge bound
	}

	// Explore: always for understand and debug; for change only when the caller
	// supplied no selectors (they already know where to look).
	runExplore := mode == TaskModeUnderstand || mode == TaskModeDebug || len(selectors) == 0
	if runExplore {
		exp, eerr := svc.Explore(ctx, cwd, task, exploreOpts)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if eerr != nil {
			rep.addPartialError("explore", eerr)
		} else {
			rep.Explore = exp
			for _, c := range exp.Contexts {
				rep.CallGraph = weakerContextCallGraph(rep.CallGraph, c.CallGraph)
			}
		}
	}

	if mode == TaskModeUnderstand {
		svc.finishTaskContext(rep, cwd)
		return rep, nil
	}

	// Targets: caller selectors when given, else explore's joined seeds.
	for i := range selectors {
		target := TaskTarget{Source: "selector", Found: false, Selector: &selectors[i]}
		if res, rerr := svc.resolveSourceSelector(cwd, selectors[i]); rerr != nil {
			rep.addPartialError("selector", rerr)
		} else if res.found {
			target.Found = true
			target.Symbol = res.node.Symbol
			target.Selector = selectorForNode(res.node)
		}
		rep.Targets = append(rep.Targets, target)
	}
	if len(selectors) == 0 && rep.Explore != nil {
		joined := make([]SymbolSelector, 0, DefaultExploreSeeds)
		for _, seed := range rep.Explore.Seeds {
			if seed.Selector == nil {
				continue
			}
			joined = append(joined, *seed.Selector)
			rep.Targets = append(rep.Targets, TaskTarget{
				Selector: seed.Selector, Symbol: seed.Symbol, Source: "explore", Found: true,
			})
		}
		selectors = capSlice(joined, DefaultExploreSeeds)
		if len(rep.Targets) > len(selectors) {
			rep.Targets = rep.Targets[:len(selectors)]
		}
	}

	// Contexts: the brief, memory-free batch over the targets (the explore
	// composition, reused rather than duplicated).
	if len(selectors) > 0 {
		batch, berr := svc.contextBatchWithContext(ctx, cwd, nil, selectors, depth, true, false)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if berr != nil {
			rep.addPartialError("contexts", berr)
		} else {
			rep.Contexts = batch
			for _, c := range batch.Results {
				if mode == TaskModeDebug {
					boundExploreContext(c, MaxExploreEdges)
				}
				rep.CallGraph = weakerContextCallGraph(rep.CallGraph, c.CallGraph)
			}
		}
	}

	if mode == TaskModeChange {
		svc.attachTaskImpacts(ctx, cwd, rep, depth)
		if err := ctx.Err(); err != nil {
			return nil, err // cancellation aborts — a truncated impacts list must never pass as complete
		}
		svc.attachTaskRelatedFiles(cwd, rep)
	}
	svc.finishTaskContext(rep, cwd)
	return rep, nil
}

// attachTaskImpacts adds the per-target impact drill-downs (change mode). One
// shared impactShared serves every target — the same seam review's per-symbol
// loop uses — so project-wide state loads once, not once per symbol.
func (svc *Service) attachTaskImpacts(ctx context.Context, cwd string, rep *TaskContextReport, depth int) {
	var targets []TaskTarget
	for _, t := range rep.Targets {
		if t.Found && t.Selector != nil {
			targets = append(targets, t)
		}
	}
	targets = capSlice(targets, taskImpactMax)
	if len(targets) == 0 {
		return
	}
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Symbol)
	}
	shared := svc.impactSharedForNames(cwd, names) // nil → per-symbol load-on-demand fallback
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		imp, err := svc.impactBySelectorShared(cwd, *t.Selector, depth, shared)
		if err != nil {
			rep.addPartialError("impact", err)
			continue
		}
		rep.CallGraph = weakerContextCallGraph(rep.CallGraph, imp.CallGraph)
		// Totals come from the full lists BEFORE capping — the context bundle's
		// own disclosure convention (service_context.go's *_total).
		dcTotal, brTotal, testsTotal := len(imp.DirectCallers), len(imp.BlastRadius), len(imp.Tests)
		imp.DirectCallers = emptyIfNil(capSlice(imp.DirectCallers, contextListCap))
		imp.BlastRadius = emptyIfNil(capSlice(imp.BlastRadius, contextListCap))
		imp.Tests = emptyIfNil(capSlice(imp.Tests, contextListCap))
		rep.Impacts = append(rep.Impacts, TaskImpact{
			Selector: t.Selector, Symbol: t.Symbol, Impact: imp,
			DirectCallersTotal: dcTotal,
			BlastRadiusTotal:   brTotal,
			TestsTotal:         testsTotal,
		})
	}
}

// attachTaskRelatedFiles adds bounded related-file groups for the first found
// targets' definition files (change mode), deduplicated — two targets in one
// file are one group, not two identical groups burning cap slots.
func (svc *Service) attachTaskRelatedFiles(cwd string, rep *TaskContextReport) {
	var files []string
	seen := map[string]bool{}
	for _, t := range rep.Targets {
		if t.Found && t.Selector != nil && t.Selector.File != "" && !seen[t.Selector.File] {
			seen[t.Selector.File] = true
			files = append(files, t.Selector.File)
		}
	}
	files = capSlice(files, taskRelatedFilesTargets)
	for _, file := range files {
		rel, err := svc.RelatedFiles(cwd, file)
		if err != nil {
			rep.addPartialError("related_files", err)
			continue
		}
		rep.RelatedFiles = append(rep.RelatedFiles, TaskRelatedFiles{
			File:         file,
			Related:      capSlice(rel.Related, taskRelatedFilesCap),
			RelatedTotal: len(rel.Related),
		})
	}
}

// finishTaskContext attaches the advisory next actions (max 2). The composite
// never acts on staleness or resolution itself — it only names the move; the
// trust decision stays with the consumer.
func (svc *Service) finishTaskContext(rep *TaskContextReport, cwd string) {
	if rep.CallGraph == CallGraphUnresolved {
		rep.Next = append(rep.Next, nextAction("codemap_index",
			"the call graph is unresolved; precise indexing is required before trusting callers, blast radius, or coverage",
			map[string]any{"path": cwd, "precise": true}))
	}
	if len(rep.Next) < 2 && rep.Freshness.Stale {
		rep.Next = append(rep.Next, nextAction("codemap_index",
			"the index has drifted from the working tree; reindex before trusting snapshot-based results",
			map[string]any{"path": cwd}))
	}
	if len(rep.Next) == 0 {
		for _, t := range rep.Targets {
			if t.Found && t.Selector != nil {
				rep.Next = append(rep.Next, nextAction("codemap_context",
					"drill into one target's full bundle (uncapped relations, source body) before acting",
					map[string]any{"path": cwd, "selector": t.Selector}))
				break
			}
		}
	}
	if len(rep.Next) > 2 {
		rep.Next = rep.Next[:2]
	}
}
