package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// A deleted test file cannot be run: it must never be listed in tests, whether
// git reports the deletion, the path is passed explicitly, or the stale index
// still maps a covering test to it. It is mentioned in deleted_tests instead.
func TestAffectedDeletedTestFileIsNotListed(t *testing.T) {
	svc, proj := reviewRepo(t)
	reviewGit(t, proj, "rm", "-q", "a_test.go")

	rep, err := svc.Affected(proj, AffectedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if affectedTest(t, rep, "a_test.go") != nil {
		t.Fatalf("git-deleted a_test.go must not be listed as a test to run: %+v", rep.Tests)
	}
	if !contains(rep.DeletedTests, "a_test.go") {
		t.Fatalf("deleted_tests = %v, want a_test.go", rep.DeletedTests)
	}

	// Explicit paths (stat'ed too), plus a covering test that no longer exists.
	rep, err = svc.Affected(proj, AffectedOpts{Files: []string{"a_test.go", "a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if affectedTest(t, rep, "a_test.go") != nil {
		t.Fatalf("explicit deleted test must not be listed (changed/covers): %+v", rep.Tests)
	}
	if !contains(rep.DeletedTests, "a_test.go") {
		t.Fatalf("deleted_tests = %v, want a_test.go", rep.DeletedTests)
	}
}

// git prints repo-root-relative paths; a project that is a subdirectory of the
// repository must still resolve them (the documented `git diff --name-only |
// codemap affected --stdin` pipeline).
func TestAffectedStdinGitRootRelativeSubdirProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	repo := t.TempDir()
	svcDir := filepath.Join(repo, "svc")
	mustWrite(t, svcDir, "a.go", "package svc\n\nfunc Helper() {}\n\nfunc Run() { Helper() }\n")
	mustWrite(t, svcDir, "a_test.go", "package svc\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { Run() }\n")
	mustWrite(t, svcDir, "gone_test.go", "package svc\n\nimport \"testing\"\n\nfunc TestGone(t *testing.T) {}\n")
	mustWrite(t, repo, "top.txt", "x\n")
	reviewGit(t, repo, "init")
	reviewGit(t, repo, "config", "user.email", "t@t")
	reviewGit(t, repo, "config", "user.name", "t")
	reviewGit(t, repo, "config", "commit.gpgsign", "false")
	reviewGit(t, repo, "add", "-A")
	reviewGit(t, repo, "commit", "-m", "init")
	if err := os.Remove(filepath.Join(svcDir, "gone_test.go")); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), svcDir, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	// cwd == project root, so only the git toplevel can resolve "svc/a.go".
	rep, err := svc.Affected(svcDir, AffectedOpts{Source: AffectedSourceFiles, Files: []string{"svc/a.go", "svc/gone_test.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.Files, "a.go") {
		t.Fatalf("repo-root-relative svc/a.go must resolve to a.go inside the project: files=%v unmapped=%v", rep.Files, rep.Unmapped)
	}
	if affectedTest(t, rep, "a_test.go") == nil {
		t.Fatalf("a_test.go should be selected for svc/a.go: %+v", rep.Tests)
	}
	if !contains(rep.DeletedTests, "gone_test.go") || len(rep.Unmapped) != 0 {
		t.Fatalf("deleted repo-root-relative test should resolve into the project: deleted=%v unmapped=%v", rep.DeletedTests, rep.Unmapped)
	}

	// A path that exists in the repo but outside the project stays unmapped.
	rep, err = svc.Affected(svcDir, AffectedOpts{Source: AffectedSourceFiles, Files: []string{"top.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tests) != 0 || !contains(rep.Unmapped, "top.txt") {
		t.Fatalf("outside-project path must be unmapped: %+v", rep)
	}
}

func TestAffectedErrorCodes(t *testing.T) {
	svc, proj := reviewRepo(t)

	for _, tc := range []struct {
		name      string
		opts      AffectedOpts
		validates bool // also rejected by ValidateAffectedOpts without git
	}{
		{"option-like since", AffectedOpts{Since: "--output=x"}, true},
		{"unknown since ref", AffectedOpts{Since: "no-such-ref-xyz"}, false},
		{"since and staged", AffectedOpts{Since: "HEAD", Staged: true}, true},
		{"depth too large", AffectedOpts{Files: []string{"a.go"}, Depth: affectedMaxDepth + 1}, true},
		{"negative depth", AffectedOpts{Files: []string{"a.go"}, Depth: -1}, true},
		{"unsupported source", AffectedOpts{Source: "bogus"}, true},
	} {
		_, err := svc.Affected(proj, tc.opts)
		if err == nil {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if got := CodeOf(err); got != CodeInvalidInput {
			t.Errorf("%s: code = %q, want %q (err=%v)", tc.name, got, CodeInvalidInput, err)
		}
		verr := ValidateAffectedOpts(tc.opts)
		if tc.validates && (verr == nil || CodeOf(verr) != CodeInvalidInput) {
			t.Errorf("%s: ValidateAffectedOpts = %v, want invalid_input", tc.name, verr)
		}
	}
	if err := ValidateAffectedOpts(AffectedOpts{Files: []string{"a.go"}, Depth: affectedMaxDepth}); err != nil {
		t.Errorf("max depth is valid: %v", err)
	}

	// Outside a git repository a git-sourced run is not_a_repo.
	isolate(t)
	plain := t.TempDir()
	mustWrite(t, plain, "a.go", "package p\n\nfunc A() {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	psvc := NewService(sess)
	if _, err := psvc.Index(context.Background(), plain, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []AffectedOpts{{}, {Staged: true}, {Since: "HEAD"}} {
		_, err := psvc.Affected(plain, opts)
		if err == nil || CodeOf(err) != CodeNotARepo {
			t.Errorf("%+v outside git: code = %q err=%v, want %q", opts, CodeOf(err), err, CodeNotARepo)
		}
	}
	// Explicit files never need git.
	if _, err := psvc.Affected(plain, AffectedOpts{Files: []string{"a.go"}}); err != nil {
		t.Errorf("explicit files outside git must work: %v", err)
	}
}

// analysis_complete keeps meaning "no staleness/cap/failure", so an empty test
// list over a changed file with no call graph must carry a note instead of
// reading as "nothing to run".
func TestAffectedNoteWhenCallGraphNone(t *testing.T) {
	svc, proj := reviewRepo(t)
	mustWrite(t, proj, "doc.md", "# Title\n\ntext\n")
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"doc.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tests) != 0 {
		t.Fatalf("doc.md should select no tests: %+v", rep.Tests)
	}
	if rep.CallGraph != CallGraphNone && rep.CallGraph != CallGraphUnresolved {
		t.Fatalf("call_graph = %q, want none or unresolved", rep.CallGraph)
	}
	if !strings.Contains(rep.Note, "call graph") {
		t.Fatalf("an empty list over a no-call-graph file needs a call graph note, got %q (report %+v)", rep.Note, rep)
	}
}

// TS without --precise has an unresolved call graph: an empty list must carry
// the note even though analysis_complete stays true.
func TestAffectedNoteWhenCallGraphUnresolved(t *testing.T) {
	svc, proj := reviewRepo(t)
	mustWrite(t, proj, "util.ts", "export function helper(): number {\n  return 1\n}\n")
	if _, err := svc.Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"util.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CallGraph != CallGraphUnresolved {
		t.Skipf("TS call graph = %q in this environment, nothing to assert", rep.CallGraph)
	}
	if len(rep.Tests) != 0 || !rep.AnalysisComplete {
		t.Fatalf("precondition: empty list with analysis_complete=true: %+v", rep)
	}
	if !strings.Contains(rep.Note, "unresolved") {
		t.Fatalf("unresolved call graph needs a note, got %q", rep.Note)
	}
}
