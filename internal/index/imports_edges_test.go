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
