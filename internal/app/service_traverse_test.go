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
		// A Go import is package-scoped: the hop must say so instead of presenting
		// the representative file as the target.
		wantScope := ""
		if hop.EdgeType == graph.EdgeImports {
			wantScope = DependencyTargetPackage
		}
		if hop.TargetScope != wantScope {
			t.Fatalf("%s hop target_scope = %q, want %q", hop.EdgeType, hop.TargetScope, wantScope)
		}
		confidence[hop.EdgeType] = hop.Confidence
	}
	if confidence[graph.EdgeCalls] != "confirmed" || confidence[graph.EdgeImports] != "candidate" {
		t.Fatalf("domain confidence = %v", confidence)
	}
}

func TestTraverseImportHopConfidenceMatchesDependencies(t *testing.T) {
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
	pid, err := g.UpsertProject("traverse-imports", root, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	tsFile := func(file string) int64 {
		id, err := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: file, Kind: graph.KindFile, Language: "typescript",
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	src := tsFile("src/a.ts")
	resolved := tsFile("src/b.ts")
	// The import resolver writes a file→file edge only when the specifier
	// resolved to exactly one project file — the same evidence `dependencies`
	// marks confirmed.
	if _, err := g.AddEdgeProv(src, resolved, graph.EdgeImports, 1, graph.ProvName); err != nil {
		t.Fatal(err)
	}
	// An import edge to a non-file target is name-based fan-out, not a
	// resolved specifier.
	extern, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "src/a.ts", Symbol: "lodash", Kind: graph.KindModule,
		Language: "typescript",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(src, extern, graph.EdgeImports, 1, graph.ProvName); err != nil {
		t.Fatal(err)
	}
	// Traversals start from an exact symbol; the owner file's import edges are
	// seeded from it.
	_, err = g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "src/a.ts", Symbol: "App", FQN: "traverse.App",
		Kind: graph.KindFunction, Language: "typescript", StartLine: 3, EndLine: 4, SourceHash: "App",
	})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := NewService(sess).TraverseBySelector(root, SymbolSelector{
		File: "src/a.ts", StartLine: 3, FQN: "traverse.App", Kind: graph.KindFunction,
	}, TraverseOptions{Direction: graph.TraversalOutgoing, EdgeTypes: []string{graph.EdgeImports}, Depth: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || len(rep.Hops) != 2 {
		t.Fatalf("traverse report = found:%v hops:%d", rep.Found, len(rep.Hops))
	}
	byKey := map[string]TraverseHop{}
	for _, hop := range rep.Hops {
		byKey[hop.Symbol.File+"#"+hop.Symbol.Symbol] = hop
	}
	if hop := byKey["src/b.ts#"]; hop.Confidence != "confirmed" ||
		hop.ConfidenceReason != "the import specifier resolved to exactly one project file" {
		t.Fatalf("resolved import hop = %+v, want confirmed with resolved-import reason", hop)
	}
	if hop := byKey["src/a.ts#lodash"]; hop.Confidence != "candidate" {
		t.Fatalf("name-based import hop = %+v, want candidate", hop)
	}
	for _, d := range rep.Domains {
		if d.EdgeType != graph.EdgeImports {
			t.Fatalf("unexpected domain %+v", d)
		}
	}
	if rep.Domains[0].Confirmed != 1 || rep.Domains[0].Candidate != 1 {
		t.Fatalf("import domain = %+v, want 1 confirmed 1 candidate", rep.Domains[0])
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
