package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/cachestate"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

// setupCacheProject creates a Service backed by an isolated data dir and a tiny
// git repo so the cache helpers have a repo hash to key on. Returns the service,
// the repo root, and a cleanup function.
func setupCacheProject(t *testing.T) (*Service, string, func()) {
	t.Helper()
	isolate(t)

	repoRoot := t.TempDir()
	if err := exec.Command("git", "-C", repoRoot, "init").Run(); err != nil {
		t.Skipf("git not available: %v", err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	svc := NewService(sess)

	writeGoFile(t, filepath.Join(repoRoot, "main.go"), "package main\n\nfunc main() {}\n")

	cleanup := func() { _ = sess.Close() }
	return svc, repoRoot, cleanup
}

func writeGoFile(t *testing.T, path, content string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCacheSaveNoFcheap verifies CacheSave is a graceful no-op when fcheap is
// absent — the auto-cache path must never fail the index.
func TestCacheSaveNoFcheap(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	stashID, _, err := svc.CacheSave(context.Background(), root)
	if err != nil {
		t.Fatalf("CacheSave should never error, got: %v", err)
	}
	if !snapshot.FcheapAvailable() && stashID != "" {
		t.Fatalf("CacheSave without fcheap should return empty stash id, got %q", stashID)
	}
}

// TestCacheRestoreNoFcheap verifies CacheRestore is a graceful miss when fcheap
// is absent.
func TestCacheRestoreNoFcheap(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	restored, _, err := svc.CacheRestore(context.Background(), root)
	if err != nil {
		t.Fatalf("CacheRestore should never error, got: %v", err)
	}
	if restored {
		t.Fatal("CacheRestore should return false (no restore)")
	}
}

// TestMaybeCacheAfterIndexSkipped verifies the auto-cache wrapper returns a
// skipped report when fcheap is unavailable.
func TestMaybeCacheAfterIndexSkipped(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	rep := svc.MaybeCacheAfterIndex(context.Background(), root)
	if rep == nil {
		t.Fatal("MaybeCacheAfterIndex returned nil report")
	}
	if rep.Action == "saved" {
		if !snapshot.FcheapAvailable() {
			t.Fatalf("expected action != 'saved' without fcheap, got %q", rep.Action)
		}
	}
}

// TestMaybeRestoreBeforeReindexMiss verifies the auto-restore wrapper returns a
// miss (restored=false) when there's no matching cache entry.
func TestMaybeRestoreBeforeReindexMiss(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	restored, rep := svc.MaybeRestoreBeforeReindex(context.Background(), root)
	if rep == nil {
		t.Fatal("MaybeRestoreBeforeReindex returned nil report")
	}
	if restored {
		t.Fatal("expected restored=false (no matching cache entry)")
	}
	if rep.Action != "miss" && rep.Action != "skipped" {
		t.Fatalf("expected action 'miss' or 'skipped', got %q", rep.Action)
	}
}

// TestCacheFcheapAvailableSafe verifies the method doesn't panic.
func TestCacheFcheapAvailableSafe(t *testing.T) {
	svc, _, cleanup := setupCacheProject(t)
	defer cleanup()
	_ = svc.CacheFcheapAvailable()
}

// TestCacheListEmpty verifies CacheList returns an empty (non-nil) report when
// there are no cached entries.
func TestCacheListEmpty(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	rep, err := svc.CacheList(context.Background(), root, false)
	if err != nil {
		t.Fatalf("CacheList: %v", err)
	}
	if rep == nil {
		t.Fatal("CacheList returned nil report")
	}
	if len(rep.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(rep.Entries))
	}
}

// TestCacheDropNoEntries verifies CacheDrop is a safe no-op when there's nothing
// to drop.
func TestCacheDropNoEntries(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	dropped, err := svc.CacheDrop(context.Background(), root, "", true)
	if err != nil {
		t.Fatalf("CacheDrop on empty cache should not error: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
}

// TestCacheStatePathNonEmpty verifies the cache state pointer file path is
// non-empty for a given repo hash.
func TestCacheStatePathNonEmpty(t *testing.T) {
	isolate(t)
	statePath := cachestate.StatePath("testrepo123")
	if statePath == "" {
		t.Fatal("StatePath returned empty")
	}
}

// TestCacheDropMatchesEitherIdentifier pins P0-02: codemap_cache_drop must
// accept either the stash_id an agent gets from codemap_cache_list OR the
// tree_hash. The prior implementation only matched the tree_hash and silently
// no-op'd on a stash_id. Seeds a cachestate entry directly so the test doesn't
// depend on fcheap.
func TestCacheDropMatchesEitherIdentifier(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	rh := git.RepoHash(root)
	csPath := cachestate.StatePath(rh)
	entry := cachestate.CacheEntry{
		StashID:          "fake_stash_for_P0_02",
		TreeHash:         "fake_tree_for_P0_02",
		EmbeddingProfile: "fake/fake/768",
		NodeCount:        1,
	}
	seed := func() {
		state := &cachestate.State{
			Schema:   "cache-v1",
			RepoRoot: root,
			RepoHash: rh,
			Entries:  map[string]cachestate.CacheEntry{entry.TreeHash: entry},
		}
		if err := state.Save(csPath); err != nil {
			t.Fatalf("seed cachestate.Save: %v", err)
		}
	}
	seed()

	// 1. drop by stash_id (the MCP surface; this was the silent no-op bug).
	dropped, err := svc.CacheDrop(context.Background(), root, entry.StashID, false)
	if err != nil {
		t.Fatalf("drop by stash_id: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("drop by stash_id: want dropped=1, got %d", dropped)
	}

	// 2. drop by tree_hash (the CLI --tree surface; still must work).
	seed()
	dropped, err = svc.CacheDrop(context.Background(), root, entry.TreeHash, false)
	if err != nil {
		t.Fatalf("drop by tree_hash: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("drop by tree_hash: want dropped=1, got %d", dropped)
	}

	// 3. drop by an id that doesn't match anything — silent no match, no error,
	//    pointer file is unchanged (callers can distinguish via dropped==0).
	seed()
	dropped, err = svc.CacheDrop(context.Background(), root, "no_such_id_at_all", false)
	if err != nil {
		t.Fatalf("drop by bogus id: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("drop by bogus: want dropped=0, got %d", dropped)
	}
	state2, err := cachestate.Load(csPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(state2.Entries) != 1 {
		t.Fatalf("bogus drop must not touch state, got %d entries", len(state2.Entries))
	}
}
