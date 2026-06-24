package index

import (
	"context"
	"os"
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
