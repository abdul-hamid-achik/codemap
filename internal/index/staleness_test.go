package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

func TestStaleness(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	langs := map[string]bool{"go": true}

	// A freshly indexed tree has no drift.
	st, err := ix.Staleness(pid, dir, langs)
	if err != nil {
		t.Fatal(err)
	}
	if st.Any() {
		t.Errorf("fresh index should not be stale, got %+v", st)
	}

	// Edit a.go → 1 changed.
	writeFile(t, dir, "a.go", fileA+"\n// touched\n")
	if st, _ = ix.Staleness(pid, dir, langs); st.Changed != 1 || st.New != 0 || st.Deleted != 0 {
		t.Errorf("after edit: got %+v, want Changed=1 only", st)
	}

	// Add c.go (recognized, already-indexed language) → 1 new (on top of changed).
	writeFile(t, dir, "c.go", "package app\nfunc New1() {}\n")
	if st, _ = ix.Staleness(pid, dir, langs); st.New != 1 {
		t.Errorf("after add: got %+v, want New=1", st)
	}

	// Delete b.go → 1 deleted.
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	if st, _ = ix.Staleness(pid, dir, langs); st.Deleted != 1 {
		t.Errorf("after delete: got %+v, want Deleted=1", st)
	}
}

// TestStalenessTracksParseErrorFile pins finding E: a scanned-but-unparseable
// file is recorded in index_state so staleness doesn't report it as perpetually
// "new" (before, a parse-error file never entered index_state → "1 new" forever,
// and a re-index never cleared it). The error is still surfaced once.
func TestStalenessTracksParseErrorFile(t *testing.T) {
	g, v := newStores(t)
	dir := t.TempDir()
	writeFile(t, dir, "good.go", "package app\nfunc Good() {}\n")
	writeFile(t, dir, "broken.go", "package app\nfunc (") // invalid Go → parse error
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)

	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The parse error is still surfaced (the file genuinely isn't indexed)...
	if len(res.Errors) == 0 {
		t.Fatal("broken.go should be recorded in res.Errors")
	}
	// ...but it is tracked in index_state, so staleness reports NO drift.
	st, err := ix.Staleness(pid, dir, map[string]bool{"go": true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Any() {
		t.Errorf("a tracked parse-error file must not show as drift, got %+v", st)
	}
	// A second incremental index re-indexes nothing (no pointless retry of the
	// unchanged broken file) — i.e. the re-index actually clears the false "new".
	res2, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.FilesIndexed != 0 {
		t.Errorf("second index should re-index nothing, got FilesIndexed=%d", res2.FilesIndexed)
	}
}

// TestStalenessIgnoresUnindexedLanguages guards the "new" restriction: a new file
// of a language not present in the index must not register as drift (otherwise a
// recognized-but-unsupported language would show perpetual false staleness).
func TestStalenessIgnoresUnindexedLanguages(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "x.ts", "export function f() {}\n")
	st, _ := ix.Staleness(pid, dir, map[string]bool{"go": true})
	if st.New != 0 {
		t.Errorf("new file of an unindexed language should not be drift, got %+v", st)
	}
}

// TestStalenessRespectsSlashAnchoredExclude pins P1-07 (B19): pre-fix
// staleness.WalkDir passed the bare base name to ix.excluded, so a
// slash-anchored pattern (e.g. "db/migrations") never matched and
// the contained file was reported as "new" forever. The fix is to
// pass the project-relative path so the same patterns that the
// indexer walked past also skip staleness.
func TestStalenessRespectsSlashAnchoredExclude(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	g, _ := newStores(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fix\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package fix\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "db/migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "db/migrations/0001_init.go"),
		[]byte("package migrations\n\nfunc Up() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject("fix", dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig().Index
	cfg.Exclude = append(cfg.Exclude, "db/migrations")
	ix := New(g, nil, fakeEmbedder{dims: 4}, cfg)
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	// Manually remove the index_state row for db/migrations/0001_init.go
	// so staleness sees it as a "candidate for new". Pre-fix this would
	// be counted as New=1 forever; post-fix the slash-anchored exclude
	// matches at the staleness walk too.
	_, _ = g.DB().Exec("DELETE FROM index_state WHERE project_id=? AND file_path='db/migrations/0001_init.go'", pid)
	st, err := ix.Staleness(pid, dir, map[string]bool{"go": true})
	if err != nil {
		t.Fatal(err)
	}
	if st.New != 0 {
		t.Errorf("P1-07 regression: db/migrations/* must be excluded from staleness.New (slash-anchored exclude should match the project-relative path); got New=%d", st.New)
	}
}
