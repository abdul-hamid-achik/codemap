package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// The scoped incremental passes (imports, declarative reconciliation) rewrite
// only the touched files plus their direct dependents. These tests pin the
// correctness contract of that scoping: edges that were valid before a
// modified-only delta must survive it, edges that the delta invalidates must be
// rebuilt, and a no-op run must not run the passes at all.

func TestScopedImportsRewriteDependents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module scoped-imports\n\ngo 1.22\n")
	writeFile(t, root, "b/b.go", "package b\n\nfunc B() {}\n")
	writeFile(t, root, "a.go", "package a\n\nimport \"scoped-imports/b\"\n\nfunc A() { b.B() }\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("scoped-imports", root, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	run := func() *Result {
		t.Helper()
		r, err := ix.IndexProject(context.Background(), pid, "scoped-imports", root, Options{NoLSP: true})
		if err != nil || len(r.Errors) > 0 {
			t.Fatalf("index=%+v err=%v", r, err)
		}
		return r
	}
	run()
	// Modified-only delta in the imported file: the inbound-closure expansion
	// (pre-existing) force-re-extracts a.go because it calls b.B(), and a.go's
	// import edge — cascade-deleted by the node replacements — must come back
	// through the scoped import pass.
	writeFile(t, root, "b/b.go", "package b\n\n// edited\nfunc B() {}\n")
	r := run()
	if r.FilesIndexed != 2 {
		t.Fatalf("files indexed = %d, want 2 (b.go + inbound-closure a.go)", r.FilesIndexed)
	}
	if n, _ := g.CountEdgesByType(pid, graph.EdgeImports); n != 1 {
		t.Fatalf("import edges after scoped run = %d, want 1 (a.go→b.go)", n)
	}
	// A no-op run neither rewrites imports nor reconciles formats nor ANALYZEs.
	r = run()
	if r.FilesIndexed != 0 || r.FilesUnchanged != 2 {
		t.Fatalf("no-op run indexed=%d unchanged=%d, want 0/2", r.FilesIndexed, r.FilesUnchanged)
	}
	if r.ImportsMs != 0 || r.FormatsMs != 0 || r.AnalyzeMs != 0 {
		t.Fatalf("no-op ran scoped passes: imports=%d formats=%d analyze=%d, want all 0",
			r.ImportsMs, r.FormatsMs, r.AnalyzeMs)
	}
	if n, _ := g.CountEdgesByType(pid, graph.EdgeImports); n != 1 {
		t.Fatalf("import edges after no-op = %d, want 1", n)
	}
}

func TestScopedFormatsPreserveUnrelatedEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "one.md", "# One\n[Two](two.md)\n")
	writeFile(t, root, "two.md", "# Two\nbody\n")
	writeFile(t, root, "three.md", "# Three\nbody\n")
	writeFile(t, root, "cfg.yaml", "key: value\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("scoped-formats", root, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	run := func() *Result {
		t.Helper()
		r, err := ix.IndexProject(context.Background(), pid, "scoped-formats", root, Options{NoLSP: true})
		if err != nil || len(r.Errors) > 0 {
			t.Fatalf("index=%+v err=%v", r, err)
		}
		return r
	}
	run()
	docs := func() int {
		t.Helper()
		n, err := g.CountEdgesByType(pid, graph.EdgeDocuments)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := docs(); got != 1 {
		t.Fatalf("documents edges after full index = %d, want 1 (one.md→two.md)", got)
	}
	// Modified-only delta in an unrelated declarative file: the one.md→two.md
	// edge belongs to neither the touched file nor a healing set, so the scoped
	// pass must leave it exactly as it was.
	writeFile(t, root, "cfg.yaml", "key: changed\n")
	r := run()
	if r.FilesIndexed != 1 {
		t.Fatalf("files indexed = %d, want 1 (cfg.yaml)", r.FilesIndexed)
	}
	if got := docs(); got != 1 {
		t.Fatalf("documents edges after scoped run = %d, want 1 (preserved)", got)
	}
	// Touch the linking file itself: its edges are replaced, not duplicated.
	writeFile(t, root, "one.md", "# One\n[Two](two.md) again\n")
	run()
	if got := docs(); got != 1 {
		t.Fatalf("documents edges after touching linker = %d, want 1", got)
	}
}

func TestUnresolvedSQLRefHealsWhenTableAppears(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "schema.sql", "CREATE TABLE sessions (id integer);\n")
	writeFile(t, root, "queries.sql", "-- name: GetUsers :one\nSELECT * FROM users;\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("scoped-heal", root, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "scoped-heal", root, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	reads := func() int {
		t.Helper()
		n, err := g.CountEdgesByType(pid, graph.EdgeReads)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := reads(); got != 0 {
		t.Fatalf("reads edges against missing table = %d, want 0", got)
	}
	// The missing table appears in an unrelated file: the persisted dangling
	// ref (to_name "users") must re-open queries.sql in the scoped pass.
	writeFile(t, root, "users.sql", "CREATE TABLE users (id integer);\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "scoped-heal", root, []string{"users.sql"}, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	if got := reads(); got != 1 {
		t.Fatalf("reads edges after table appears = %d, want 1 (healed)", got)
	}
}

func TestDeletedTargetKeepsHealingTrail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "target.md", "# Target\nbody\n")
	writeFile(t, root, "linker.md", "# Linker\n[Target](target.md)\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("scoped-prune", root, "markdown")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	run := func() *Result {
		t.Helper()
		r, err := ix.IndexProject(context.Background(), pid, "scoped-prune", root, Options{NoLSP: true})
		if err != nil || len(r.Errors) > 0 {
			t.Fatalf("index=%+v err=%v", r, err)
		}
		return r
	}
	run()
	docs := func() int {
		t.Helper()
		n, err := g.CountEdgesByType(pid, graph.EdgeDocuments)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := docs(); got != 1 {
		t.Fatalf("documents edges = %d, want 1", got)
	}
	if err := os.Remove(filepath.Join(root, "target.md")); err != nil {
		t.Fatal(err)
	}
	if r := run(); r.FilesDeleted != 1 {
		t.Fatalf("files deleted = %d, want 1", r.FilesDeleted)
	}
	if got := docs(); got != 0 {
		t.Fatalf("documents edges after delete = %d, want 0", got)
	}
	// The pruned file's inbound declarative refs were recorded as dangling
	// (path-keyed for file nodes), so restoring the file heals the link even
	// though linker.md itself never changed.
	writeFile(t, root, "target.md", "# Target\nbody restored\n")
	r, err := ix.IndexFiles(context.Background(), pid, "scoped-prune", root, []string{"target.md"}, Options{NoLSP: true})
	if err != nil || len(r.Errors) > 0 {
		t.Fatalf("incremental=%+v %v", r, err)
	}
	if got := docs(); got != 1 {
		t.Fatalf("documents edges after restore = %d, want 1 (healed via prune trail)", got)
	}
}

func TestStatCacheShortcut(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module stat-cache\n\ngo 1.22\n")
	writeFile(t, root, "a.go", "package a\n\nfunc Alpha() {}\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("stat-cache", root, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	run := func() *Result {
		t.Helper()
		r, err := ix.IndexProject(context.Background(), pid, "stat-cache", root, Options{NoLSP: true})
		if err != nil || len(r.Errors) > 0 {
			t.Fatalf("index=%+v err=%v", r, err)
		}
		return r
	}
	run()
	ms, mn, sz, err := g.FileStat(pid, "a.go")
	if err != nil || ms == -1 || mn == -1 || sz == -1 {
		t.Fatalf("stat cache not recorded: (%d,%d,%d) err=%v — every indexed file must carry its stat", ms, mn, sz, err)
	}
	// A pure mtime drift (content identical) stays unchanged and refreshes the
	// cached stat, so the next run short-circuits on stat alone.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(root, "a.go"), future, future); err != nil {
		t.Fatal(err)
	}
	if r := run(); r.FilesIndexed != 0 || r.FilesUnchanged != 1 {
		t.Fatalf("touch-only run indexed=%d unchanged=%d, want 0/1", r.FilesIndexed, r.FilesUnchanged)
	}
	ms2, _, _, err := g.FileStat(pid, "a.go")
	if err != nil || ms2 != future.Unix() {
		t.Fatalf("stat cache not refreshed after touch: %d err=%v, want %d", ms2, err, future.Unix())
	}
	// A real content change is still detected through the stat cache miss.
	writeFile(t, root, "a.go", "package a\n\nfunc Alpha() {}\nfunc Bravo() {}\n")
	if r := run(); r.FilesIndexed != 1 {
		t.Fatalf("content change indexed=%d, want 1 (stat cache must not mask real changes)", r.FilesIndexed)
	}
}

func TestLSPWorkPendingGate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module lsp-gate\n\ngo 1.22\n")
	writeFile(t, root, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, root, "app.ts", "export function run(): void {}\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("lsp-gate", root, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	// Nothing indexed yet: the .ts file is new, so a server is required.
	if !ix.lspWorkPending(root, pid, []string{"typescript", "javascript"}, Options{}) {
		t.Fatal("gate closed on a never-indexed TS file — it would be silently skipped forever")
	}
	// Simulate the file being indexed: hash + stat recorded → gate closes.
	data, err := os.ReadFile(filepath.Join(root, "app.ts"))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "app.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.SetFileHashStat(pid, "app.ts", sha256hex(data),
		fi.ModTime().Unix(), int64(fi.ModTime().Nanosecond()), fi.Size()); err != nil {
		t.Fatal(err)
	}
	if ix.lspWorkPending(root, pid, []string{"typescript", "javascript"}, Options{}) {
		t.Fatal("gate open with no TS drift — a no-op run would pay the server handshake for nothing")
	}
	// A precise pass over indexed files needs the server even without drift.
	if !ix.lspWorkPending(root, pid, []string{"typescript", "javascript"}, Options{Precise: true}) {
		t.Fatal("gate closed under --precise — the callHierarchy pass needs the live server")
	}
	// Content drift re-opens it.
	writeFile(t, root, "app.ts", "export function run2(): void {}\n")
	if !ix.lspWorkPending(root, pid, []string{"typescript", "javascript"}, Options{}) {
		t.Fatal("gate closed despite a changed TS file")
	}
	// A brand-new file of the language re-opens it (app.ts's content was
	// restored, so only other.ts is pending).
	writeFile(t, root, "other.ts", "export const x = 1;\n")
	writeFile(t, root, "app.ts", "export function run(): void {}\n")
	if !ix.lspWorkPending(root, pid, []string{"typescript", "javascript"}, Options{}) {
		t.Fatal("gate closed despite a new TS file")
	}
}

func TestStylesScopedToOwnStylesheets(t *testing.T) {
	root := t.TempDir()
	// Two artifacts that each embed the SAME style block — the shape of
	// generated test reports, whose global name-matching used to produce N²
	// cross-file style edges.
	shared := "<style>\n.btn { color: red }\n.card { color: blue }\n</style>\n"
	writeFile(t, root, "one.html", shared+"<div class=\"btn card\"></div>\n")
	writeFile(t, root, "two.html", shared+"<div class=\"btn card\"></div>\n")
	// A consumer with no local definitions keeps the global fallback (layout
	// imports the stylesheet, children use the classes).
	writeFile(t, root, "three.html", "<div class=\"btn\"></div>\n")
	g, _ := newStores(t)
	pid, err := g.UpsertProject("styles-scope", root, "html")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "styles-scope", root, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]graph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	styleTargets := func(fromFile string) []string {
		var out []string
		for _, n := range nodes {
			if n.Kind != graph.KindFile || n.FilePath != fromFile {
				continue
			}
			for _, e := range edges {
				if e.SourceID == n.ID && e.EdgeType == graph.EdgeStyles {
					out = append(out, byID[e.TargetID].FilePath)
				}
			}
		}
		return out
	}
	// one.html's classes resolve to ITS OWN embedded selectors only — not to
	// two.html's identically-named ones.
	one := styleTargets("one.html")
	if len(one) != 2 {
		t.Fatalf("one.html style targets = %v, want exactly its own 2 selectors (no cross-file fan-out)", one)
	}
	for _, f := range one {
		if f != "one.html" {
			t.Fatalf("one.html style target %q — cross-file fan-out must not happen for locally-defined classes", f)
		}
	}
	// three.html has no local .btn: the global fallback keeps the edge.
	three := styleTargets("three.html")
	if len(three) == 0 {
		t.Fatal("three.html lost its style edge — the no-local-definition fallback is gone")
	}
}
