package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// reviewGit runs a git command in dir, failing the test on error.
func reviewGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// reviewRepo builds a git repo with a tiny call graph (Other→Run, TestRun→Run),
// commits it, and indexes it (structure only). Returns the service and the
// EvalSymlinks-resolved root so git's toplevel and the indexed path agree on macOS.
func reviewRepo(t *testing.T) (*Service, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	// Deliberately do NOT resolve symlinks: on macOS t.TempDir() lives under
	// /var → /private, so git's resolved root differs from this cwd — exactly the
	// symlinked-checkout case that symbolsForChangedFile must handle.
	proj := t.TempDir()
	files := map[string]string{
		"a.go":      "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n}\n",
		"b.go":      "package app\n\nfunc Other() {\n\tRun()\n}\n",
		"a_test.go": "package app\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tRun()\n}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reviewGit(t, proj, "init")
	reviewGit(t, proj, "config", "user.email", "t@t")
	reviewGit(t, proj, "config", "user.name", "t")
	reviewGit(t, proj, "config", "commit.gpgsign", "false")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "init")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func hasSymbol(refs []SymbolRef, name string) bool {
	for _, r := range refs {
		if r.Symbol == name {
			return true
		}
	}
	return false
}

func TestReviewWorking(t *testing.T) {
	svc, proj := reviewRepo(t)

	// Edit inside Run's body (line 6) without shifting line numbers, so the stale
	// index still maps the hunk to Run.
	edited := "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper() // touched\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IsRepo || !rep.Indexed {
		t.Fatalf("expected IsRepo && Indexed, got %+v", rep)
	}
	if !hasSymbol(rep.ChangedSymbols, "Run") {
		t.Errorf("Run should be a changed symbol, got %+v", rep.ChangedSymbols)
	}
	// Run is covered by TestRun → it must surface as a covering test and Run must
	// NOT be flagged untested.
	if len(rep.CoveringTests) == 0 {
		t.Errorf("expected covering tests for a change to Run, got none")
	}
	if hasSymbol(rep.UntestedSymbols, "Run") {
		t.Errorf("Run is covered by TestRun; it must not be reported untested")
	}
	// Blast radius should reach Run's caller Other and/or the test.
	reached := false
	for _, b := range rep.BlastRadius {
		if b.Symbol == "Other" || b.Symbol == "TestRun" {
			reached = true
		}
	}
	if !reached {
		t.Errorf("blast radius should reach Other or TestRun, got %+v", rep.BlastRadius)
	}
}

func TestReviewNoChanges(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ChangedSymbols) != 0 || len(rep.ChangedFiles) != 0 {
		t.Errorf("clean working tree → no changes, got %+v", rep)
	}
}

func TestReviewFlagsHotspot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	proj := t.TempDir()
	// Hub() has 8 direct callers → it should be flagged a hotspot when changed.
	var b strings.Builder
	b.WriteString("package app\n\nfunc Hub() {}\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "func C%d() { Hub() }\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, proj, "init")
	reviewGit(t, proj, "config", "user.email", "t@t")
	reviewGit(t, proj, "config", "user.name", "t")
	reviewGit(t, proj, "config", "commit.gpgsign", "false")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "init")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	// Edit inside Hub's body (line 3) without shifting line numbers.
	edited := strings.Replace(b.String(), "func Hub() {}", "func Hub() { _ = 1 }", 1)
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(rep.Hotspots, "Hub") {
		t.Errorf("Hub (8 callers) should be flagged a changed hotspot, got %+v", rep.Hotspots)
	}
}

func TestReviewRepoButUnindexed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, proj, "init")
	reviewGit(t, proj, "config", "user.email", "t@t")
	reviewGit(t, proj, "config", "user.name", "t")
	reviewGit(t, proj, "config", "commit.gpgsign", "false")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n\nfunc Run() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// A git repo that was NOT indexed → IsRepo true, Indexed false, changed files
	// still listed (no symbols), with an explanatory note.
	rep, err := NewService(sess).Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IsRepo || rep.Indexed {
		t.Fatalf("expected IsRepo:true, Indexed:false, got %+v", rep)
	}
	if len(rep.ChangedFiles) == 0 || rep.Note == "" {
		t.Errorf("unindexed repo review should still list changed files + a note, got %+v", rep)
	}
}

func TestReviewNotARepo(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).Review(t.TempDir(), ReviewOpts{})
	if err != nil {
		t.Fatalf("non-repo must not error: %v", err)
	}
	if rep.IsRepo {
		t.Errorf("a non-git dir should report IsRepo=false, got %+v", rep)
	}
}

// TestReviewSinceRejectsOptionShapedRef pins P0-03 at the service boundary: a
// `since` value starting with "-" must be refused at validation time with a
// graceful note, never reaching git's argv parser. The actual blocking at the
// git/diff.go layer is covered by TestChangedFilesSinceRejectsOptionShapedRef;
// this test guards the higher-level graceful degradation contract — the
// service returns a populated report with a Note, NOT an error, so the agent
// always gets an actionable answer.
func TestReviewSinceRejectsOptionShapedRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, proj, "init")
	reviewGit(t, proj, "config", "user.email", "t@t")
	reviewGit(t, proj, "config", "user.name", "t")
	reviewGit(t, proj, "config", "commit.gpgsign", "false")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "init")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	rep, err := NewService(sess).Review(proj, ReviewOpts{Mode: "since", Since: "--output=/tmp/PWNED_for_P0_03"})
	if err != nil {
		t.Fatalf("bad --since must not error, got %v", err)
	}
	if rep == nil {
		t.Fatal("Review returned nil report")
	}
	if !strings.Contains(rep.Note, "invalid --since ref") {
		t.Errorf("expected graceful Note explaining invalid ref, got %q", rep.Note)
	}
	if _, statErr := os.Stat("/tmp/PWNED_for_P0_03"); statErr == nil {
		t.Fatal("exploit must not write a file from a bad --since")
	}
}

// TestReviewRiskBand pins feature 1: the aggregate risk band folded into
// ReviewReport from the per-symbol signals over every changed symbol. An
// untested hub (8 callers, no test) must produce a high band with the
// untested_changes + hotspot_fanin factors; a covered leaf must be low with
// no factors. Absent when the diff maps to no indexed symbols.
func TestReviewRiskBand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	proj := t.TempDir()
	// Hub() has 8 direct callers and NO test → untested_changes + hotspot_fanin.
	var b strings.Builder
	b.WriteString("package app\n\nfunc Hub() {}\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "func C%d() { Hub() }\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, proj, "init")
	reviewGit(t, proj, "config", "user.email", "t@t")
	reviewGit(t, proj, "config", "user.name", "t")
	reviewGit(t, proj, "config", "commit.gpgsign", "false")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "init")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	// Touch Hub's body without shifting line numbers.
	edited := strings.Replace(b.String(), "func Hub() {}", "func Hub() { _ = 1 }", 1)
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Risk == nil {
		t.Fatal("expected a risk band for a diff with changed symbols, got nil")
	}
	if rep.Risk.Level != "high" {
		t.Errorf("untested 8-caller hub → high risk, got %s (%.3f)", rep.Risk.Level, rep.Risk.Score)
	}
	if !hasReviewRiskFactor(rep.Risk.Factors, "untested_changes") {
		t.Errorf("expected untested_changes factor, got %+v", rep.Risk.Factors)
	}
	if !hasReviewRiskFactor(rep.Risk.Factors, "hotspot_fanin") {
		t.Errorf("expected hotspot_fanin factor, got %+v", rep.Risk.Factors)
	}
}

// TestReviewCallGraph pins feature 2 on review: a Go (name-based) diff reports
// call_graph="name"; a clean tree (no changes) omits it.
func TestReviewCallGraph(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	svc, proj := reviewRepo(t)
	edited := "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper() // touched\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ChangedSymbols) == 0 {
		t.Skip("no changed symbols detected")
	}
	if rep.CallGraph != CallGraphName {
		t.Errorf("Go name-based review call_graph = %q, want %q", rep.CallGraph, CallGraphName)
	}

	// A FRESH clean repo → no changes → call_graph omitted (empty string).
	svc2, proj2 := reviewRepo(t)
	clean, err := svc2.Review(proj2, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if clean.CallGraph != "" {
		t.Errorf("clean tree review call_graph = %q, want empty (omitted)", clean.CallGraph)
	}
	if clean.Risk != nil {
		t.Errorf("clean tree review risk = %+v, want nil (no changed symbols)", clean.Risk)
	}
}

func hasReviewRiskFactor(fs []RiskFactor, name string) bool {
	for _, f := range fs {
		if f.Factor == name {
			return true
		}
	}
	return false
}
