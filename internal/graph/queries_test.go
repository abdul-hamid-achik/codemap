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

// TestCalleeClosureDefaultsMaxDepth pins P3-04 (O21): a caller
// passing maxDepth=0 (or a negative) must NOT silently get the
// "0-hop closure" — which is just the start nodes, and produces
// a confident-wrong answer for any agent that reads the result
// as "the blast radius of this function." Without the default,
// SecretImpact and risk calls would report a tiny reachable set
// instead of the truth. The default matches BlastRadius (3 hops).
func TestCalleeClosureDefaultsMaxDepth(t *testing.T) {
	s := openStore(t)
	pid, _ := s.UpsertProject("defaults", t.TempDir(), "go")
	// Chain A→B→C→D (4 nodes, 3 edges). The test reaches
	// "B" and "C" via startNodeIDs(symbol="...") — a single
	// start each. With maxDepth=0/negative, the default
	// (3) must walk all 3 hops and reach all 4 nodes.
	type node struct {
		sym, file string
	}
	for _, n := range []node{{"A", "a.go"}, {"B", "b.go"}, {"C", "c.go"}, {"D", "d.go"}} {
		_, _ = s.AddNode(&Node{ProjectID: pid, FilePath: n.file, Symbol: n.sym, FQN: "defaults." + n.sym, Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
	}
	for _, def := range []struct{ from, to string }{
		{"A", "B"}, {"B", "C"}, {"C", "D"},
	} {
		var fromID, toID int64
		_ = s.db.QueryRow("SELECT id FROM nodes WHERE project_id=? AND symbol=?", pid, def.from).Scan(&fromID)
		_ = s.db.QueryRow("SELECT id FROM nodes WHERE project_id=? AND symbol=?", pid, def.to).Scan(&toID)
		if _, err := s.AddEdge(fromID, toID, EdgeCalls, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	for _, maxDepth := range []int{0, -1, -100} {
		got, err := s.CalleeClosure(pid, "A", maxDepth)
		if err != nil {
			t.Fatalf("CalleeClosure(A, maxDepth=%d): %v", maxDepth, err)
		}
		if len(got) != 4 {
			t.Errorf("CalleeClosure(A, maxDepth=%d) reached %d nodes, want 4 (A, B, C, D — the 0/negative default must NOT be 0 hops; pin P3-04 O21)", maxDepth, len(got))
		}
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
