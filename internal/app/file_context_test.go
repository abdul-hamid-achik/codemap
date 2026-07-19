package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileContextBundlesOutlineImpactAndRelated verifies the one-call file
// orientation bundle composes the symbol outline, the file-level impact, and the
// related files — the three queries an agent would otherwise chain.
func TestFileContextBundlesOutlineImpactAndRelated(t *testing.T) {
	svc, proj := fileImpactProj(t)
	rep, err := svc.FileContext(proj, "a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || !rep.Indexed {
		t.Fatalf("a.go should be found/indexed, got %+v", rep)
	}
	// Outline: a.go defines Helper, A, and Untested.
	for _, want := range []string{"Helper", "A", "Untested"} {
		if !hasSymbol(rep.Symbols, want) {
			t.Errorf("outline missing %q, got %+v", want, rep.Symbols)
		}
	}
	// Impact: b.go depends on a.go and TestHelper covers it.
	if rep.Impact == nil {
		t.Fatal("impact bundle is nil")
	}
	if !contains(rep.Impact.DependentFiles, "b.go") {
		t.Errorf("impact.dependent_files should include b.go, got %v", rep.Impact.DependentFiles)
	}
	if len(rep.Impact.CoveringTests) == 0 {
		t.Errorf("impact.covering_tests should include TestHelper")
	}
	// Related files: b.go calls into a.go, so it is a co-change candidate.
	foundRelated := false
	for _, rf := range rep.RelatedFiles {
		if rf.RelativePath == "b.go" {
			foundRelated = true
		}
	}
	if !foundRelated {
		t.Errorf("related_files should include b.go (a caller file), got %+v", rep.RelatedFiles)
	}
}

// TestFileContextUnindexed verifies the bundle degrades gracefully (indexed:false,
// no error) on a project that isn't indexed.
func TestFileContextUnindexed(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package app\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	rep, err := svc.FileContext(proj, "a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed {
		t.Errorf("an unindexed project should report indexed:false, got %+v", rep)
	}
	if rep.Impact == nil {
		t.Fatal("impact bundle should still be present (indexed:false), got nil")
	}
}
