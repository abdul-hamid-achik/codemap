package app

import (
	"context"
	"fmt"
	"testing"
)

// Regression tests: impact buckets and explore processes must obey max_tokens
// and stay consistent with the lists they summarize.

// checkBucketsMatchBlast asserts every bucket node is a member of the (possibly
// trimmed) blast_radius, i.e. buckets never contradict the flat list.
func checkBucketsMatchBlast(t *testing.T, rep *ImpactReport) {
	t.Helper()
	in := map[string]bool{}
	for _, n := range rep.BlastRadius {
		in[symKey(n.FQN, n.File, n.StartLine)+n.Symbol] = true
	}
	if rep.Buckets == nil {
		return
	}
	for _, n := range append(append([]ImpactNode{}, rep.Buckets.Direct...), rep.Buckets.Transitive...) {
		if !in[symKey(n.FQN, n.File, n.StartLine)+n.Symbol] {
			t.Fatalf("bucket node %s is not in the kept blast_radius (%d nodes)", n.Symbol, len(rep.BlastRadius))
		}
	}
}

func bigImpactWithBuckets() *ImpactReport {
	rep := bigImpact()
	rep.BlastRadiusTotal = len(rep.BlastRadius)
	rep.Buckets = buildImpactBuckets(rep.BlastRadius)
	return rep
}

func TestImpactBudgetTrimsBucketsWithBlastRadius(t *testing.T) {
	full := EstimateTokens(bigImpactWithBuckets())
	rep := bigImpactWithBuckets()
	max := full / 3
	if err := ApplyImpactBudget(rep, max); err != nil {
		t.Fatal(err)
	}
	b := rep.Budget
	if b.EstimatedTokens > max {
		t.Fatalf("estimate %d > max %d: buckets escaped the budget", b.EstimatedTokens, max)
	}
	if b.Dropped["blast_radius"] == 0 || b.Dropped["buckets"] == 0 {
		t.Fatalf("dropped must account for buckets: %v", b.Dropped)
	}
	checkBucketsMatchBlast(t, rep)
	// counts stay the TRUE totals; blast_radius_total reports the true size.
	if rep.Buckets.DirectCount+rep.Buckets.TransitiveCount != 120 || rep.BlastRadiusTotal != 120 {
		t.Fatalf("totals changed: direct=%d transitive=%d total=%d", rep.Buckets.DirectCount, rep.Buckets.TransitiveCount, rep.BlastRadiusTotal)
	}
	if rep.Buckets.Direct == nil || rep.Buckets.Transitive == nil {
		t.Fatal("bucket lists must stay non-nil ([] not null)")
	}
}

func TestImpactBatchBudgetTrimsBuckets(t *testing.T) {
	ib := &ImpactBatchReport{Project: "demo", Indexed: true, Requested: 2, Processed: 2,
		Results: []*ImpactReport{bigImpactWithBuckets(), bigImpactWithBuckets()}}
	max := EstimateTokens(ib) / 4
	if err := ApplyImpactBatchBudget(ib, max); err != nil {
		t.Fatal(err)
	}
	if ib.Budget.EstimatedTokens > max {
		t.Fatalf("estimate %d > max %d", ib.Budget.EstimatedTokens, max)
	}
	if ib.Budget.Dropped["buckets"] == 0 {
		t.Fatalf("dropped = %v", ib.Budget.Dropped)
	}
	for _, r := range ib.Results {
		checkBucketsMatchBlast(t, r)
	}
}

func TestServiceImpactReportsBlastRadiusTotalAndBudgetKeepsBucketsHonest(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	rep, err := svc.ImpactBySelector(root, sel, 3)
	if err != nil {
		t.Fatal(err)
	}
	total := len(rep.BlastRadius)
	if rep.BlastRadiusTotal != total || total < 30 {
		t.Fatalf("blast_radius_total = %d, blast_radius = %d", rep.BlastRadiusTotal, total)
	}
	if err := ApplyImpactBudget(rep, EstimateTokens(rep)/4); err != nil {
		t.Fatal(err)
	}
	if rep.BlastRadiusTotal != total || rep.Buckets.DirectCount+rep.Buckets.TransitiveCount != total {
		t.Fatalf("true size lost after trimming: total=%d buckets=%+v", rep.BlastRadiusTotal, rep.Buckets)
	}
	if len(rep.BlastRadius) >= total {
		t.Fatalf("blast radius was not trimmed (%d)", len(rep.BlastRadius))
	}
	checkBucketsMatchBlast(t, rep)
}

func TestTaskContextCapsImpactBucketsLikeTheLists(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	rep, err := svc.TaskContext(context.Background(), root, "change Hub", TaskContextOptions{Mode: TaskModeChange, Selectors: []SymbolSelector{sel}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Impacts) == 0 {
		t.Fatal("no impacts")
	}
	imp := rep.Impacts[0]
	if imp.BlastRadiusTotal <= contextListCap {
		t.Fatalf("fixture must exceed the cap, blast total = %d", imp.BlastRadiusTotal)
	}
	if len(imp.Impact.BlastRadius) != contextListCap {
		t.Fatalf("blast_radius = %d, want cap %d", len(imp.Impact.BlastRadius), contextListCap)
	}
	b := imp.Impact.Buckets
	if b == nil || len(b.Direct)+len(b.Transitive) > contextListCap {
		t.Fatalf("buckets are uncapped: %+v", b)
	}
	if b.DirectCount+b.TransitiveCount != imp.BlastRadiusTotal {
		t.Fatalf("bucket counts %d+%d must be the true total %d", b.DirectCount, b.TransitiveCount, imp.BlastRadiusTotal)
	}
	checkBucketsMatchBlast(t, imp.Impact)
}

func exploreWithProcesses() *ExploreReport {
	rep := &ExploreReport{SchemaVersion: ExploreSchemaVersion, Query: "q", Project: "demo", Indexed: true, SearchMode: "name"}
	for i := 0; i < 3; i++ {
		sel := &SymbolSelector{File: fmt.Sprintf("f%d.go", i), StartLine: 1, FQN: fmt.Sprintf("p.F%d", i), Kind: "function"}
		rep.Seeds = append(rep.Seeds, ExploreSeed{SemanticHit: SemanticHit{Symbol: fmt.Sprintf("F%d", i)}, Selector: sel})
	}
	rep.Contexts = []*ContextReport{}
	for i := 0; i < 3; i++ {
		steps := make([]ProcessStep, 8)
		for j := range steps {
			steps[j] = ProcessStep{Symbol: fmt.Sprintf("S%d_%d", i, j), File: "x.go", StartLine: j + 1, Kind: "function"}
		}
		rep.Processes = append(rep.Processes, ExploreProcess{
			ID: fmt.Sprintf("http_route:R%d", i), Kind: "http_route", Name: fmt.Sprintf("R%d", i),
			Entry:        &SymbolSelector{File: "x.go", StartLine: 1, FQN: "p.entry", Kind: "function"},
			MatchedSeeds: []string{fmt.Sprintf("p.F%d", i)}, Steps: steps, CallGraph: CallGraphName,
		})
	}
	return rep
}

func TestExploreBudgetTrimsProcesses(t *testing.T) {
	full := EstimateTokens(exploreWithProcesses())
	probe := exploreWithProcesses()
	probe.Processes = probe.Processes[:1]
	max := EstimateTokens(probe) + 60
	if max >= full {
		t.Fatalf("fixture too small: %d >= %d", max, full)
	}
	rep := exploreWithProcesses()
	applyExploreBudget(rep, max)
	if rep.Budget.Dropped["processes"] != 2 || len(rep.Processes) != 1 || rep.Budget.EstimatedTokens > max {
		t.Fatalf("processes=%d dropped=%v est=%d max=%d", len(rep.Processes), rep.Budget.Dropped, rep.Budget.EstimatedTokens, max)
	}
	if rep.Processes[0].ID != "http_route:R0" {
		t.Fatalf("processes must shrink from the end, kept %s", rep.Processes[0].ID)
	}
}

func TestExploreBudgetDroppedSeedsNeverStayInMatchedSeeds(t *testing.T) {
	rep := exploreWithProcesses()
	rep.Processes[0].MatchedSeeds = []string{"p.F0", "p.F2"}
	steps := exploreBudgetSteps(rep)
	var seeds *budgetStep
	for i := range steps {
		if steps[i].key == "seeds" {
			seeds = &steps[i]
		}
	}
	if seeds == nil {
		t.Fatal("missing seeds step")
	}
	seeds.keep(1) // F0 survives, F1 and F2 are dropped
	for _, p := range rep.Processes {
		for _, m := range p.MatchedSeeds {
			if m != "p.F0" {
				t.Fatalf("process %s still names dropped seed %s", p.ID, m)
			}
		}
	}
	if len(rep.Processes) != 1 || rep.Processes[0].ID != "http_route:R0" {
		t.Fatalf("a process whose matched seeds were all dropped must go: %+v", rep.Processes)
	}
	// keep functions are order independent: restoring all seeds restores them.
	seeds.keep(3)
	if len(rep.Processes) != 3 || len(rep.Processes[0].MatchedSeeds) != 2 {
		t.Fatalf("keep(all) must restore: %d processes", len(rep.Processes))
	}
}

func TestTaskContextBudgetReachesExploreProcesses(t *testing.T) {
	sel := &SymbolSelector{File: "a.go", StartLine: 3, FQN: "a.Hub", Kind: "function"}
	mk := func() *TaskContextReport {
		return &TaskContextReport{
			SchemaVersion: TaskContextSchemaVersion, Task: "t", Mode: TaskModeUnderstand, Project: "demo", Indexed: true,
			Targets:   []TaskTarget{{Selector: sel, Symbol: "Hub", Source: "selector", Found: true}},
			Explore:   exploreWithProcesses(),
			CallGraph: CallGraphName,
		}
	}
	probe := mk()
	probe.Explore.Processes = probe.Explore.Processes[:1]
	max := EstimateTokens(probe) + 60
	rep := mk()
	applyTaskContextBudget(rep, max)
	if rep.Budget.Dropped["processes"] != 2 || rep.Budget.EstimatedTokens > max {
		t.Fatalf("dropped=%v est=%d max=%d", rep.Budget.Dropped, rep.Budget.EstimatedTokens, max)
	}
}
