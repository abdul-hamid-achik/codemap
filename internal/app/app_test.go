package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
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

func TestInitReportsCanonicalNameForSameBasenameProjects(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	proj1 := filepath.Join(root, "one", "app")
	proj2 := filepath.Join(root, "two", "app")
	for _, dir := range []string{proj1, proj2} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	first, err := svc.Init(proj1, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Init(proj2, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Project == second.Project {
		t.Fatalf("same-basename projects reported the same canonical name %q", first.Project)
	}
	g, _ := sess.Graph()
	stored, err := g.GetProjectByID(second.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Project != stored.Name {
		t.Fatalf("Init project = %q, stored canonical name = %q", second.Project, stored.Name)
	}
}

func TestIndexSameBasenameProjectsKeepsVectorScopesSeparate(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	proj1 := filepath.Join(root, "one", "app")
	proj2 := filepath.Join(root, "two", "app")
	writeFile := func(dir, symbol string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := fmt.Sprintf("package app\n\nfunc %s() {}\n", symbol)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(proj1, "AlphaUnique")
	writeFile(proj2, "BetaUnique")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8})
	svc := NewService(sess)
	first, err := svc.Index(context.Background(), proj1, index.Options{}, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Index(context.Background(), proj2, index.Options{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Project == second.Project {
		t.Fatalf("direct Index reused vector project key %q for both roots", first.Project)
	}

	v, err := sess.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	firstCount, err := v.CountByProject(first.Project)
	if err != nil {
		t.Fatal(err)
	}
	secondCount, err := v.CountByProject(second.Project)
	if err != nil {
		t.Fatal(err)
	}
	if firstCount != 1 || secondCount != 1 {
		t.Fatalf("vector counts by canonical project = %d/%d, want 1/1", firstCount, secondCount)
	}
	firstRecords, err := v.IterByProject(first.Project)
	if err != nil || len(firstRecords) != 1 {
		t.Fatalf("first project records = %d err=%v", len(firstRecords), err)
	}
	for project, forbidden := range map[string]string{
		first.Project:  "BetaUnique",
		second.Project: "AlphaUnique",
	} {
		hits, err := v.Search(firstRecords[0].Vector, 10, project)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Meta.Project != project {
			t.Fatalf("search scope %q returned %+v", project, hits)
		}
		if hits[0].Meta.Symbol == forbidden {
			t.Fatalf("search scope %q leaked symbol %q from the other project", project, forbidden)
		}
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

type failingEmbedder struct {
	dims int
	err  error
}

func (f failingEmbedder) Profile() embed.EmbeddingProfile {
	return embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: f.dims, Distance: "cosine"}
}

func (f failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, f.err
}

func hasSymbolHit(hits []SemanticHit, symbol string) bool {
	for _, hit := range hits {
		if hit.Symbol == symbol {
			return true
		}
	}
	return false
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

// A nonexistent symbol and a real symbol-with-no-callers both yield empty
// Results; Found is what tells them apart (so the CLI can say "no such symbol"
// vs "no callers" instead of a misleading "none" for both).
func TestCallersFoundDistinguishesTypoFromNoCallers(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// Run calls Helper; nothing calls Run; Helper calls nothing.
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

	// Real symbol, no callers → Found, empty Results.
	run, err := svc.Callers(proj, "Run")
	if err != nil {
		t.Fatal(err)
	}
	if !run.Found || len(run.Results) != 0 {
		t.Errorf("Run: want Found=true with no callers, got Found=%v results=%+v", run.Found, run.Results)
	}
	// Nonexistent symbol → not Found.
	if miss, err := svc.Callers(proj, "NoSuchSymbol"); err != nil {
		t.Fatal(err)
	} else if miss.Found {
		t.Error("a nonexistent symbol should report Found=false")
	}
	// Callees of a real leaf symbol → Found even with no callees.
	if leaf, err := svc.Callees(proj, "Helper"); err != nil {
		t.Fatal(err)
	} else if !leaf.Found {
		t.Error("Helper exists, so Callees should report Found=true")
	}
}

// TestCallGraphUnavailableDetection guards the §1 honesty fix: impact/callers must
// flag (not confidently-empty) a no-name-based-call-edge language (TS/JS/Python) on a
// name-based index, but stay silent for Go and only after the queried file has
// successful precise-resolution coverage.
func TestCallGraphUnavailableDetection(t *testing.T) {
	for _, l := range []string{"typescript", "javascript", "python"} {
		if !noNameBasedCallLang(l) {
			t.Errorf("%q has no name-based call edges and should be flagged", l)
		}
	}
	for _, l := range []string{"go", "ruby", "c", ""} {
		if noNameBasedCallLang(l) {
			t.Errorf("%q has name-based call edges and must NOT be flagged", l)
		}
	}

	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := g.UpsertProject("p", "/p", "typescript")
	tsNode := &graph.Node{ProjectID: pid, FilePath: "a.ts", Symbol: "compute", FQN: "compute", Kind: graph.KindFunction, Language: "typescript", SourceHash: "h"}
	tsID, _ := g.AddNode(tsNode)
	goNode := &graph.Node{ProjectID: pid, FilePath: "b.go", Symbol: "Run", FQN: "Run", Kind: graph.KindFunction, Language: "go", SourceHash: "h"}
	goID, _ := g.AddNode(goNode)

	// Name-based index: a TS symbol's call graph is unavailable (unresolved, not absent).
	if lang, yes := svc.callGraphUnavailable(g, pid, []graph.Node{*tsNode}); !yes || lang != "typescript" {
		t.Errorf("TS on name-based index: want (typescript,true), got (%q,%v)", lang, yes)
	}
	// A Go symbol on the same index has real name-based call edges → available.
	if _, yes := svc.callGraphUnavailable(g, pid, []graph.Node{*goNode}); yes {
		t.Error("Go symbol must not be flagged unavailable on a name-based index")
	}
	// A precise edge elsewhere must NOT upgrade this TypeScript definition.
	if _, err := g.AddEdgeProv(goID, tsID, graph.EdgeCalls, 1.0, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	if _, yes := svc.callGraphUnavailable(g, pid, []graph.Node{*tsNode}); !yes {
		t.Error("an unrelated precise edge must not make the TS symbol available")
	}
	if err := g.MarkCallGraphResolved(pid, "b.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	if _, yes := svc.callGraphUnavailable(g, pid, []graph.Node{*tsNode}); !yes {
		t.Error("precise coverage for b.go must not upgrade a.ts")
	}
	if err := g.MarkCallGraphResolved(pid, "a.ts", "lsp"); err != nil {
		t.Fatal(err)
	}
	if _, yes := svc.callGraphUnavailable(g, pid, []graph.Node{*tsNode}); yes {
		t.Error("TS symbol should be available once its own file is resolved")
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

func TestIndexNonGoWarns(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// A project with only recognized non-Go source (no Go files).
	if err := os.WriteFile(filepath.Join(proj, "app.ts"), []byte("export function a() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.py"), []byte("def a():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// NoLSP so the result is deterministic regardless of which language servers are
	// installed: with typescript-language-server on PATH the .ts file would index.
	rep, err := NewService(sess).Index(context.Background(), proj, index.Options{NoLSP: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesScanned != 0 || rep.Nodes != 0 {
		t.Errorf("expected nothing indexed for a non-Go project (NoLSP), got %d files / %d nodes", rep.FilesScanned, rep.Nodes)
	}
	if !strings.Contains(rep.Warning, "typescript") || !strings.Contains(rep.Warning, "python") {
		t.Errorf("expected a warning naming the skipped languages, got %q", rep.Warning)
	}
}

func TestIndexAdvisory(t *testing.T) {
	// A present language whose server is missing — actionable, even though Go files
	// indexed (FilesScanned > 0), so it is never silently dropped.
	r := &index.Result{
		FilesScanned:   5,
		Unsupported:    map[string]int{"typescript": 3},
		MissingServers: map[string]string{"typescript": "typescript-language-server"},
		ServerIssues: []tooling.Issue{{
			Code:      tooling.CodeNotFound,
			Severity:  "error",
			Languages: []string{"typescript"},
			Binary:    "typescript-language-server",
		}},
	}
	if adv := indexAdvisory(r); !strings.Contains(adv, "typescript-language-server") || !strings.Contains(adv, "3 typescript") {
		t.Errorf("missing-server advisory = %q", adv)
	} else if !strings.Contains(adv, "not found on PATH") {
		t.Errorf("structured missing-server advisory should say not found, got %q", adv)
	}
	// Genuinely unsupported language, nothing indexed — informational ("planned").
	r2 := &index.Result{FilesScanned: 0, Unsupported: map[string]int{"ruby": 2}}
	if adv := indexAdvisory(r2); !strings.Contains(adv, "ruby") || !strings.Contains(adv, "planned") {
		t.Errorf("planned advisory = %q", adv)
	}
	// T0 languages stay visible in a mixed project; successfully indexed Go
	// must not reduce Rust to an unexplained numeric skipped count.
	r3 := &index.Result{FilesScanned: 5, Unsupported: map[string]int{"rust": 1}}
	if adv := indexAdvisory(r3); !strings.Contains(adv, "1 rust") || !strings.Contains(adv, "T0") {
		t.Errorf("mixed-project T0 advisory = %q", adv)
	}
	// Everything indexed — nothing to say.
	if adv := indexAdvisory(&index.Result{FilesScanned: 5}); adv != "" {
		t.Errorf("expected no advisory, got %q", adv)
	}
}

func TestServiceAnnotations(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// annotate works before indexing (auto-registers the project).
	if _, _, err := svc.AnnotateNode(proj, "app.B", "postgres", "hot", `{"rows":7}`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.AnnotatePath(proj, "app.A", "app.B", "note", "entry chain", ""); err != nil {
		t.Fatal(err)
	}

	nodeAnns, err := svc.NodeAnnotations(proj, "app.B")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeAnns.Annotations) != 1 || nodeAnns.Annotations[0].Data != `{"rows":7}` {
		t.Errorf("node annotations = %+v, want one with opaque data", nodeAnns.Annotations)
	}
	pathAnns, _ := svc.PathAnnotations(proj, "app.A", "app.B")
	if len(pathAnns.Annotations) != 1 || pathAnns.Annotations[0].Target != "app.A -> app.B" {
		t.Errorf("path annotations = %+v, want one on app.A -> app.B", pathAnns.Annotations)
	}
	all, _ := svc.AllAnnotations(proj)
	if len(all.Annotations) != 2 {
		t.Errorf("all annotations = %d, want 2", len(all.Annotations))
	}

	// annotations survive reindex (keyed by project, not node id).
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true}, false); err != nil {
		t.Fatal(err)
	}
	if all, _ = svc.AllAnnotations(proj); len(all.Annotations) != 2 {
		t.Errorf("annotations should survive reindex, got %d", len(all.Annotations))
	}

	// remove one.
	ok, err := svc.RemoveAnnotation(proj, all.Annotations[0].ID)
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	if all, _ = svc.AllAnnotations(proj); len(all.Annotations) != 1 {
		t.Errorf("after remove, annotations = %d, want 1", len(all.Annotations))
	}
}

func TestImpactSurfacesAnnotations(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
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
	// Pinned by FQN; queried by short name — surfaced via the resolved location FQN.
	if _, _, err := svc.AnnotateNode(proj, "app.B", "postgres", "hot in prod", `{"rows":9}`); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Impact(proj, "B", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Annotations) != 1 || rep.Annotations[0].Note != "hot in prod" {
		t.Errorf("impact should surface the pinned annotation, got %+v", rep.Annotations)
	}
}

// TestPathSurfacesAnnotations verifies a note pinned to a call path (annotate
// <from> <to>) shows up in the `path` query — not only in `annotations`.
func TestPathSurfacesAnnotations(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
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
	if _, _, _, err := svc.AnnotatePath(proj, "A", "B", "note", "the A->B flow", ""); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Path(proj, "A", "B")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("path A->B should be found (A calls B)")
	}
	if len(rep.Annotations) != 1 || rep.Annotations[0].Note != "the A->B flow" {
		t.Errorf("path should surface the pinned path annotation, got %+v", rep.Annotations)
	}
}

// TestServiceStaleness checks the agent-facing drift signal: a fresh index
// reports no drift, editing a file reports it changed, and an unregistered
// project reports nil (nothing to compare).
func TestServiceStaleness(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
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

	st, err := svc.Staleness(proj)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || st.Any() {
		t.Errorf("fresh index should report no drift, got %+v", st)
	}

	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B(); B() }\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err = svc.Staleness(proj); err != nil {
		t.Fatal(err)
	} else if st == nil || st.Changed != 1 {
		t.Errorf("after edit: want Changed=1, got %+v", st)
	}

	if st, err = svc.Staleness(t.TempDir()); err != nil {
		t.Fatal(err)
	} else if st != nil {
		t.Errorf("unregistered project should have nil staleness, got %+v", st)
	}
}

// TestServiceContext checks the one-call bundle composes definition, callers,
// callees, covering tests, and blast radius for a symbol.
func TestServiceContext(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// A does the thing.\nfunc A() { B() }\n\nvar Hook = struct{ Run func() }{Run: A}\n\nfunc B() {}\n\nfunc C() { A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "a_test.go"),
		[]byte("package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { A() }\n"), 0o644); err != nil {
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

	rep, err := svc.Context(proj, "A", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("A should be found")
	}
	if len(rep.Definitions) != 1 || !strings.Contains(rep.Definitions[0].Signature, "func A") {
		t.Errorf("definitions: want 1 with signature, got %+v", rep.Definitions)
	}
	if !strings.Contains(rep.Definitions[0].Doc, "A does the thing") {
		t.Errorf("definition should carry the docstring, got %q", rep.Definitions[0].Doc)
	}
	has := func(refs []SymbolRef, sym string) bool {
		for _, r := range refs {
			if r.Symbol == sym {
				return true
			}
		}
		return false
	}
	if !has(rep.Callers, "C") {
		t.Errorf("callers should include C, got %+v", rep.Callers)
	}
	if !has(rep.Callees, "B") {
		t.Errorf("callees should include B, got %+v", rep.Callees)
	}
	if len(rep.Tests) == 0 {
		t.Errorf("A should have a covering test (TestA), got none")
	}
	if rep.BlastRadius < 1 {
		t.Errorf("A should have a non-empty blast radius, got %d", rep.BlastRadius)
	}
	if rep.ReferencesTotal != 1 || len(rep.References) != 1 || rep.References[0].Source.Kind != graph.KindFile {
		t.Errorf("A should include its top-level value wiring as a file scope, got %+v", rep.References)
	}
	if rep.ReferencesCoverage != ReferenceCoveragePartial || !strings.Contains(rep.ReferencesResolution, "not exact expression lines") {
		t.Errorf("context lost reference-specific coverage honesty: %+v", rep)
	}
	// Not truncated here, so the totals equal the (small) list lengths.
	if rep.CallersTotal != len(rep.Callers) || rep.CalleesTotal != len(rep.Callees) || rep.TestsTotal != len(rep.Tests) {
		t.Errorf("totals should equal list lengths when not truncated: %+v", rep)
	}

	// Unknown symbol → not found, no error.
	if miss, err := svc.Context(proj, "NotASymbol", 3, false); err != nil {
		t.Fatal(err)
	} else if miss.Found {
		t.Errorf("unknown symbol should report Found=false, got %+v", miss)
	}
}

// TestServiceContextBrief is the I05 contract: brief:true drops each
// definition's Source body (keeping signature/doc/location) and sets
// SourceOmitted, while everything else in the bundle — callers, callees,
// tests, blast radius, references, notes — is byte-identical to the
// non-brief bundle.
func TestServiceContextBrief(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// A does the thing.\nfunc A() { B() }\n\nfunc B() {}\n\nfunc C() { A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "a_test.go"),
		[]byte("package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { A() }\n"), 0o644); err != nil {
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

	full, err := svc.Context(proj, "A", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Definitions) != 1 || full.Definitions[0].Source == "" {
		t.Fatalf("non-brief context should carry a source body, got %+v", full.Definitions)
	}
	if full.Definitions[0].SourceOmitted {
		t.Errorf("non-brief context should never set source_omitted, got %+v", full.Definitions[0])
	}

	brief, err := svc.Context(proj, "A", 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Definitions) != 1 {
		t.Fatalf("brief context definitions = %d, want 1: %+v", len(brief.Definitions), brief.Definitions)
	}
	bd := brief.Definitions[0]
	if bd.Source != "" {
		t.Errorf("brief context should drop the source body, got %q", bd.Source)
	}
	if !bd.SourceOmitted {
		t.Errorf("brief context should set source_omitted:true, got %+v", bd)
	}
	if bd.Signature != full.Definitions[0].Signature || bd.Doc != full.Definitions[0].Doc {
		t.Errorf("brief context should keep signature/doc, got %+v want signature/doc of %+v", bd, full.Definitions[0])
	}

	// Byte-compare: everything outside Definitions[].Source/SourceOmitted stays
	// unchanged by brief mode. Normalize brief's Definitions to full's Source/
	// SourceOmitted values, then the two reports must be deep-equal.
	normalized := *brief
	normalized.Definitions = append([]SourceMatch(nil), brief.Definitions...)
	for i := range normalized.Definitions {
		normalized.Definitions[i].Source = full.Definitions[i].Source
		normalized.Definitions[i].SourceOmitted = full.Definitions[i].SourceOmitted
	}
	if !reflect.DeepEqual(&normalized, full) {
		t.Errorf("brief context changed a field other than Definitions[].Source/SourceOmitted:\nbrief(normalized)=%+v\nfull=%+v", normalized, full)
	}
}

// TestContextCapsLargeLists guards the bundle's context-window safety: a hub with
// many callers has its lists capped to contextListCap, while the *Total fields
// still report the true count so a harness knows to drill for the rest.
func TestContextCapsLargeLists(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	var b strings.Builder
	b.WriteString("package app\n\nfunc Target() {}\n")
	const callers = contextListCap + 5
	for i := 0; i < callers; i++ {
		fmt.Fprintf(&b, "func C%d() { Target() }\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(b.String()), 0o644); err != nil {
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
	rep, err := svc.Context(proj, "Target", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CallersTotal != callers {
		t.Errorf("CallersTotal = %d, want %d (true count)", rep.CallersTotal, callers)
	}
	if len(rep.Callers) != contextListCap {
		t.Errorf("callers list = %d, want capped to %d", len(rep.Callers), contextListCap)
	}
}

// TestAnnotationsFlagDangling pins that the annotations list flags entries whose
// target no longer resolves to an indexed symbol (so stale notes after a refactor
// can be pruned), while resolving ones aren't flagged.
func TestAnnotationsFlagDangling(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Auth() {}\n\nfunc B() {}\n"), 0o644); err != nil {
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
	realID, _, _ := svc.AnnotateNode(proj, "Auth", "note", "ok", "")
	ghostID, _, _ := svc.AnnotateNode(proj, "Ghost", "note", "stale", "")
	pathOK, _, _, _ := svc.AnnotatePath(proj, "Auth", "B", "note", "ok", "")
	pathBad, _, _, _ := svc.AnnotatePath(proj, "Auth", "Ghost", "note", "stale", "")

	rep, err := svc.AllAnnotations(proj)
	if err != nil {
		t.Fatal(err)
	}
	d := make(map[int64]bool, len(rep.Dangling))
	for _, id := range rep.Dangling {
		d[id] = true
	}
	if !d[ghostID] {
		t.Error("a note on a missing symbol should be flagged dangling")
	}
	if !d[pathBad] {
		t.Error("a path note with a missing endpoint should be flagged dangling")
	}
	if d[realID] {
		t.Error("a note on an indexed symbol must not be flagged dangling")
	}
	if d[pathOK] {
		t.Error("a path note whose endpoints both resolve must not be flagged dangling")
	}
}

func TestSourceAndCallersSurfaceAnnotations(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
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
	if _, _, err := svc.AnnotateNode(proj, "app.B", "note", "check this", ""); err != nil {
		t.Fatal(err)
	}

	sr, err := svc.Source(proj, "B", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Annotations) != 1 {
		t.Errorf("source should surface the symbol's annotations, got %+v", sr.Annotations)
	}
	cr, err := svc.Callers(proj, "B")
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Annotations) != 1 {
		t.Errorf("callers should surface the queried symbol's annotations, got %+v", cr.Annotations)
	}
}

func TestFindSurfacesAnnotations(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Helper() {}\n\nfunc Other() {}\n"), 0o644); err != nil {
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
	if _, _, err := svc.AnnotateNode(proj, "app.Helper", "note", "look here", ""); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.FindSymbols(proj, "Helper", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got *SemanticHit
	for i := range rep.Hits {
		if rep.Hits[i].Symbol == "Helper" {
			got = &rep.Hits[i]
		}
	}
	if got == nil || len(got.Annotations) != 1 || got.Annotations[0].Note != "look here" {
		t.Errorf("find hit for Helper should carry its annotation, got %+v", rep.Hits)
	}
}

func TestDocs(t *testing.T) {
	full := Docs("")
	for _, want := range []string{"## overview", "## workflow", "## accuracy", "codemap_impact", "precise:true"} {
		if !strings.Contains(full, want) {
			t.Errorf("full docs should contain %q", want)
		}
	}
	// The guide must teach the agent-facing commands so an agent discovers them —
	// keep this in sync as commands ship (a stale guide hides capabilities).
	for _, want := range []string{"codemap_review", "codemap_read_order", "codemap_dependencies", "codemap_file_impact", "codemap_risk", "codemap_context_batch", "codemap_coverage", "codemap_grep", "deletion_analysis", "confirmed", "candidate", "candidates", "positions", "selectors"} {
		if !strings.Contains(full, want) {
			t.Errorf("agent guide should teach %q so agents discover it", want)
		}
	}
	// a known topic returns just that section.
	acc := Docs("accuracy")
	if !strings.Contains(acc, "name-based") || strings.Contains(acc, "## overview") {
		t.Errorf("topic docs should be just that section, got: %q", acc[:min(80, len(acc))])
	}
	// an unknown topic lists the available ones.
	if got := Docs("nope"); !strings.Contains(got, "available:") {
		t.Errorf("unknown topic should list available topics, got %q", got)
	}
}

func TestServiceProjects(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() {}\n\nfunc B() { A() }\n"), 0o644); err != nil {
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

	rep, err := svc.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Projects) != 1 {
		t.Fatalf("Projects() = %d, want 1: %+v", len(rep.Projects), rep.Projects)
	}
	p := rep.Projects[0]
	if p.Nodes == 0 || p.Files == 0 {
		t.Errorf("registered project should report index sizes: %+v", p)
	}
}

func TestServiceSource(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\n// Add sums two ints.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	rep, err := svc.Source(proj, "Add", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Matches) != 1 {
		t.Fatalf("Source(Add) matches = %d, want 1: %+v", len(rep.Matches), rep.Matches)
	}
	m := rep.Matches[0]
	if !strings.Contains(m.Source, "func Add(a, b int) int") || !strings.Contains(m.Source, "return a + b") {
		t.Errorf("source body not returned:\n%s", m.Source)
	}
	if m.Signature != "func Add(a, b int) int" || m.Doc != "Add sums two ints." {
		t.Errorf("source match missing signature/doc: %+v", m)
	}
	if m.SourceOmitted {
		t.Errorf("non-brief source should not set source_omitted: %+v", m)
	}

	// I05: brief mode drops the body but keeps signature/doc/location, and
	// flags what it dropped so an agent knows to re-call without brief.
	brief, err := svc.Source(proj, "Add", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Matches) != 1 {
		t.Fatalf("brief Source(Add) matches = %d, want 1: %+v", len(brief.Matches), brief.Matches)
	}
	bm := brief.Matches[0]
	if bm.Source != "" {
		t.Errorf("brief source should drop the body, got %q", bm.Source)
	}
	if !bm.SourceOmitted {
		t.Errorf("brief source should set source_omitted:true: %+v", bm)
	}
	if bm.Signature != m.Signature || bm.Doc != m.Doc || bm.File != m.File || bm.StartLine != m.StartLine || bm.EndLine != m.EndLine {
		t.Errorf("brief source should keep signature/doc/location identical to non-brief: brief=%+v full=%+v", bm, m)
	}
}

func TestStatusReportsVectors(t *testing.T) {
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
	sess.SetEmbedder(fakeEmbedder{dims: 8}) // embed without Ollama
	svc := NewService(sess)

	// structure-only first: no vectors.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Status(proj); st.Vectors != 0 {
		t.Errorf("structure-only status vectors = %d, want 0", st.Vectors)
	}
	// reindex with embeddings: vectors reported.
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true}, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Status(proj); st.Vectors == 0 {
		t.Errorf("embedded status should report vectors > 0, got %d", st.Vectors)
	}
	// A structure-only rebuild removes the project's old semantic records. They
	// must not survive and describe source that no longer exists in the graph.
	// Reopen without the test embedder too: maintenance cleanup must bypass the
	// profile guard so --no-embed remains usable after a model/dimension change.
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	maintenanceSession, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer maintenanceSession.Close()
	maintenanceSvc := NewService(maintenanceSession)
	rep, err := maintenanceSvc.Index(context.Background(), proj, index.Options{Reindex: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded {
		t.Error("structure-only rebuild must report embedded:false")
	}
	if st, _ := maintenanceSvc.Status(proj); st.Vectors != 0 {
		t.Errorf("structure-only rebuild left %d stale vectors, want 0", st.Vectors)
	}
}

func TestVecgrepSemanticOwnerSkipsAndClearsLocalEmbeddings(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "main.go", "package app\n\nfunc Authenticate() {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8})
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Status(proj); st.Vectors == 0 {
		t.Fatal("test setup did not create local vectors")
	}

	sess.Config.Semantic.Backend = "vecgrep"
	rep, err := svc.Index(context.Background(), proj, index.Options{Reindex: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded || !strings.Contains(rep.Warning, "semantic.backend=vecgrep") {
		t.Fatalf("vecgrep-owned index report = %+v", rep)
	}
	if st, _ := svc.Status(proj); st.Vectors != 0 || st.SemanticBackend != "vecgrep" {
		t.Fatalf("vecgrep semantic owner status = vectors:%d backend:%q", st.Vectors, st.SemanticBackend)
	}
}

func TestEmbeddingFailureClearsSemanticModeAndFallsBack(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "a.go", "package app\n\nfunc Authenticate() {}\n")
	mustWrite(t, proj, "b.go", "package app\n\nfunc Render() {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8})
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, true); err != nil {
		t.Fatal(err)
	}
	if st, err := svc.Status(proj); err != nil || st.Vectors < 2 {
		t.Fatalf("seed status vectors = (%v, %v), want vectors from both files", st, err)
	}

	// Change only a.go, then fail its replacement embeddings. Keeping b.go's old
	// vector would expose a silently partial local semantic collection.
	mustWrite(t, proj, "a.go", "package app\n\nfunc Authenticate() {}\nfunc Authorize() {}\n")
	sess.SetEmbedder(failingEmbedder{dims: 8, err: errors.New("provider unavailable")})
	rep, err := svc.Index(context.Background(), proj, index.Options{}, true)
	if err != nil {
		t.Fatalf("structural indexing should survive embedding degradation: %v", err)
	}
	if rep.Embedded || !strings.Contains(rep.Warning, "embeddings skipped") {
		t.Fatalf("degraded index report = %+v, want structure-only warning", rep)
	}
	if st, err := svc.Status(proj); err != nil || st.Vectors != 0 {
		t.Fatalf("degraded status vectors = (%v, %v), want project-wide zero", st, err)
	}
	semantic, err := svc.Semantic(context.Background(), proj, "authentication", 5)
	if err != nil || semantic.Mode != "none" {
		t.Fatalf("Semantic after degradation = (%+v, %v), want mode none without calling failed provider", semantic, err)
	}
	search, err := svc.Search(context.Background(), proj, "Auth", 5)
	if err != nil || search.Mode != "name" || !hasSymbolHit(search.Hits, "Authenticate") {
		t.Fatalf("Search after degradation = (%+v, %v), want name fallback with Authenticate", search, err)
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

	// A query that names a symbol surfaces it first via the hybrid (vector + BM25
	// over symbol/fqn) path — the keyword match boosts it even though the fake
	// vectors carry no real meaning. This is what makes Semantic use HybridSearch
	// instead of pure vector search.
	named, err := svc.Semantic(context.Background(), proj, "Render", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(named.Hits) == 0 || named.Hits[0].Symbol != "Render" {
		t.Errorf("naming a symbol should rank it first via hybrid BM25, got %+v", named.Hits)
	}
}

// TestServiceDoctor checks the environment report's shape regardless of which
// tools the host has installed: every expected check is present, and the
// hint/OK invariant holds (a failing check carries remediation, a passing one
// doesn't). The fake embedder skips the network probe.
func TestServiceDoctor(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8}) // not an Ollama probe → no network call

	rep := NewService(sess).Doctor(context.Background())
	if rep.DataDir == "" {
		t.Error("doctor should report the data dir")
	}
	names := map[string]bool{}
	for _, c := range rep.Checks {
		names[c.Name] = true
	}
	for _, want := range []string{
		"data directory", "go toolchain", "gopls",
		"typescript-language-server (typescript/javascript)",
		"pyright-langserver (python)", "embeddings (Ollama)",
		"background daemon",
	} {
		if !names[want] {
			t.Errorf("doctor missing check %q (have %v)", want, names)
		}
	}
	for _, c := range rep.Checks {
		if !c.OK && c.Hint == "" {
			t.Errorf("failing check %q should carry a remediation hint", c.Name)
		}
		if c.OK && c.Hint != "" {
			t.Errorf("passing check %q should not carry a hint", c.Name)
		}
	}
}

func TestHotspotsFlagsSharedNames(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func Solo() {}\n\n" +
		"func A() { var t T; t.Close() }\n" +
		"func B() { var u U; u.Close() }\n" +
		"func C() { Solo() }\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	rep, err := svc.Hotspots(proj, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawClose, sawSolo bool
	for _, h := range rep.Hotspots {
		switch h.Symbol {
		case "Close":
			sawClose = true
			if h.SharedName != 2 {
				t.Errorf("Close is defined twice; SharedName = %d, want 2", h.SharedName)
			}
		case "Solo":
			sawSolo = true
			if h.SharedName != 0 {
				t.Errorf("Solo is unique; SharedName = %d, want 0 (no flag)", h.SharedName)
			}
		}
	}
	if !sawClose || !sawSolo {
		t.Fatalf("expected both Close and Solo in hotspots (saw Close=%v Solo=%v)", sawClose, sawSolo)
	}
}

func TestAmbiguityNoteIsProvenanceAware(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/a\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package a\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func P() { var t T; t.Close() }\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// Name-based index: the note flags name-based over-matching and points at --precise.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	nb, _ := svc.Callers(proj, "Close")
	if !strings.Contains(nb.Note, "name-based") || !strings.Contains(nb.Note, "--precise") {
		t.Errorf("name-based ambiguity note should mention name-based + --precise, got %q", nb.Note)
	}

	// Precise index: the note acknowledges exact resolution, not "name-based".
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	pr, _ := svc.Callers(proj, "Close")
	if !strings.Contains(pr.Note, "resolved precisely") {
		t.Errorf("precise ambiguity note should acknowledge exact resolution, got %q", pr.Note)
	}
	if strings.Contains(pr.Note, "name-based") {
		t.Errorf("precise ambiguity note must not call the results name-based, got %q", pr.Note)
	}
}

func TestStatusReportsPreciseEdges(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/s\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package s\n\nfunc A() { B() }\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// Name-based index: no precise edges.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Status(proj); st.PreciseEdges != 0 {
		t.Errorf("name-based index PreciseEdges = %d, want 0", st.PreciseEdges)
	}

	// Precise reindex: the A->B call edge is now go/types-resolved.
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Status(proj); st.PreciseEdges == 0 {
		t.Errorf("precise index PreciseEdges = %d, want > 0", st.PreciseEdges)
	}
}

func TestHotspotsInflationFlagIsProvenanceAware(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/h\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package h\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func A() { var t T; t.Close() }\n" +
		"func B() { var t T; t.Close() }\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	flagOf := func(want string) int {
		rep, err := svc.Hotspots(proj, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range rep.Hotspots {
			if h.FQN == want {
				return h.SharedName
			}
		}
		return -1 // not present
	}

	// Name-based: T.Close's in-degree is inflated by fan-out → flagged.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if got := flagOf("h.T.Close"); got != 2 {
		t.Errorf("name-based T.Close SharedName = %d, want 2 (inflated, flagged)", got)
	}

	// Precise: T.Close's callers were resolved exactly → accurate count, no flag,
	// even though the name "Close" is still shared by two definitions.
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := flagOf("h.T.Close"); got != 0 {
		t.Errorf("precise T.Close SharedName = %d, want 0 (count is accurate, must not be mislabeled inflated)", got)
	}
}

func TestOrphansExcludesMainAndInit(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// main and init are runtime-invoked (never dead); Orphan genuinely has no caller.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package main\n\nfunc main() {}\n\nfunc init() {}\n\nfunc Orphan() {}\n"), 0o644); err != nil {
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

	rep, err := svc.Orphans(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range rep.Orphans {
		got[o.Symbol] = true
	}
	if !got["Orphan"] {
		t.Errorf("Orphan (uncalled) should be a dead-code candidate, got %v", got)
	}
	if got["main"] || got["init"] {
		t.Errorf("main/init are runtime-invoked and must not appear as dead-code candidates, got %v", got)
	}
}

func TestAnnotateReportsUnknownTarget(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Real() {}\n"), 0o644); err != nil {
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

	// Real symbol: matched.
	if _, matched, err := svc.AnnotateNode(proj, "Real", "src", "note", ""); err != nil || !matched {
		t.Errorf("annotating an indexed symbol should report matched=true, got matched=%v err=%v", matched, err)
	}
	// Ghost symbol: saved but not matched (so the caller can warn).
	id, matched, err := svc.AnnotateNode(proj, "GhostXYZ", "src", "note", "")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("annotating a nonexistent symbol should report matched=false")
	}
	if id == 0 {
		t.Error("the annotation should still be saved (reindex-durable), got id 0")
	}
}

func TestAnnotatePathReportsUnknownEndpoint(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\nfunc B() {}\n"), 0o644); err != nil {
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

	// Both endpoints indexed → matched.
	if _, _, matched, err := svc.AnnotatePath(proj, "A", "B", "s", "chain", ""); err != nil || !matched {
		t.Errorf("path over two indexed symbols should be matched, got matched=%v err=%v", matched, err)
	}
	// One ghost endpoint → saved but not matched.
	id, _, matched, err := svc.AnnotatePath(proj, "A", "GhostXYZ", "s", "chain", "")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("a path with a nonexistent endpoint should report matched=false")
	}
	if id == 0 {
		t.Error("the path annotation should still be saved, got id 0")
	}
}

func TestPathReportsMissingEndpoint(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// A calls B: A→B has a path, B→A does not; both A and B exist.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc A() { B() }\nfunc B() {}\n"), 0o644); err != nil {
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

	// Typo'd endpoint: a clear "not a symbol" note, not "no call path".
	miss, err := svc.Path(proj, "Nope", "B")
	if err != nil {
		t.Fatal(err)
	}
	if miss.Found || miss.Note == "" || !strings.Contains(miss.Note, "Nope") {
		t.Errorf("missing endpoint should be reported as not-a-symbol, got found=%v note=%q", miss.Found, miss.Note)
	}

	// Real path: found, no note.
	hit, err := svc.Path(proj, "A", "B")
	if err != nil {
		t.Fatal(err)
	}
	if !hit.Found || hit.Note != "" {
		t.Errorf("A→B should be found with no note, got found=%v note=%q", hit.Found, hit.Note)
	}

	// Both exist but unconnected: not found, and NO missing-endpoint note
	// (so the CLI falls back to the plain "no call path" message).
	none, err := svc.Path(proj, "B", "A")
	if err != nil {
		t.Fatal(err)
	}
	if none.Found || none.Note != "" {
		t.Errorf("B→A (both real, no path) should be not-found with no note, got found=%v note=%q", none.Found, none.Note)
	}
}

func TestCallersWarnsOnAmbiguousName(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func RunT() { var t T; t.Close() }\n" +
		"func RunU() { var u U; u.Close() }\n" +
		"func Solo() {}\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	// Ambiguous name: callers merge both Close definitions and must say so.
	amb, err := svc.Callers(proj, "Close")
	if err != nil {
		t.Fatal(err)
	}
	if amb.Note == "" || !strings.Contains(amb.Note, "definitions") {
		t.Errorf("ambiguous callers should warn it merges same-named defs, got %q", amb.Note)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous callers should surface the merged definition set, got %d candidates: %+v", len(amb.Candidates), amb.Candidates)
	}
	assertPlausibleCandidates(t, amb.Candidates)
	// Unique name: no warning.
	solo, err := svc.Callees(proj, "Solo")
	if err != nil {
		t.Fatal(err)
	}
	if solo.Note != "" {
		t.Errorf("unambiguous callees should carry no note, got %q", solo.Note)
	}
	if len(solo.Candidates) != 0 {
		t.Errorf("unambiguous callees should carry no candidates, got %+v", solo.Candidates)
	}
}

// assertPlausibleCandidates checks that every AmbiguityCandidate carries a
// usable selector and location — the shape an agent re-issues a query with.
func assertPlausibleCandidates(t *testing.T, candidates []AmbiguityCandidate) {
	t.Helper()
	for i, c := range candidates {
		if c.Selector == nil || c.Selector.File == "" || c.Selector.StartLine <= 0 || c.Selector.FQN == "" || c.Selector.Kind == "" {
			t.Errorf("candidate[%d] selector incomplete: %+v", i, c)
		}
		if c.File == "" || c.StartLine <= 0 {
			t.Errorf("candidate[%d] missing file/start_line: %+v", i, c)
		}
		if c.Signature == "" {
			t.Errorf("candidate[%d] missing signature: %+v", i, c)
		}
	}
}

func TestImpactWarnsOnAmbiguousName(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// Two same-named methods make "Close" ambiguous; "Solo" is unique.
	src := "package app\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func RunT() { var t T; t.Close() }\n" +
		"func RunU() { var u U; u.Close() }\n" +
		"func Solo() {}\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	// Ambiguous: impact merges both Close definitions and must say so.
	amb, err := svc.Impact(proj, "Close", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(amb.Locations) < 2 {
		t.Fatalf("expected >1 definition for Close, got %d", len(amb.Locations))
	}
	if amb.Note == "" || !strings.Contains(amb.Note, "definitions") {
		t.Errorf("ambiguous impact should warn it merges same-named defs, got %q", amb.Note)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous impact should surface the merged definition set, got %d candidates: %+v", len(amb.Candidates), amb.Candidates)
	}
	assertPlausibleCandidates(t, amb.Candidates)

	// Unambiguous: no warning.
	solo, err := svc.Impact(proj, "Solo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if solo.Note != "" {
		t.Errorf("unambiguous impact should carry no note, got %q", solo.Note)
	}
	if len(solo.Candidates) != 0 {
		t.Errorf("unambiguous impact should carry no candidates, got %+v", solo.Candidates)
	}
}

// ambiguousCloseProject builds the shared T/U.Close() + Solo() fixture used
// across the candidates tests: two same-named methods make "Close" ambiguous,
// while "Solo" is unique.
func ambiguousCloseProject(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Close() {}\nfunc (U) Close() {}\n\n" +
		"func RunT() { var t T; t.Close() }\n" +
		"func RunU() { var u U; u.Close() }\n" +
		"func Solo() {}\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
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

func TestContextCandidatesOnAmbiguousName(t *testing.T) {
	svc, proj := ambiguousCloseProject(t)

	amb, err := svc.Context(proj, "Close", 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous context should surface the merged definition set (via the callers-relation passthrough), got %d candidates: %+v", len(amb.Candidates), amb.Candidates)
	}
	assertPlausibleCandidates(t, amb.Candidates)

	solo, err := svc.Context(proj, "Solo", 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(solo.Candidates) != 0 {
		t.Errorf("unambiguous context should carry no candidates, got %+v", solo.Candidates)
	}
}

func TestRiskCandidatesOnAmbiguousName(t *testing.T) {
	svc, proj := ambiguousCloseProject(t)

	amb, err := svc.Risk(proj, "Close", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous risk should surface the merged definition set, got %d candidates: %+v", len(amb.Candidates), amb.Candidates)
	}
	assertPlausibleCandidates(t, amb.Candidates)
	if !strings.Contains(amb.Note, "definitions") {
		t.Errorf("ambiguous risk should now carry the impact ambiguity note (riskFromImpact fix), got %q", amb.Note)
	}

	solo, err := svc.Risk(proj, "Solo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(solo.Candidates) != 0 || solo.Note != "" {
		t.Errorf("unambiguous risk should carry no candidates/note, got candidates=%+v note=%q", solo.Candidates, solo.Note)
	}
}

func TestSourceCandidatesOnAmbiguousName(t *testing.T) {
	svc, proj := ambiguousCloseProject(t)

	amb, err := svc.Source(proj, "Close", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(amb.Candidates) != 2 || len(amb.Candidates) != len(amb.Matches) {
		t.Fatalf("ambiguous source candidates should mirror matches 1:1, got %d candidates, %d matches", len(amb.Candidates), len(amb.Matches))
	}
	assertPlausibleCandidates(t, amb.Candidates)

	solo, err := svc.Source(proj, "Solo", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(solo.Candidates) != 0 {
		t.Errorf("unambiguous source should carry no candidates, got %+v", solo.Candidates)
	}
}

// TestImpactBySelectorNeverSetsCandidates pins design decision #4: a query
// already scoped to one exact definition via a selector never reports
// candidates, even when the bare name is ambiguous project-wide — the
// selector-resolution seam (resolveSourceSelector) already errors instead of
// unioning when the selector itself is still ambiguous, so the downstream
// impactFromLocations call always sees a one-node slice.
func TestImpactBySelectorNeverSetsCandidates(t *testing.T) {
	svc, proj := ambiguousCloseProject(t)

	amb, err := svc.Impact(proj, "Close", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(amb.Locations) != 2 {
		t.Fatalf("expected 2 definitions for Close, got %d", len(amb.Locations))
	}
	selector := SymbolSelector{File: amb.Locations[0].File, StartLine: amb.Locations[0].StartLine, FQN: amb.Locations[0].FQN, Kind: amb.Locations[0].Kind}

	exact, err := svc.ImpactBySelector(proj, selector, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.Candidates) != 0 {
		t.Errorf("a selector-scoped impact query must never set candidates, got %+v", exact.Candidates)
	}
	if exact.Note != "" && strings.Contains(exact.Note, "matches") {
		t.Errorf("a selector-scoped impact query should not repeat the name-ambiguity note, got %q", exact.Note)
	}
}

func TestPreciseFallbackToNameBased(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	// Two same-named methods + their callers: name-based callers of Process
	// over-matches (both RunT and RunU), which is exactly the result the fallback
	// should return when precise resolution is unavailable.
	src := "package app\n\n" +
		"type T struct{}\ntype U struct{}\n\n" +
		"func (T) Process() {}\nfunc (U) Process() {}\n\n" +
		"func RunT() { var t T; t.Process() }\n" +
		"func RunU() { var u U; u.Process() }\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	// Simulate a language-server failure (e.g. gopls "no views") and confirm we
	// degrade to name-based results with an explanatory note rather than erroring.
	rep, err := svc.preciseFallback(proj, "Process", errors.New("jsonrpc error 0: no views"), svc.Callers)
	if err != nil {
		t.Fatalf("fallback should not error, got %v", err)
	}
	if rep.Note == "" || !strings.Contains(rep.Note, "name-based") {
		t.Errorf("fallback should note it used name-based results, got %q", rep.Note)
	}
	if len(rep.Results) == 0 {
		t.Error("fallback should return the name-based callers, got none")
	}
}

func TestIndexedReportsRegistration(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// Cold repo: not indexed.
	if ok, _, err := svc.Indexed(proj); err != nil || ok {
		t.Fatalf("Indexed before index = (%v, %v), want (false, nil)", ok, err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	// After indexing: registered.
	if ok, name, err := svc.Indexed(proj); err != nil || !ok || name == "" {
		t.Fatalf("Indexed after index = (%v, %q, %v), want (true, <name>, nil)", ok, name, err)
	}
}

// TestIndexedFalseWhenRegisteredButEmpty pins that a project registered with init
// but never indexed (0 nodes) reports NOT indexed — so query commands say "run
// codemap index" instead of misleading empties like "callers: none".
func TestIndexedFalseWhenRegisteredButEmpty(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// Registered (init) but not indexed → not indexed.
	if _, err := svc.Init(proj, false); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := svc.Indexed(proj); err != nil || ok {
		t.Fatalf("Indexed after init-only = (%v, %v), want (false, nil) — registered but 0 nodes", ok, err)
	}
	// After indexing → indexed.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := svc.Indexed(proj); err != nil || !ok {
		t.Fatalf("Indexed after index = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestSemanticNoEmbeddings(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Authenticate() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// Deliberately leave the embedder as the default (Ollama) and DON'T embed:
	// a structure-only project must answer without ever calling it.
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Semantic(context.Background(), proj, "authentication", 5)
	if err != nil {
		t.Fatalf("semantic on a structure-only project should not error (no embedder call), got %v", err)
	}
	if rep.Mode != "none" {
		t.Errorf("mode = %q, want %q", rep.Mode, "none")
	}
	if len(rep.Hits) != 0 {
		t.Errorf("expected no hits without embeddings, got %d", len(rep.Hits))
	}
	if !strings.Contains(rep.Note, "no embeddings") {
		t.Errorf("note should explain the project has no embeddings, got %q", rep.Note)
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

	// pin an annotation so we can confirm the precise path surfaces it too.
	if _, _, err := svc.AnnotateNode(proj, "Helper", "note", "precise too", ""); err != nil {
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
	if len(rep.Annotations) != 1 || rep.Annotations[0].Note != "precise too" {
		t.Errorf("precise callers should surface the symbol's annotations, got %+v", rep.Annotations)
	}
}

// TestPreciseCallersTypeScript pins P0: precise callers resolve for TypeScript ON
// DEMAND — the project is indexed WITHOUT --precise (so the TS call graph is empty
// by default), yet PreciseCallers drives typescript-language-server's callHierarchy
// for the one queried symbol and finds its caller. Local-only (skips without the
// server / node), like the gopls tests.
func TestPreciseCallersTypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.ts"),
		[]byte("export function helper(): void {}\n\nexport function run(): void { helper(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	// Structure-only index (no --precise) → the TS call graph is empty by default.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	rep, err := svc.PreciseCallers(ctx, proj, "helper")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Results {
		if r.Symbol == "run" {
			found = true
		}
	}
	if !found {
		t.Errorf("precise callers of helper (TS, on demand, no --precise) = %+v, want to include run", rep.Results)
	}
}

// TestCallersAutoUpgradesTypeScript pins P0 slice 2: a plain Callers query on a TS
// symbol (project indexed WITHOUT --precise) auto-upgrades to an on-demand
// callHierarchy and returns the real caller — clearing the "unresolved" note.
func TestCallersAutoUpgradesTypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.ts"),
		[]byte("export function helper(): void {}\n\nexport function run(): void { helper(); }\n"), 0o644); err != nil {
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
	rep, err := svc.Callers(proj, "helper")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resolution != "" {
		t.Errorf("Callers should have auto-upgraded (no resolution note), got %q", rep.Resolution)
	}
	found := false
	for _, r := range rep.Results {
		if r.Symbol == "run" {
			found = true
		}
	}
	if !found {
		t.Errorf("auto-upgraded TS callers of helper = %+v, want to include run", rep.Results)
	}
}

// TestImpactHeuristicTestCoverage pins P0 slice 3: a symbol with NO call-graph
// test coverage but REFERENCED in a test file is reported tested (heuristically),
// not untested — the fix for #196's filtered-callback blind spot (and TS without
// --precise). Pure-Go, no language server: the test file references Foo as a value
// (no call edge), so the call graph sees no coverage but the file scan does.
func TestImpactHeuristicTestCoverage(t *testing.T) {
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "foo.go"), []byte("package m\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// References Foo as a value (no call edge) → invisible to the call graph.
	if err := os.WriteFile(filepath.Join(proj, "foo_test.go"),
		[]byte("package m\n\nimport \"testing\"\n\nfunc TestThing(t *testing.T) { _ = Foo }\n"), 0o644); err != nil {
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
	rep, err := svc.Impact(proj, "Foo", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Untested {
		t.Errorf("Foo is referenced by a test file — must not be reported untested")
	}
	hasHeuristic := false
	for _, te := range rep.Tests {
		if te.Heuristic && strings.Contains(te.File, "foo_test.go") {
			hasHeuristic = true
		}
	}
	if !hasHeuristic {
		t.Errorf("expected a heuristic covering test naming foo_test.go, got tests=%+v", rep.Tests)
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

// TestPreciseRelationsSoftMissUnregisteredProject pins P1-06 (B1): a soft miss
// from preciseRelations (here, a project that was never indexed) must return the
// errPreciseUnresolved sentinel, not a nil error with empty results. A nil error
// is what autoUpgradeRelation reads as "genuinely resolved" — conflating the two
// is exactly how a soft miss used to get reported as a confidently-wrong
// "resolved on demand" with zero callers. No language server is needed: the
// project lookup fails before any server is spawned.
func TestPreciseRelationsSoftMissUnregisteredProject(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callers, callees, _, err := svc.preciseRelations(ctx, t.TempDir(), "Anything", "", 0)
	if !errors.Is(err, errPreciseUnresolved) {
		t.Fatalf("preciseRelations on an unregistered project err = %v, want errPreciseUnresolved", err)
	}
	if callers != nil || callees != nil {
		t.Errorf("soft miss should return nil callers/callees, got %+v / %+v", callers, callees)
	}
}

// TestCallersSoftMissKeepsHonestNote pins P1-06 (B1) end-to-end through
// autoUpgradeRelation: when the live file no longer has a documentSymbol
// matching the queried name (findSymbolPos misses — the symbol was renamed
// after indexing, simulating a stale/racing index), preciseRelations returns
// errPreciseUnresolved and Callers must keep the honest "unresolved" note
// instead of overwriting it with "resolved on demand" and an empty result.
func TestCallersSoftMissKeepsHonestNote(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tsFile := filepath.Join(proj, "main.ts")
	if err := os.WriteFile(tsFile,
		[]byte("export function foo(): void {}\n\nexport function run(): void { foo(); }\n"), 0o644); err != nil {
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

	// The graph still knows "foo" at its indexed position, but the live file on
	// disk no longer declares anything named "foo" — preciseRelations reads the
	// file fresh, so its documentSymbol pass can't find "foo" any more.
	if err := os.WriteFile(tsFile,
		[]byte("export function renamedAwayFromFoo(): void {}\n\nexport function run(): void { renamedAwayFromFoo(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Callers(proj, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resolution == "" {
		t.Error("a soft miss (renamed-away symbol) must keep the honest unresolved note, got empty Resolution")
	}
	if rep.Note == "resolved on demand via the language server's callHierarchy (no --precise needed)" {
		t.Error("a soft miss must not claim it was resolved on demand")
	}
	if rep.CallGraph == CallGraphResolved {
		t.Errorf("call_graph = %q, want anything but resolved on a soft miss", rep.CallGraph)
	}
	if len(rep.Results) != 0 {
		t.Errorf("a soft miss must not fabricate results, got %+v", rep.Results)
	}
}

// TestCallersAutoUpgradeGenuineZero pins the other half of P1-06 (B1): a symbol
// the server DID locate and prepare a call hierarchy for, but which genuinely has
// no callers, must be reported as resolved (honest zero) — not conflated with a
// soft miss. orphan() is a real, well-formed function with no call sites.
func TestCallersAutoUpgradeGenuineZero(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.ts"),
		[]byte("export function orphan(): void {}\n\nexport function run(): void {}\n"), 0o644); err != nil {
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

	rep, err := svc.Callers(proj, "orphan")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resolution != "" {
		t.Errorf("a genuinely-resolved zero must clear the unresolved note, got %q", rep.Resolution)
	}
	if rep.CallGraph != CallGraphResolved {
		t.Errorf("call_graph = %q, want %q for a genuinely-resolved symbol", rep.CallGraph, CallGraphResolved)
	}
	if !rep.Found {
		t.Error("orphan is a real, indexed symbol — Found should be true")
	}
	if len(rep.Results) != 0 {
		t.Errorf("orphan has no callers — expected an honest empty, got %+v", rep.Results)
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

// TestImpactBlankSymbolRejected pins P1-04: pre-fix Impact("") returned
// Found:true with every file as a "location" (a confidently-wrong
// answer for the agent audience). The fix: the service seam rejects
// blank symbols up front with a clear Note and Found:false.
func TestImpactBlankSymbolRejected(t *testing.T) {
	isolate(t)
	svc := NewService(noopSess(t))
	for _, in := range []string{"", " ", "\t", "   \n"} {
		rep, err := svc.Impact(t.TempDir(), in, 3)
		if err != nil {
			t.Errorf("Impact(%q) errored: %v", in, err)
			continue
		}
		if rep.Found {
			t.Errorf("Impact(%q) must return Found:false for a blank symbol (P1-04 regression — pre-fix it matched every file node)", in)
		}
		if rep.Note == "" {
			t.Errorf("Impact(%q) must include a Note explaining the rejection", in)
		}
	}
}

// TestCallersCalleesBlankSymbolRejected covers the same P1-04 contract
// on the two RelationReport-shaped read queries.
func TestCallersCalleesBlankSymbolRejected(t *testing.T) {
	isolate(t)
	svc := NewService(noopSess(t))
	for name, fn := range map[string]func(string, string) (*RelationReport, error){
		"Callers": svc.Callers,
		"Callees": svc.Callees,
	} {
		for _, in := range []string{"", "  "} {
			rep, err := fn(t.TempDir(), in)
			if err != nil {
				t.Errorf("%s(%q) errored: %v", name, in, err)
				continue
			}
			if rep.Found {
				t.Errorf("%s(%q) must return Found:false for a blank symbol (P1-04 regression)", name, in)
			}
		}
	}
}

// noopSess returns a Session with the bare minimum for service calls
// that reject before they reach the graph layer. It deliberately
// doesn't open a database — the validators must short-circuit.
func noopSess(t *testing.T) *Session {
	t.Helper()
	// Open a temp-home so cfg.New() doesn't fail, but never call Open()
	// on the database (the path that needs it never runs in this test).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEMAP_CONFIG", "")
	cfg := *config.DefaultConfig()
	return &Session{Config: &cfg}
}

// TestVueCallGraphHonesty pins P1-06 (B2): Vue SFC symbols are stored
// with Language="vue", but noNameBasedCallLang didn't list "vue" — so
// Vue symbols got a confidently-empty callers/impact (untested:true,
// no resolution note). Adding "vue" to noNameBasedCallLang makes the
// absence honest: callers/impact on a Vue symbol without --precise
// carry a resolution note instead of a false "none".
func TestVueCallGraphHonesty(t *testing.T) {
	if !noNameBasedCallLang("vue") {
		t.Error("noNameBasedCallLang must return true for 'vue' (P1-06: Vue has no name-based call edges)")
	}
	// TS and Python should still be there.
	if !noNameBasedCallLang("typescript") || !noNameBasedCallLang("python") {
		t.Error("noNameBasedCallLang must still return true for typescript and python")
	}
	// Go should still be false (has name-based call edges).
	if noNameBasedCallLang("go") {
		t.Error("noNameBasedCallLang must return false for 'go' (has name-based call edges)")
	}
}

// TestFindSymbolsMatchedIn pins panel idea I19: the no-Ollama search floor
// (FindSymbols, via Search's fallback) reports which field satisfied a
// multi-word query — name match ("symbol") ranks ahead of a docstring-only
// match ("docstring") — and Ollama never enters the picture (pure fixture,
// no embeddings requested).
func TestFindSymbolsMatchedIn(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\n" +
		"func ParseSelector() {}\n\n" +
		"// ValidateInput parses the given selector before use.\n" +
		"func ValidateInput() {}\n"
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(src), 0o644); err != nil {
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

	rep, err := svc.FindSymbols(proj, "parse selector", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) < 2 {
		t.Fatalf("FindSymbols('parse selector') = %d hits, want >= 2 (name + docstring match); got %+v", len(rep.Hits), rep.Hits)
	}
	if rep.Hits[0].Symbol != "ParseSelector" || rep.Hits[0].MatchedIn != "symbol" {
		t.Errorf("name match should rank first with matched_in=symbol: got %+v", rep.Hits[0])
	}
	found := false
	for _, h := range rep.Hits[1:] {
		if h.Symbol == "ValidateInput" {
			found = true
			if h.MatchedIn != "docstring" {
				t.Errorf("ValidateInput matched_in = %q, want docstring", h.MatchedIn)
			}
		}
	}
	if !found {
		t.Errorf("docstring-only match ValidateInput missing from hits: %+v", rep.Hits)
	}
}

// TestCallersKeepsNameCandidatesWhenOnDemandFindsZero pins the interaction of
// the tsscan name-based JSX call edges with autoUpgradeRelation: when the
// on-demand callHierarchy genuinely resolves but reports ZERO callers (in a
// bare .jsx project without a ts/jsconfig the server cannot see cross-file JSX
// composition), the graph's name-based candidate edges must survive — not be
// overwritten by a confidently-wrong empty "resolved on demand" answer.
func TestCallersKeepsNameCandidatesWhenOnDemandFindsZero(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "widget.jsx"),
		[]byte("export function Widget({ label }) {\n  return <button>{label}</button>;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "app.jsx"),
		[]byte("import { Widget } from './widget';\n\nexport function App() {\n  return <div><Widget label=\"hi\" /></div>;\n}\n"), 0o644); err != nil {
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

	rep, err := svc.Callers(proj, "Widget")
	if err != nil {
		t.Fatal(err)
	}
	var hasApp bool
	for _, r := range rep.Results {
		if r.Symbol == "App" {
			hasApp = true
		}
	}
	if !hasApp {
		t.Fatalf("callers of Widget = %+v, want the name-based candidate App to survive the on-demand upgrade", rep.Results)
	}
	if rep.CallGraph == CallGraphResolved && len(rep.Results) == 0 {
		t.Error("an empty on-demand answer must not claim a resolved call graph over existing candidates")
	}
}
