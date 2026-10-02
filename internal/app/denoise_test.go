package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// denoiseProj indexes a project shaped like the noisy real-world cases:
//   - Close is defined by two product types AND a test mock; a test calls Close a
//     lot (name-based fan-out credits every Close definition with every call);
//   - Unique() is a uniquely named helper with two product callers;
//   - cmd/app has a real main(), an Error method, a helper and a handler wired by
//     value (runThing, referenced in a command table but never called);
//   - bench/main.go has an auxiliary main().
func denoiseProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/dn\n\ngo 1.25\n",
		"svc/svc.go": "package svc\n\ntype A struct{}\n\nfunc (A) Close() {}\n\ntype B struct{}\n\nfunc (B) Close() {}\n\n" +
			"func Unique() {}\n\nfunc UseA(a A) { a.Close(); Unique() }\n\nfunc UseB(b B) { b.Close(); Unique() }\n",
		"svc/svc_test.go": "package svc\n\nimport \"testing\"\n\ntype mockCloser struct{}\n\nfunc (mockCloser) Close() {}\n\n" +
			"func helperForTests() { Unique() }\n\n" +
			"func TestClose(t *testing.T) {\n\tvar m mockCloser\n\tm.Close()\n\tm.Close()\n\tm.Close()\n\tm.Close()\n\tm.Close()\n\tm.Close()\n\thelperForTests()\n\thelperForTests()\n}\n",
		"cmd/app/main.go": "package main\n\ntype outcomeError struct{}\n\nfunc (outcomeError) Error() string { return \"x\" }\n\n" +
			"func helper() {}\n\nfunc main() { helper() }\n",
		"cmd/app/cmds.go": "package main\n\ntype command struct{ RunE func() }\n\nvar table = []command{{RunE: runThing}}\n\nfunc runThing() {}\n",
		"bench/main.go":   "package main\n\nfunc main() {}\n",
	}
	for name, source := range files {
		path := filepath.Join(proj, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
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

func TestHotspotsIgnoreTestsAndDivideSharedNames(t *testing.T) {
	svc, proj := denoiseProj(t)
	rep, err := svc.Hotspots(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TestsExcluded {
		t.Error("tests_excluded should be true by default")
	}
	var unique, closeA *HotspotRef
	for i := range rep.Hotspots {
		h := &rep.Hotspots[i]
		if strings.HasSuffix(h.File, "_test.go") {
			t.Errorf("test-defined symbol ranked by default: %+v", *h)
		}
		switch h.FQN {
		case "svc.Unique":
			unique = h
		case "svc.A.Close":
			closeA = h
		}
	}
	if unique == nil || closeA == nil {
		t.Fatalf("expected Unique and A.Close in hotspots: %+v", rep.Hotspots)
	}
	// Unique: 2 product callers; the test caller (helperForTests) is not counted.
	if unique.InDegree != 2 || unique.EffectiveInDegree != 2 {
		t.Errorf("Unique in_degree/effective = %d/%v, want 2/2 (test callers ignored)", unique.InDegree, unique.EffectiveInDegree)
	}
	// Close: 3 definitions share the name (A, B and the test mock); name-based
	// resolution fans UseA and UseB out to each of them, so raw=2
	// (UseA, UseB) and effective = 2/3.
	if closeA.SharedName != 3 {
		t.Errorf("A.Close shared_name = %d, want 3", closeA.SharedName)
	}
	if closeA.InDegree != 2 {
		t.Errorf("A.Close raw in_degree = %d, want 2 (test calls excluded)", closeA.InDegree)
	}
	if want := round2(2.0 / 3.0); closeA.EffectiveInDegree != want {
		t.Errorf("A.Close effective = %v, want %v", closeA.EffectiveInDegree, want)
	}
	// Unique now outranks the shared-name Close.
	if rep.Hotspots[0].FQN != "svc.Unique" {
		t.Errorf("uniquely named hub should lead, got %+v", rep.Hotspots[0])
	}
}

func TestHotspotsIncludeTestsOptIn(t *testing.T) {
	svc, proj := denoiseProj(t)
	rep, err := svc.HotspotsWith(proj, HotspotOpts{Limit: 50, IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.TestsExcluded {
		t.Error("tests_excluded should be false with IncludeTests")
	}
	var mock, unique *HotspotRef
	for i := range rep.Hotspots {
		switch rep.Hotspots[i].FQN {
		case "svc.mockCloser.Close":
			mock = &rep.Hotspots[i]
		case "svc.Unique":
			unique = &rep.Hotspots[i]
		}
	}
	if mock == nil {
		t.Fatalf("test mock should be ranked with include_tests: %+v", rep.Hotspots)
	}
	if unique == nil || unique.InDegree != 3 {
		t.Errorf("Unique raw in_degree with tests = %+v, want 3", unique)
	}
}

func TestReadOrderDenoisesTestsEntrypointsAndSharedNames(t *testing.T) {
	svc, proj := denoiseProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{Top: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TestsExcluded {
		t.Error("tests_excluded should be true by default")
	}
	var mainIdx, benchIdx = -1, -1
	for i, e := range rep.Entries {
		if strings.HasSuffix(e.File, "_test.go") {
			t.Errorf("test-defined symbol in read-order by default: %+v", e)
		}
		if e.Symbol == "Error" {
			t.Errorf("Error method must not be an entrypoint: %+v", e)
		}
		switch e.File {
		case "cmd/app/main.go":
			if e.Symbol == "main" {
				mainIdx = i
			}
		case "bench/main.go":
			benchIdx = i
			if !strings.Contains(e.Reason, "auxiliary") {
				t.Errorf("bench main reason = %q, want auxiliary", e.Reason)
			}
		}
	}
	if mainIdx != 0 {
		t.Errorf("cmd main() should lead the reading order, entries=%+v", rep.Entries)
	}
	if benchIdx < 0 || benchIdx < mainIdx {
		t.Errorf("auxiliary bench main must rank below the real main (main=%d bench=%d)", mainIdx, benchIdx)
	}
	wired := findEntry(rep.Entries, "runThing")
	if wired == nil || !wired.Entrypoint || !strings.Contains(wired.Reason, "wired handler") {
		t.Errorf("runThing should be a wired-handler entrypoint, got %+v", wired)
	}
	if h := findEntry(rep.Entries, "helper"); h != nil && h.Entrypoint {
		t.Errorf("a plain cmd/ helper is not an entrypoint: %+v", h)
	}
	// Effective in-degree is exposed and divided for the shared Close.
	for _, e := range rep.Entries {
		if e.Symbol == "Close" && e.EffectiveInDegree >= float64(e.InDegree) {
			t.Errorf("Close effective_in_degree %v should be below raw %d", e.EffectiveInDegree, e.InDegree)
		}
	}
	// The uniquely named hub must outrank the shared-name Close.
	uniq, closeIdx := -1, -1
	for i, e := range rep.Entries {
		if e.Symbol == "Unique" {
			uniq = i
		}
		if e.Symbol == "Close" && closeIdx < 0 {
			closeIdx = i
		}
	}
	if uniq < 0 || (closeIdx >= 0 && uniq > closeIdx) {
		t.Errorf("Unique (idx %d) should outrank shared-name Close (idx %d)", uniq, closeIdx)
	}

	withTests, err := svc.ReadOrder(proj, ReadOrderOpts{Top: 50, IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if withTests.TestsExcluded {
		t.Error("tests_excluded should be false with IncludeTests")
	}
	found := false
	for _, e := range withTests.Entries {
		if strings.HasSuffix(e.File, "_test.go") {
			found = true
		}
	}
	if !found {
		t.Error("include_tests should surface test-defined symbols")
	}
}

func TestArchitectureMapExcludesTestEdgesFromBridges(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/tm\n\ngo 1.25\n",
		"internal/b/b.go":      "package b\n\nfunc Fn() {}\n",
		"internal/a/a.go":      "package a\n\nimport \"example.com/tm/internal/b\"\n\nfunc A() { b.Fn() }\n",
		"internal/a/a_test.go": "package a\n\nimport (\n\t\"testing\"\n\n\t\"example.com/tm/internal/b\"\n)\n\nfunc TestA(t *testing.T) { b.Fn(); b.Fn(); b.Fn() }\n",
	}
	for name, source := range files {
		path := filepath.Join(proj, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
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
	rep, err := svc.ArchitectureMap(proj, ArchitectureMapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TestsExcluded {
		t.Error("map must report tests_excluded: true")
	}
	calls := 0
	for _, b := range rep.Bridges {
		if b.From == "internal/a" && b.To == "internal/b" && b.EdgeType == "calls" {
			calls = b.Count
			for _, f := range b.SourceFiles {
				if strings.HasSuffix(f, "_test.go") {
					t.Errorf("test file counted as bridge source: %v", b.SourceFiles)
				}
			}
		}
	}
	if calls != 1 {
		t.Errorf("internal/a -> internal/b calls = %d, want 1 (the three test calls excluded): %+v", calls, rep.Bridges)
	}
}
