package index

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// recordingEmbedder records the size of each (possibly concurrent) Embed call so a
// test can assert how the embed phase batched its work. fail, if set, makes every
// call error — to exercise graceful degradation.
type recordingEmbedder struct {
	dims    int
	fail    error
	mu      sync.Mutex
	batches []int
}

func (r *recordingEmbedder) Profile() embed.EmbeddingProfile {
	return embed.EmbeddingProfile{Provider: "rec", Model: "rec", Dimensions: r.dims, Distance: "cosine"}
}

func (r *recordingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	r.mu.Lock()
	r.batches = append(r.batches, len(texts))
	r.mu.Unlock()
	if r.fail != nil {
		return nil, r.fail
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, r.dims)
		out[i][0] = 1
	}
	return out, nil
}

// TestEmbedPhaseBatchesEveryNode: with a small batch size and concurrency, every
// symbol still gets exactly one vector (no item lost across batch/worker bounds),
// and no request exceeds the configured batch size.
func TestEmbedPhaseBatchesEveryNode(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package app\n")
	for i := 0; i < 21; i++ { // 21 funcs → several batches of 4
		fmt.Fprintf(&b, "func F%d() {}\n", i)
	}
	writeFile(t, dir, "a.go", b.String())
	pid, _ := g.UpsertProject("app", dir, "go")

	cfg := config.DefaultConfig().Index
	cfg.EmbedBatchSize = 4
	cfg.EmbedConcurrency = 3
	rec := &recordingEmbedder{dims: 4}
	res, err := New(g, v, rec, cfg).IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.EmbedNote != "" {
		t.Fatalf("unexpected embed note: %s", res.EmbedNote)
	}
	if got := v.Count(); got != 21 {
		t.Errorf("vector count = %d, want 21 (one per function across batches)", got)
	}
	if len(rec.batches) < 2 {
		t.Errorf("expected multiple batches with batch size 4, got %v", rec.batches)
	}
	for _, n := range rec.batches {
		if n > 4 {
			t.Errorf("a batch had %d texts, exceeds configured batch size 4", n)
		}
	}
}

// TestEmbedPhaseDegradesOnError: an embedder failure keeps the structural index
// (nodes/edges) and reports it via EmbedNote rather than failing the whole index.
func TestEmbedPhaseDegradesOnError(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	rec := &recordingEmbedder{dims: 4, fail: fmt.Errorf("ollama unreachable")}
	res, err := New(g, v, rec, config.DefaultConfig().Index).IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatalf("index must not fail when embedding fails: %v", err)
	}
	if res.EmbedNote == "" {
		t.Error("expected EmbedNote when the embedder errors")
	}
	if got := v.Count(); got != 0 {
		t.Errorf("no vectors should be stored on embed failure, got %d", got)
	}
	if res.Nodes < 3 {
		t.Errorf("structure should still be indexed despite the embed failure, got %d nodes", res.Nodes)
	}
}

// TestEmbedTextCap: a cap truncates a long body but never the leading
// docstring+signature, and 0 means no cap.
func TestEmbedTextCap(t *testing.T) {
	s := extract.Symbol{Docstring: "doc", Signature: "func F()", Source: strings.Repeat("x", 1000)}
	if full := embedText(s, 0); len(full) < 1000 {
		t.Fatalf("uncapped should keep the full source, got %d chars", len(full))
	}
	capped := embedText(s, 64)
	if len(capped) != 64 {
		t.Errorf("capped length = %d, want 64", len(capped))
	}
	if !strings.HasPrefix(capped, "doc\nfunc F()\n") {
		t.Errorf("cap should keep docstring+signature first, got %q", capped)
	}
}
