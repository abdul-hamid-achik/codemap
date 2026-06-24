package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFcheapRoundTrip exercises the real fcheap binary (skips when it's absent,
// like the gopls/tsserver-gated tests): save a snapshot dir, find it via list,
// restore it verified into a fresh dir.
func TestFcheapRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not installed")
	}
	FcheapStashDir = t.TempDir() // isolate from the user's real stash store
	t.Cleanup(func() { FcheapStashDir = "" })
	ctx := context.Background()

	snap := t.TempDir()
	if err := os.WriteFile(filepath.Join(snap, "snapshot.json"), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap, "nodes.jsonl"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := FcheapSave(ctx, snap, "codemap", "repo@main", []string{"codemap-index", "repo:abc", "branch:main"}, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("FcheapSave returned an empty stash id")
	}

	// AND-match two tags: server-side filters one, client-side the other.
	stashes, err := FcheapList(ctx, []string{"repo:abc", "branch:main"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stashes {
		if s.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("FcheapList(repo:abc, branch:main) did not return the saved stash %q, got %+v", id, stashes)
	}

	dst := t.TempDir()
	verified, err := FcheapRestore(ctx, id, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Errorf("restore was not verified")
	}
	if _, err := os.Stat(filepath.Join(dst, "nodes.jsonl")); err != nil {
		t.Errorf("restored dir missing nodes.jsonl: %v", err)
	}
}
