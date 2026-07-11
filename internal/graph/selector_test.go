package graph

import "testing"

func TestNodesAtSourcePrefersFileFQNOverShiftedLine(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("selectors", t.TempDir(), "go")
	current, _ := s.AddNode(&Node{
		ProjectID: pid, FilePath: "worker.go", Symbol: "Close", FQN: "app.Worker.Close",
		Kind: KindMethod, Language: "go", StartLine: 18, EndLine: 20, SourceHash: "h",
	})
	_, _ = s.AddNode(&Node{
		ProjectID: pid, FilePath: "worker.go", Symbol: "Open", FQN: "app.Worker.Open",
		Kind: KindMethod, Language: "go", StartLine: 9, EndLine: 12, SourceHash: "h",
	})

	// The selector was emitted before lines were inserted above Close. FQN+kind
	// is the durable identity; the old line is only a tie-break.
	got, err := s.NodesAtSource(pid, "worker.go", 7, "app.Worker.Close", KindMethod)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != current {
		t.Fatalf("shifted selector resolved %+v, want node %d", got, current)
	}
	if byLine, err := s.NodesAtSource(pid, "worker.go", 9, "", KindMethod); err != nil || len(byLine) != 1 || byLine[0].Symbol != "Open" {
		t.Fatalf("line-only selector = %+v err=%v, want Open", byLine, err)
	}
}

func TestExactNodeQueriesDoNotUnionSameNamedDefinitions(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("exact", t.TempDir(), "go")
	add := func(file, symbol, fqn string, line int) int64 {
		t.Helper()
		id, err := s.AddNode(&Node{
			ProjectID: pid, FilePath: file, Symbol: symbol, FQN: fqn,
			Kind: KindFunction, Language: "go", StartLine: line, EndLine: line, SourceHash: fqn,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	left := add("left.go", "Shared", "left.Shared", 5)
	right := add("right.go", "Shared", "right.Shared", 7)
	callerLeft := add("a.go", "CallLeft", "app.CallLeft", 3)
	callerRight := add("b.go", "CallRight", "app.CallRight", 3)
	leaf := add("leaf.go", "Leaf", "app.Leaf", 2)
	for _, edge := range [][2]int64{{callerLeft, left}, {callerRight, right}, {left, leaf}} {
		if _, err := s.AddEdgeProv(edge[0], edge[1], EdgeCalls, 1, ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}

	merged, err := s.Callers(pid, "Shared")
	if err != nil || len(merged) != 2 {
		t.Fatalf("name callers = %d err=%v, want merged 2", len(merged), err)
	}
	exact, err := s.CallersOfNode(pid, left)
	if err != nil || len(exact) != 1 || exact[0].ID != callerLeft {
		t.Fatalf("exact callers = %+v err=%v, want CallLeft", exact, err)
	}
	callees, err := s.CalleesOfNode(pid, left)
	if err != nil || len(callees) != 1 || callees[0].ID != leaf {
		t.Fatalf("exact callees = %+v err=%v, want Leaf", callees, err)
	}
	radius, err := s.BlastRadiusFromNode(pid, left, 3)
	if err != nil || len(radius) != 1 || radius[0].Node.ID != callerLeft {
		t.Fatalf("exact blast radius = %+v err=%v, want CallLeft", radius, err)
	}
	path, err := s.PathFromNodes(pid, callerLeft, left, 0)
	if err != nil || len(path) != 2 || path[1].ID != left {
		t.Fatalf("exact path = %+v err=%v", path, err)
	}
	if noPath, err := s.PathFromNodes(pid, callerLeft, right, 0); err != nil || len(noPath) != 0 {
		t.Fatalf("cross-definition path = %+v err=%v, want none", noPath, err)
	}
}
