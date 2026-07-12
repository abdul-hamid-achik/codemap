package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func contextBatchProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	// Shared() calls both A and B, so it's a common caller of the {A,B} batch.
	src := "package app\n\nfunc A() {}\n\nfunc B() {}\n\nfunc Shared() {\n\tA()\n\tB()\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func TestContextBatchSharedCaller(t *testing.T) {
	svc, proj := contextBatchProj(t)
	rep, err := svc.ContextBatch(proj, []string{"A", "B", "DoesNotExist"}, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requested != 3 || len(rep.Results) != 3 {
		t.Fatalf("expected 3 requested + 3 results, got requested=%d results=%d", rep.Requested, len(rep.Results))
	}
	if !contains(rep.NotFound, "DoesNotExist") {
		t.Errorf("DoesNotExist should be in not_found, got %v", rep.NotFound)
	}
	// Shared() calls both A and B → it is a common caller.
	if !hasSymbol(rep.CommonCallers, "Shared") {
		t.Errorf("Shared should be a common caller of {A,B}, got %+v", rep.CommonCallers)
	}
	if rep.CombinedBlastRadius <= 0 {
		t.Errorf("combined blast radius should be > 0 for two called symbols, got %d", rep.CombinedBlastRadius)
	}
}

func TestContextBatchDedupAndUnindexed(t *testing.T) {
	svc, proj := contextBatchProj(t)
	// Duplicate symbols collapse to one result.
	rep, err := svc.ContextBatch(proj, []string{"A", "A", ""}, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Errorf("duplicate + blank symbols should dedup to 1 result, got %d", len(rep.Results))
	}

	isolate(t)
	sess, _ := Open("")
	defer sess.Close()
	un, err := NewService(sess).ContextBatch(t.TempDir(), []string{"X"}, nil, 3)
	if err != nil {
		t.Fatalf("unindexed must not error: %v", err)
	}
	if un.Indexed {
		t.Errorf("unindexed → indexed:false, got %+v", un)
	}
}

func TestContextBatchCapsAt25(t *testing.T) {
	svc, proj := contextBatchProj(t)
	syms := make([]string, 30) // mostly nonexistent — cheap, they land in not_found
	for i := range syms {
		syms[i] = fmt.Sprintf("Sym%d", i)
	}
	rep, err := svc.ContextBatch(proj, syms, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) > contextBatchMax {
		t.Errorf("batch should cap results at %d, got %d", contextBatchMax, len(rep.Results))
	}
	if !strings.Contains(rep.Note, "analyzed the first") {
		t.Errorf("a >25 batch should note the elision, got %q", rep.Note)
	}
}

// selectorForSymbol resolves symbol to its durable source selector, using the
// graph directly — the same projection an agent would build from a prior
// find/symbols/context result.
func selectorForSymbol(t *testing.T, svc *Service, proj, symbol string) SymbolSelector {
	t.Helper()
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	_, projectName, err := svc.resolveProject(proj)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(projectName)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatalf("%s not indexed in %s", symbol, proj)
	}
	return *selectorForNode(nodes[0])
}

// TestContextBatchSelectorsUnionAndDedup pins design decision #6: selectors is
// a UNION input, unioned with (not cross-deduped against) symbols, while
// duplicate selectors within the selectors list collapse to one item.
func TestContextBatchSelectorsUnionAndDedup(t *testing.T) {
	svc, proj := contextBatchProj(t)
	selForA := selectorForSymbol(t, svc, proj, "A")
	selForB := selectorForSymbol(t, svc, proj, "B")

	rep, err := svc.ContextBatch(proj, []string{"A"}, []SymbolSelector{selForA, selForA, selForB}, 3)
	if err != nil {
		t.Fatal(err)
	}
	// The duplicated selForA collapses to one selector item; name "A" + selector
	// A do NOT cross-dedup (documented limitation), so both appear alongside
	// selector B: 1 name item + 2 selector items = 3.
	if rep.Requested != 3 {
		t.Fatalf("Requested = %d, want 3 (name A + selector A + selector B, deduped within each list only)", rep.Requested)
	}
	if len(rep.Results) != 3 {
		t.Fatalf("Results = %d, want 3, got %+v", len(rep.Results), rep.Results)
	}
	var nameA, selA, selB int
	for _, r := range rep.Results {
		switch {
		case r.Symbol == "A" && r.Selector == nil:
			nameA++
		case r.Symbol == "A" && r.Selector != nil:
			selA++
		case r.Symbol == "B" && r.Selector != nil:
			selB++
		}
	}
	if nameA != 1 || selA != 1 || selB != 1 {
		t.Fatalf("expected one name-path A, one selector-path A, one selector-path B; got nameA=%d selA=%d selB=%d results=%+v", nameA, selA, selB, rep.Results)
	}
}

// TestContextBatchSelectorsCap pins that the combined symbols+selectors length
// (after per-list dedup, before the cap) is what contextBatchMax bounds.
func TestContextBatchSelectorsCap(t *testing.T) {
	svc, proj := contextBatchProj(t)
	syms := make([]string, 15)
	for i := range syms {
		syms[i] = fmt.Sprintf("Sym%d", i)
	}
	selectors := make([]SymbolSelector, 15)
	for i := range selectors {
		selectors[i] = SymbolSelector{File: fmt.Sprintf("nofile%d.go", i), StartLine: 1}
	}
	rep, err := svc.ContextBatch(proj, syms, selectors, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requested != 30 {
		t.Errorf("Requested should reflect the full deduped union before capping, got %d", rep.Requested)
	}
	if len(rep.Results) > contextBatchMax {
		t.Errorf("batch should cap results at %d, got %d", contextBatchMax, len(rep.Results))
	}
	if !strings.Contains(rep.Note, "analyzed the first") {
		t.Errorf("a >25 combined batch should note the elision, got %q", rep.Note)
	}
}

// TestContextBatchMalformedSelectorIsPartialNotFatal pins the riskiest edit in
// this plan (design decision #7): one bad selector in the batch must land as a
// partial_errors + not_found entry, never abort the whole call — mirroring the
// existing "never errors on a bad individual input" contract that name-only
// batches already honor.
func TestContextBatchMalformedSelectorIsPartialNotFatal(t *testing.T) {
	svc, proj := contextBatchProj(t)
	selForA := selectorForSymbol(t, svc, proj, "A")
	// Neither a positive start_line nor an fqn: resolveSourceSelector rejects
	// this before ever touching the graph ("selector needs a positive
	// start_line or an fqn").
	bad := SymbolSelector{File: "a.go"}

	rep, err := svc.ContextBatch(proj, nil, []SymbolSelector{selForA, bad}, 3)
	if err != nil {
		t.Fatalf("a malformed selector must not fail the whole batch: %v", err)
	}
	if len(rep.Results) != 1 || rep.Results[0].Symbol != "A" {
		t.Fatalf("the valid selector item should still resolve, got %+v", rep.Results)
	}
	if len(rep.PartialErrors) != 1 || rep.PartialErrors[0].Component != "selector" || rep.PartialErrors[0].Error == "" {
		t.Fatalf("malformed selector should land in partial_errors with component=selector, got %+v", rep.PartialErrors)
	}
	if !contains(rep.NotFound, bad.File+":0") {
		t.Fatalf("malformed selector should also land in not_found (as file:line), got %v", rep.NotFound)
	}
}
