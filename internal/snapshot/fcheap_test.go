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

	// AND-match two tags server-side (fcheap v0.27.0 repeatable --tag): both
	// tags are passed as separate --tag flags and fcheap AND-filters them.
	stashes, err := FcheapList(ctx, []string{"repo:abc", "branch:main"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stashes {
		if s.ID == id {
			found = true
			// FcheapList unescapes tags; the raw values round-trip intact.
			if !containsTag(s.Tags, "codemap-index") || !containsTag(s.Tags, "repo:abc") {
				t.Errorf("stash tags missing expected values, got %v", s.Tags)
			}
			// custom.source carries the --source base-sha (fcheap v0.27.0).
			if s.Custom == nil || s.Custom["source"] != "deadbeef" {
				t.Errorf("custom.source = %v, want deadbeef", s.Custom)
			}
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

// TestFcheapCommaInTagValue is the B57 regression: a tag VALUE containing a comma
// (e.g. a raw branch name `feature,x` carried in `branchname:feature,x`) must not
// be shattered by fcheap v0.27.0's comma-splitting StringSliceVar. FcheapSave
// emits one --tag per tag and percent-escapes commas, and FcheapList unescapes,
// so the raw branch name round-trips intact and the AND filter still matches.
func TestFcheapCommaInTagValue(t *testing.T) {
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not installed")
	}
	FcheapStashDir = t.TempDir()
	t.Cleanup(func() { FcheapStashDir = "" })
	ctx := context.Background()

	snap := t.TempDir()
	if err := os.WriteFile(filepath.Join(snap, "snapshot.json"), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A branch name with a comma — the exact case B57 calls out.
	const branch = "feature,x"
	tags := []string{"codemap-index", "repo:comma", "branchname:" + branch}
	id, err := FcheapSave(ctx, snap, "codemap", "r@"+branch, tags, "base-sha-comma")
	if err != nil {
		t.Fatal(err)
	}

	// List with the repo tag; the saved stash must be found and its
	// `branchname:feature,x` tag must round-trip with the comma intact.
	stashes, err := FcheapList(ctx, []string{"codemap-index", "repo:comma"})
	if err != nil {
		t.Fatal(err)
	}
	match := false
	for _, s := range stashes {
		if s.ID != id {
			continue
		}
		match = true
		if !containsTag(s.Tags, "branchname:feature,x") {
			t.Errorf("branchname tag did not round-trip the comma, got %v", s.Tags)
		}
		// No spurious `x` tag should exist (the pre-fix corruption signature).
		if containsTag(s.Tags, "x") {
			t.Errorf("spurious 'x' tag present (comma-split corruption), got %v", s.Tags)
		}
		if s.Custom == nil || s.Custom["source"] != "base-sha-comma" {
			t.Errorf("custom.source = %v, want base-sha-comma", s.Custom)
		}
	}
	if !match {
		t.Fatalf("saved stash %q not found by FcheapList, got %+v", id, stashes)
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
