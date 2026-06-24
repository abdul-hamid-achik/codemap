package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
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
