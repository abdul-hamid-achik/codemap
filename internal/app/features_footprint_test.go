package app

import (
	"fmt"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

type fpBuilder struct {
	nodes []graph.Node
	edges []graph.Edge
}

func (b *fpBuilder) fn(file, symbol string) int64 {
	id := int64(len(b.nodes) + 1)
	b.nodes = append(b.nodes, graph.Node{ID: id, FilePath: file, Symbol: symbol, FQN: "pkg." + symbol, Kind: graph.KindFunction, Language: "go"})
	return id
}

func (b *fpBuilder) test(file, symbol string) int64 {
	id := int64(len(b.nodes) + 1)
	b.nodes = append(b.nodes, graph.Node{ID: id, FilePath: file, Symbol: symbol, FQN: "pkg." + symbol, Kind: graph.KindTest, Language: "go"})
	return id
}

func (b *fpBuilder) call(from, to int64, prov string) {
	b.edges = append(b.edges, graph.Edge{ID: int64(len(b.edges) + 1), SourceID: from, TargetID: to, EdgeType: graph.EdgeCalls, Provenance: prov})
}

func (b *fpBuilder) graph() *footprintGraph { return newFootprintGraph(b.nodes, b.edges) }

func TestFootprintWalkHandlesCyclesAndDepth(t *testing.T) {
	b := &fpBuilder{}
	a, c, d, e := b.fn("p/a.go", "A"), b.fn("p/b.go", "B"), b.fn("p/c.go", "C"), b.fn("p/d.go", "D")
	b.call(a, c, graph.ProvName)
	b.call(c, d, graph.ProvName)
	b.call(d, a, graph.ProvName) // cycle back
	b.call(d, e, graph.ProvName)
	fg := b.graph()

	fp, visited := fg.walk([]int64{a}, 3)
	if fp.Symbols != 4 || len(visited) != 4 || fp.Files != 4 {
		t.Fatalf("depth 3 = %+v", fp)
	}
	fp, _ = fg.walk([]int64{a}, 2)
	if fp.Symbols != 3 {
		t.Fatalf("depth 2 should stop before D->E, got %+v", fp)
	}
	if fp2, _ := fg.walk([]int64{a}, 3); len(fp2.Subsystems) == 0 || fp2.Subsystems[0].Name != "p" {
		t.Fatalf("subsystems missing: %+v", fp2)
	}
}

func TestFootprintWalkCapsNodes(t *testing.T) {
	b := &fpBuilder{}
	root := b.fn("p/root.go", "Root")
	for i := 0; i < featureFootprintNodeCap+50; i++ {
		b.call(root, b.fn(fmt.Sprintf("p/f%03d.go", i), fmt.Sprintf("F%03d", i)), graph.ProvName)
	}
	fp, visited := b.graph().walk([]int64{root}, 3)
	if fp.Symbols != featureFootprintNodeCap || len(visited) != featureFootprintNodeCap || !fp.Truncated {
		t.Fatalf("cap = %+v", fp)
	}
	fp2, _ := b.graph().walk([]int64{root}, 3)
	if fp.Symbols != fp2.Symbols {
		t.Fatal("capped walk must be deterministic")
	}
}

func TestFootprintNameAmbiguityRules(t *testing.T) {
	b := &fpBuilder{}
	caller := b.fn("internal/app/run.go", "Run")
	sameDir := b.fn("internal/app/close.go", "Close")
	otherPkg := b.fn("internal/graph/close.go", "Close")
	b.call(caller, sameDir, graph.ProvName)
	b.call(caller, otherPkg, graph.ProvName)
	fp, visited := b.graph().walk([]int64{caller}, 3)
	if fp.Symbols != 2 || fp.AmbiguousEdges != 1 {
		t.Fatalf("same-directory candidate should win: %+v", fp)
	}
	for _, id := range visited {
		if id == otherPkg {
			t.Fatal("followed the cross-package candidate")
		}
	}

	// No same-dir candidate, but exactly one in the caller's subsystem.
	b = &fpBuilder{}
	caller = b.fn("internal/app/run.go", "Run")
	inSub := b.fn("internal/app/sub/close.go", "Close")
	elsewhere := b.fn("internal/graph/close.go", "Close")
	b.call(caller, inSub, graph.ProvName)
	b.call(caller, elsewhere, graph.ProvName)
	fp, _ = b.graph().walk([]int64{caller}, 3)
	if fp.Symbols != 2 || fp.AmbiguousEdges != 1 {
		t.Fatalf("subsystem candidate should win: %+v", fp)
	}

	// Two candidates in the caller's directory and none decisive: skip both.
	b = &fpBuilder{}
	caller = b.fn("cmd/x/run.go", "Run")
	c1 := b.fn("cmd/x/a.go", "Close")
	c2 := b.fn("cmd/x/b.go", "Close")
	b.call(caller, c1, graph.ProvName)
	b.call(caller, c2, graph.ProvName)
	fp, _ = b.graph().walk([]int64{caller}, 3)
	if fp.Symbols != 1 || fp.AmbiguousEdges != 2 {
		t.Fatalf("undecidable name must not be followed: %+v", fp)
	}

	// Precise edges are always followed, even to a shared name.
	b = &fpBuilder{}
	caller = b.fn("cmd/x/run.go", "Run")
	c1 = b.fn("cmd/x/a.go", "Close")
	c2 = b.fn("cmd/x/b.go", "Close")
	b.call(caller, c1, graph.ProvPrecise)
	b.call(caller, c2, graph.ProvPrecise)
	fp, _ = b.graph().walk([]int64{caller}, 3)
	if fp.Symbols != 3 || fp.AmbiguousEdges != 0 {
		t.Fatalf("precise edges must be followed: %+v", fp)
	}
}

func TestFootprintSkipsTestsAndCountsCoveringTests(t *testing.T) {
	b := &fpBuilder{}
	h := b.fn("p/h.go", "Handler")
	w := b.fn("p/w.go", "Work")
	tn := b.test("p/h_test.go", "TestHandler")
	tw := b.test("p/w_test.go", "TestWork")
	b.fn("p/h_test.go", "helperInTestFile")
	other := b.fn("q/o.go", "Other")
	b.call(h, w, graph.ProvName)
	b.call(h, tn, graph.ProvName) // production code never "calls" a test, but must not enter the footprint
	b.call(tn, h, graph.ProvName)
	b.call(tn, w, graph.ProvName)
	b.call(tw, w, graph.ProvName)
	b.call(tw, other, graph.ProvName)
	fp, visited := b.graph().walk([]int64{h}, 3)
	if fp.Symbols != 2 || len(visited) != 2 {
		t.Fatalf("test nodes must not enter the footprint: %+v", fp)
	}
	if fp.Tests != 2 {
		t.Fatalf("distinct covering tests = %d, want 2 (TestHandler, TestWork)", fp.Tests)
	}
}

func TestFootprintDuplicateFQNDoesNotInheritTwinCalls(t *testing.T) {
	// Every `main.main` of a multi-binary repo shares one FQN, so the indexer
	// attributes all of their calls to one node. The walk must not follow a
	// twin's edges into the twin's own directory.
	b := &fpBuilder{}
	mainA := b.fn("cmd/a/main.go", "main")
	mainB := b.fn("cmd/b/main.go", "main")
	b.nodes[mainA-1].FQN, b.nodes[mainB-1].FQN = "main.main", "main.main"
	ownHelper := b.fn("cmd/a/util.go", "utilA")
	twinHelper := b.fn("cmd/b/util.go", "utilB")
	shared := b.fn("internal/lib/lib.go", "Lib")
	b.call(mainA, ownHelper, graph.ProvName)
	b.call(mainA, twinHelper, graph.ProvName) // really mainB's call
	b.call(mainA, shared, graph.ProvName)
	fp, visited := b.graph().walk([]int64{mainA}, 3)
	if fp.Symbols != 3 || fp.AmbiguousEdges != 1 {
		t.Fatalf("twin territory leaked: %+v visited=%v", fp, visited)
	}
}

func TestFeaturesResolutionStrings(t *testing.T) {
	if got := featuresResolution(CallGraphUnresolved, []graph.Node{{Language: "typescript"}}, false); got == "" {
		t.Error("unresolved call graph needs a resolution sentence")
	}
	if got := featuresResolution(CallGraphName, nil, false); got == "" {
		t.Error("name-based call graph needs a resolution sentence")
	}
	if got := featuresResolution(CallGraphName, nil, true); got != "" {
		t.Errorf("no footprint, no caveat: %q", got)
	}
	if got := featuresResolution(CallGraphResolved, nil, false); got != "" {
		t.Errorf("resolved needs no caveat: %q", got)
	}
}
