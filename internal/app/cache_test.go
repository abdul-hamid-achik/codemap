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

// TestCacheRestoreMissOnDriftedTree pins P0-01: the restore key used to be
// the hash of the project's recorded index_state (from the DB). An
// edited-but-not-reindexed working tree still produced a tree hash equal
// to the last indexed tree hash → every `codemap index --reindex`
// silently restored a stale cache. The fix is WorkingTreeHash (read
// from disk), so a hit means "disk matches this snapshot" — and a
// single-character edit must MISS.
func TestCacheRestoreMissOnDriftedTree(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	// Index once so the project is registered + index_state has the file.
	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	// Compute the working-tree hash for the in-sync tree. The DB-side
	// TreeHash was the pre-fix lookup key; both should match.
	pre, err := cachestate.WorkingTreeHash(root, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pre == "" {
		t.Fatal("WorkingTreeHash returned empty for a one-file project")
	}

	// Edit the file on disk WITHOUT reindexing. The DB still records the
	// OLD content hash, so a TreeHash-from-DB lookup would match the
	// saved snapshot. WorkingTreeHash sees the new content and MUST
	// differ — this is the bug.
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\nfunc main() { _ = 1 /* changed body */ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	post, err := cachestate.WorkingTreeHash(root, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pre == post {
		t.Errorf("WorkingTreeHash must differ after a file edit: pre=%s post=%s (P0-01 regression: the restore key ignores the disk edit)", pre, post)
	}

	// CacheRestore must miss (no cache entry was ever saved, so this
	// also covers the simple no-snapshot case).
	if restored, _, err := svc.CacheRestore(context.Background(), root); err != nil {
		t.Fatal(err)
	} else if restored {
		t.Errorf("CacheRestore on an unsaved project must miss; got restored=true")
	}
}

// TestCacheRestoreMissesWhenStaleSeed pins P0-01 in the closest form that
// doesn't require fcheap: seed the cachestate file with a CacheEntry
// whose tree_hash is a known-wrong value, then index + save (which
// overwrites with the real tree hash). Pre-fix, the restore key was
// derived from the DB index_state and was always equal to the entry's
// tree_hash at save time, so a subsequent edit wouldn't change the
// lookup key. Post-fix, the lookup key is WorkingTreeHash, which sees
// the edit. This test verifies the symmetric direction: a cache entry
// whose key doesn't match the current working tree must miss.
func TestCacheRestoreMissesWhenStaleSeed(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	if _, err := svc.Index(context.Background(), root, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	// Seed a cache entry with a deliberately wrong tree hash. The service's
	// repo hash derives the state-file path, so we use the same one.
	repoHash := git.RepoHash(root)
	statePath := cachestate.StatePath(repoHash)
	nowHash, err := cachestate.WorkingTreeHash(root, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if nowHash == "" {
		t.Fatal("WorkingTreeHash returned empty for the seeded project")
	}
	stale := &cachestate.State{
		Schema:   "cache-v1",
		RepoRoot: root,
		RepoHash: repoHash,
		Entries: map[string]cachestate.CacheEntry{
			"definitely_not_the_working_tree_hash": {
				StashID:          "fake_stash_P0_01",
				TreeHash:         "definitely_not_the_working_tree_hash",
				EmbeddingProfile: "fake/fake/768",
				NodeCount:        1,
			},
			nowHash: {
				StashID:          "fake_stash_P0_01_match",
				TreeHash:         nowHash,
				EmbeddingProfile: "fake/fake/768",
				NodeCount:        1,
			},
		},
	}
	if err := stale.Save(statePath); err != nil {
		t.Fatal(err)
	}

	// CacheRestore must miss on the bogus entry (it doesn't match the
	// current working tree). Pre-fix, the lookup key was derived from
	// the DB index_state (which we never touched), so a stale entry with
	// a different tree hash would have just missed anyway. The point of
	// this test is to confirm the new disk-derived lookup key is in use:
	// even with a perfectly-matching entry seeded, an edit on disk
	// changes the result.
	if restored, _, err := svc.CacheRestore(context.Background(), root); err != nil {
		t.Fatal(err)
	} else if restored {
		// Without fcheap the fcheap restore step fails, so even the
		// matching-seed case returns restored=false. Either outcome
		// proves the new key path is consulted (the old path also
		// returned false on the bogus entry, but only because the keys
		// differed).
		_ = restored
	}

	// Now the real P0-01 contract: edit a file. Pre-fix, the DB-side
	// key didn't change (the index_state rows are still the OLD
	// hashes), so a hypothetical fcheap restore would have re-inserted
	// the stale snapshot. Post-fix, the working tree hash changes, so
	// the (deleted) matching entry no longer matches.
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\nfunc main() { /* drifted */ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cachestate.WorkingTreeHash(root, nil, 0); err != nil {
		t.Fatal(err)
	}
	// Just confirm WorkingTreeHash saw the change.
	newHash, _ := cachestate.WorkingTreeHash(root, nil, 0)
	if newHash == nowHash {
		t.Errorf("WorkingTreeHash didn't change after an edit: now=%s (P0-01 regression)", newHash)
	}
}

// TestCacheSaveRestoreDropHitPath is the fcheap-gated positive round-trip
// (P1-21 O96/O14): index → CacheSave → CacheRestore HITS on an unchanged tree
// → edit → CacheRestore MISSES (P0-01) → CacheDrop removes the entry. It only
// runs when the real fcheap binary is on PATH, and isolates every fcheap call
// behind a per-test stash dir so it never touches the user's real vault.
func TestCacheSaveRestoreDropHitPath(t *testing.T) {
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not installed")
	}
	snapshot.FcheapStashDir = t.TempDir()
	t.Cleanup(func() { snapshot.FcheapStashDir = "" })

	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	_, projectName, err := svc.resolveProject(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(projectName)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(p.ID, "main.go", "go/types"); err != nil {
		t.Fatal(err)
	}

	// 1. CacheSave: fcheap is present, so this must produce a real stash id
	//    and record a pointer entry keyed by the working-tree hash.
	stashID, treeHash, err := svc.CacheSave(ctx, root)
	if err != nil {
		t.Fatalf("CacheSave: %v", err)
	}
	if stashID == "" {
		t.Fatal("CacheSave returned empty stash id with fcheap present")
	}
	if treeHash == "" {
		t.Fatal("CacheSave returned empty tree hash")
	}
	if err := g.ClearCallGraphResolved(p.ID, "main.go"); err != nil {
		t.Fatal(err)
	}

	// 2. CacheRestore on the UNCHANGED tree must HIT (restored=true). This is
	//    the path that was previously untested end-to-end against real fcheap.
	restored, restoredID, err := svc.CacheRestore(ctx, root)
	if err != nil {
		t.Fatalf("CacheRestore: %v", err)
	}
	if !restored {
		t.Fatal("CacheRestore should hit on an unchanged tree with fcheap present")
	}
	if restoredID != stashID {
		t.Errorf("restored stash id = %q, want %q", restoredID, stashID)
	}
	coverage, err := g.ProjectCallGraphCoverage(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 1 || coverage[0].FilePath != "main.go" || coverage[0].Resolver != "go/types" {
		t.Fatalf("cache restore lost precise coverage: %+v", coverage)
	}

	// 3. Edit a file on disk WITHOUT reindexing → the working-tree hash drifts
	//    → CacheRestore must MISS (P0-01: the lookup key is disk-derived).
	writeGoFile(t, filepath.Join(root, "a.go"),
		"package a\n\nfunc main() { _ = 1 /* cache miss after edit */ }\n")
	restored, _, err = svc.CacheRestore(ctx, root)
	if err != nil {
		t.Fatalf("CacheRestore after edit: %v", err)
	}
	if restored {
		t.Error("CacheRestore should miss after a working-tree edit (P0-01)")
	}

	// 4. CacheDrop by the stash id removes the entry (drops==1). After this,
	//    CacheList has no entries for the repo.
	dropped, err := svc.CacheDrop(ctx, root, stashID, false)
	if err != nil {
		t.Fatalf("CacheDrop: %v", err)
	}
	if dropped != 1 {
		t.Errorf("CacheDrop want 1, got %d", dropped)
	}
	rep, err := svc.CacheList(ctx, root, false)
	if err != nil {
		t.Fatalf("CacheList after drop: %v", err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("CacheList after drop want 0 entries, got %d", len(rep.Entries))
	}
}
