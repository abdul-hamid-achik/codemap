package index

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// preciseFixture: three callers all invoke Run() on the SAME concrete type T1,
// while T2 and T3 also declare a Run(). Name-based resolution fans each caller's
// edge out to all three same-named methods, spuriously inflating T2.Run and
// T3.Run to in-degree 3; precise resolution routes all three to T1.Run only.
const preciseFixture = `package fix

type T1 struct{}
type T2 struct{}
type T3 struct{}

func (T1) Run() {}
func (T2) Run() {}
func (T3) Run() {}

func A() { var t T1; t.Run() }
func B() { var t T1; t.Run() }
func C() { var t T1; t.Run() }
`

// inDegree counts incoming `calls` edges to the node with the given FQN.
func inDegree(t *testing.T, g *graph.Store, pid int64, symbol, fqn string) int {
	t.Helper()
	nodes, err := g.FindNodesBySymbol(pid, symbol)
	if err != nil {
		t.Fatal(err)
	}
	var id int64 = -1
	for _, n := range nodes {
		if n.FQN == fqn {
			id = n.ID
		}
	}
	if id < 0 {
		t.Fatalf("no node with FQN %q (have %d %q nodes)", fqn, len(nodes), symbol)
	}
	var c int
	if err := g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls'", id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPreciseCollapsesNameFanout(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fix\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.go"), []byte(preciseFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, err := g.UpsertProject("fix", dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	// Name-based baseline: every Run method is spuriously credited with all three
	// callers (the over-matching this epic eliminates).
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T1.Run"); got != 3 {
		t.Errorf("name-based T1.Run in-degree = %d, want 3", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 3 {
		t.Errorf("name-based T2.Run in-degree = %d, want 3 (spurious fan-out baseline)", got)
	}

	// Precise pass: the three calls resolve to T1.Run alone; T2/T3 deflate to 0.
	res, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Reindex: true, Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseNote != "" {
		t.Fatalf("precise pass should have run, got note: %q", res.PreciseNote)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatal("PreciseUpgraded should be > 0 (a silent inert run means the join missed)")
	}
	if got := inDegree(t, g, pid, "Run", "fix.T1.Run"); got != 3 {
		t.Errorf("precise T1.Run in-degree = %d, want 3 (real callers preserved)", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 0 {
		t.Errorf("precise T2.Run in-degree = %d, want 0 (spurious fan-out eliminated)", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T3.Run"); got != 0 {
		t.Errorf("precise T3.Run in-degree = %d, want 0", got)
	}

	// Double-counting guard: T1.Run must have exactly 3 calls edges total (one per
	// caller), not 6 — i.e. the name edges were deleted before precise re-insert,
	// not left to coexist with the precise 1.0 edges.
	nodes, _ := g.FindNodesBySymbol(pid, "Run")
	var t1 int64 = -1
	for _, n := range nodes {
		if n.FQN == "fix.T1.Run" {
			t1 = n.ID
		}
	}
	var total int
	if err := g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls'", t1).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("T1.Run total calls edges = %d, want 3 (no name/precise double-count)", total)
	}
}

func TestPreciseResolvesTestCallers(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/fix\n\ngo 1.25\n")
	mustWrite("fix.go", `package fix

type T1 struct{}
type T2 struct{}

func (T1) Run() {}
func (T2) Run() {}

func Prod() { var t T1; t.Run() }
`)
	// An in-package test file caller: name-based fans its t.Run() to both T1.Run
	// and T2.Run; Tests:true precise must resolve it to T1.Run alone.
	mustWrite("fix_test.go", `package fix

func useInTest() { var t T1; t.Run() }
`)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("fix", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	// Name-based: Prod + useInTest each fan to both Run methods -> T2.Run = 2.
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 2 {
		t.Errorf("name-based T2.Run in-degree = %d, want 2 (Prod + test caller fan-out)", got)
	}

	// Precise (Tests:true): both callers, including the _test.go one, resolve to
	// T1.Run, so T2.Run deflates to 0 and T1.Run holds both callers.
	res, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Reindex: true, Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatalf("expected precise upgrades incl. the test caller, got note %q", res.PreciseNote)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 0 {
		t.Errorf("precise T2.Run in-degree = %d, want 0 (test caller resolved, not fanned)", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T1.Run"); got != 2 {
		t.Errorf("precise T1.Run in-degree = %d, want 2 (Prod + test caller)", got)
	}
}

func TestPreciseHandlesSameLineDecls(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/fix\n\ngo 1.25\n")
	// Real.Handle and Other.Handle share ONE line, so they collide in the
	// position-keyed callee join; resolution must fall back to the unique FQN and
	// still route A/B's calls to Real.Handle (not the same-line Other.Handle).
	mustWrite("fix.go", `package fix

type Real struct{}
type Other struct{}

func (Real) Handle() {}; func (Other) Handle() {}

func A() { var r Real; r.Handle() }
func B() { var r Real; r.Handle() }
`)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("fix", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	res, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatalf("expected precise upgrades, got note %q", res.PreciseNote)
	}
	if got := inDegree(t, g, pid, "Handle", "fix.Real.Handle"); got != 2 {
		t.Errorf("Real.Handle in-degree = %d, want 2 (both callers, despite same-line collision)", got)
	}
	if got := inDegree(t, g, pid, "Handle", "fix.Other.Handle"); got != 0 {
		t.Errorf("Other.Handle in-degree = %d, want 0 (never called; not mis-routed by the collision)", got)
	}
}

func TestPreciseDegradesGracefully(t *testing.T) {
	// A non-module dir (no go.mod): --precise must degrade to name-based with a
	// note, never error, and never wipe the name edges it can't replace.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"),
		[]byte("package x\n\nfunc Helper() {}\nfunc Run() { Helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("x", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "x", dir, Options{Precise: true})
	if err != nil {
		t.Fatalf("precise on a non-module dir should not error, got %v", err)
	}
	if res.PreciseNote == "" {
		t.Error("expected a precise note explaining the degrade")
	}
	// Name-based edge Run->Helper must survive (precise couldn't supersede it).
	if got := inDegree(t, g, pid, "Helper", "x.Helper"); got != 1 {
		t.Errorf("name-based Helper in-degree = %d, want 1 (kept after degrade)", got)
	}
}

// TestPreciseIdempotent pins P0-05: a second --precise run (without --reindex
// of the project) must NOT double the precise-edge count. Pre-fix the
// `resolvePreciseEdgesWith` and `resolveLSPCallEdgesWith` passes only deleted
// `ProvName` edges before re-inserting their `ProvPrecise` ones, so a second
// --precise left the prior ProvPrecise edges in place AND inserted fresh
// copies. The fix is a second `DeleteCallEdgesBySource(..., ProvPrecise)`
// before the insert loop in both passes.
func TestPreciseIdempotent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fix\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.go"), []byte(preciseFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, err := g.UpsertProject("fix", dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	// First --precise run.
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Reindex: true, Precise: true}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := g.FindNodesBySymbol(pid, "Run")
	var t1 int64 = -1
	for _, n := range nodes {
		if n.FQN == "fix.T1.Run" {
			t1 = n.ID
		}
	}
	var firstPrecise int
	if err := g.DB().QueryRow(
		"SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls' AND provenance='precise'", t1,
	).Scan(&firstPrecise); err != nil {
		t.Fatal(err)
	}
	if firstPrecise == 0 {
		t.Fatal("first precise run produced no precise edges — fix the fixture before asserting stability")
	}

	// Second --precise run (same files, no --reindex of the project — just
	// re-runs the resolve pass to test the delete-first contract). Edge
	// count must be IDENTICAL, not doubled/tripled.
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	// Repeat --precise deliberately re-extracts previously resolved Go files so
	// a newly type-failing package can downgrade cleanly; refresh the target id.
	nodes, _ = g.FindNodesBySymbol(pid, "Run")
	for _, n := range nodes {
		if n.FQN == "fix.T1.Run" {
			t1 = n.ID
		}
	}
	var secondPrecise int
	if err := g.DB().QueryRow(
		"SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls' AND provenance='precise'", t1,
	).Scan(&secondPrecise); err != nil {
		t.Fatal(err)
	}
	if secondPrecise != firstPrecise {
		t.Errorf("P0-05 regression: precise-edge count after 2nd --precise = %d, want %d (delete-first missing)", secondPrecise, firstPrecise)
	}
}

func TestPreciseCoverageIncludesGoLeafFile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/leaf\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leaf.go"), []byte("package leaf\n\nfunc Leaf() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("leaf", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "leaf", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved["leaf.go"] {
		t.Fatalf("go/types successfully checked leaf.go but coverage is missing: %v", resolved)
	}
	if n, err := g.CountEdgesByProvenance(pid, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Fatalf("leaf fixture unexpectedly produced %d precise edges", n)
	}
}

func TestPreciseCoverageExcludesGoFilesWithTypeErrors(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/mixed\n\ngo 1.25\n")
	writeFile(t, dir, "good/good.go", "package good\n\nfunc Good() {}\n")
	writeFile(t, dir, "bad/bad.go", "package bad\n\nvar _ int = \"not an int\"\nfunc Bad() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("mixed", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "mixed", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseNote == "" {
		t.Fatal("type-error package should produce a precise degradation note")
	}
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved[filepath.Join("good", "good.go")] {
		t.Errorf("clean package file missing precise coverage: %v", resolved)
	}
	if resolved[filepath.Join("bad", "bad.go")] {
		t.Errorf("type-error package file incorrectly marked resolved: %v", resolved)
	}
}

func TestPreciseDowngradeRebuildsNameEdgesAfterNewTypeError(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/downgrade\n\ngo 1.25\n")
	writeFile(t, dir, "calls.go", "package downgrade\n\nfunc A() { B() }\nfunc B() {}\n")
	writeFile(t, dir, "state.go", "package downgrade\n\nvar State = 1\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("downgrade", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "downgrade", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	resolved, _ := g.CallGraphResolvedFiles(pid)
	if !resolved["calls.go"] || !resolved["state.go"] {
		t.Fatalf("initial precise pass did not cover both files: %v", resolved)
	}
	if n, _ := g.CountEdgesByProvenance(pid, graph.ProvPrecise); n == 0 {
		t.Fatal("initial precise pass produced no precise A→B edge")
	}

	// Only state.go changes, but its type error poisons the whole package.
	// calls.go must be forced through parser extraction so A→B degrades to a
	// fresh name edge rather than retaining the old precise edge.
	writeFile(t, dir, "state.go", "package downgrade\n\nvar State int = \"bad\"\n")
	if _, err := ix.IndexProject(context.Background(), pid, "downgrade", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	resolved, _ = g.CallGraphResolvedFiles(pid)
	if resolved["calls.go"] || resolved["state.go"] {
		t.Fatalf("type-error package retained precise coverage: %v", resolved)
	}
	if n, _ := g.CountEdgesByProvenance(pid, graph.ProvPrecise); n != 0 {
		t.Fatalf("type-error package retained %d stale precise edges", n)
	}
	callees, err := g.Callees(pid, "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 || callees[0].Symbol != "B" {
		t.Fatalf("downgraded parser graph lost A→B: %+v", callees)
	}
	var nameEdges int
	if err := g.DB().QueryRow(`
		SELECT COUNT(*) FROM edges e
		JOIN nodes src ON src.id=e.source_id
		WHERE src.project_id=? AND src.symbol='A' AND e.edge_type=? AND e.provenance=?`,
		pid, graph.EdgeCalls, graph.ProvName).Scan(&nameEdges); err != nil {
		t.Fatal(err)
	}
	if nameEdges != 1 {
		t.Fatalf("downgraded A→B name edges = %d, want 1", nameEdges)
	}
}

func TestIncrementalExtractionClearsOnlyChangedCoverage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package app\n\nfunc A() {}\n")
	writeFile(t, dir, "b.go", "package app\n\nfunc B() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"a.go", "b.go"} {
		if err := g.MarkCallGraphResolved(pid, file, preciseResolverGoTypes); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(t, dir, "a.go", "package app\n\nfunc A() { _ = 1 }\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"a.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["a.go"] {
		t.Errorf("changed a.go retained stale precise coverage: %v", resolved)
	}
	if !resolved["b.go"] {
		t.Errorf("unchanged b.go lost precise coverage: %v", resolved)
	}
}

type coverageCallResolver struct {
	fail map[string]bool
}

func (coverageCallResolver) Language() string { return "typescript" }

func (coverageCallResolver) ExtractFile(path string, _ []byte) (*extract.FileResult, error) {
	return &extract.FileResult{
		Path: path, Language: "typescript",
		Symbols: []extract.Symbol{{
			Name: "leaf", FQN: path + ".leaf", Kind: extract.KindFunction,
			Language: "typescript", StartLine: 1, EndLine: 1, Source: "function leaf() {}",
		}},
	}, nil
}

func (r coverageCallResolver) CallEdges(_ context.Context, path string) ([]extract.CallEdge, error) {
	if r.fail[path] {
		return nil, fmt.Errorf("simulated call hierarchy failure")
	}
	return nil, nil // successful leaf file: precise with zero edges
}

func TestLSPPreciseCoverageMarksSuccessAndClearsFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.ts", "export function good() {}\n")
	writeFile(t, dir, "bad.ts", "export function bad() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(coverageCallResolver{fail: map[string]bool{"bad.ts": true}})
	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	// Simulate coverage from an earlier successful pass. The next precise pass
	// must retain/re-mark good.ts and remove bad.ts when its request fails.
	for _, file := range []string{"good.ts", "bad.ts"} {
		if err := g.MarkCallGraphResolved(pid, file, preciseResolverLSP); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved["good.ts"] {
		t.Errorf("successful leaf callHierarchy request was not marked resolved: %v", resolved)
	}
	if resolved["bad.ts"] {
		t.Errorf("failed callHierarchy request retained stale coverage: %v", resolved)
	}
}
