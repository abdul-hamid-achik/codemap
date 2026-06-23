package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestQueryResultsCarrySignature(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// Run sends x through Helper.\nfunc Run(x int) int { return Helper(x) }\n\n// Helper returns n unchanged.\nfunc Helper(n int) int { return n }\n"), 0o644); err != nil {
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

	// callers carry the caller's signature AND docstring (no file read needed).
	cr, err := svc.Callers(proj, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Results) != 1 || cr.Results[0].Signature != "func Run(x int) int" {
		t.Errorf("caller signature = %q, want %q", sigOf(cr.Results), "func Run(x int) int")
	}
	if len(cr.Results) == 1 && cr.Results[0].Doc != "Run sends x through Helper." {
		t.Errorf("caller doc = %q, want %q", cr.Results[0].Doc, "Run sends x through Helper.")
	}

	// blast-radius nodes carry signatures too.
	ir, err := svc.Impact(proj, "Helper", 3)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range ir.BlastRadius {
		if n.Symbol == "Run" && n.Signature == "func Run(x int) int" && n.Doc == "Run sends x through Helper." {
			found = true
		}
	}
	if !found {
		t.Errorf("blast radius missing Run's signature/doc: %+v", ir.BlastRadius)
	}

	// offline name search carries them as well.
	fr, err := svc.FindSymbols(proj, "Helper", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Hits) == 0 || fr.Hits[0].Signature != "func Helper(n int) int" || fr.Hits[0].Doc != "Helper returns n unchanged." {
		t.Errorf("find signature/doc = %+v, want func Helper(n int) int / Helper returns n unchanged.", fr.Hits)
	}
}

func sigOf(refs []SymbolRef) string {
	if len(refs) == 0 {
		return "(no results)"
	}
	return refs[0].Signature
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
	// Signatures and docstrings are resolved from the graph even though the
	// vector payload lacks them, so semantic hits are as self-contained as
	// name-search hits.
	var gotSig, gotDoc string
	for _, h := range rep.Hits {
		if h.Symbol == "Authenticate" {
			gotSig, gotDoc = h.Signature, h.Doc
		}
	}
	if gotSig != "func Authenticate()" {
		t.Errorf("semantic hit signature = %q, want %q", gotSig, "func Authenticate()")
	}
	if gotDoc != "Authenticate validates a jwt token." {
		t.Errorf("semantic hit doc = %q, want %q", gotDoc, "Authenticate validates a jwt token.")
	}
}

func TestServiceImpact(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app; func Helper() {}; func Run() { Helper() }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main_test.go"),
		[]byte("package app; import \"testing\"; func TestRun(t *testing.T) { Run() }"), 0o644); err != nil {
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

	rep, err := svc.Impact(proj, "Helper", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Helper should be found")
	}
	// Run calls Helper (direct); TestRun calls Run (depth 2).
	if len(rep.DirectCallers) != 1 || rep.DirectCallers[0].Symbol != "Run" {
		t.Errorf("direct callers = %+v, want [Run]", rep.DirectCallers)
	}
	if len(rep.BlastRadius) != 2 {
		t.Errorf("blast radius = %+v, want 2 (Run, TestRun)", rep.BlastRadius)
	}
	if len(rep.Tests) != 1 || rep.Tests[0].Symbol != "TestRun" {
		t.Errorf("tests = %+v, want [TestRun]", rep.Tests)
	}
	if rep.Untested {
		t.Error("Helper is reached by TestRun, should not be untested")
	}

	// Run is reached by TestRun, so it is covered too.
	rep2, err := svc.Impact(proj, "Run", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Untested || len(rep2.Tests) != 1 {
		t.Errorf("Run should be covered by TestRun: untested=%v tests=%d", rep2.Untested, len(rep2.Tests))
	}
}

func TestPreciseCallersGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	// Isolate only codemap's data dir; leave HOME real so gopls uses its
	// persistent cache (a temp HOME gets polluted with read-only Go cache files
	// that t.TempDir can't clean up).
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use a METHOD (gopls names it "(T).Helper") to guard receiver-prefix matching.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package m\n\ntype T struct{}\n\nfunc (t T) Helper() {}\n\nfunc Run() { var x T; x.Helper() }\n"), 0o644); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := svc.PreciseCallers(ctx, proj, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Results {
		if r.Symbol == "Run" {
			found = true
		}
	}
	if !found {
		t.Errorf("precise callers of Helper = %+v, want to include Run", rep.Results)
	}
}

func TestPreciseCalleesGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package m\n\ntype T struct{}\n\nfunc (t T) Helper() {}\n\nfunc Run() { var x T; x.Helper() }\n"), 0o644); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := svc.PreciseCallees(ctx, proj, "Run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Results {
		if r.Symbol == "Helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("precise callees of Run = %+v, want to include Helper", rep.Results)
	}
}

func TestServiceSymbols(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\ntype T struct{}\n\nfunc (t T) M() {}\n\nfunc Run() {}\n"), 0o644); err != nil {
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

	rep, err := svc.Symbols(proj, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if rep.File != "main.go" {
		t.Errorf("file = %q, want main.go", rep.File)
	}
	kinds := map[string]string{}
	for _, s := range rep.Symbols {
		kinds[s.Symbol] = s.Kind
	}
	if kinds["T"] != "type" || kinds["M"] != "method" || kinds["Run"] != "function" {
		t.Errorf("symbols = %+v, want T:type M:method Run:function", kinds)
	}
	// the file node itself must not be listed
	for _, s := range rep.Symbols {
		if s.Kind == "file" {
			t.Error("symbols should not include the file node")
		}
	}
}

func TestServiceSearchNameFallback(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Authenticate() {}\n\nfunc Render() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	// Structure-only index → no embeddings, so Search must fall back to name search.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Search(context.Background(), proj, "Auth", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "name" {
		t.Errorf("mode = %q, want name (no embeddings present)", rep.Mode)
	}
	found := false
	for _, h := range rep.Hits {
		if h.Symbol == "Authenticate" {
			found = true
		}
	}
	if !found {
		t.Errorf("search 'Auth' = %+v, want Authenticate", rep.Hits)
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
