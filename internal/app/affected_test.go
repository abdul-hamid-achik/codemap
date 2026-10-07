package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func affectedTest(t *testing.T, rep *AffectedReport, file string) *AffectedTest {
	t.Helper()
	for i := range rep.Tests {
		if rep.Tests[i].File == file {
			return &rep.Tests[i]
		}
	}
	return nil
}

func hasReasonPrefix(reasons []string, prefix string) bool {
	for _, r := range reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestAffectedCoversChangedFile(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || !rep.Indexed || rep.Source != AffectedSourceFiles {
		t.Fatalf("unexpected header: %+v", rep)
	}
	if got := rep.Files; len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("files = %v", got)
	}
	tst := affectedTest(t, rep, "a_test.go")
	if tst == nil {
		t.Fatalf("a_test.go should cover a.go (TestRun → Run), got %+v", rep.Tests)
	}
	if !hasReasonPrefix(tst.Reasons, "covers:") {
		t.Errorf("a_test.go reasons = %v, want a covers: reason", tst.Reasons)
	}
	if len(rep.Tests) != 1 {
		t.Errorf("only a_test.go should be selected, got %+v", rep.Tests)
	}
	if rep.CallGraph != CallGraphName {
		t.Errorf("call_graph = %q, want name (Go without --precise)", rep.CallGraph)
	}
	if !strings.Contains(rep.Note, "name-based") {
		t.Errorf("note should flag name-based coverage: %q", rep.Note)
	}
	if len(rep.Unmapped) != 0 {
		t.Errorf("a.go is indexed, unmapped = %v", rep.Unmapped)
	}
}

func TestAffectedDepthOneStillCoversDirectTest(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"a.go"}, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if affectedTest(t, rep, "a_test.go") == nil {
		t.Fatalf("Run is directly covered by TestRun even at depth 1: %+v", rep.Tests)
	}
}

func TestAffectedChangedTestFileAndAbsolutePath(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{filepath.Join(proj, "a_test.go"), "./a_test.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 || rep.Files[0] != "a_test.go" {
		t.Fatalf("absolute and ./ forms should normalize and dedupe, got %v", rep.Files)
	}
	tst := affectedTest(t, rep, "a_test.go")
	if tst == nil || !contains(tst.Reasons, "changed") {
		t.Fatalf("a changed test file must select itself with reason changed: %+v", rep.Tests)
	}
	if len(rep.Unmapped) != 0 {
		t.Errorf("unmapped = %v", rep.Unmapped)
	}
}

func TestAffectedUnmappedAndIncomplete(t *testing.T) {
	svc, proj := reviewRepo(t)
	mustWrite(t, proj, "README.md", "# hi\n")
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"README.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.Unmapped, "README.md") || len(rep.Tests) != 0 {
		t.Fatalf("README.md should be unmapped with no tests: %+v", rep)
	}
	if !rep.AnalysisComplete {
		t.Errorf("an unmapped doc file must not make the analysis incomplete: %+v", rep)
	}

	rep, err = svc.Affected(proj, AffectedOpts{Files: []string{"missing.go", "../outside.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.Unmapped, "missing.go") || !contains(rep.Unmapped, "../outside.go") {
		t.Fatalf("unindexed and outside-project files must be unmapped: %v", rep.Unmapped)
	}
	if rep.AnalysisComplete {
		t.Errorf("an unindexed source file must make the analysis incomplete")
	}
	if rep.Tests == nil || rep.Unmapped == nil || rep.Files == nil {
		t.Errorf("slices must never be nil (JSON arrays): %+v", rep)
	}
}

func TestAffectedFilter(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"a.go"}, Filter: "*.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tests) != 0 || rep.FilteredOut != 1 || rep.Filter != "*.ts" {
		t.Fatalf("filter *.ts should drop a_test.go: %+v", rep)
	}
	rep, err = svc.Affected(proj, AffectedOpts{Files: []string{"a.go"}, Filter: "*_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tests) != 1 || rep.FilteredOut != 0 {
		t.Fatalf("filter *_test.go should keep a_test.go: %+v", rep)
	}
}

func TestAffectedFromGitDiff(t *testing.T) {
	svc, proj := reviewRepo(t)
	// No explicit files and no source: the working tree.
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n\tHelper()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Affected(proj, AffectedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != AffectedSourceWorking || len(rep.Files) != 1 || rep.Files[0] != "a.go" {
		t.Fatalf("working tree diff should select a.go: %+v", rep)
	}
	if affectedTest(t, rep, "a_test.go") == nil {
		t.Fatalf("a_test.go should cover the edited Run: %+v", rep.Tests)
	}

	// Staged only sees nothing until the edit is added.
	rep, err = svc.Affected(proj, AffectedOpts{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != AffectedSourceStaged || len(rep.Files) != 0 || len(rep.Tests) != 0 {
		t.Fatalf("nothing is staged yet: %+v", rep)
	}
	reviewGit(t, proj, "add", "a.go")
	rep, err = svc.Affected(proj, AffectedOpts{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 || affectedTest(t, rep, "a_test.go") == nil {
		t.Fatalf("staged a.go should select a_test.go: %+v", rep)
	}

	// --since unions committed changes with explicit files.
	reviewGit(t, proj, "commit", "-m", "edit")
	rep, err = svc.Affected(proj, AffectedOpts{Since: "HEAD~1", Files: []string{"b.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != AffectedSourceSince || rep.Since != "HEAD~1" || len(rep.Files) != 2 {
		t.Fatalf("since + explicit file should union to 2 files: %+v", rep)
	}
	if _, err := svc.Affected(proj, AffectedOpts{Since: "--output=x"}); err == nil {
		t.Error("an option-like --since ref must be rejected")
	}
	// An explicit-files-only run never consults git, even when the list is empty.
	rep, err = svc.Affected(proj, AffectedOpts{Source: AffectedSourceFiles})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 0 || len(rep.Tests) != 0 {
		t.Fatalf("empty explicit list means nothing changed: %+v", rep)
	}
}

func TestAffectedImportsTransitiveAndDepth(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "util.lua", "local M = {}\nfunction M.helper() end\nreturn M\n")
	mustWrite(t, proj, "mid.lua", "local util = require(\"util\")\nlocal M = {}\nreturn M\n")
	mustWrite(t, proj, "util_test.lua", "local util = require(\"util\")\nreturn util\n")
	mustWrite(t, proj, "mid_test.lua", "local mid = require(\"mid\")\nreturn mid\n")
	mustWrite(t, proj, "other_test.lua", "return {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Affected(proj, AffectedOpts{Files: []string{"util.lua"}})
	if err != nil {
		t.Fatal(err)
	}
	direct := affectedTest(t, rep, "util_test.lua")
	if direct == nil || !contains(direct.Reasons, "imports:util.lua") {
		t.Fatalf("util_test.lua imports util.lua directly: %+v", rep.Tests)
	}
	transitive := affectedTest(t, rep, "mid_test.lua")
	if transitive == nil || !contains(transitive.Reasons, "imports:util.lua") {
		t.Fatalf("mid_test.lua imports util.lua through mid.lua: %+v", rep.Tests)
	}
	if affectedTest(t, rep, "other_test.lua") != nil {
		t.Errorf("other_test.lua is unrelated: %+v", rep.Tests)
	}

	rep, err = svc.Affected(proj, AffectedOpts{Files: []string{"util.lua"}, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if affectedTest(t, rep, "util_test.lua") == nil || affectedTest(t, rep, "mid_test.lua") != nil {
		t.Fatalf("depth 1 should reach only the direct importer: %+v", rep.Tests)
	}
}

func TestAffectedUnindexedProject(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).Affected(proj, AffectedOpts{Files: []string{"a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || len(rep.Tests) != 0 || rep.Note == "" {
		t.Fatalf("unindexed project should degrade with a note: %+v", rep)
	}
}

func TestAffectedFilterGlobs(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"*_test.go", "a_test.go", true},
		{"*_test.go", "internal/app/a_test.go", true}, // slash-free patterns match base names
		{"*.test.ts", "web/src/a.test.ts", true},
		{"internal/*", "internal/app/a_test.go", false}, // * stays within one segment
		{"internal/**", "internal/app/a_test.go", true},
		{"**/a_test.go", "a_test.go", true},
		{"**/a_test.go", "x/y/a_test.go", true},
		{"internal/app/?_test.go", "internal/app/a_test.go", true},
		{"*.py", "a_test.go", false},
	}
	for _, c := range cases {
		match, err := compileAffectedFilter(c.glob)
		if err != nil {
			t.Fatalf("%q: %v", c.glob, err)
		}
		if got := match(c.path); got != c.want {
			t.Errorf("filter %q on %q = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
	if m, err := compileAffectedFilter("  "); err != nil || m != nil {
		t.Errorf("blank filter should mean none, got nil-func=%v err=%v", m == nil, err)
	}
}
