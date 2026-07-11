package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
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
	if rep.DeleteVerdict != DeleteVerdictUnsafe {
		t.Errorf("a.go has proven inbound calls: delete_verdict = %q, want %q", rep.DeleteVerdict, DeleteVerdictUnsafe)
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

func TestFileImpactDeletionUnknownWithoutInboundCalls(t *testing.T) {
	svc, proj := fileImpactProj(t)
	rep, err := svc.FileImpact(proj, "c.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatalf("c.go should be found, got %+v", rep)
	}
	if len(rep.DependentFiles) != 0 {
		t.Errorf("c.go's Lonely has no callers, got dependent files %+v", rep.DependentFiles)
	}
	if rep.SafeToDelete || rep.DeleteVerdict != DeleteVerdictUnknown {
		t.Errorf("absence of call edges cannot prove deletion safe, got %+v", rep)
	}
	if rep.Note == "" {
		t.Error("unknown deletion verdict should explain the missing dependency evidence")
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

// TestFileImpactVerdictsWithheldOnTruncation pins the conservative legacy
// safe_to_delete field even for a complete, small calls-only analysis.
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
	if rep.SafeToDelete || rep.DeleteVerdict != DeleteVerdictUnknown {
		t.Errorf("calls-only analysis must not claim deletion safety; got %+v", rep)
	}
}

func TestFileImpactUsesExactDefinitionSelectors(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "a.go", "package app\n\ntype A struct{}\nfunc (A) Close() {}\n")
	mustWrite(t, proj, "b.go", "package app\n\ntype B struct{}\nfunc (B) Close() {}\n")
	mustWrite(t, proj, "use.go", "package app\n\nfunc UseA() {}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(config.DeriveProjectName(proj), proj, "go")
	if err != nil {
		t.Fatal(err)
	}
	add := func(file, symbol, fqn, kind string, line int) int64 {
		t.Helper()
		id, addErr := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: file, Symbol: symbol, FQN: fqn, Kind: kind,
			Language: "go", StartLine: line, EndLine: line, SourceHash: "h",
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return id
	}
	for _, file := range []string{"a.go", "b.go", "use.go"} {
		add(file, "", "", graph.KindFile, 1)
		if err := g.MarkCallGraphResolved(pid, file, "test"); err != nil {
			t.Fatal(err)
		}
	}
	add("a.go", "A", "app.A", graph.KindType, 3)
	aClose := add("a.go", "Close", "app.A.Close", graph.KindMethod, 4)
	add("b.go", "B", "app.B", graph.KindType, 3)
	add("b.go", "Close", "app.B.Close", graph.KindMethod, 4)
	useA := add("use.go", "UseA", "app.UseA", graph.KindFunction, 3)
	if _, err := g.AddEdgeProv(useA, aClose, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}

	svc := NewService(sess)
	aImpact, err := svc.FileImpact(proj, "a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	bImpact, err := svc.FileImpact(proj, "b.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if aImpact.DeleteVerdict != DeleteVerdictUnsafe || !contains(aImpact.DependentFiles, "use.go") {
		t.Fatalf("A.Close exact impact = %+v, want use.go dependency", aImpact)
	}
	if bImpact.DeleteVerdict != DeleteVerdictUnknown || bImpact.BreakingChange || bImpact.BlastRadius != 0 || len(bImpact.DependentFiles) != 0 {
		t.Fatalf("B.Close must not inherit A.Close callers from the shared short name: %+v", bImpact)
	}
}
