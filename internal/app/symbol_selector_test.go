package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestSourceSelectorsKeepExactQueriesOnOneDefinition(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("left.go", "package app\n\ntype Left struct{}\n\nfunc (Left) Shared() { leftOnly() }\nfunc leftOnly() {}\nfunc CallLeft() { Left{}.Shared() }\n")
	write("right.go", "package app\n\ntype Right struct{}\n\nfunc (Right) Shared() { rightOnly() }\nfunc rightOnly() {}\nfunc CallRight() { Right{}.Shared() }\n")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	g, err := sess.Graph()
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
	findInFile := func(symbol, file string) graph.Node {
		t.Helper()
		nodes, findErr := g.FindNodesBySymbol(p.ID, symbol)
		if findErr != nil {
			t.Fatal(findErr)
		}
		for _, n := range nodes {
			if n.FilePath == file {
				return n
			}
		}
		t.Fatalf("%s in %s not indexed: %+v", symbol, file, nodes)
		return graph.Node{}
	}
	left := findInFile("Shared", "left.go")
	right := findInFile("Shared", "right.go")
	callLeft := findInFile("CallLeft", "left.go")
	callRight := findInFile("CallRight", "right.go")
	leftOnly := findInFile("leftOnly", "left.go")
	rightOnly := findInFile("rightOnly", "right.go")

	// Replace the parser's same-name fan-out with the exact edges a precise
	// index stores. This keeps the test independent of a local go toolchain.
	for _, source := range []graph.Node{left, right, callLeft, callRight} {
		if err := g.DeleteCallEdgesBySource([]int64{source.ID}, graph.ProvName); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range [][2]int64{
		{callLeft.ID, left.ID}, {callRight.ID, right.ID},
		{left.ID, leftOnly.ID}, {right.ID, rightOnly.ID},
	} {
		if _, err := g.AddEdgeProv(edge[0], edge[1], graph.EdgeCalls, 1, graph.ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"left.go", "right.go"} {
		if err := g.MarkCallGraphResolved(p.ID, file, "test"); err != nil {
			t.Fatal(err)
		}
	}

	merged, err := svc.Impact(proj, "Shared", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Locations) != 2 || len(merged.DirectCallers) != 2 {
		t.Fatalf("name impact should remain a backward-compatible union, got locations=%d callers=%+v", len(merged.Locations), merged.DirectCallers)
	}

	// Use a stale declaration line deliberately. file+FQN+kind is preferred,
	// so the selector survives lines inserted above the definition.
	selector := SymbolSelector{File: left.FilePath, StartLine: left.StartLine + 100, FQN: left.FQN, Kind: left.Kind}
	callers, err := svc.CallersBySelector(proj, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !callers.Found || callers.Selector == nil || callers.Selector.StartLine != left.StartLine || len(callers.Results) != 1 || callers.Results[0].Symbol != "CallLeft" {
		t.Fatalf("exact callers = %+v, want only CallLeft and canonical selector", callers)
	}
	// No nested payload is needed: a SymbolRef itself is field-compatible with
	// SymbolSelector, so an agent can project/unmarshal it directly.
	projectedJSON, err := json.Marshal(callers.Results[0])
	if err != nil {
		t.Fatal(err)
	}
	var projected SymbolSelector
	if err := json.Unmarshal(projectedJSON, &projected); err != nil {
		t.Fatal(err)
	}
	if projected.File != callLeft.FilePath || projected.StartLine != callLeft.StartLine || projected.FQN != callLeft.FQN || projected.Kind != callLeft.Kind {
		t.Fatalf("SymbolRef is not selector-compatible: ref=%+v selector=%+v", callers.Results[0], projected)
	}
	impact, err := svc.ImpactBySelector(proj, selector, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(impact.Locations) != 1 || impact.Locations[0].File != "left.go" || len(impact.DirectCallers) != 1 || impact.DirectCallers[0].Symbol != "CallLeft" {
		t.Fatalf("exact impact merged another Shared: %+v", impact)
	}
	risk, err := svc.RiskBySelector(proj, selector, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, factor := range risk.Factors {
		if factor.Factor == "ambiguous_name" {
			t.Fatalf("exact selector retained name ambiguity: %+v", risk)
		}
	}
	contextRep, err := svc.ContextBySelectorWithContext(context.Background(), proj, selector, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(contextRep.Definitions) != 1 || len(contextRep.Callers) != 1 || len(contextRep.Callees) != 1 || contextRep.Callees[0].Symbol != "leftOnly" {
		t.Fatalf("exact context = %+v", contextRep)
	}
	source, err := svc.SourceBySelector(proj, selector, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Matches) != 1 || !strings.Contains(source.Matches[0].Source, "leftOnly") || strings.Contains(source.Matches[0].Source, "rightOnly") {
		t.Fatalf("exact source = %+v", source.Matches)
	}

	path, err := svc.PathBySelectors(proj, *selectorForNode(callLeft), selector)
	if err != nil {
		t.Fatal(err)
	}
	if !path.Found || len(path.Path) != 2 || path.Path[1].File != "left.go" {
		t.Fatalf("exact path = %+v", path)
	}
	wrong, err := svc.PathBySelectors(proj, *selectorForNode(callLeft), *selectorForNode(right))
	if err != nil {
		t.Fatal(err)
	}
	if wrong.Found {
		t.Fatalf("selector path crossed into same-named right definition: %+v", wrong.Path)
	}
	qualified, err := svc.Path(proj, callLeft.FQN, left.FQN)
	if err != nil || !qualified.Found || qualified.ToSelector == nil || qualified.ToSelector.File != "left.go" {
		t.Fatalf("unique FQN path should preserve exact endpoints: %+v err=%v", qualified, err)
	}
	qualifiedWrong, err := svc.Path(proj, callLeft.FQN, right.FQN)
	if err != nil {
		t.Fatal(err)
	}
	if qualifiedWrong.Found {
		t.Fatalf("unique FQN path crossed into same-named right definition: %+v", qualifiedWrong.Path)
	}
	mixedEndpoint, err := svc.Path(proj, callLeft.FQN, "leftOnly")
	if err != nil || !mixedEndpoint.Found || len(mixedEndpoint.Path) != 3 || mixedEndpoint.FromSelector == nil || mixedEndpoint.ToSelector == nil {
		t.Fatalf("exact FQN + unique bare endpoint path = %+v err=%v", mixedEndpoint, err)
	}
}

func TestSourceSelectorWithoutFQNUsesDeclarationLine(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte("package app\n\nfunc A() {}\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SourceBySelector(proj, SymbolSelector{File: "main.go", StartLine: 4, Kind: graph.KindFunction}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Matches) != 1 || rep.Matches[0].Symbol != "B" {
		t.Fatalf("line-only selector = %+v, want B", rep.Matches)
	}
}
