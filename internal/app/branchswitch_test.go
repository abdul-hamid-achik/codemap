package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestBranchSwitchRestoresSnapshot is the end-to-end proof of branch-aware index
// switching: snapshot main's index, add a symbol on a feature branch, then switch
// back to main and confirm the feature-only symbol is gone and main's is restored
// — WITHOUT reindexing the working tree. Gated on git + fcheap (structure-only, so
// no Ollama).
func TestBranchSwitchRestoresSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not installed")
	}
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	snapshot.FcheapStashDir = t.TempDir()
	t.Cleanup(func() { snapshot.FcheapStashDir = "" })
	ctx := context.Background()

	root := t.TempDir()
	runGit(t, root, "-c", "init.defaultBranch=main", "init", "-q")
	runGit(t, root, "config", "user.email", "t@example.com")
	runGit(t, root, "config", "user.name", "t")
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.25\n")
	write("main.go", "package m\n\nfunc MainOnly() {}\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "main")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	// Index + snapshot main.
	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.BranchSnapshot(ctx, root, "main"); err != nil {
		t.Fatal(err)
	}

	// Feature branch adds a symbol; index + snapshot it.
	runGit(t, root, "checkout", "-q", "-b", "feature")
	write("feature.go", "package m\n\nfunc FeatureOnly() {}\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "feature")
	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.BranchSnapshot(ctx, root, "feature"); err != nil {
		t.Fatal(err)
	}

	g, _ := sess.Graph()
	projs, _ := g.ListProjects()
	if len(projs) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projs))
	}
	pid := projs[0].ID
	// Sanity: on feature, both symbols are present.
	if ns, _ := g.FindNodesBySymbol(pid, "FeatureOnly"); len(ns) != 1 {
		t.Fatalf("FeatureOnly should be indexed on feature, got %d", len(ns))
	}

	// Switch feature → main: restores main's snapshot (MainOnly only).
	if err := svc.BranchSwitch(ctx, root, "feature", "main"); err != nil {
		t.Fatal(err)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "FeatureOnly"); len(ns) != 0 {
		t.Errorf("after switching to main, FeatureOnly should be gone, got %d nodes", len(ns))
	}
	if ns, _ := g.FindNodesBySymbol(pid, "MainOnly"); len(ns) != 1 {
		t.Errorf("after switching to main, MainOnly should be restored, got %d nodes", len(ns))
	}
}
