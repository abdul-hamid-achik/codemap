package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImpactPositionsPartialSuccessPreservesOrder(t *testing.T) {
	svc, proj := relatedProj(t)
	rep, err := svc.ImpactPositions(proj, []FilePosition{
		{File: "a.go", Line: 3},
		{File: "a.go", Line: 999},
		{File: "a.go", Line: 2},
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || rep.Requested != 3 || rep.Processed != 3 || rep.Truncated != 0 {
		t.Fatalf("batch metadata = %+v", rep)
	}
	if len(rep.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(rep.Results))
	}
	if got := rep.Results[0]; !got.Found || got.Symbol != "Run" || got.Position == nil || got.Position.Line != 3 {
		t.Errorf("result[0] = %+v, want Run at original first position", got)
	}
	if got := rep.Results[1]; got.Found || got.CallGraph != CallGraphNone || got.Error == nil || got.Error.Code != "symbol_not_found" || got.Position == nil || got.Position.Line != 999 {
		t.Errorf("result[1] = %+v, want item-level symbol_not_found", got)
	}
	if got := rep.Results[2]; !got.Found || got.Symbol != "Helper" || got.Position == nil || got.Position.Line != 2 {
		t.Errorf("result[2] = %+v, want Helper at original third position", got)
	}
}

func TestImpactPositionsAllMissesStaySuccessful(t *testing.T) {
	svc, proj := relatedProj(t)
	rep, err := svc.ImpactPositions(proj, []FilePosition{
		{File: "missing.go", Line: 1},
		{File: "a.go", Line: 999},
	}, 3)
	if err != nil {
		t.Fatalf("ordinary frame misses must not fail the batch: %v", err)
	}
	if rep.Processed != 2 || len(rep.Results) != 2 {
		t.Fatalf("all-miss batch = %+v", rep)
	}
	for i, result := range rep.Results {
		if result.Found || result.Error == nil || result.Error.Code != "symbol_not_found" {
			t.Errorf("result[%d] = %+v, want item-level miss", i, result)
		}
	}
}

func TestImpactPositionsSingleItemUsesBatchReport(t *testing.T) {
	svc, proj := relatedProj(t)
	rep, err := svc.ImpactPositions(proj, []FilePosition{{File: "a.go", Line: 2}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requested != 1 || rep.Processed != 1 || len(rep.Results) != 1 || !rep.Results[0].Found {
		t.Fatalf("single-item batch = %+v", rep)
	}
}

func TestImpactPositionsCapsBeforeResolution(t *testing.T) {
	svc, proj := relatedProj(t)
	positions := make([]FilePosition, 30)
	for i := range positions {
		positions[i] = FilePosition{File: "a.go", Line: 2}
	}
	// Inputs after the cap are deliberately misses. They must not be resolved or
	// returned, and the metadata must make the truncation explicit.
	for i := MaxImpactBatchPositions; i < len(positions); i++ {
		positions[i] = FilePosition{File: "missing.go", Line: i + 1}
	}
	rep, err := svc.ImpactPositions(proj, positions, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requested != 30 || rep.Processed != MaxImpactBatchPositions || rep.Truncated != 5 || len(rep.Results) != MaxImpactBatchPositions || rep.Note == "" {
		t.Fatalf("capped batch = %+v", rep)
	}
	for i, result := range rep.Results {
		if !result.Found {
			t.Errorf("capped result[%d] unexpectedly resolved an input after the cap: %+v", i, result)
		}
	}
}

func TestImpactPositionsPreservesPreciseConfidence(t *testing.T) {
	svc, proj := relatedProj(t)
	pid, _, found, err := svc.project(proj)
	if err != nil || !found {
		t.Fatalf("project: found=%t err=%v", found, err)
	}
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(pid, "a.go", "test"); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.ImpactPositions(proj, []FilePosition{{File: "a.go", Line: 2}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Results[0].CallGraph; got != CallGraphResolved {
		t.Fatalf("batch call_graph = %q, want precise coverage to remain resolved", got)
	}
}

func TestImpactPositionsUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).ImpactPositions(t.TempDir(), []FilePosition{{File: "main.go", Line: 1}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Requested != 1 || rep.Processed != 0 || len(rep.Results) != 0 {
		t.Fatalf("unindexed batch = %+v", rep)
	}
}

func TestImpactBatchFreshnessTracksWorkingTree(t *testing.T) {
	svc, root := relatedProj(t)
	selectors := []SymbolSelector{{File: "a.go", StartLine: 2}}
	before, err := svc.ImpactBatch(root, selectors, 3)
	if err != nil || !before.Freshness.Checked || before.Freshness.Stale {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	path := filepath.Join(root, "a.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// changed\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := svc.ImpactBatch(root, selectors, 3)
	if err != nil || !after.Freshness.Checked || !after.Freshness.Stale || !after.Results[0].Found {
		t.Fatalf("after=%+v err=%v", after, err)
	}
}
