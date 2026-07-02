package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo makes a throwaway repo with deterministic identity so commits succeed
// in CI (no global git config) and gpg signing never blocks.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init")
	gitCmd(t, dir, "config", "user.email", "t@t")
	gitCmd(t, dir, "config", "user.name", "t")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findFile(files []ChangedFile, path string) *ChangedFile {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

func TestChangedFilesWorking(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	write(t, dir, "a.go", "package a\n\nfunc Run() {\n\tx := 1\n\t_ = x\n}\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "init")

	// Edit line 4 (inside Run) and add an untracked file.
	write(t, dir, "a.go", "package a\n\nfunc Run() {\n\tx := 2\n\t_ = x\n}\n")
	write(t, dir, "b.go", "package a\n\nfunc New() {}\n")

	files, err := ChangedFiles(ctx, dir, "working", "")
	if err != nil {
		t.Fatal(err)
	}
	a := findFile(files, "a.go")
	if a == nil {
		t.Fatalf("a.go not reported as changed; got %+v", files)
	}
	if a.Status != "M" {
		t.Errorf("a.go status = %q, want M", a.Status)
	}
	hit := false
	for _, h := range a.Hunks {
		if h.Overlaps(4, 4) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("a.go hunks %+v should cover the edited line 4", a.Hunks)
	}
	if b := findFile(files, "b.go"); b == nil || b.Status != "?" {
		t.Errorf("untracked b.go should be reported with status '?', got %+v", b)
	}
}

func TestChangedFilesStaged(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	write(t, dir, "a.go", "package a\n\nfunc Run() {}\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "init")

	// Unstaged edit: must NOT appear in staged mode.
	write(t, dir, "a.go", "package a\n\nfunc Run() { _ = 1 }\n")
	if files, err := ChangedFiles(ctx, dir, "staged", ""); err != nil {
		t.Fatal(err)
	} else if len(files) != 0 {
		t.Errorf("nothing staged yet → expected 0 files, got %+v", files)
	}

	gitCmd(t, dir, "add", "a.go")
	files, err := ChangedFiles(ctx, dir, "staged", "")
	if err != nil {
		t.Fatal(err)
	}
	if findFile(files, "a.go") == nil {
		t.Errorf("staged a.go should be reported, got %+v", files)
	}
}

func TestChangedFilesSince(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	write(t, dir, "a.go", "package a\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "c1")
	base, err := HeadSHA(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.go", "package a\n\nfunc Added() {}\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "c2")

	files, err := ChangedFiles(ctx, dir, "since", base)
	if err != nil {
		t.Fatal(err)
	}
	if findFile(files, "a.go") == nil {
		t.Errorf("a.go changed since base %s should be reported, got %+v", base, files)
	}
}

// TestChangedFilesSinceRejectsOptionShapedRef pins P0-03: a `since` value that
// starts with "-" must never be passed through to git's argv parser. Pre-fix
// this let an agent that controls `codemap_review --since` invoke `git
// diff --output=/tmp/PWNED` and write an arbitrary file. The test asserts both
// that ChangedFiles returns an error AND that no arbitrary file was created.
func TestChangedFilesSinceRejectsOptionShapedRef(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.go", "package a\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "c1")

	output := filepath.Join(t.TempDir(), "PWNED.txt")
	bogus := "--output=" + output
	_, err := ChangedFiles(context.Background(), dir, "since", bogus)
	if err == nil {
		t.Fatalf("ChangedFiles must reject leading-dash since ref, got nil error")
	}
	if _, statErr := os.Stat(output); statErr == nil {
		t.Fatalf("exploit succeeded: %s was written", output)
	}
}

// TestValidRef pins the cheap ValidRef guard the diff/branch/resolver layers
// rely on. Empty + leading-dash are the only rejection conditions; everything
// else (commits, branches, tags, refs with slashes, caret/tilde prefixes) is
// accepted and validated server-side by git via ResolveRef.
func TestValidRef(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"-", false},
		{"--", false}, // a bare "--" is a separator/option marker, not a real ref
		{"--output=/x", false},
		{"--end-of-options", false}, // itself looks like an option
		{"HEAD", true},
		{"HEAD~3", true},
		{"origin/main", true},
		{"v1.2.3", true},
		{"abc1234", true},
		{"main", true},
	}
	for _, tc := range cases {
		if got := ValidRef(tc.in); got != tc.want {
			t.Errorf("ValidRef(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseUnifiedDiffHunkBodyLooksLikeHeader(t *testing.T) {
	// With -U0, an added line whose content begins with "++ " is emitted as a body
	// line "+++ <content>" — it must NOT be mistaken for a new-file header (which
	// would corrupt the path and drop the file's changed symbols).
	sample := "diff --git a/f.go b/f.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/f.go\n" +
		"+++ b/f.go\n" +
		"@@ -3,0 +4 @@ func F() {\n" +
		"+++ injected header-looking line\n"
	f := findFile(parseUnifiedDiff(sample), "f.go")
	if f == nil {
		t.Fatal("f.go should still be reported under its real path")
	}
	if len(f.Hunks) != 1 || !f.Hunks[0].Overlaps(4, 4) {
		t.Errorf("expected a hunk at line 4, got %+v", f.Hunks)
	}
}

func TestParseUnifiedDiffQuotedDelete(t *testing.T) {
	// git C-quotes non-ASCII paths and a delete's +++ is /dev/null, so the path must
	// come (unquoted) from the `--- a/<path>` line.
	sample := "diff --git \"a/na\\303\\257ve.txt\" \"b/na\\303\\257ve.txt\"\n" +
		"deleted file mode 100644\n" +
		"index 4444444..0000000\n" +
		"--- \"a/na\\303\\257ve.txt\"\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n"
	f := findFile(parseUnifiedDiff(sample), "naïve.txt")
	if f == nil || f.Status != "D" {
		t.Errorf("quoted-path delete should be reported (status D, unquoted path), got %+v", parseUnifiedDiff(sample))
	}
}

func TestStripDiffPathQuoted(t *testing.T) {
	// Unquote must precede the b/ strip (the prefix is inside the quotes).
	if got := stripDiffPath(`"b/na\303\257ve.txt"`); got != "naïve.txt" {
		t.Errorf("stripDiffPath quoted = %q, want naïve.txt", got)
	}
	if got := stripDiffPath("b/plain.go"); got != "plain.go" {
		t.Errorf("stripDiffPath plain = %q, want plain.go", got)
	}
}

func TestChangedFilesWorkingUnbornBranch(t *testing.T) {
	// A fresh repo with no commit (no HEAD) must still surface staged + untracked
	// files via the no-HEAD "working" fallback.
	ctx := context.Background()
	dir := initRepo(t)
	write(t, dir, "staged.go", "package a\n")
	gitCmd(t, dir, "add", "staged.go")
	write(t, dir, "untracked.go", "package a\n")
	files, err := ChangedFiles(ctx, dir, "working", "")
	if err != nil {
		t.Fatal(err)
	}
	if findFile(files, "staged.go") == nil {
		t.Errorf("staged file should surface on an unborn branch, got %+v", files)
	}
	if findFile(files, "untracked.go") == nil {
		t.Errorf("untracked file should surface on an unborn branch, got %+v", files)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	// Add, modify, and delete in one diff (paths as git emits with -U0).
	sample := `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package x
+
+func A() {}
diff --git a/edit.go b/edit.go
index 2222222..3333333 100644
--- a/edit.go
+++ b/edit.go
@@ -10,2 +10,3 @@ func B() {
+	added()
diff --git a/gone.go b/gone.go
deleted file mode 100644
index 4444444..0000000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
`
	files := parseUnifiedDiff(sample)
	if n := findFile(files, "new.go"); n == nil || n.Status != "A" || len(n.Hunks) != 1 || n.Hunks[0] != (LineRange{1, 3}) {
		t.Errorf("new.go = %+v, want status A, hunk {1,3}", n)
	}
	if e := findFile(files, "edit.go"); e == nil || e.Status != "M" || !e.Hunks[0].Overlaps(11, 12) {
		t.Errorf("edit.go = %+v, want status M with a hunk near lines 10-12", e)
	}
	if g := findFile(files, "gone.go"); g == nil || g.Status != "D" || len(g.Hunks) != 0 {
		t.Errorf("gone.go = %+v, want status D, no hunks", g)
	}
}

// TestGitRunSurfacesStderr pins P3-03 (O61/O107): pre-fix git.run
// returned an opaque "exit status 128" on any git failure, discarding
// git's actual stderr message. Post-fix the error includes git's
// stderr text so the user/agent knows what went wrong (missing ref,
// no repo, etc.).
func TestGitRunSurfacesStderr(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.go", "package a\n")
	gitCmd(t, dir, "add", "a.go")
	gitCmd(t, dir, "commit", "-m", "c1")

	// Ask git for a non-existent ref — git returns exit 128 with a
	// stderr message like "unknown revision or path not in the
	// working tree". Pre-fix we'd get "exit status 128"; post-fix
	// we get the actual stderr text.
	_, err := run(context.Background(), dir, "rev-parse", "--verify", "nonexistent_ref_xyz")
	if err == nil {
		t.Fatal("expected an error for a nonexistent ref")
	}
	// The error should NOT be the bare "exit status 128" — it should
	// contain git's actual stderr (which includes the ref name or
	// a "unknown revision" message).
	if strings.Contains(err.Error(), "exit status 128") && !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("P3-03 regression: error is opaque %q, should contain git's stderr text", err.Error())
	}
}
