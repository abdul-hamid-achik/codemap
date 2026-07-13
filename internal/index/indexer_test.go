package index

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// TestDefaultExcludesBuildOutput proves the default excludes catch build-output
// directory variants (dist-chrome, build-web) and coverage — not just the exact
// "dist"/"build" — so minified/generated code doesn't pollute the index. Found
// dogfooding a real TS project whose dist-chrome/dist-firefox extension build
// was getting indexed (garbage "<function>" symbols from minified JS).
func TestDefaultExcludesBuildOutput(t *testing.T) {
	g, _ := newStores(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/real.go", "package src\nfunc RealSource() {}\n")
	writeFile(t, dir, "dist-chrome/built.go", "package x\nfunc BuiltArtifact() {}\n")
	writeFile(t, dir, "build-web/gen.go", "package x\nfunc GeneratedWeb() {}\n")
	writeFile(t, dir, "coverage/cov.go", "package x\nfunc CoverageJunk() {}\n")
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1 (dist-chrome/build-web/coverage excluded)", res.FilesIndexed)
	}
	for _, sym := range []string{"BuiltArtifact", "GeneratedWeb", "CoverageJunk"} {
		if nodes, _ := g.FindNodesBySymbol(pid, sym); len(nodes) != 0 {
			t.Errorf("%s is build output and should be excluded, but it was indexed", sym)
		}
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "RealSource"); len(nodes) != 1 {
		t.Errorf("RealSource should be indexed, got %d nodes", len(nodes))
	}
}

// TestIndexSkipsGeneratedCode pins finding B: generated Go is skipped two ways —
// by the *_gen.go exclude glob AND by the canonical "// Code generated … DO NOT
// EDIT." header (regardless of filename) — so it doesn't pollute find/orphans.
func TestIndexSkipsGeneratedCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.go", "package app\nfunc Real() {}\n")
	writeFile(t, dir, "queries.go", "// Code generated by sqlc. DO NOT EDIT.\n\npackage app\n\nfunc GenByHeader() {}\n") // header-detected
	writeFile(t, dir, "mock_gen.go", "package app\nfunc GenByName() {}\n")                                               // glob-excluded
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Generated) != 1 || !strings.Contains(res.Generated[0], "queries.go") {
		t.Errorf("Generated should name queries.go, got %v", res.Generated)
	}
	for _, sym := range []string{"GenByHeader", "GenByName"} {
		if nodes, _ := g.FindNodesBySymbol(pid, sym); len(nodes) != 0 {
			t.Errorf("%s is generated and must not be indexed", sym)
		}
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "Real"); len(nodes) != 1 {
		t.Errorf("Real (hand-written) should be indexed, got %d nodes", len(nodes))
	}
}

// TestIsGenerated pins the header heuristic: the canonical marker is honored only
// before the package clause and must include the "DO NOT EDIT." tail.
func TestIsGenerated(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"// Code generated by protoc-gen-go. DO NOT EDIT.\npackage x\n", true},
		{"//go:build linux\n// Code generated by stringer. DO NOT EDIT.\npackage x\n", true}, // after a build tag
		{"package x\n// Code generated by foo. DO NOT EDIT.\n", false},                       // after package: not honored
		{"// Code generated by foo.\npackage x\n", false},                                    // missing DO NOT EDIT
		{"package x\nfunc F() {}\n", false},                                                  // ordinary source
	}
	for i, c := range cases {
		if got := isGenerated([]byte(c.src)); got != c.want {
			t.Errorf("case %d: isGenerated = %v, want %v", i, got, c.want)
		}
	}
}

// TestIndexFilesIncremental pins BD.9: IndexFiles (re)indexes just the named
// files (resolving their edges) and prunes ones gone from disk — the daemon's
// incremental-sync path.
func TestIndexFilesIncremental(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	// Add a new file; incrementally index just it.
	writeFile(t, dir, "c.go", "package app\nfunc New1() { Run() }\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"c.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "New1"); len(nodes) != 1 {
		t.Errorf("New1 should be indexed after IndexFiles, got %d nodes", len(nodes))
	}
	// Its call edge resolved against the project-wide symbols.
	hasNew1 := false
	callers, _ := g.Callers(pid, "Run")
	for _, n := range callers {
		if n.Symbol == "New1" {
			hasNew1 = true
		}
	}
	if !hasNew1 {
		t.Errorf("New1's call to Run should resolve, callers=%+v", callers)
	}

	// Delete a file; incrementally prune it.
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	res, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"a.go"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", res.FilesDeleted)
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "Helper"); len(nodes) != 0 {
		t.Errorf("Helper (from the deleted a.go) should be gone, got %d nodes", len(nodes))
	}
}

// TestIndexProjectOnFileHook pins the progress hook: OnFile fires exactly once
// per scanned file, with total == FilesScanned and a non-empty rel path. The
// done counter is 1-based and unique (no duplicates) but not necessarily
// monotonic — Go files are indexed in parallel, so the order is non-deterministic.
func TestIndexProjectOnFileHook(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)

	type call struct {
		done, total int
		rel         string
	}
	var (
		mu    sync.Mutex
		calls []call
	)
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{
		OnFile: func(done, total int, rel string) {
			mu.Lock()
			calls = append(calls, call{done, total, rel})
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != res.FilesScanned {
		t.Fatalf("OnFile fired %d times, want FilesScanned=%d", len(calls), res.FilesScanned)
	}
	// Every done value must be unique and in [1, FilesScanned].
	seen := make(map[int]bool, len(calls))
	for i, c := range calls {
		if c.done < 1 || c.done > res.FilesScanned {
			t.Errorf("call %d: done=%d out of range [1,%d]", i, c.done, res.FilesScanned)
		}
		if seen[c.done] {
			t.Errorf("call %d: done=%d fired more than once", i, c.done)
		}
		seen[c.done] = true
		if c.total != res.FilesScanned {
			t.Errorf("call %d: total=%d, want FilesScanned=%d", i, c.total, res.FilesScanned)
		}
		if c.rel == "" {
			t.Errorf("call %d: empty rel path", i)
		}
	}
}

// TestIndexProjectOnFileIsObservational confirms the hook is side-effect-free:
// a nil OnFile (studio/MCP/default path) yields the same Result as a non-nil one.
func TestIndexProjectOnFileIsObservational(t *testing.T) {
	run := func(opts Options) *Result {
		g, v := newStores(t)
		dir := setupProject(t)
		pid, _ := g.UpsertProject("app", dir, "go")
		res, err := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index).
			IndexProject(context.Background(), pid, "app", dir, opts)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a := run(Options{})
	b := run(Options{OnFile: func(int, int, string) {}})
	if a.FilesScanned != b.FilesScanned || a.FilesIndexed != b.FilesIndexed ||
		a.FilesSkipped != b.FilesSkipped || a.Nodes != b.Nodes || a.Edges != b.Edges {
		t.Errorf("OnFile altered the Result: nil=%+v cb=%+v", a, b)
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
	if res.FilesIndexed != 0 || res.FilesUnchanged != 2 {
		t.Errorf("re-run: indexed=%d unchanged=%d, want 0/2", res.FilesIndexed, res.FilesUnchanged)
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
	if res.FilesIndexed != 1 || res.FilesUnchanged != 1 {
		t.Errorf("after edit: indexed=%d unchanged=%d, want 1/1", res.FilesIndexed, res.FilesUnchanged)
	}
	if res.Nodes != 6 {
		t.Errorf("after edit nodes = %d, want 6 (added Extra)", res.Nodes)
	}
}

// TestIndexProjectRebuildsInboundEdges pins the full-project incremental path:
// editing a callee file replaces its nodes (and cascades inbound edges), so
// unchanged caller files must be re-extracted before edge resolution. IndexFiles
// already expands its changed set this way; IndexProject must provide the same
// graph-integrity guarantee.
func TestIndexProjectRebuildsInboundEdges(t *testing.T) {
	g, _ := newStores(t)
	dir := setupProject(t)
	writeFile(t, dir, "c.go", "package app\n\nfunc Top() { Other() }\n")
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCallee := func(want bool) {
		t.Helper()
		callees, err := g.Callees(pid, "Other")
		if err != nil {
			t.Fatal(err)
		}
		got := false
		for _, n := range callees {
			if n.Symbol == "Run" {
				got = true
			}
		}
		if got != want {
			t.Fatalf("Other calls Run = %v, want %v; callees=%+v", got, want, callees)
		}
	}
	assertCallee(true)

	writeFile(t, dir, "a.go", "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n\t_ = 1 // edited callee file\n}\n")
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCallee(true)
	callees, err := g.Callees(pid, "Top")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 || callees[0].Symbol != "Other" {
		t.Fatalf("Top should retain its transitive inbound edge to Other, got %+v", callees)
	}
}

// TestIndexParseFailureKeepsLastGoodStateHonest verifies that a failed
// extraction does not stamp the broken content as successfully indexed. The
// previous, last-good graph remains available, and an unchanged broken file is
// retried (and reported) on the next index instead of being hash-skipped as
// fresh.
func TestIndexParseFailureKeepsLastGoodStateHonest(t *testing.T) {
	g, _ := newStores(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package app\n\nfunc LastGood() {}\n")
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	lastGoodHash, err := g.FileHash(pid, "a.go")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.go", "package app\n\nfunc LastGood(\n")
	for run := 1; run <= 2; run++ {
		res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("failed extraction run %d: errors=%v, want one visible parse error", run, res.Errors)
		}
		gotHash, err := g.FileHash(pid, "a.go")
		if err != nil {
			t.Fatal(err)
		}
		if gotHash != lastGoodHash {
			t.Fatalf("failed extraction run %d stamped hash %q, want last-good hash %q", run, gotHash, lastGoodHash)
		}
		if nodes, err := g.FindNodesBySymbol(pid, "LastGood"); err != nil {
			t.Fatal(err)
		} else if len(nodes) != 1 {
			t.Fatalf("failed extraction run %d lost last-good graph node: %+v", run, nodes)
		}
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

// TestDefaultExcludesAreRootAnchoredNotAnySegment is the P1-11 (B66)
// end-to-end regression: real Go subpackages that happen to share a name
// with an ambiguous default exclude ("env", "build") must still be indexed
// under the default config, while a root-level directory of the same name
// is still excluded. Pre-fix, matchExclude trimmed the trailing slash off
// "env/"/"build/" before checking for a slash, silently collapsing them
// back to the any-segment/any-depth form and dropping internal/env and
// pkg/build entirely — with no error, no warning, just missing symbols.
func TestDefaultExcludesAreRootAnchoredNotAnySegment(t *testing.T) {
	dir := t.TempDir()
	// Real source that happens to live under a directory named like an
	// ambiguous default exclude — must be indexed.
	writeFile(t, dir, "internal/env/e.go", "package env\n\nfunc LoadEnv() {}\n")
	writeFile(t, dir, "pkg/build/b.go", "package build\n\nfunc Compile() {}\n")
	// A root-level dir of the same name is a real build artifact / venv —
	// must still be excluded.
	writeFile(t, dir, "env/generated.go", "package env\n\nfunc RootEnvArtifact() {}\n")
	writeFile(t, dir, "build/generated.go", "package build\n\nfunc RootBuildArtifact() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"LoadEnv", "Compile"} {
		if ns, _ := g.FindNodesBySymbol(pid, sym); len(ns) == 0 {
			t.Errorf("P1-11 regression: %s under a real nested subpackage should be indexed, but it was excluded", sym)
		}
	}
	for _, sym := range []string{"RootEnvArtifact", "RootBuildArtifact"} {
		if ns, _ := g.FindNodesBySymbol(pid, sym); len(ns) != 0 {
			t.Errorf("%s is under a root-level default-excluded dir — it should NOT be indexed", sym)
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

// TestIndexFilesRebuildsInboundEdges pins P0-04: when a file changes, the
// incremental IndexFiles path used to drop (via the edges FK CASCADE) every
// inbound call edge from unchanged files into the changed file, and never
// rebuild them — so callers-of-edited-symbols returned a confidently-empty
// answer. The fix expands the changed set with every file that has edges
// targeting nodes in a changed file (SourceFilesTargeting), so the
// re-extraction of those source files refreshes their outbound refs.
func TestIndexFilesRebuildsInboundEdges(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)

	// Full index first — establishes a.go:Helper/Run and b.go:Other + the
	// Other→Run name-based edge.
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	// Sanity: Other has Run as a callee in the name-based graph.
	var hasRunEdge bool
	otherCallees, err := g.Callees(pid, "Other")
	if err != nil {
		t.Fatal(err)
	}
	hasRunEdge = false
	for _, n := range otherCallees {
		if n.Symbol == "Run" {
			hasRunEdge = true
		}
	}
	if !hasRunEdge {
		t.Fatalf("baseline: Other should have Run as a callee, got %+v", otherCallees)
	}

	// Edit a.go (where Helper+Run live) — change Run's body slightly. This
	// hash-marks a.go as changed, which (pre-fix) dropped every inbound
	// edge into a.go's nodes via the FK cascade on the DELETE.
	writeFile(t, dir, "a.go", "package app\n\n// Helper does work.\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n\t_ = 1 // changed body\n}\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"a.go"}, Options{}); err != nil {
		t.Fatal(err)
	}

	// Post-fix: the expansion found b.go (Other targets Run) and re-extracted
	// it, so Other→Run is present again. Pre-fix: it'd be gone.
	otherCallees, err = g.Callees(pid, "Other")
	if err != nil {
		t.Fatal(err)
	}
	hasRunEdge = false
	for _, n := range otherCallees {
		if n.Symbol == "Run" {
			hasRunEdge = true
		}
	}
	// Direct query as a sanity check (Callees filters by source.symbol; we
	// can also enumerate every edge in the project to confirm at least one
	// source points at Run).
	var edgeCount int
	if err := g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE edge_type='calls'").Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if !hasRunEdge {
		// Diagnostic: show all nodes named Run and every calls edge.
		nodes, _ := g.FindNodesBySymbol(pid, "Run")
		var srcSym, srcFile, tgtSym, tgtFile string
		rows, _ := g.DB().Query("SELECT src.symbol, src.file_path, tgt.symbol, tgt.file_path FROM edges e JOIN nodes src ON e.source_id=src.id JOIN nodes tgt ON e.target_id=tgt.id WHERE e.edge_type='calls'")
		var edges []string
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				_ = rows.Scan(&srcSym, &srcFile, &tgtSym, &tgtFile)
				edges = append(edges, fmt.Sprintf("%s/%s -> %s/%s", srcFile, srcSym, tgtFile, tgtSym))
			}
		}
		t.Errorf("P0-04 regression: after editing a.go, Other's callees should still include Run; got %+v (Run nodes=%+v, edges=%v, total edge count=%d)", otherCallees, nodes, edges, edgeCount)
	}
}

// TestIndexerRegisterLSPForProject pins P0-11 at the indexer layer (the
// daemon's pre-fix bug was "watcher only re-indexes Go", which traces
// down to index.New only registering gosrc). The fix is a public
// RegisterLSPForProject that the daemon calls on startup. We assert it
// populates the extractors map for the languages present in the project.
//
// Gated on the relevant language server being on PATH; otherwise skip.
func TestIndexerRegisterLSPForProject(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	g, v := newStores(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	// Pre-fix: the indexer had no typescript extractor registered.
	if _, ok := ix.extractors["typescript"]; ok {
		t.Skip("unexpected: typescript already registered (test fixture stale?)")
	}
	missing, err := ix.RegisterLSPForProject(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// After the call, typescript + vue extractors are wired.
	if _, ok := ix.extractors["typescript"]; !ok {
		t.Errorf("RegisterLSPForProject did not register a typescript extractor: missing=%+v", missing)
	}
	// A .ts-only project with the TS server on PATH has no missing servers.
	if len(missing) != 0 {
		t.Errorf("expected no missing servers, got %+v", missing)
	}
}

// TestIndexerGeneratedFileStripsGhostNodes pins P1-01 (B16): a file
// that gained a "Code generated … DO NOT EDIT." header (e.g. edited
// to regenerate) was leaving its previously-indexed symbols in the
// graph as ghost entries — find/callers/orphans happily returned
// the now-stale data. The fix deletes nodes + vectors before
// recording the new hash in the early-return branch.
func TestIndexerGeneratedFileStripsGhostNodes(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package x\n\nfunc HandWritten() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, _ := g.UpsertProject("x", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "x", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	// Sanity: HandWritten is indexed.
	if n, _ := g.FindNodesBySymbol(pid, "HandWritten"); len(n) != 1 {
		t.Fatalf("baseline: HandWritten should be indexed, got %d nodes", len(n))
	}
	// Now rewrite the file to be generated. The early-return branch
	// records the new hash; pre-fix the old nodes survived.
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("// Code generated by protoc-gen-go. DO NOT EDIT.\npackage x\n\nfunc Generated() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.IndexProject(context.Background(), pid, "x", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := g.FindNodesBySymbol(pid, "HandWritten"); len(n) != 0 {
		t.Errorf("P1-01 regression: after the file became generated, HandWritten (old symbol) still in graph: %+v", n)
	}
	if n, _ := g.FindNodesBySymbol(pid, "Generated"); len(n) != 0 {
		t.Errorf("Generated itself should be skipped (it's generated), but the symbol survived: %+v", n)
	}
}
