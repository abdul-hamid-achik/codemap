package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// fileImpactProj indexes: a.go defines Helper (called internally by A, externally
// by b.go's External, and by TestHelper) and Untested (called externally by b.go's
// External2, with no test). c.go defines Lonely with no callers at all.
func fileImpactProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"a.go":      "package app\n\nfunc Helper() {}\n\nfunc A() { Helper() }\n\nfunc Untested() {}\n",
		"b.go":      "package app\n\nfunc External() { Helper() }\n\nfunc External2() { Untested() }\n",
		"a_test.go": "package app\nimport \"testing\"\nfunc TestHelper(t *testing.T) { Helper() }\n",
		"c.go":      "package app\n\nfunc Lonely() {}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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

func TestFileImpactDependedOn(t *testing.T) {
	svc, proj := fileImpactProj(t)
	rep, err := svc.FileImpact(proj, "a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.Symbols < 2 {
		t.Fatalf("a.go should have indexed symbols, got %+v", rep)
	}
	if !contains(rep.DependentFiles, "b.go") {
		t.Errorf("b.go calls into a.go and should be a dependent file, got %v", rep.DependentFiles)
	}
	if rep.SafeToDelete {
		t.Errorf("a.go is referenced by b.go — must not be safe to delete")
	}
	if len(rep.CoveringTests) == 0 {
		t.Errorf("TestHelper covers a.go's Helper — expected covering tests")
	}
	// Untested is externally called (by External2) with no test → breaking change.
	if !rep.BreakingChange {
		t.Errorf("an externally-called untested symbol should flag a breaking change")
	}
	if !hasSymbol(rep.UntestedSymbols, "Untested") {
		t.Errorf("Untested should be listed as externally-called-but-untested, got %+v", rep.UntestedSymbols)
	}
}

func TestFileImpactSafeToDelete(t *testing.T) {
	svc, proj := fileImpactProj(t)
	rep, err := svc.FileImpact(proj, "c.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatalf("c.go should be found, got %+v", rep)
	}
	if len(rep.DependentFiles) != 0 || !rep.SafeToDelete {
		t.Errorf("c.go's Lonely has no callers — should be safe to delete, got %+v", rep)
	}
}

func TestFileImpactNoSymbols(t *testing.T) {
	svc, proj := fileImpactProj(t)
	// An indexed project, but a path with no indexed symbols → found:false + a note,
	// not a crash or a misleading verdict.
	rep, err := svc.FileImpact(proj, "nope.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || rep.Found {
		t.Errorf("nonexistent file in an indexed project → indexed:true, found:false, got %+v", rep)
	}
	if rep.Note == "" {
		t.Errorf("expected a note explaining no indexed symbols")
	}
}

func TestFileImpactUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).FileImpact(t.TempDir(), "x.go", 3)
	if err != nil {
		t.Fatalf("unindexed project must not error: %v", err)
	}
	if rep.Indexed {
		t.Errorf("unindexed → indexed:false, got %+v", rep)
	}
}

// TestFileImpactVerdictsWithheldOnTruncation pins P1-16 (B10): pre-fix
// SafeToDelete stayed true even when the file was truncated to
// the fileImpactMaxSymbols cap or when any per-symbol Impact lookup
// failed silently. A false green that could delete live code. The
// fix withholds the verdicts + adds a note when the analysis was
// incomplete.
func TestFileImpactVerdictsWithheldOnTruncation(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustExec(t, "git", "-C", proj, "init", "-q")
	mustExec(t, "git", "-C", proj, "config", "user.email", "t@t")
	mustExec(t, "git", "-C", proj, "config", "user.name", "t")
	// One small function in big.go; no external callers.
	mustWrite(t, proj, "big.go", "package a\n\nfunc Big() {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := NewService(sess).FileImpact(proj, "big.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Only 1 symbol in the file; nothing truncated; the file is
	// safe to delete (no external callers). The verdict should
	// still be available (we only withhold on truncation or skips).
	if !rep.SafeToDelete {
		t.Errorf("small file with no external callers should be SafeToDelete=true; got false (Note=%q)", rep.Note)
	}
}
