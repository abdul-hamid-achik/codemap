package graph

import (
	"testing"
	"time"
)

// TestCalleeClosureCycle pins P1-21 (O99): CalleeClosure must not
// hang on a cyclic call graph. Pre-fix (if the visited guard is
// dropped) the BFS loops forever; post-fix the `reached` map
// terminates. Seeds a tiny project with A→B→A and a depth that's
// large enough to loop many times.
func TestCalleeClosureCycle(t *testing.T) {
	s := openStore(t)
	pid, _ := s.UpsertProject("cycle", t.TempDir(), "go")
	// A→B, B→A (cycle), A→C (terminating branch).
	for _, def := range []struct{ from, to, fname string }{
		{"A", "B", "a.go"},
		{"B", "A", "b.go"},
		{"A", "C", "a.go"},
	} {
		fromID, _ := s.AddNode(&Node{ProjectID: pid, FilePath: def.fname, Symbol: def.from, FQN: "cycle." + def.from, Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
		toID, _ := s.AddNode(&Node{ProjectID: pid, FilePath: def.to + ".go", Symbol: def.to, FQN: "cycle." + def.to, Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
		if _, err := s.AddEdge(fromID, toID, EdgeCalls, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	// Bounded maxDepth: if cycle-safety regresses, the test would
	// take far longer (or hang). 50 is large enough to loop 25+ times
	// through the A↔B cycle.
	done := make(chan map[int64]bool, 1)
	go func() {
		got, err := s.CalleeClosure(pid, "A", 50)
		if err != nil {
			t.Errorf("CalleeClosure: %v", err)
		}
		done <- got
	}()
	select {
	case got := <-done:
		// Should reach A, B (cycle), and C (terminating). Not A's
		// 50x-scaled self-replication.
		if len(got) < 2 || len(got) > 10 {
			t.Errorf("CalleeClosure cycle: got %d nodes, want 2-10 (cycle-safe BFS reached the actual nodes, not the 50-deep tree)", len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CalleeClosure hung on a cyclic graph (P1-21 regression: visited guard missing)")
	}
}

// TestPathCycle pins P1-21 (O99): Path must not hang on a cyclic
// graph. A→B→A, ask for A→B — should return [A, B] (or just [B]
// for the partial-path heuristic; either way it must terminate and
// not return an error). The crucial test is the timeout.
func TestPathCycle(t *testing.T) {
	s := openStore(t)
	pid, _ := s.UpsertProject("pathcycle", t.TempDir(), "go")
	aID, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", FQN: "pathcycle.A", Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
	bID, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "b.go", Symbol: "B", FQN: "pathcycle.B", Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
	_, _ = s.AddEdge(aID, bID, EdgeCalls, 1.0)
	_, _ = s.AddEdge(bID, aID, EdgeCalls, 1.0)

	done := make(chan struct {
		len int
		err error
	}, 1)
	go func() {
		path, err := s.Path(pid, "A", "B", 50)
		done <- struct {
			len int
			err error
		}{len(path), err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Path: %v", res.err)
		}
		if res.len != 2 {
			t.Errorf("Path A→B on a cyclic graph returned %d nodes, want 2", res.len)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Path hung on a cyclic graph (P1-21 regression: visited guard missing)")
	}
}
