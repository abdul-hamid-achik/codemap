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
