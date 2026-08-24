package index

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/vuesrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// TestImportsEdgesWired pins P2-04 (O30): the schema declared
// `imports` for E2.3, but no extractor or indexer ever wrote one.
// The fix: in indexFile, resolve fr.Imports against a project-wide
// importIndex (built once per IndexProject) and write a file→file
// EdgeImports in the same transaction as the file's symbol nodes.
//
// Per-test store (not the package-shared newStores helper) so the
// test is order-independent under -count=N and doesn't see ghost
// edges from earlier tests.
func TestImportsEdgesWired(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module imports-test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a.go in subpackage a; b.go at root in package b, importing a.
	// This is the canonical subpackage layout — package per directory.
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "alpha.go"), []byte("package a\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n\nimport \"imports-test/a\"\n\nfunc Run() { a.Alpha() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	v, err := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	n, err := g.CountEdgesByType(pid, graph.EdgeImports)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		// Pin a precise failure: b.go's import "imports-test/a" must
		// produce a file→file edge, since the schema declares the
		// type but no writer ever materialized one. Pre-fix this
		// silently returned 0.
		t.Fatalf("P2-04 (O30): import edge count = %d, want exactly 1 for b.go→a/alpha.go", n)
	}

	// A no-op IndexProject still runs the deferred import pass. It must
	// replace the source file's import edges, not append duplicates.
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	n, err = g.CountEdgesByType(pid, graph.EdgeImports)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("no-op incremental index duplicated imports: got %d edges, want 1", n)
	}
}

// TestIndexFilesMaintainsImportEdges verifies that the watcher path resolves
// imports against the whole project and replaces stale edges when an importing
// file changes. Repeating the same IndexFiles call must remain idempotent.
func TestIndexFilesMaintainsImportEdges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module imports-test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"a", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "alpha.go"), []byte("package a\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c", "gamma.go"), []byte("package c\n\nfunc Gamma() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bPath := filepath.Join(dir, "b.go")
	if err := os.WriteFile(bPath, []byte("package b\n\nimport \"imports-test/a\"\n\nfunc Run() { a.Alpha() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bPath, []byte("package b\n\nimport \"imports-test/c\"\n\nfunc Run() { c.Gamma() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"b.go"}, Options{}); err != nil {
			t.Fatal(err)
		}
		var count int
		var target string
		err := g.DB().QueryRow(`
			SELECT COUNT(*), COALESCE(MIN(dst.file_path), '')
			FROM edges e
			JOIN nodes src ON src.id=e.source_id
			JOIN nodes dst ON dst.id=e.target_id
			WHERE src.project_id=? AND src.file_path='b.go' AND e.edge_type=?`,
			pid, graph.EdgeImports).Scan(&count, &target)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 || filepath.ToSlash(target) != "c/gamma.go" {
			t.Fatalf("IndexFiles run %d imports = %d target %q, want one edge to c/gamma.go", run, count, target)
		}
	}
}

// TestRelativeImportsResolve pins the TS/JS path: a relative import
// to a sibling .ts file is resolved to a project file path. We
// drive resolveImportFile directly here (no LSP session needed) so
// the test is order-independent and doesn't depend on a language
// server being on PATH.
func TestRelativeImportsResolve(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.ts"), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.ts"), []byte("import { x } from './a'\nexport { x }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build the index by hand from a synthetic fileTask list.
	idx := newImportIndex(root, []fileTask{
		{rel: "a.ts", lang: "typescript"},
		{rel: "index.ts", lang: "typescript"},
	})
	if got := resolveImportFile("typescript", "index.ts", "./a", idx); got != "a.ts" {
		t.Errorf("resolveImportFile(typescript, index.ts, ./a) = %q, want a.ts", got)
	}
	// Negative: a bare specifier ("foo") is a package import, not a
	// project file — it must resolve to "" (no project file maps to
	// a bare specifier).
	if got := resolveImportFile("typescript", "index.ts", "react", idx); got != "" {
		t.Errorf("resolveImportFile(typescript, index.ts, react) = %q, want \"\" (bare specifier is a package import)", got)
	}
}

// TestImportsEdgesExternal pins the negative case: an external
// import (e.g. github.com/some/dep) does NOT produce an
// EdgeImports edge. A dangling edge to "" is worse than no edge
// — it shows up in the import-coverage view as a silently-broken
// target rather than as "this file imports an external package."
func TestImportsEdgesExternal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module imports-test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport _ \"github.com/some/external/pkg\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	v, err := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	n, err := g.CountEdgesByType(pid, graph.EdgeImports)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("external import must NOT produce an EdgeImports edge (dangling), got %d", n)
	}
}

type stubLangExtractor struct{ lang string }

func (s stubLangExtractor) Language() string { return s.lang }

func (s stubLangExtractor) ExtractFile(relPath string, _ []byte) (*extract.FileResult, error) {
	return &extract.FileResult{
		Path:     relPath,
		Language: s.lang,
		Symbols: []extract.Symbol{{
			Name: "x", FQN: "x", Kind: extract.KindFunction,
			Language: s.lang, StartLine: 1, EndLine: 1,
		}},
	}, nil
}

func importTargets(t *testing.T, g *graph.Store, pid int64, from string) []string {
	t.Helper()
	rows, err := g.DB().Query(`
		SELECT dst.file_path
		FROM edges e
		JOIN nodes src ON src.id=e.source_id
		JOIN nodes dst ON dst.id=e.target_id
		WHERE src.project_id=? AND src.file_path=? AND src.kind=? AND e.edge_type=?
		ORDER BY dst.file_path`,
		pid, from, graph.KindFile, graph.EdgeImports)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(p))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestImportPassDoesNotReextractLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.ts", "export function b() { return 1 }\n")
	writeFile(t, dir, "a.ts", "import { b } from './b'\nexport function a() { return b() }\n")

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	var calls int64
	ix.Register(&countingExtractor{Extractor: stubLangExtractor{lang: "typescript"}, calls: &calls})

	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("ExtractFile calls after first index = %d, want 2 (one per file, not import-pass doubles)", got)
	}
	if got := importTargets(t, g, pid, "a.ts"); len(got) != 1 || got[0] != "b.ts" {
		t.Fatalf("a.ts imports = %v, want [b.ts]", got)
	}

	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("ExtractFile calls after no-op index = %d, want 2 (import pass must not re-extract)", got)
	}
	if got := importTargets(t, g, pid, "a.ts"); len(got) != 1 || got[0] != "b.ts" {
		t.Fatalf("after no-op, a.ts imports = %v, want [b.ts]", got)
	}
}

func TestUnchangedImporterGainsEdgeWhenTargetAppears(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.ts", "import { b } from './b'\nexport function a() { return 1 }\n")

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(stubLangExtractor{lang: "typescript"})

	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := importTargets(t, g, pid, "a.ts"); len(got) != 0 {
		t.Fatalf("a.ts imports = %v, want none before b.ts exists", got)
	}

	writeFile(t, dir, "b.ts", "export function b() { return 1 }\n")
	if _, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := importTargets(t, g, pid, "a.ts"); len(got) != 1 || got[0] != "b.ts" {
		t.Fatalf("a.ts imports after adding b.ts = %v, want [b.ts] (unchanged importer must re-resolve)", got)
	}
}

func TestPythonImportEdgesWithoutLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pkg/__init__.py", "")
	writeFile(t, dir, "pkg/helper.py", "def h():\n    return 1\n")
	writeFile(t, dir, "pkg/a.py", "from . import helper\nfrom .helper import h\ndef a():\n    return helper.h()\n")

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("py", dir, "python")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	var calls int64
	ix.Register(&countingExtractor{Extractor: stubLangExtractor{lang: "python"}, calls: &calls})

	if _, err := ix.IndexProject(context.Background(), pid, "py", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("ExtractFile calls = %d, want 3 (no import-pass re-extract)", got)
	}
	got := importTargets(t, g, pid, "pkg/a.py")
	if len(got) != 2 || got[0] != "pkg/__init__.py" || got[1] != "pkg/helper.py" {
		t.Fatalf("pkg/a.py imports = %v, want [pkg/__init__.py pkg/helper.py]", got)
	}
}

func TestVueImportEdgesWithoutLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "useCounter.ts", "export function useCounter() { return 1 }\n")
	writeFile(t, dir, "Counter.vue", `<script setup lang="ts">
import { useCounter } from './useCounter'
export function go() { return useCounter() }
</script>
`)

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("vue", dir, "vue")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	var tsCalls, vueCalls int64
	ts := stubLangExtractor{lang: "typescript"}
	ix.Register(&countingExtractor{Extractor: ts, calls: &tsCalls})
	ix.Register(&countingExtractor{Extractor: vuesrc.New(ts, nil), calls: &vueCalls})

	if _, err := ix.IndexProject(context.Background(), pid, "vue", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	firstTS, firstVue := atomic.LoadInt64(&tsCalls), atomic.LoadInt64(&vueCalls)
	if firstTS < 1 || firstVue != 1 {
		t.Fatalf("first index ExtractFile ts=%d vue=%d, want ts>=1 vue=1", firstTS, firstVue)
	}
	if got := importTargets(t, g, pid, "Counter.vue"); len(got) != 1 || got[0] != "useCounter.ts" {
		t.Fatalf("Counter.vue imports = %v, want [useCounter.ts]", got)
	}

	if _, err := ix.IndexProject(context.Background(), pid, "vue", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&tsCalls) != firstTS || atomic.LoadInt64(&vueCalls) != firstVue {
		t.Fatalf("no-op index re-extracted (ts %d→%d vue %d→%d)", firstTS, atomic.LoadInt64(&tsCalls), firstVue, atomic.LoadInt64(&vueCalls))
	}
}

func TestGDScriptImportEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "scripts/helper.gd", "func help():\n\treturn 1\n")
	writeFile(t, dir, "main.gd", "extends Node\nvar S = load(\"res://scripts/helper.gd\")\n")

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("gd", dir, "gdscript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "gd", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := importTargets(t, g, pid, "main.gd"); len(got) != 1 || got[0] != "scripts/helper.gd" {
		t.Fatalf("main.gd imports = %v, want [scripts/helper.gd]", got)
	}
}
