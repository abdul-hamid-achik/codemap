package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestServiceCoveragePreciseFreshCoverage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/cov1\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package cov1\n\nfunc A() { B() }\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Coverage(proj, CoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalFiles != 1 || rep.CoveredFiles != 1 {
		t.Fatalf("TotalFiles/CoveredFiles = %d/%d, want 1/1: %+v", rep.TotalFiles, rep.CoveredFiles, rep)
	}
	if rep.StaleFiles != 0 {
		t.Errorf("StaleFiles = %d, want 0 (nothing edited since index)", rep.StaleFiles)
	}
	lr, ok := rep.ByLanguage["go"]
	if !ok || lr.Files != 1 || lr.Covered != 1 {
		t.Errorf("ByLanguage[go] = %+v (ok=%v), want files=1 covered=1", lr, ok)
	}
	// No filter and Detail:false → Files must stay nil/empty (rollups-only default).
	if len(rep.Files) != 0 {
		t.Errorf("Files = %+v, want empty by default (no filter, Detail=false)", rep.Files)
	}
}

func TestServiceCoverageEditReindexFlipsOnlyThatFile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/cov2\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aPath := filepath.Join(proj, "a.go")
	bPath := filepath.Join(proj, "b.go")
	if err := os.WriteFile(aPath, []byte("package cov2\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("package cov2\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Coverage(proj, CoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CoveredFiles != 2 {
		t.Fatalf("after precise index CoveredFiles = %d, want 2: %+v", rep.CoveredFiles, rep)
	}

	// Edit b.go, then a PLAIN (non-precise) incremental reindex. b.go's node
	// generation is replaced → ClearCallGraphResolvedTx fires for it; a.go is
	// untouched and keeps its coverage row.
	if err := os.WriteFile(bPath, []byte("package cov2\n\nfunc B() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Coverage(proj, CoverageOptions{Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	byFile := make(map[string]CoverageFile, len(rep.Files))
	for _, f := range rep.Files {
		byFile[f.File] = f
	}
	if a := byFile["a.go"]; !a.Covered {
		t.Errorf("a.go (untouched) = %+v, want still covered", a)
	}
	if b := byFile["b.go"]; b.Covered {
		t.Errorf("b.go (edited + non-precise reindexed) = %+v, want covered=false", b)
	}
}

func TestServiceCoverageStaleWithoutReindex(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/cov3\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(proj, "main.go")
	if err := os.WriteFile(mainPath, []byte("package cov3\n\nfunc A() { B() }\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}

	// Edit the file on disk WITHOUT reindexing — its call_graph_coverage row and
	// index_state hash are both untouched, so this is the "about to be wrong"
	// case: covered stays true, but stale must independently flip true.
	if err := os.WriteFile(mainPath, []byte("package cov3\n\nfunc A() { B() }\nfunc B() { println(\"edited\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Coverage(proj, CoverageOptions{Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 {
		t.Fatalf("Files = %+v, want 1 entry", rep.Files)
	}
	f := rep.Files[0]
	if !f.Covered {
		t.Errorf("main.go covered = false, want true (its coverage row was never cleared)")
	}
	if !f.Stale {
		t.Errorf("main.go stale = false, want true (on-disk content drifted since index)")
	}
	if rep.StaleFiles != 1 {
		t.Errorf("StaleFiles = %d, want 1", rep.StaleFiles)
	}
}

func TestServiceCoverageFiltersDoNotChangeTotals(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "root.go"), []byte("package cov4\n\nfunc Root() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "sub", "a.go"), []byte("package sub\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "sub", "b.go"), []byte("package sub\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	base, err := svc.Coverage(proj, CoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if base.TotalFiles != 3 {
		t.Fatalf("TotalFiles = %d, want 3", base.TotalFiles)
	}

	checkTotalsUnchanged := func(t *testing.T, rep *CoverageReport) {
		t.Helper()
		if rep.TotalFiles != base.TotalFiles || rep.CoveredFiles != base.CoveredFiles || rep.StaleFiles != base.StaleFiles {
			t.Errorf("filtered totals = %d/%d/%d, want unchanged %d/%d/%d",
				rep.TotalFiles, rep.CoveredFiles, rep.StaleFiles,
				base.TotalFiles, base.CoveredFiles, base.StaleFiles)
		}
		if len(rep.ByLanguage) != len(base.ByLanguage) || len(rep.ByDirectory) != len(base.ByDirectory) {
			t.Errorf("filtered rollups changed shape: by_language=%v by_directory=%v, want same shape as unfiltered", rep.ByLanguage, rep.ByDirectory)
		}
	}

	prefixRep, err := svc.Coverage(proj, CoverageOptions{PathPrefix: "sub/"})
	if err != nil {
		t.Fatal(err)
	}
	checkTotalsUnchanged(t, prefixRep)
	if len(prefixRep.Files) != 2 {
		t.Errorf("prefix filter Files = %d, want 2 (sub/a.go, sub/b.go)", len(prefixRep.Files))
	}

	langRep, err := svc.Coverage(proj, CoverageOptions{Language: "typescript"})
	if err != nil {
		t.Fatal(err)
	}
	checkTotalsUnchanged(t, langRep)
	if len(langRep.Files) != 0 {
		t.Errorf("language filter (no typescript files) Files = %d, want 0", len(langRep.Files))
	}

	uncoveredRep, err := svc.Coverage(proj, CoverageOptions{OnlyUncovered: true})
	if err != nil {
		t.Fatal(err)
	}
	checkTotalsUnchanged(t, uncoveredRep)
	if len(uncoveredRep.Files) != 3 {
		t.Errorf("uncovered filter (name-based index, nothing precise) Files = %d, want 3", len(uncoveredRep.Files))
	}
}

func TestServiceCoverageTruncation(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	dirs := []string{"a", "b", "c"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj, d, "f.go"), []byte("package "+d+"\n\nfunc F() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Coverage(proj, CoverageOptions{Detail: true, Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalFiles != 3 {
		t.Fatalf("TotalFiles = %d, want 3", rep.TotalFiles)
	}
	if !rep.ByDirTruncated {
		t.Errorf("ByDirTruncated = false, want true (3 dirs > top=1)")
	}
	if len(rep.ByDirectory) != 1 {
		t.Errorf("ByDirectory len = %d, want 1 (capped at top)", len(rep.ByDirectory))
	}
	if !rep.FilesTruncated {
		t.Errorf("FilesTruncated = false, want true (3 files > top=1)")
	}
	if len(rep.Files) != 1 {
		t.Errorf("Files len = %d, want 1 (capped at top)", len(rep.Files))
	}
	if rep.FilesTotal != 3 {
		t.Errorf("FilesTotal = %d, want 3 (true filtered count, uncapped)", rep.FilesTotal)
	}
}
