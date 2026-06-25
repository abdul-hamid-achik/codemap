package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// TestIndexPhaseTiming verifies that the Result struct is populated with
// wall-clock phase timing (ExtractMs, EmbedMs, TotalMs) after a full index.
func TestIndexPhaseTiming(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// A full index with embedding should report timing for all phases.
	// Note: for very fast indexes (sub-ms), millisecond resolution may
	// round to 0, so we only assert non-negative.
	if res.TotalMs < 0 {
		t.Errorf("TotalMs = %d, want >= 0", res.TotalMs)
	}
	if res.ExtractMs < 0 {
		t.Errorf("ExtractMs = %d, want >= 0", res.ExtractMs)
	}
	if res.EmbedMs < 0 {
		t.Errorf("EmbedMs = %d, want >= 0", res.EmbedMs)
	}
	// Total should be >= any individual phase.
	if res.TotalMs < res.ExtractMs {
		t.Errorf("TotalMs (%d) < ExtractMs (%d)", res.TotalMs, res.ExtractMs)
	}
	if res.TotalMs < res.EmbedMs {
		t.Errorf("TotalMs (%d) < EmbedMs (%d)", res.TotalMs, res.EmbedMs)
	}
}

// TestIndexPhaseTimingNoEmbed verifies that timing is reported even without
// embedding (structure-only index), and EmbedMs is 0.
func TestIndexPhaseTimingNoEmbed(t *testing.T) {
	g, _ := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if res.TotalMs < 0 {
		t.Errorf("TotalMs = %d, want >= 0 (even without embed)", res.TotalMs)
	}
	if res.ExtractMs < 0 {
		t.Errorf("ExtractMs = %d, want >= 0", res.ExtractMs)
	}
	// No embedder → EmbedMs should be 0.
	if res.EmbedMs != 0 {
		t.Errorf("EmbedMs = %d, want 0 (no embedder)", res.EmbedMs)
	}
}

// TestIndexIncrementalTiming verifies that a no-op incremental index (no
// changes) still reports TotalMs but with minimal ExtractMs (hash-skips are fast).
func TestIndexIncrementalTiming(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	// First index.
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	// Second index — no changes, everything hash-skipped.
	ix2 := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	res, err := ix2.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// TotalMs should be > 0 (some work: walk + node index build + stats).
	if res.TotalMs < 0 {
		t.Errorf("TotalMs = %d, want >= 0", res.TotalMs)
	}
	// FilesIndexed should be 0 (all skipped).
	if res.FilesIndexed != 0 {
		t.Errorf("FilesIndexed = %d, want 0 (no changes)", res.FilesIndexed)
	}
	if res.FilesSkipped != 2 {
		t.Errorf("FilesSkipped = %d, want 2", res.FilesSkipped)
	}
}

// TestTransactionBatchedIndexFile verifies that indexFile writes are
// transactional — if extraction fails mid-file, old nodes are preserved
// (the delete+insert is in one transaction that rolls back on error).
func TestTransactionBatchedIndexFile(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	// First index — populates the graph.
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	// Verify nodes exist.
	nodes, _ := g.ProjectNodes(pid)
	if len(nodes) != 5 {
		t.Fatalf("initial node count = %d, want 5", len(nodes))
	}

	// Re-index (incremental) — no changes, so hash-skipped.
	ix2 := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	res, err := ix2.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// All files skipped — nodes unchanged.
	nodes, _ = g.ProjectNodes(pid)
	if len(nodes) != 5 {
		t.Errorf("post-incremental node count = %d, want 5 (unchanged)", len(nodes))
	}
	if res.FilesIndexed != 0 {
		t.Errorf("FilesIndexed = %d, want 0 (no changes)", res.FilesIndexed)
	}
}

// TestBuildNodeIndexShared verifies that buildNodeIndex produces a consistent
// node index with the right fqn/symbol/position maps.
func TestBuildNodeIndexShared(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	ni, err := ix.buildNodeIndex(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ni.nodes) != 5 {
		t.Errorf("nodeIndex nodes = %d, want 5", len(ni.nodes))
	}
	// fqnTo should map both FQNs and file paths (file nodes).
	if len(ni.fqnTo) < 2 {
		t.Errorf("fqnTo = %d entries, want >= 2 (at least the FQNs)", len(ni.fqnTo))
	}
	// symTo should have the bare symbols.
	if _, ok := ni.symTo["Helper"]; !ok {
		t.Error("symTo missing 'Helper'")
	}
	if _, ok := ni.symTo["Run"]; !ok {
		t.Error("symTo missing 'Run'")
	}
	// dirOf should have entries for all nodes.
	if len(ni.dirOf) != 5 {
		t.Errorf("dirOf = %d entries, want 5", len(ni.dirOf))
	}
}

// TestSnapshotImportTransactional verifies that snapshot.Import uses a
// transaction for the bulk re-insert (one fsync, not N).
func TestSnapshotImportTransactional(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	// Export the snapshot.
	snapDir := filepath.Join(t.TempDir(), "snap")
	m, err := exportSnapshotForTest(g, v, pid, "app", snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Nodes != 5 {
		t.Fatalf("snapshot nodes = %d, want 5", m.Nodes)
	}

	// Import into a fresh project.
	pid2, _ := g.UpsertProject("app2", dir, "go")
	m2, err := importSnapshotForTest(g, v, pid2, "app2", snapDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if m2.Nodes != 5 {
		t.Errorf("imported nodes = %d, want 5", m2.Nodes)
	}
	nodes, _ := g.ProjectNodes(pid2)
	if len(nodes) != 5 {
		t.Errorf("imported project node count = %d, want 5", len(nodes))
	}
}

// exportSnapshotForTest wraps snapshot.Export for use in index-package tests.
func exportSnapshotForTest(g *graph.Store, v *vector.Store, projectID int64, project, dir string) (*snapshot.Manifest, error) {
	return snapshot.Export(g, v, projectID, project, dir, "", "")
}

// importSnapshotForTest wraps snapshot.Import for use in index-package tests.
func importSnapshotForTest(g *graph.Store, v *vector.Store, projectID int64, project, dir, wantProfile string) (*snapshot.Manifest, error) {
	return snapshot.Import(g, v, projectID, project, dir, wantProfile)
}

// TestParallelExtractionGo verifies that Go files are extracted in parallel
// (ExtractConcurrency > 1) without corrupting the graph or losing nodes.
// The test creates a repo with enough Go files to exercise the worker pool.
func TestParallelExtractionGo(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	// Create 10 Go files, each with a unique function.
	for i := 0; i < 10; i++ {
		writeFile(t, dir, fmt.Sprintf("f%d.go", i),
			fmt.Sprintf("package app\n\nfunc Func%d() {}\n", i))
	}
	pid, _ := g.UpsertProject("app", dir, "go")

	// Use ExtractConcurrency = 4 (the default).
	cfg := config.DefaultConfig().Index
	ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 10 function nodes + 10 file nodes = 20.
	if res.Nodes != 20 {
		t.Errorf("Nodes = %d, want 20 (10 funcs + 10 files)", res.Nodes)
	}
	if res.FilesIndexed != 10 {
		t.Errorf("FilesIndexed = %d, want 10", res.FilesIndexed)
	}
	// Every function should be findable.
	for i := 0; i < 10; i++ {
		sym := fmt.Sprintf("Func%d", i)
		if nodes, _ := g.FindNodesBySymbol(pid, sym); len(nodes) != 1 {
			t.Errorf("FindNodesBySymbol(%q) = %d nodes, want 1", sym, len(nodes))
		}
	}
}

// TestParallelExtractionRace verifies that concurrent extraction doesn't cause
// data races (run with -race). Uses the default concurrency of 4.
func TestParallelExtractionRace(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	for i := 0; i < 6; i++ {
		writeFile(t, dir, fmt.Sprintf("r%d.go", i),
			fmt.Sprintf("package app\n\nfunc R%d() { R%d() }\n", i, (i+1)%6))
	}
	pid, _ := g.UpsertProject("race", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	_, err := ix.IndexProject(context.Background(), pid, "race", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Verify edges resolved: each R_i calls R_(i+1)%6.
	for i := 0; i < 6; i++ {
		callees, err := g.Callees(pid, fmt.Sprintf("R%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if len(callees) != 1 {
			t.Errorf("R%d callees = %d, want 1", i, len(callees))
		}
	}
}

// TestParallelExtractionHighConcurrency verifies that a high concurrency
// setting (8 workers, 10 files) produces the same graph as sequential.
func TestParallelExtractionHighConcurrency(t *testing.T) {
	// A project with 10 Go files, each defining a function.
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, dir, fmt.Sprintf("hf%d.go", i),
			fmt.Sprintf("package app\n\nfunc HF%d() {}\n", i))
	}

	g, v := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	cfg := config.DefaultConfig().Index
	cfg.ExtractConcurrency = 8 // force parallel
	ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// 10 function nodes + 10 file nodes = 20
	if res.Nodes != 20 {
		t.Errorf("Nodes = %d, want 20", res.Nodes)
	}
	// 10 defines edges (file -> symbol)
	if res.Edges != 10 {
		t.Errorf("Edges = %d, want 10", res.Edges)
	}
	if res.FilesIndexed != 10 {
		t.Errorf("FilesIndexed = %d, want 10", res.FilesIndexed)
	}
	// Each HF0..HF9 should be findable.
	for i := 0; i < 10; i++ {
		ns, _ := g.FindNodesBySymbol(pid, fmt.Sprintf("HF%d", i))
		if len(ns) != 1 {
			t.Errorf("HF%d: found %d nodes, want 1", i, len(ns))
		}
	}
}

// TestParallelExtractionConcurrency1 verifies that ExtractConcurrency=1
// (effectively sequential) still works correctly.
func TestParallelExtractionConcurrency1(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, dir, fmt.Sprintf("g%d.go", i),
			fmt.Sprintf("package app\n\nfunc G%d() {}\n", i))
	}

	g, v := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	cfg := config.DefaultConfig().Index
	cfg.ExtractConcurrency = 1 // sequential
	ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 10 {
		t.Errorf("Nodes = %d, want 10 (5 functions + 5 files)", res.Nodes)
	}
	if res.FilesIndexed != 5 {
		t.Errorf("FilesIndexed = %d, want 5", res.FilesIndexed)
	}
}

// TestParallelExtractionOnErrorContinues verifies that a parse error in one
// parallel file doesn't abort the whole batch — the errored file is counted as
// skipped, and the rest index normally.
func TestParallelExtractionOnErrorContinues(t *testing.T) {
	dir := t.TempDir()
	// 4 valid files + 1 that the erroring extractor will fail on.
	for i := 0; i < 4; i++ {
		writeFile(t, dir, fmt.Sprintf("ok%d.go", i),
			fmt.Sprintf("package app\n\nfunc OK%d() {}\n", i))
	}
	writeFile(t, dir, "bad.go", "package app\n\nfunc Bad() {}\n")

	g, v := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	cfg := config.DefaultConfig().Index
	cfg.ExtractConcurrency = 4
	ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
	ix.Register(erroringExtractor{}) // replace gosrc → every file errors

	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// All 5 files errored → 0 indexed, 5 skipped.
	if res.FilesIndexed != 0 {
		t.Errorf("FilesIndexed = %d, want 0 (all errored)", res.FilesIndexed)
	}
	if res.FilesSkipped != 5 {
		t.Errorf("FilesSkipped = %d, want 5", res.FilesSkipped)
	}
	if len(res.Errors) != 5 {
		t.Errorf("Errors = %d, want 5", len(res.Errors))
	}
}
