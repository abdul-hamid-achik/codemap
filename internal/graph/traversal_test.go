package graph

import (
	"fmt"
	"testing"
)

func TestTraverseFromNodeIsTypedProvenancedAndCycleSafe(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("traverse", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	file := addTraversalNode(t, s, pid, "a.go", "", KindFile)
	a := addTraversalNode(t, s, pid, "a.go", "A", KindFunction)
	b := addTraversalNode(t, s, pid, "b.go", "B", KindFunction)
	c := addTraversalNode(t, s, pid, "c.go", "C", KindFunction)
	for _, edge := range []struct {
		from, to int64
		kind     string
		prov     string
	}{
		{file, a, EdgeDefines, ProvPrecise},
		{a, b, EdgeCalls, ProvPrecise},
		{b, c, EdgeReferences, ProvName},
		{c, a, EdgeCalls, ProvName},
	} {
		if _, err := s.AddEdgeProv(edge.from, edge.to, edge.kind, 1, edge.prov); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := s.TraverseFromNode(pid, a, TraversalOptions{Direction: TraversalOutgoing, MaxDepth: 5})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Start.ID != a || rep.Truncated || len(rep.Steps) != 2 {
		t.Fatalf("cycle-safe traversal = %+v", rep)
	}
	if rep.Steps[0].Node.ID != b || rep.Steps[0].Edge.EdgeType != EdgeCalls || rep.Steps[0].Edge.Provenance != ProvPrecise || rep.Steps[0].Depth != 1 {
		t.Errorf("first step = %+v", rep.Steps[0])
	}
	if rep.Steps[1].Node.ID != c || rep.Steps[1].Edge.EdgeType != EdgeReferences || rep.Steps[1].Depth != 2 {
		t.Errorf("second step = %+v", rep.Steps[1])
	}
	for _, step := range rep.Steps {
		if step.Node.ID == file {
			t.Fatal("defines should not be traversed by default")
		}
	}
}

func TestTraverseFromNodeDirectionFilterBoundsAndProjectScope(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("one", t.TempDir(), "go")
	foreignPID, _ := s.UpsertProject("two", t.TempDir(), "go")
	a := addTraversalNode(t, s, pid, "a.go", "A", KindFunction)
	b := addTraversalNode(t, s, pid, "b.go", "B", KindFunction)
	c := addTraversalNode(t, s, pid, "c.go", "C", KindFunction)
	foreign := addTraversalNode(t, s, foreignPID, "x.go", "X", KindFunction)
	_, _ = s.AddEdgeProv(b, a, EdgeCalls, 1, ProvPrecise)
	_, _ = s.AddEdgeProv(c, a, EdgeImports, 0.7, ProvName)
	_, _ = s.AddEdgeProv(foreign, a, EdgeCalls, 1, ProvPrecise)

	rep, err := s.TraverseFromNode(pid, a, TraversalOptions{
		Direction: TraversalIncoming, EdgeTypes: []string{EdgeCalls}, MaxDepth: 1, MaxNodes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Steps) != 1 || rep.Steps[0].Node.ID != b || rep.Steps[0].Direction != TraversalIncoming {
		t.Fatalf("filtered incoming traversal = %+v", rep)
	}
	if rep.Truncated {
		t.Fatal("foreign/import edges must not make the one matching result truncated")
	}

	if _, err := normalizeTraversalOptions(TraversalOptions{Direction: "sideways"}); err == nil {
		t.Fatal("invalid direction should fail")
	}
}

func TestTraverseFromSymbolSeedsOwnerFileImports(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("imports", t.TempDir(), "go")
	owner := addTraversalNode(t, s, pid, "a.go", "", KindFile)
	start := addTraversalNode(t, s, pid, "a.go", "Start", KindFunction)
	imported := addTraversalNode(t, s, pid, "b.go", "", KindFile)
	_, _ = s.AddEdgeProv(owner, start, EdgeDefines, 1, ProvPrecise)
	_, _ = s.AddEdgeProv(owner, imported, EdgeImports, WeightLSP, ProvName)

	rep, err := s.TraverseFromNode(pid, start, TraversalOptions{
		Direction: TraversalOutgoing, EdgeTypes: []string{EdgeImports}, MaxDepth: 1, MaxNodes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Truncated || len(rep.Steps) != 1 {
		t.Fatalf("owner-file import traversal = %+v", rep)
	}
	step := rep.Steps[0]
	if step.Node.ID != imported || step.Node.Kind != KindFile || step.ParentID != start || step.Edge.SourceID != owner || step.Edge.EdgeType != EdgeImports {
		t.Fatalf("import step = %+v", step)
	}
}

func TestTraverseFromNodeBoundsHighDegreeWorkAndOutput(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("high-degree", t.TempDir(), "go")
	start := addTraversalNode(t, s, pid, "hub.go", "Hub", KindFunction)
	tx, err := s.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < 10_000; i++ {
		n := &Node{
			ProjectID: pid, FilePath: fmt.Sprintf("leaf/%05d.go", i), Symbol: fmt.Sprintf("Leaf%d", i),
			FQN: fmt.Sprintf("high.Leaf%d", i), Kind: KindFunction, Language: "go",
			StartLine: 1, EndLine: 2, SourceHash: "h",
		}
		id, err := AddNodeTx(tx, n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AddEdgeProvTx(tx, id, start, EdgeCalls, WeightLSP, ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rep, err := s.TraverseFromNode(pid, start, TraversalOptions{
		Direction: TraversalIncoming, EdgeTypes: []string{EdgeCalls}, MaxDepth: 1, MaxNodes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Truncated || len(rep.Steps) != 100 {
		t.Fatalf("bounded hub traversal: steps=%d truncated=%t", len(rep.Steps), rep.Truncated)
	}
	if rep.Steps[0].Node.FilePath != "leaf/00000.go" || rep.Steps[99].Node.FilePath != "leaf/00099.go" {
		t.Fatalf("deterministic bounded prefix = %s ... %s", rep.Steps[0].Node.FilePath, rep.Steps[99].Node.FilePath)
	}
}

func addTraversalNode(t *testing.T, s *Store, pid int64, file, symbol, kind string) int64 {
	t.Helper()
	id, err := s.AddNode(&Node{
		ProjectID: pid, FilePath: file, Symbol: symbol, FQN: "traverse." + symbol,
		Kind: kind, Language: "go", StartLine: 1, EndLine: 2, SourceHash: file + symbol,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
