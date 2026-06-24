package branchstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// A missing file loads as an empty, usable state.
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Branches) != 0 {
		t.Errorf("fresh state should have no branches, got %+v", s.Branches)
	}

	s.RepoHash = "abc123"
	s.ActiveBranch = "main"
	s.Record("main", BranchEntry{StashID: "s_main", BaseSHA: "deadbeef", EmbeddingProfile: "fake:fake:4:cosine", NodeCount: 10})
	s.Record("feature/x", BranchEntry{StashID: "s_feat"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	// Reload and verify both branches survived.
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepoHash != "abc123" || got.ActiveBranch != "main" {
		t.Errorf("header not persisted: %+v", got)
	}
	main, ok := got.Lookup("main")
	if !ok || main.StashID != "s_main" || main.BaseSHA != "deadbeef" || main.NodeCount != 10 {
		t.Errorf("main entry = %+v, ok=%v", main, ok)
	}
	if main.LastSwitchedAt == "" {
		t.Errorf("Record should have stamped LastSwitchedAt")
	}
	if _, ok := got.Lookup("feature/x"); !ok {
		t.Errorf("feature/x entry missing after round-trip")
	}

	// Atomic rewrite over an existing file: add a branch and re-save.
	got.Record("release", BranchEntry{StashID: "s_rel"})
	if err := got.Save(path); err != nil {
		t.Fatal(err)
	}
	final, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Branches) != 3 {
		t.Errorf("after rewrite want 3 branches, got %d (%+v)", len(final.Branches), final.Branches)
	}
	// No stray temp files left in the dir.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("left a temp file behind: %s", e.Name())
		}
	}
}

// TestRebuildFromFcheap reconstructs the pointer file from fcheap stashes (gated
// on the fcheap binary, like the snapshot wrapper test).
func TestRebuildFromFcheap(t *testing.T) {
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not installed")
	}
	snapshot.FcheapStashDir = t.TempDir()
	t.Cleanup(func() { snapshot.FcheapStashDir = "" })
	ctx := context.Background()
	repoHash := "rebuild123"

	for _, branch := range []string{"main", "feature/y"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte(`{"schema_version":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		tags := []string{"codemap-index", "repo:" + repoHash, "branch:" + branch}
		if _, err := snapshot.FcheapSave(ctx, dir, "codemap", "r@"+branch, tags, "sha-"+branch); err != nil {
			t.Fatal(err)
		}
	}

	s, err := Rebuild(ctx, repoHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("main"); !ok {
		t.Errorf("rebuild missing 'main', got %+v", s.Branches)
	}
	if e, ok := s.Lookup("feature/y"); !ok || e.StashID == "" {
		t.Errorf("rebuild missing 'feature/y' with a stash id, got %+v", s.Branches)
	}
}
