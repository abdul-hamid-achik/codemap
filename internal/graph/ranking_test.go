package graph

import "testing"

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"a/b_test.go":              true,
		"src/x.test.ts":            true,
		"pkg/test_x.py":            true,
		"testdata/fixture.go":      true,
		"internal/x/testdata/a.go": true,
		"web/__tests__/a.js":       true,
		"tests/helper.py":          true,
		"internal/app/session.go":  false,
		"cmd/main.go":              false,
		"contest/main.go":          false,
		"latest.go":                false,
	} {
		if got := IsTestPath(p); got != want {
			t.Errorf("IsTestPath(%q) = %v, want %v", p, got, want)
		}
	}
	if !IsTestNode(Node{FilePath: "a.go", Kind: KindTest}) {
		t.Error("a KindTest node is test code")
	}
}

func TestRankedHotspotsEffectiveInDegree(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	add := func(file, sym, kind string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: "p." + sym, Kind: kind, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	callerA := add("a.go", "CallerA", KindFunction)
	callerB := add("b.go", "CallerB", KindFunction)
	testCaller := add("a_test.go", "TestX", KindTest)
	shared1 := add("c.go", "Close", KindMethod)
	shared2 := add("d.go", "Close", KindMethod)
	mock := add("c_test.go", "Close", KindMethod)
	unique := add("e.go", "Unique", KindFunction)
	exact := add("f.go", "Exact", KindFunction)

	for _, tgt := range []int64{shared1, shared2, mock} {
		for _, src := range []int64{callerA, callerB, testCaller} {
			if _, err := s.AddEdgeProv(src, tgt, EdgeCalls, 0.7, ProvName); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, src := range []int64{callerA, callerB, testCaller} {
		if _, err := s.AddEdgeProv(src, unique, EdgeCalls, 0.7, ProvName); err != nil {
			t.Fatal(err)
		}
	}
	// A precise edge counts fully even though "Exact" is not shared.
	if _, err := s.AddEdgeProv(callerA, exact, EdgeCalls, 1.0, ProvPrecise); err != nil {
		t.Fatal(err)
	}
	// Precise edge to a shared-name def counts fully too.
	if _, err := s.AddEdgeProv(callerB, shared1, EdgeCalls, 1.0, ProvPrecise); err != nil {
		t.Fatal(err)
	}

	got, err := s.RankedHotspots(pid, HotspotRankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]RankedHotspot{}
	for _, h := range got {
		byFile[h.Node.FilePath] = h
		if IsTestNode(h.Node) {
			t.Errorf("test node ranked by default: %+v", h.Node)
		}
	}
	// Unique: 2 non-test name callers / 1 definition.
	if h := byFile["e.go"]; h.InDegree != 2 || h.EffectiveInDegree != 2 {
		t.Errorf("Unique = %+v, want in 2 effective 2", h)
	}
	// shared1: name edges from A,B (the precise edge from B upgrades to the same
	// (source,target) pair, stored once per edge row) -> 2 name + 1 precise.
	h := byFile["c.go"]
	if h.SharedDefs != 3 || h.PreciseInDegree != 1 {
		t.Errorf("shared1 = %+v, want shared 3 / precise 1", h)
	}
	want := 1 + float64(h.NameInDegree)/3
	if h.EffectiveInDegree != want {
		t.Errorf("shared1 effective = %v, want %v", h.EffectiveInDegree, want)
	}
	if h := byFile["f.go"]; h.EffectiveInDegree != 1 {
		t.Errorf("precise-only effective = %v, want 1", h.EffectiveInDegree)
	}

	with, err := s.RankedHotspots(pid, HotspotRankOptions{IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	sawMock := false
	for _, h := range with {
		if h.Node.FilePath == "c_test.go" {
			sawMock = true
		}
		if h.Node.FilePath == "e.go" && h.InDegree != 3 {
			t.Errorf("include_tests Unique in_degree = %d, want 3", h.InDegree)
		}
	}
	if !sawMock {
		t.Error("include_tests should rank the test mock")
	}
}
