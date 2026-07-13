package app

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

func TestTraverseBySelectorPreservesDomainConfidence(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pid, err := g.UpsertProject("traverse-app", root, "go")
	if err != nil {
		t.Fatal(err)
	}
	owner := addAppTraverseFile(t, g, pid, "a.go")
	start := addAppTraverseNode(t, g, pid, "a.go", "Start", 2)
	called := addAppTraverseNode(t, g, pid, "b.go", "Called", 4)
	imported := addAppTraverseFile(t, g, pid, "c.go")
	if _, err := g.AddEdgeProv(owner, start, graph.EdgeDefines, 1, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(start, called, graph.EdgeCalls, 1, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(owner, imported, graph.EdgeImports, 0.7, graph.ProvName); err != nil {
		t.Fatal(err)
	}

	rep, err := NewService(sess).TraverseBySelector(root, SymbolSelector{
		File: "a.go", StartLine: 2, FQN: "traverse.Start", Kind: graph.KindFunction,
	}, TraverseOptions{Direction: graph.TraversalOutgoing, Depth: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || !rep.Found || rep.Start == nil || len(rep.Hops) != 2 || len(rep.Domains) != 2 {
		t.Fatalf("traverse report = %+v", rep)
	}
	confidence := map[string]string{}
	for _, hop := range rep.Hops {
		if hop.Selector == nil || hop.ParentSelector == nil || hop.ParentSelector.FQN != "traverse.Start" {
			t.Fatalf("hop selectors = %+v", hop)
		}
		if hop.EdgeType == graph.EdgeImports && hop.Symbol.Kind != graph.KindFile {
			t.Fatalf("production import must reach its file node: %+v", hop)
		}
		confidence[hop.EdgeType] = hop.Confidence
	}
	if confidence[graph.EdgeCalls] != "confirmed" || confidence[graph.EdgeImports] != "candidate" {
		t.Fatalf("domain confidence = %v", confidence)
	}
}

func addAppTraverseFile(t *testing.T, g *graph.Store, pid int64, file string) int64 {
	t.Helper()
	id, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: file, Kind: graph.KindFile, Language: "go",
		StartLine: 1, EndLine: 1, SourceHash: file,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTraverseBySelectorValidatesBoundsAndMissingStart(t *testing.T) {
	for _, opts := range []TraverseOptions{
		{Direction: "sideways"}, {EdgeTypes: []string{"magic"}},
		{Depth: MaxTraverseDepth + 1}, {Limit: MaxTraverseLimit + 1},
	} {
		if _, err := normalizeTraverseOptions(opts); err == nil {
			t.Fatalf("normalizeTraverseOptions(%+v) should fail", opts)
		}
	}

	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	rep, err := NewService(sess).TraverseBySelector(t.TempDir(), SymbolSelector{File: "none.go", StartLine: 1}, TraverseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Found || rep.Hops == nil || rep.SchemaVersion != 1 {
		t.Fatalf("missing traversal = %+v", rep)
	}
}

func addAppTraverseNode(t *testing.T, g *graph.Store, pid int64, file, symbol string, line int) int64 {
	t.Helper()
	id, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: file, Symbol: symbol, FQN: "traverse." + symbol,
		Kind: graph.KindFunction, Language: "go", StartLine: line, EndLine: line + 1, SourceHash: symbol,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
