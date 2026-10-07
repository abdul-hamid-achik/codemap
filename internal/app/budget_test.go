package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bigContext is a synthetic bundle with every trimmable list populated.
func bigContext() *ContextReport {
	refs := func(prefix string, n int) []SymbolRef {
		out := make([]SymbolRef, n)
		for i := range out {
			out[i] = SymbolRef{Symbol: fmt.Sprintf("%s%02d", prefix, i), File: "pkg/file.go", StartLine: i + 1, Kind: "function"}
		}
		return out
	}
	tests := make([]ImpactNode, 8)
	for i := range tests {
		tests[i] = ImpactNode{Symbol: fmt.Sprintf("TestX%d", i), Kind: "test", File: "pkg/file_test.go", StartLine: i + 1}
	}
	sites := make([]ReferenceSite, 6)
	for i := range sites {
		sites[i] = ReferenceSite{Source: SymbolRef{Symbol: fmt.Sprintf("Wire%d", i), File: "pkg/wire.go", StartLine: i + 1}}
	}
	sel := &SymbolSelector{File: "pkg/file.go", StartLine: 3, FQN: "pkg.Hub", Kind: "function"}
	return &ContextReport{
		Symbol: "Hub", Selector: sel, Project: "demo", Found: true,
		Definitions: []SourceMatch{
			{Symbol: "Hub", Kind: "function", File: "pkg/file.go", StartLine: 3, EndLine: 40, Source: strings.Repeat("x := 1\n", 80)},
			{Symbol: "Hub", Kind: "function", File: "pkg/other.go", StartLine: 9, EndLine: 50, Source: strings.Repeat("y := 2\n", 80)},
		},
		Callers: refs("Caller", 25), Callees: refs("Callee", 25), References: sites, Tests: tests,
		TestCommands: []string{"go test ./pkg -run TestX0", "go test ./pkg -run TestX1"},
		CallersTotal: 40, CalleesTotal: 25, ReferencesTotal: 9, ReferencesTruncated: 3, TestsTotal: 8,
		ReferencesCoverage: "partial", ReferencesConfidence: "candidate",
		BlastRadius: 77, BlastDepth: 3, CallGraph: CallGraphName,
		Memories: []MemoryNote{{Content: strings.Repeat("remember ", 40)}, {Content: "second"}},
		Next:     []NextAction{{Tool: "codemap_risk", Why: "broadly depended on"}},
	}
}

func TestEstimateTokensIsCeilOfCompactBytesOverFour(t *testing.T) {
	v := map[string]string{"a": "b"} // {"a":"b"} = 9 bytes
	if got := EstimateTokens(v); got != 3 {
		t.Fatalf("EstimateTokens = %d, want ceil(9/4)=3", got)
	}
	if got := EstimateTokens("<&>"); got != 2 { // "<&>" = 5 bytes, no HTML escaping
		t.Fatalf("HTML must not be escaped: got %d, want 2", got)
	}
}

func TestValidateMaxTokensRejectsNegative(t *testing.T) {
	if err := ValidateMaxTokens(-1); err == nil || CodeOf(err) != CodeInvalidInput {
		t.Fatalf("negative budget = %v (code %s)", err, CodeOf(err))
	}
	if err := ValidateMaxTokens(0); err != nil {
		t.Fatal(err)
	}
	rep := bigContext()
	if err := ApplyContextBudget(rep, -5); err == nil {
		t.Fatal("ApplyContextBudget must reject a negative budget")
	}
	if rep.Budget != nil {
		t.Fatal("a rejected budget must not mutate the report")
	}
}

func TestBudgetNoopWithoutMaxTokens(t *testing.T) {
	rep := bigContext()
	before := jsonOf(t, rep)
	if err := ApplyContextBudget(rep, 0); err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, rep) != before || rep.Budget != nil {
		t.Fatal("max_tokens 0 must leave the report byte-identical, with no budget object")
	}
}

func TestBudgetFitsUntouched(t *testing.T) {
	rep := bigContext()
	full := EstimateTokens(rep)
	if err := ApplyContextBudget(rep, full+500); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b == nil || b.Truncated || len(b.Dropped) != 0 || b.MaxTokens != full+500 {
		t.Fatalf("budget = %+v", b)
	}
	if b.EstimatedTokens != EstimateTokens(rep) || b.EstimatedTokens > b.MaxTokens {
		t.Fatalf("estimated_tokens %d must equal the final payload estimate %d and fit", b.EstimatedTokens, EstimateTokens(rep))
	}
	want := bigContext()
	if len(rep.Callers) != len(want.Callers) || rep.Definitions[0].Source != want.Definitions[0].Source {
		t.Fatal("a report that fits must not lose anything")
	}
	if out := jsonOf(t, rep.Budget); !strings.Contains(out, `"dropped":{}`) {
		t.Fatalf("an untrimmed budget must render dropped as an empty object: %s", out)
	}
}

func TestBudgetTrimsInPriorityOrder(t *testing.T) {
	full := bigContext()
	fullTokens := EstimateTokens(full)
	// Trim steps, least important first. A step may only be touched once every
	// earlier step is spent down to its floor.
	order := []struct {
		key   string
		floor int
	}{
		{"source", 0}, {"memories", 0}, {"next", 0}, {"references", 0}, {"callees", 0},
		{"callers", 0}, {"test_commands", 0}, {"tests", 0}, {"definitions", 1},
	}
	totals := map[string]int{
		"source": 2, "memories": 2, "next": 1, "references": 6, "callees": 25,
		"callers": 25, "test_commands": 2, "tests": 8, "definitions": 2,
	}
	seenDropping := map[string]bool{}
	for max := fullTokens; max >= 1; max -= 7 {
		rep := bigContext()
		if err := ApplyContextBudget(rep, max); err != nil {
			t.Fatal(err)
		}
		b := rep.Budget
		spent := true // every earlier step fully spent so far
		for _, st := range order {
			d := b.Dropped[st.key]
			if d > 0 && !spent {
				t.Fatalf("max=%d: %q dropped %d before an earlier step was exhausted: %v", max, st.key, d, b.Dropped)
			}
			if d != totals[st.key]-st.floor {
				spent = false
			}
			if d > 0 {
				seenDropping[st.key] = true
			}
		}
		if b.Truncated != (len(b.Dropped) > 0) {
			t.Fatalf("truncated flag inconsistent: %+v", b)
		}
		if b.EstimatedTokens != EstimateTokens(rep) {
			t.Fatalf("max=%d estimated_tokens %d != actual %d", max, b.EstimatedTokens, EstimateTokens(rep))
		}
		if spent && len(b.Dropped) == len(order) {
			// Floor reached: identity-only payload may legitimately exceed the budget.
			continue
		}
		if b.EstimatedTokens > max {
			t.Fatalf("max=%d: trimmable material remains yet estimate=%d exceeds budget (dropped %v)", max, b.EstimatedTokens, b.Dropped)
		}
	}
	for _, st := range order {
		if !seenDropping[st.key] {
			t.Fatalf("sweep never exercised step %q", st.key)
		}
	}
}

func TestBudgetDropsSourceBeforeStructure(t *testing.T) {
	probe := bigContext()
	for i := range probe.Definitions {
		probe.Definitions[i].Source = ""
		probe.Definitions[i].SourceOmitted = true
	}
	max := EstimateTokens(probe) + 40 // fits only once the bodies are gone
	rep := bigContext()
	if err := ApplyContextBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if !b.Truncated || b.Dropped["source"] != 2 || len(b.Dropped) != 1 {
		t.Fatalf("expected only source bodies dropped, got %+v", b)
	}
	for _, d := range rep.Definitions {
		if d.Source != "" || !d.SourceOmitted {
			t.Fatalf("dropped body must be flagged source_omitted: %+v", d)
		}
	}
	if len(rep.Callers) != 25 || len(rep.Callees) != 25 || len(rep.Tests) != 8 {
		t.Fatal("structural evidence must survive while source bodies can still be dropped")
	}
}

func TestBudgetKeepsBodiesOfHighestRankedDefinitionsLongest(t *testing.T) {
	probe := bigContext()
	probe.Definitions[1].Source = ""
	probe.Definitions[1].SourceOmitted = true
	max := EstimateTokens(probe) + 60 // room for exactly the first body (budget object overhead included)
	rep := bigContext()
	if err := ApplyContextBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	if rep.Definitions[0].Source == "" || rep.Definitions[1].Source != "" || rep.Budget.Dropped["source"] != 1 {
		t.Fatalf("tail body must go first: def0=%d bytes def1=%d bytes dropped=%v",
			len(rep.Definitions[0].Source), len(rep.Definitions[1].Source), rep.Budget.Dropped)
	}
}

func TestBudgetIdentityFieldsSurviveAnyBudget(t *testing.T) {
	want := bigContext()
	rep := bigContext()
	if err := ApplyContextBudget(rep, 1); err != nil {
		t.Fatal(err)
	}
	if rep.Symbol != want.Symbol || !reflect.DeepEqual(rep.Selector, want.Selector) || rep.Project != want.Project ||
		!rep.Found || rep.CallGraph != want.CallGraph || rep.ReferencesCoverage != want.ReferencesCoverage ||
		rep.ReferencesConfidence != want.ReferencesConfidence || rep.BlastRadius != want.BlastRadius ||
		rep.CallersTotal != want.CallersTotal || rep.CalleesTotal != want.CalleesTotal ||
		rep.TestsTotal != want.TestsTotal || rep.ReferencesTotal != want.ReferencesTotal {
		t.Fatalf("identity/honesty fields changed: %+v", rep)
	}
	if len(rep.Definitions) != 1 || rep.Definitions[0].Symbol != "Hub" || rep.Definitions[0].File != "pkg/file.go" {
		t.Fatalf("the first definition's location must stay: %+v", rep.Definitions)
	}
	if rep.Budget.EstimatedTokens <= rep.Budget.MaxTokens || !rep.Budget.Truncated {
		t.Fatalf("an unreachable budget must report truncated with an honest estimate: %+v", rep.Budget)
	}
	if rep.Callers == nil || rep.Tests == nil || rep.References == nil {
		t.Fatal("emptied lists must stay [] not null")
	}
	if out := jsonOf(t, rep); !strings.Contains(out, `"callers":[]`) {
		t.Fatalf("callers must marshal as []: %s", out)
	}
}

func TestBudgetKeepsReferencesTruncatedConsistent(t *testing.T) {
	probe := bigContext()
	probe.Definitions[0].Source, probe.Definitions[1].Source = "", ""
	probe.Memories, probe.Next = nil, nil
	probe.References = probe.References[:2]
	max := EstimateTokens(probe) + 10
	rep := bigContext()
	if err := ApplyContextBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	if got := rep.ReferencesTotal - len(rep.References); rep.ReferencesTruncated != got || got <= 3 {
		t.Fatalf("references_truncated=%d, total=%d shown=%d (want truncated == total-shown, grown past the original 3)",
			rep.ReferencesTruncated, rep.ReferencesTotal, len(rep.References))
	}
	if int(rep.Budget.Dropped["references"]) != len(bigContext().References)-len(rep.References) {
		t.Fatalf("dropped references = %v", rep.Budget.Dropped)
	}
}

func TestBudgetIsDeterministic(t *testing.T) {
	max := EstimateTokens(bigContext()) / 3
	a, b := bigContext(), bigContext()
	if err := ApplyContextBudget(a, max); err != nil {
		t.Fatal(err)
	}
	if err := ApplyContextBudget(b, max); err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, a) != jsonOf(t, b) {
		t.Fatal("same input and budget must yield byte-identical output")
	}
}

func bigImpact() *ImpactReport {
	nodes := func(prefix string, n int) []ImpactNode {
		out := make([]ImpactNode, n)
		for i := range out {
			out[i] = ImpactNode{Symbol: fmt.Sprintf("%s%02d", prefix, i), Kind: "function", File: "a.go", StartLine: i + 1, Depth: 1 + i%3}
		}
		return out
	}
	callers := make([]SymbolRef, 12)
	for i := range callers {
		callers[i] = SymbolRef{Symbol: fmt.Sprintf("Direct%02d", i), File: "a.go", StartLine: i + 1}
	}
	return &ImpactReport{
		Symbol: "Hub", Selector: &SymbolSelector{File: "a.go", StartLine: 3, FQN: "a.Hub", Kind: "function"},
		Project: "demo", Found: true, Locations: []SymbolRef{{Symbol: "Hub", File: "a.go", StartLine: 3}},
		DirectCallers: callers, BlastRadius: nodes("Blast", 120), Tests: nodes("TestY", 6),
		TestCommands: []string{"go test ./..."}, CallGraph: CallGraphResolved,
		Next: []NextAction{{Tool: "codemap_risk", Why: "risky"}},
	}
}

func TestImpactBudgetTrimsBlastRadiusFirstAndKeepsIdentity(t *testing.T) {
	probe := bigImpact()
	probe.BlastRadius = probe.BlastRadius[:10]
	max := EstimateTokens(probe) + 10
	rep := bigImpact()
	if err := ApplyImpactBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if !b.Truncated || b.Dropped["next"] != 1 || b.Dropped["blast_radius"] == 0 {
		t.Fatalf("dropped = %v", b.Dropped)
	}
	if _, touched := b.Dropped["direct_callers"]; touched || len(rep.DirectCallers) != 12 || len(rep.Tests) != 6 {
		t.Fatalf("callers/tests must outlive the blast radius: %v", b.Dropped)
	}
	if rep.Symbol != "Hub" || rep.Selector == nil || rep.CallGraph != CallGraphResolved || len(rep.Locations) != 1 || !rep.Found {
		t.Fatalf("identity lost: %+v", rep)
	}
	if b.EstimatedTokens > max {
		t.Fatalf("estimate %d > max %d", b.EstimatedTokens, max)
	}
	// Tail shrink: the retained blast-radius prefix is the original prefix.
	for i, n := range rep.BlastRadius {
		if n.Symbol != fmt.Sprintf("Blast%02d", i) {
			t.Fatalf("blast_radius must shrink from the end, got %v at %d", n.Symbol, i)
		}
	}
}

func TestExploreBudgetTrimsLowestRankedFirstAndFixesNotJoined(t *testing.T) {
	mk := func() *ExploreReport {
		rep := &ExploreReport{SchemaVersion: ExploreSchemaVersion, Query: "q", Project: "demo", Indexed: true, SearchMode: "name"}
		for i := 0; i < 4; i++ {
			sel := &SymbolSelector{File: fmt.Sprintf("f%d.go", i), StartLine: 1, FQN: fmt.Sprintf("p.F%d", i), Kind: "function"}
			c := bigContext()
			c.Symbol, c.Selector = fmt.Sprintf("F%d", i), sel
			rep.Contexts = append(rep.Contexts, c)
			rep.Seeds = append(rep.Seeds, ExploreSeed{SemanticHit: SemanticHit{Symbol: c.Symbol}, Selector: sel})
		}
		rep.Seeds = append(rep.Seeds, ExploreSeed{SemanticHit: SemanticHit{Symbol: "Loose", File: "doc.md"}})
		rep.NotJoined = 1
		return rep
	}
	full := EstimateTokens(mk())
	rep := mk()
	applyExploreBudget(rep, full/2)
	b := rep.Budget
	if !b.Truncated || b.EstimatedTokens > full/2 {
		t.Fatalf("budget = %+v", b)
	}
	// The last context must have lost at least as much as the first.
	last, first := rep.Contexts[len(rep.Contexts)-1], rep.Contexts[0]
	if len(last.Callers) > len(first.Callers) || len(last.Tests) > len(first.Tests) {
		t.Fatalf("a lower-ranked context kept more than the top one: last=%d first=%d", len(last.Callers), len(first.Callers))
	}
	for i, c := range rep.Contexts {
		if c.Symbol != fmt.Sprintf("F%d", i) || c.Selector == nil || c.CallGraph != CallGraphName {
			t.Fatalf("context identity lost: %+v", c)
		}
	}

	tiny := mk()
	applyExploreBudget(tiny, 1)
	if len(tiny.Seeds) != 1 || tiny.Seeds[0].Symbol != "F0" || len(tiny.Contexts) != 0 || tiny.NotJoined != 0 {
		t.Fatalf("seeds=%d contexts=%d not_joined=%d (top seed must stay; dropped unjoined seed leaves not_joined)",
			len(tiny.Seeds), len(tiny.Contexts), tiny.NotJoined)
	}
	if tiny.Query != "q" || tiny.SchemaVersion != ExploreSchemaVersion || tiny.SearchMode != "name" {
		t.Fatalf("explore identity lost: %+v", tiny)
	}
	if tiny.Budget.Dropped["seeds"] != 4 || tiny.Budget.Dropped["contexts"] != 4 {
		t.Fatalf("dropped = %v", tiny.Budget.Dropped)
	}
}

func TestTaskContextBudgetKeepsTargetsAndFreshness(t *testing.T) {
	sel := &SymbolSelector{File: "a.go", StartLine: 3, FQN: "a.Hub", Kind: "function"}
	mk := func() *TaskContextReport {
		return &TaskContextReport{
			SchemaVersion: TaskContextSchemaVersion, Task: "t", Mode: TaskModeChange, Project: "demo", Indexed: true,
			Freshness: TaskFreshness{Checked: true, Stale: true},
			Targets:   []TaskTarget{{Selector: sel, Symbol: "Hub", Source: "selector", Found: true}},
			Explore:   &ExploreReport{SchemaVersion: 1, Query: "t", Contexts: []*ContextReport{bigContext()}, Seeds: []ExploreSeed{{Selector: sel}}},
			Contexts:  &ContextBatchReport{Project: "demo", Indexed: true, Results: []*ContextReport{bigContext()}, CommonCallers: []SymbolRef{{Symbol: "X"}}},
			Impacts:   []TaskImpact{{Selector: sel, Symbol: "Hub", Impact: bigImpact(), BlastRadiusTotal: 120}},
			RelatedFiles: []TaskRelatedFiles{{File: "a.go", RelatedTotal: 3, Related: []RelatedFile{
				{RelativePath: "b.go"}, {RelativePath: "c.go"}, {RelativePath: "d.go"}}}},
			CallGraph: CallGraphName,
			Next:      []NextAction{{Tool: "codemap_context", Why: "drill in"}},
		}
	}
	rep := mk()
	applyTaskContextBudget(rep, 1)
	if rep.Task != "t" || rep.Mode != TaskModeChange || !rep.Freshness.Stale || !rep.Freshness.Checked ||
		len(rep.Targets) != 1 || rep.Targets[0].Selector == nil || rep.CallGraph != CallGraphName {
		t.Fatalf("identity/freshness lost: %+v", rep)
	}
	if !rep.Budget.Truncated || rep.Budget.Dropped["impacts"] != 1 || rep.Budget.Dropped["related_files"] != 1 {
		t.Fatalf("dropped = %v", rep.Budget.Dropped)
	}
	if len(rep.Impacts) != 0 {
		t.Fatal("impacts should be gone at budget 1")
	}

	// Mild budget: only the earliest steps (next, related lists) may be touched.
	full := EstimateTokens(mk())
	mild := mk()
	probe := mk()
	probe.Next = nil
	applyTaskContextBudget(mild, EstimateTokens(probe)+30)
	if mild.Budget.Dropped["next"] != 1 || len(mild.Budget.Dropped) != 1 || len(mild.Impacts) != 1 {
		t.Fatalf("mild budget (full=%d) dropped = %v", full, mild.Budget.Dropped)
	}
}

func TestContextBatchAndImpactBatchBudget(t *testing.T) {
	batch := &ContextBatchReport{Project: "demo", Indexed: true, Requested: 3, CombinedBlastRadius: 231,
		Results: []*ContextReport{bigContext(), bigContext(), bigContext()}}
	if err := ApplyContextBatchBudget(batch, 1); err != nil {
		t.Fatal(err)
	}
	if batch.Requested != 3 || batch.CombinedBlastRadius != 231 || len(batch.Results) != 1 || batch.Budget.Dropped["results"] != 2 {
		t.Fatalf("batch = %+v dropped=%v", batch, batch.Budget.Dropped)
	}

	ib := &ImpactBatchReport{Project: "demo", Indexed: true, Requested: 2, Processed: 2,
		Results: []*ImpactReport{bigImpact(), bigImpact()}}
	if err := ApplyImpactBatchBudget(ib, 1); err != nil {
		t.Fatal(err)
	}
	if ib.Requested != 2 || len(ib.Results) != 1 || ib.Budget.Dropped["results"] != 1 {
		t.Fatalf("impact batch = %+v dropped=%v", ib, ib.Budget.Dropped)
	}
}

// ---- service-level tests over an indexed fixture project ----

// hubSelector returns the Hub selector from the shared task-context fixture:
// Hub has 31 callers (past the 25 cap), one source body and no tests.
func TestServiceContextMaxTokens(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	rep, err := svc.ContextBySelectorWithContext(context.Background(), root, sel, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Budget != nil {
		t.Fatal("no budget object without max_tokens")
	}
	full := EstimateTokens(rep)
	if len(rep.Callers) == 0 || rep.Definitions[0].Source == "" {
		t.Fatalf("fixture lost callers/source: %+v", rep)
	}

	tight := full / 3
	if err := ApplyContextBudget(rep, tight); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b == nil || !b.Truncated || b.MaxTokens != tight || b.EstimatedTokens > tight {
		t.Fatalf("budget = %+v (full=%d)", b, full)
	}
	if b.Dropped["source"] != 1 || b.Dropped["callers"] == 0 {
		t.Fatalf("dropped = %v", b.Dropped)
	}
	if rep.Symbol != "Hub" || rep.Selector == nil || rep.CallGraph == "" || rep.CallersTotal != 31 {
		t.Fatalf("identity/totals changed: %+v", rep)
	}
	if got := EstimateTokens(rep); got != b.EstimatedTokens {
		t.Fatalf("estimated_tokens %d != recomputed %d", b.EstimatedTokens, got)
	}
}

func TestServiceImpactMaxTokens(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	rep, err := svc.ImpactBySelector(root, sel, 3)
	if err != nil {
		t.Fatal(err)
	}
	full := EstimateTokens(rep)
	if len(rep.BlastRadius) < 30 {
		t.Fatalf("fixture blast radius = %d", len(rep.BlastRadius))
	}
	max := full / 2
	if err := ApplyImpactBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b == nil || !b.Truncated || b.EstimatedTokens > max || b.Dropped["blast_radius"] == 0 {
		t.Fatalf("budget = %+v", b)
	}
	if rep.Symbol != "Hub" || rep.Selector == nil || !rep.Found || rep.CallGraph == "" {
		t.Fatalf("identity lost: %+v", rep)
	}
}

func TestServiceExploreMaxTokens(t *testing.T) {
	svc, root, _ := taskContextFixture(t)
	opts := ExploreOptions{Seeds: 5, Edges: 20}
	base, err := svc.Explore(context.Background(), root, "Hub", opts)
	if err != nil {
		t.Fatal(err)
	}
	if base.Budget != nil || len(base.Seeds) == 0 {
		t.Fatalf("baseline = %+v", base)
	}
	full := EstimateTokens(base)
	opts.MaxTokens = full / 3
	rep, err := svc.Explore(context.Background(), root, "Hub", opts)
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b == nil || !b.Truncated || b.EstimatedTokens > opts.MaxTokens || len(b.Dropped) == 0 {
		t.Fatalf("budget = %+v (full=%d)", b, full)
	}
	if rep.Query != "Hub" || rep.SchemaVersion != ExploreSchemaVersion || len(rep.Seeds) == 0 || rep.Seeds[0].Selector == nil {
		t.Fatalf("identity lost: %+v", rep)
	}
	again, err := svc.Explore(context.Background(), root, "Hub", opts)
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, again) != jsonOf(t, rep) {
		t.Fatal("budgeted explore must be deterministic")
	}
	if _, err := svc.Explore(context.Background(), root, "Hub", ExploreOptions{MaxTokens: -1}); err == nil || CodeOf(err) != CodeInvalidInput {
		t.Fatalf("negative max_tokens = %v", err)
	}
}

func TestServiceTaskContextMaxTokens(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	opts := TaskContextOptions{Mode: TaskModeChange, Selectors: []SymbolSelector{sel}}
	base, err := svc.TaskContext(context.Background(), root, "change Hub", opts)
	if err != nil {
		t.Fatal(err)
	}
	if base.Budget != nil || len(base.Impacts) == 0 {
		t.Fatalf("baseline = %+v", base)
	}
	full := EstimateTokens(base)
	opts.MaxTokens = full / 3
	rep, err := svc.TaskContext(context.Background(), root, "change Hub", opts)
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b == nil || !b.Truncated || b.EstimatedTokens > opts.MaxTokens {
		t.Fatalf("budget = %+v (full=%d)", b, full)
	}
	if rep.SchemaVersion != TaskContextSchemaVersion || rep.Task != "change Hub" || rep.Mode != TaskModeChange ||
		!rep.Freshness.Checked || len(rep.Targets) != 1 || rep.CallGraph == "" {
		t.Fatalf("identity/freshness lost: %+v", rep)
	}
	opts.MaxTokens = -1
	if _, err := svc.TaskContext(context.Background(), root, "change Hub", opts); err == nil || CodeOf(err) != CodeInvalidInput {
		t.Fatalf("negative max_tokens = %v", err)
	}
}
