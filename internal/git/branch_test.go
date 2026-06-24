package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestInspect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// A fresh, non-git dir → IsRepo false, no error.
	st, err := Inspect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsRepo {
		t.Errorf("fresh dir should not be a repo, got %+v", st)
	}

	// init (deterministic default branch) + a commit.
	gitCmd(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	gitCmd(t, dir, "config", "user.email", "t@example.com")
	gitCmd(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "a.txt")
	gitCmd(t, dir, "commit", "-q", "-m", "init")

	st, err = Inspect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsRepo {
		t.Fatal("should be a repo after init+commit")
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.Detached {
		t.Errorf("should not be detached on a branch")
	}
	if len(st.SHA) < 7 {
		t.Errorf("sha = %q, want a commit sha", st.SHA)
	}
	if st.RepoHash == "" || st.Key == "" {
		t.Errorf("expected repo hash + branch key, got %+v", st)
	}

	// Detached HEAD: checking out the raw sha.
	gitCmd(t, dir, "checkout", "-q", st.SHA)
	st, err = Inspect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detached {
		t.Errorf("should be detached after checkout <sha>, got %+v", st)
	}
	if st.Branch != "" || st.Key != "" {
		t.Errorf("detached HEAD should have empty branch/key, got %+v", st)
	}
}

func TestSanitizeBranch(t *testing.T) {
	a := SanitizeBranch("feature/x")
	b := SanitizeBranch("feature-x")
	if a == b {
		t.Errorf("distinct raw names must not collide: %q == %q", a, b)
	}
	if SanitizeBranch("feature/x") != a {
		t.Errorf("SanitizeBranch must be stable for the same input")
	}
	if strings.ContainsAny(a, `/\: `) {
		t.Errorf("sanitized name still contains an unsafe char: %q", a)
	}
	// An all-unsafe / empty name still yields a usable segment.
	if got := SanitizeBranch("///"); got == "" || strings.HasPrefix(got, "-") {
		t.Errorf("degenerate name produced %q", got)
	}
}

func TestRepoHashStable(t *testing.T) {
	dir := t.TempDir()
	h1 := RepoHash(dir)
	h2 := RepoHash(dir)
	if h1 != h2 || len(h1) != 12 {
		t.Errorf("RepoHash unstable or wrong length: %q vs %q", h1, h2)
	}
	if RepoHash(dir+string(filepath.Separator)) != h1 {
		t.Errorf("a trailing separator should not change the repo hash")
	}
}
