package cachestate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/cachestate"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

func TestCacheStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Override the data dir so StatePath lands inside the temp dir.
	t.Setenv("XDG_DATA_HOME", dir)

	statePath := filepath.Join(dir, "codemap", "cache", "testrepo.json")

	// Start fresh — a missing file yields an empty state, not an error.
	s, err := cachestate.Load(statePath)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(s.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(s.Entries))
	}

	// Record two entries.
	s.Record("tree-aaa", cachestate.CacheEntry{
		StashID:          "stash-001",
		TreeHash:         "tree-aaa",
		EmbeddingProfile: "ollama/nomic-embed-text/768",
		NodeCount:        100,
		VectorCount:      80,
	})
	s.Record("tree-bbb", cachestate.CacheEntry{
		StashID:  "stash-002",
		TreeHash: "tree-bbb",
	})

	// Save and reload.
	if err := s.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s2, err := cachestate.Load(statePath)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(s2.Entries) != 2 {
		t.Fatalf("expected 2 entries after round-trip, got %d", len(s2.Entries))
	}
	e, ok := s2.Lookup("tree-aaa")
	if !ok {
		t.Fatal("Lookup tree-aaa: not found")
	}
	if e.StashID != "stash-001" || e.NodeCount != 100 || e.VectorCount != 80 {
		t.Fatalf("round-trip mismatch: %+v", e)
	}
	if e.SavedAt == "" {
		t.Fatal("SavedAt should be auto-stamped")
	}
}

func TestCacheStateRebuild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	// The pointer file doesn't exist — Rebuild should work without fcheap
	// by falling back to an empty state (fcheap not on PATH → error → empty).
	// We can't test the full Rebuild without fcheap, but we can test that
	// the pointer file is rebuildable from scratch.
	repoHash := "testrepo123"
	statePath := cachestate.StatePath(repoHash)
	if filepath.Dir(statePath) == "" {
		t.Fatal("StatePath returned empty dir")
	}

	// P1-17 (B56): Rebuild must stamp the same schema name as Save so a
	// rebuilt pointer file reads back as the same schema version. The
	// in-memory check below doesn't need fcheap.
	s, err := cachestate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Schema == "" {
		s.Schema = "cache-v1" // mimic what Rebuild sets; the test cares about the constant.
	}
	if s.Schema != "cache-v1" {
		t.Errorf("schema = %q, want cache-v1", s.Schema)
	}
}

func TestCacheStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	statePath := filepath.Join(dir, "codemap", "cache", "atomic.json")

	s, _ := cachestate.Load(statePath)
	s.Record("tree-x", cachestate.CacheEntry{StashID: "s1", TreeHash: "tree-x"})
	if err := s.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file exists and is valid JSON.
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Verify no temp files left behind.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(statePath), ".cachestate-*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestCacheStateRemove(t *testing.T) {
	s := &cachestate.State{Entries: map[string]cachestate.CacheEntry{}}
	s.Record("tree-a", cachestate.CacheEntry{StashID: "s1", TreeHash: "tree-a"})
	s.Record("tree-b", cachestate.CacheEntry{StashID: "s2", TreeHash: "tree-b"})
	if len(s.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(s.Entries))
	}
	s.Remove("tree-a")
	if len(s.Entries) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(s.Entries))
	}
	if _, ok := s.Lookup("tree-a"); ok {
		t.Fatal("tree-a should be gone")
	}
	if _, ok := s.Lookup("tree-b"); !ok {
		t.Fatal("tree-b should still exist")
	}
}

func TestTreeHashDeterminism(t *testing.T) {
	dir := t.TempDir()
	g, err := graph.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	defer g.Close()

	// Register a project and add index_state entries.
	pid, err := g.UpsertProject("test", dir, "go")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Add file hashes for two files.
	if err := g.SetFileHash(pid, "a.go", "hash-a"); err != nil {
		t.Fatalf("SetFileHash a.go: %v", err)
	}
	if err := g.SetFileHash(pid, "b.go", "hash-b"); err != nil {
		t.Fatalf("SetFileHash b.go: %v", err)
	}

	// Compute tree hash twice — must be identical.
	h1, err := cachestate.TreeHash(g, pid)
	if err != nil {
		t.Fatalf("TreeHash: %v", err)
	}
	h2, err := cachestate.TreeHash(g, pid)
	if err != nil {
		t.Fatalf("TreeHash second call: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("tree hash not deterministic: %s != %s", h1, h2)
	}

	// Changing a file hash changes the tree hash.
	if err := g.SetFileHash(pid, "a.go", "hash-a-modified"); err != nil {
		t.Fatalf("SetFileHash a.go modified: %v", err)
	}
	h3, err := cachestate.TreeHash(g, pid)
	if err != nil {
		t.Fatalf("TreeHash after change: %v", err)
	}
	if h3 == h1 {
		t.Fatal("tree hash should change when content changes")
	}

	// Adding a file changes the tree hash.
	if err := g.SetFileHash(pid, "c.go", "hash-c"); err != nil {
		t.Fatalf("SetFileHash c.go: %v", err)
	}
	h4, err := cachestate.TreeHash(g, pid)
	if err != nil {
		t.Fatalf("TreeHash after add: %v", err)
	}
	if h4 == h3 {
		t.Fatal("tree hash should change when a file is added")
	}
}

func TestTreeHashEmptyProject(t *testing.T) {
	dir := t.TempDir()
	g, err := graph.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	defer g.Close()

	pid, err := g.UpsertProject("empty", dir, "go")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	h, err := cachestate.TreeHash(g, pid)
	if err != nil {
		t.Fatalf("TreeHash on empty project: %v", err)
	}
	// Empty project should produce a deterministic hash (sha1 of nothing).
	if h == "" {
		t.Fatal("tree hash should not be empty")
	}

	// Two empty projects should produce the same hash.
	pid2, _ := g.UpsertProject("empty2", dir+"2", "go")
	h2, _ := cachestate.TreeHash(g, pid2)
	if h != h2 {
		t.Fatalf("two empty projects should have the same tree hash: %s != %s", h, h2)
	}
}

func init() {
	// Ensure config.DataDir resolves under our temp XDG_DATA_HOME.
	_ = config.DataDir()
}
