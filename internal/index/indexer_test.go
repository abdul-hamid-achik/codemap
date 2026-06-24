package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// fakeEmbedder returns deterministic vectors so tests need no Ollama.
type fakeEmbedder struct{ dims int }

func (f fakeEmbedder) Profile() embed.EmbeddingProfile {
	return embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: f.dims, Distance: "cosine"}
}

func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, f.dims)
		for j := 0; j < f.dims; j++ {
			if len(t) > 0 {
				v[j] = float32((int(t[j%len(t)]) + j) % 17)
			}
		}
		out[i] = v
	}
	return out, nil
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newStores(t *testing.T) (*graph.Store, *vector.Store) {
	t.Helper()
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(); _ = v.Close() })
	return g, v
}

const fileA = `package app

// Helper does work.
func Helper() {}

func Run() {
	Helper()
}
`

const fileB = `package app

func Other() {
	Run()
}
`

func setupProject(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", fileA)
	writeFile(t, dir, "b.go", fileB)
	writeFile(t, dir, "README.md", "# not indexed")
	writeFile(t, dir, "node_modules/dep.go", "package dep\nfunc Z() {}\n")
	return dir
}

func TestIndexerCloseNoServers(t *testing.T) {
	g, v := newStores(t)
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	// With nothing spawned, Close is a no-op and idempotent.
	if err := ix.Close(); err != nil {
		t.Errorf("Close with no closers = %v, want nil", err)
	}
	if err := ix.Close(); err != nil {
		t.Errorf("second Close = %v, want nil (idempotent)", err)
	}
}

func TestIndexProject(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2 (md ignored, node_modules excluded)", res.FilesScanned)
	}
	if res.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2", res.FilesIndexed)
	}
	// nodes: Helper, Run, Other + a.go, b.go file nodes = 5
	if res.Nodes != 5 {
		t.Errorf("Nodes = %d, want 5", res.Nodes)
	}
	// edges: 3 defines (file->symbol) + 2 calls (Run->Helper, Other->Run) = 5
	if res.Edges != 5 {
		t.Errorf("Edges = %d, want 5", res.Edges)
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected errors: %v", res.Errors)
	}
	if v.Count() != 3 {
		t.Errorf("vector count = %d, want 3 (symbols only)", v.Count())
	}
}

func TestIndexIncremental(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)

	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	// Re-run with no changes: everything skipped.
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 0 || res.FilesSkipped != 2 {
		t.Errorf("re-run: indexed=%d skipped=%d, want 0/2", res.FilesIndexed, res.FilesSkipped)
	}
	if res.Nodes != 5 {
		t.Errorf("re-run nodes = %d, want stable 5", res.Nodes)
	}

	// Change one file: only it re-indexes.
	writeFile(t, dir, "b.go", "package app\n\nfunc Other() {\n\tRun()\n}\n\nfunc Extra() {}\n")
	res, err = ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 1 || res.FilesSkipped != 1 {
		t.Errorf("after edit: indexed=%d skipped=%d, want 1/1", res.FilesIndexed, res.FilesSkipped)
	}
	if res.Nodes != 6 {
		t.Errorf("after edit nodes = %d, want 6 (added Extra)", res.Nodes)
	}
}

func TestIndexReindexIsStable(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)

	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{Reindex: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 2 {
		t.Errorf("reindex FilesIndexed = %d, want 2", res.FilesIndexed)
	}
	if res.Nodes != 5 || res.Edges != 5 {
		t.Errorf("reindex nodes/edges = %d/%d, want 5/5 (no duplication)", res.Nodes, res.Edges)
	}
	if v.Count() != 3 {
		t.Errorf("reindex vector count = %d, want 3 (no duplication)", v.Count())
	}
}

// TestIndexUnqualifiedCallSamePackage verifies that a bare-identifier call
// (Helper()) resolves only within the caller's package, not to a same-named
// symbol in another package — eliminating cross-package false edges that the
// old by-name resolution produced.
func TestIndexUnqualifiedCallSamePackage(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	// Two packages, each defining Helper. pkga.Run calls Helper() unqualified.
	writeFile(t, dir, "pkga/a.go", "package pkga\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n}\n")
	writeFile(t, dir, "pkgb/b.go", "package pkgb\n\nfunc Helper() {}\n")
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	callees, err := g.Callees(pid, "Run")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 {
		t.Fatalf("Run callees = %d, want 1 (same-package Helper only): %+v", len(callees), callees)
	}
	if got := filepath.ToSlash(callees[0].FilePath); got != "pkga/a.go" {
		t.Errorf("Run calls Helper in %q, want pkga/a.go", got)
	}
}

func TestIndexStructureOnly(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, nil, nil, config.DefaultConfig().Index) // no embedder/vectors
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 5 || res.Edges != 5 {
		t.Errorf("structure-only nodes/edges = %d/%d, want 5/5", res.Nodes, res.Edges)
	}
}

// TestIndexSurfacesOversizedFiles verifies a recognized source file skipped for
// exceeding max_file_bytes is named in Result.Oversized (not silently dropped),
// while a smaller file is indexed normally.
func TestIndexSurfacesOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.go", "package m\n\nfunc Small() {}\n")
	writeFile(t, dir, "big.go", "package m\n\nfunc Big() {} //"+strings.Repeat("x", 300)+"\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("m", dir, "go")
	cfg := config.DefaultConfig().Index
	cfg.MaxFileBytes = 60 // small.go fits; big.go (~330 bytes) doesn't
	ix := New(g, nil, nil, cfg)

	res, err := ix.IndexProject(context.Background(), pid, "m", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Oversized) != 1 || !strings.Contains(res.Oversized[0], "big.go") {
		t.Errorf("Oversized should name big.go, got %v", res.Oversized)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Small"); len(ns) == 0 {
		t.Error("small.go (under the limit) should be indexed")
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Big"); len(ns) != 0 {
		t.Error("big.go (over the limit) should be skipped")
	}
}

// TestIndexExcludesDependencyDirs checks that the default excludes keep
// dependency/build dirs out of the graph — critically Python virtualenvs
// (venv/site-packages), which would otherwise flood it with library code.
func TestIndexExcludesDependencyDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/app.go", "package m\n\nfunc Mine() {}\n")
	writeFile(t, dir, "node_modules/lib/x.go", "package lib\n\nfunc NodeDep() {}\n")
	writeFile(t, dir, "venv/lib/site-packages/pkg/y.go", "package pkg\n\nfunc VenvDep() {}\n")
	writeFile(t, dir, "vendor/dep/z.go", "package dep\n\nfunc VendorDep() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("m", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "m", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Mine"); len(ns) == 0 {
		t.Error("src/app.go should be indexed")
	}
	for _, dep := range []string{"NodeDep", "VenvDep", "VendorDep"} {
		if ns, _ := g.FindNodesBySymbol(pid, dep); len(ns) != 0 {
			t.Errorf("%s is in an excluded dependency dir — it should NOT be indexed", dep)
		}
	}
}

// TestIndexPrunesDeletedFiles checks that an incremental reindex removes the
// nodes of a file deleted from disk — otherwise ghost symbols linger in
// find/callers/search. Files still on disk are untouched.
func TestIndexPrunesDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package m\n\nfunc Alpha() { Beta() }\n\nfunc Beta() {}\n")
	writeFile(t, dir, "b.go", "package m\n\nfunc Gamma() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("m", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "m", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Gamma"); len(ns) == 0 {
		t.Fatal("Gamma should be indexed initially")
	}

	// Delete b.go and reindex incrementally.
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	res, err := ix.IndexProject(context.Background(), pid, "m", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (b.go gone from disk)", res.FilesDeleted)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Gamma"); len(ns) != 0 {
		t.Error("Gamma (from deleted b.go) should be pruned, but it's still present")
	}
	if ns, _ := g.FindNodesBySymbol(pid, "Beta"); len(ns) == 0 {
		t.Error("Beta (a.go, still on disk) must NOT be pruned")
	}
}

// erroringExtractor stands in for a backend that fails on every file (e.g. a
// language server that timed out) so the indexer's error accounting is testable.
type erroringExtractor struct{}

func (erroringExtractor) Language() string { return "go" }
func (erroringExtractor) ExtractFile(string, []byte) (*extract.FileResult, error) {
	return nil, fmt.Errorf("simulated extract failure")
}

// TestIndexErroredFileCountedAsSkipped checks the accounting invariant: a file
// that fails to extract is recorded in Errors AND counted as skipped, so the
// summary's "scanned = indexed + skipped" always holds (it didn't before — an
// errored file vanished from the totals).
func TestIndexErroredFileCountedAsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package p\n\nfunc F() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("p", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(erroringExtractor{}) // replace gosrc for "go" → every file errors

	res, err := ix.IndexProject(context.Background(), pid, "p", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected the errored file recorded in res.Errors")
	}
	if res.FilesIndexed != 0 {
		t.Errorf("an errored file must not count as indexed, got %d", res.FilesIndexed)
	}
	if res.FilesScanned != res.FilesIndexed+res.FilesSkipped {
		t.Errorf("accounting broken: scanned %d != indexed %d + skipped %d",
			res.FilesScanned, res.FilesIndexed, res.FilesSkipped)
	}
}

// TestIndexValueReferencedHandlerNotOrphan is the end-to-end proof for the
// function-value reference pipeline: a handler wired only by value in a
// top-level table (cobra-style `RunE: runInit`) must NOT be flagged as dead
// code, yet must NOT leak into the call graph either. Exercises gosrc value-ref
// extraction → the file-keyed resolution in resolveEdges → the Orphans query.
func TestIndexValueReferencedHandlerNotOrphan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main

func runInit() error { return nil }

var cmd = &struct{ RunE func() error }{RunE: runInit}

func main() { _ = cmd }
`)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	orph, err := g.Orphans(pid, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range orph {
		if n.Symbol == "runInit" {
			t.Errorf("runInit is wired by value (RunE: runInit) — must not be a dead-code candidate")
		}
	}
	// The value reference is a `references` edge, not a `calls` edge: the call
	// graph stays clean (no phantom caller).
	callers, err := g.Callers(pid, "runInit")
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 0 {
		t.Errorf("a function value must not create call-graph callers, got %+v", callers)
	}
}
