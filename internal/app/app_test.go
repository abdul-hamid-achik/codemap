package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// isolate points all codemap dirs at a temp HOME so the test never touches the
// real data dir.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")
}

func TestServiceLifecycle(t *testing.T) {
	isolate(t)

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// Run runs.\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	ir, err := svc.Init(proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if ir.Project == "" {
		t.Error("empty project name")
	}

	// Structure-only index (no Ollama needed).
	rep, err := svc.Index(context.Background(), proj, index.Options{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded {
		t.Error("expected structure-only (Embedded=false)")
	}
	if rep.Nodes < 3 {
		t.Errorf("nodes = %d, want >= 3 (Run, Helper, file)", rep.Nodes)
	}
	if rep.Edges < 3 {
		t.Errorf("edges = %d, want >= 3 (2 defines + 1 call)", rep.Edges)
	}

	st, err := svc.Status(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Registered {
		t.Fatal("project should be registered")
	}
	if st.Nodes != rep.Nodes {
		t.Errorf("status nodes %d != index nodes %d", st.Nodes, rep.Nodes)
	}
	if st.Kinds["function"] < 2 {
		t.Errorf("kinds = %v, want >= 2 functions", st.Kinds)
	}
}

// fakeEmbedder produces deterministic vectors so semantic tests need no Ollama.
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
				v[j] = float32((int(t[j%len(t)]) + j) % 13)
			}
		}
		out[i] = v
	}
	return out, nil
}

func TestServiceCallers(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Callers(proj, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || rep.Results[0].Symbol != "Run" {
		t.Errorf("callers of Helper = %+v, want [Run]", rep.Results)
	}
}

func TestServiceSemantic(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// Authenticate validates a jwt token.\nfunc Authenticate() {}\n\n// Render renders html.\nfunc Render() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8}) // no Ollama needed
	svc := NewService(sess)

	if _, err := svc.Index(context.Background(), proj, index.Options{}, true); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Semantic(context.Background(), proj, "authentication", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) == 0 {
		t.Fatal("semantic search returned no hits")
	}
}

func TestStatusUnregistered(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	st, err := NewService(sess).Status(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Registered {
		t.Error("fresh dir should not be registered")
	}
}
