package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/google/jsonschema-go/jsonschema"
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

func assertReviewSchemaVersion(t *testing.T, rep *ReviewReport) {
	t.Helper()
	if rep.SchemaVersion != 1 {
		t.Fatalf("review schema_version = %d, want 1", rep.SchemaVersion)
	}
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
	assertReviewSchemaVersion(t, rep)
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
	if len(rep.TestCommands) == 0 || !strings.Contains(rep.TestCommands[0], "go test") || !strings.Contains(rep.TestCommands[0], "TestRun") {
		t.Errorf("review should emit a runnable Go regression command, got %+v", rep.TestCommands)
	}
	foundTerminalNext := false
	for _, next := range rep.Next {
		if next.Tool == "terminal" && next.Args["command"] == rep.TestCommands[0] {
			foundTerminalNext = true
		}
	}
	if !foundTerminalNext {
		t.Errorf("review next actions should point directly at the selected test command, got %+v", rep.Next)
	}
}

func TestReviewDeletedFileUsesLastIndexedSymbols(t *testing.T) {
	svc, proj := reviewRepo(t)
	if err := os.Remove(filepath.Join(proj, "a.go")); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	assertReviewSchemaVersion(t, rep)
	if rep.DeletionAnalysis == nil {
		t.Fatal("deleted file should emit deletion_analysis")
	}
	if got := *rep.DeletionAnalysis; got.Files != 1 || got.Analyzed != 1 || got.Missing != 0 || !got.Complete || got.Source != "last_index" {
		t.Fatalf("deletion analysis = %+v", got)
	}
	if !hasSymbol(rep.ChangedSymbols, "Run") || !hasSymbol(rep.ChangedSymbols, "Helper") {
		t.Fatalf("deleted definitions were not analyzed: %+v", rep.ChangedSymbols)
	}
	var deleted ReviewFile
	for _, file := range rep.ChangedFiles {
		if file.Path == "a.go" {
			deleted = file
		}
	}
	if deleted.Status != "D" || deleted.Symbols < 2 {
		t.Fatalf("deleted file coverage = %+v", deleted)
	}
	if !hasImpactSymbol(rep.BlastRadius, "Other") || len(rep.CoveringTests) == 0 || len(rep.TestCommands) == 0 {
		t.Fatalf("deleted-file impact/tests missing: blast=%+v tests=%+v commands=%+v", rep.BlastRadius, rep.CoveringTests, rep.TestCommands)
	}
	if !strings.Contains(rep.Note, "last indexed snapshot") {
		t.Fatalf("deleted-file note must explain evidence source: %q", rep.Note)
	}
	if len(rep.Next) < 2 || rep.Next[0].Tool != "terminal" || rep.Next[1].Tool != "codemap_index" {
		t.Fatalf("deletion next actions must test before reindex: %+v", rep.Next)
	}
}

func TestReviewDeletedFileReportsMissingAfterReindex(t *testing.T) {
	svc, proj := reviewRepo(t)
	if err := os.Remove(filepath.Join(proj, "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletionAnalysis == nil || rep.DeletionAnalysis.Files != 1 || rep.DeletionAnalysis.Analyzed != 0 || rep.DeletionAnalysis.Missing != 1 || rep.DeletionAnalysis.Complete {
		t.Fatalf("pruned deletion analysis = %+v", rep.DeletionAnalysis)
	}
	if !strings.Contains(rep.Note, "prior impact is unavailable") {
		t.Fatalf("missing deletion evidence must be explicit: %q", rep.Note)
	}
}

func TestReviewKeepsSameNamedMethodsExact(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	proj := t.TempDir()
	src := `package app

type A struct{}
func (A) Close() {}

type B struct{}
func (B) Close() {}

func CallA(a A) { a.Close() }
func CallB(b B) { b.Close() }
`
	mustWrite(t, proj, "same.go", src)
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
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, _, indexed, err := svc.project(proj)
	if err != nil || !indexed {
		t.Fatalf("project lookup = pid:%d indexed:%v err:%v", pid, indexed, err)
	}
	nodes, err := g.NodesInFile(pid, "same.go")
	if err != nil {
		t.Fatal(err)
	}
	byFQN := map[string]int64{}
	for _, n := range nodes {
		byFQN[n.FQN] = n.ID
	}
	ids := func(suffix string) int64 {
		t.Helper()
		for fqn, id := range byFQN {
			if strings.HasSuffix(fqn, suffix) {
				return id
			}
		}
		t.Fatalf("no node ending in %q: %+v", suffix, byFQN)
		return 0
	}
	callA, callB := ids(".CallA"), ids(".CallB")
	closeA, closeB := ids(".A.Close"), ids(".B.Close")
	if err := g.DeleteCallEdgesBySource([]int64{callA, callB}, graph.ProvName); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(callA, closeA, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(callB, closeB, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}

	edited := strings.Replace(src, "func (A) Close() {}", "func (A) Close() { _ = 1 }", 1)
	mustWrite(t, proj, "same.go", edited)
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(rep.ChangedSymbols, "Close") {
		t.Fatalf("changed A.Close not found: %+v", rep.ChangedSymbols)
	}
	if !hasSymbolRefByFQN(rep.ChangedSymbols, ".A.Close") {
		t.Fatalf("review selected the wrong Close definition: %+v", rep.ChangedSymbols)
	}
	if !hasImpactSymbol(rep.BlastRadius, "CallA") {
		t.Fatalf("A.Close blast radius should contain CallA: %+v", rep.BlastRadius)
	}
	if hasImpactSymbol(rep.BlastRadius, "CallB") {
		t.Fatalf("A.Close review merged B.Close's caller: %+v", rep.BlastRadius)
	}
}

func hasSymbolRefByFQN(refs []SymbolRef, suffix string) bool {
	for _, ref := range refs {
		if strings.HasSuffix(ref.FQN, suffix) {
			return true
		}
	}
	return false
}

func hasImpactSymbol(nodes []ImpactNode, symbol string) bool {
	for _, node := range nodes {
		if node.Symbol == symbol {
			return true
		}
	}
	return false
}

func TestTestCommandsFallsBackToPackageForLargeGoSelection(t *testing.T) {
	tests := make([]ImpactNode, 0, 13)
	for i := 0; i < 13; i++ {
		tests = append(tests, ImpactNode{File: "internal/app/review_test.go", Symbol: fmt.Sprintf("TestCase%d", i)})
	}
	got := testCommands(tests)
	if len(got) != 1 || got[0] != "go test ./internal/app" {
		t.Fatalf("large selected test set = %+v, want package-level command", got)
	}
}

func TestReviewNoChanges(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	assertReviewSchemaVersion(t, rep)
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
	assertReviewSchemaVersion(t, rep)
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
	assertReviewSchemaVersion(t, rep)
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
	assertReviewSchemaVersion(t, rep)
	if !strings.Contains(rep.Note, "invalid --since ref") {
		t.Errorf("expected graceful Note explaining invalid ref, got %q", rep.Note)
	}
	if _, statErr := os.Stat("/tmp/PWNED_for_P0_03"); statErr == nil {
		t.Fatal("exploit must not write a file from a bad --since")
	}
}

func TestReviewRejectsUnsupportedModeAndMissingSince(t *testing.T) {
	svc, proj := reviewRepo(t)
	cases := []struct {
		name    string
		opts    ReviewOpts
		wantErr string
	}{
		{name: "unsupported mode", opts: ReviewOpts{Mode: "bogus"}, wantErr: "unsupported review mode"},
		{name: "since without ref", opts: ReviewOpts{Mode: "since"}, wantErr: "requires a non-empty since ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := svc.Review(proj, tc.opts)
			if err == nil {
				t.Fatalf("Review(%+v) = %+v, nil error; want rejection", tc.opts, rep)
			}
			if rep != nil {
				t.Fatalf("Review(%+v) report = %+v, want nil on invalid input", tc.opts, rep)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Review(%+v) error = %q, want %q", tc.opts, err, tc.wantErr)
			}
		})
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

func TestReviewContractV1(t *testing.T) {
	svc, proj := reviewRepo(t)
	edited := "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper() // touched\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	assertReviewSchemaVersion(t, rep)
	rep.Project = "contract-project"
	for i := range rep.Next {
		if rep.Next[i].Tool == "codemap_index" {
			rep.Next[i].Args["path"] = "contract-project"
		}
	}

	got, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	goldenPath := filepath.Join("testdata", "contracts", "codemap.review.v1.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReviewReport JSON does not match %s\n--- got ---\n%s--- want ---\n%s", goldenPath, got, want)
	}

	schemaPath := filepath.Join("..", "..", "schemas", "codemap.review.v1.schema.json")
	schemaJSON, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve %s: %v", schemaPath, err)
	}
	var document any
	if err := json.Unmarshal(got, &document); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(document); err != nil {
		t.Fatalf("%s does not validate against %s: %v", goldenPath, schemaPath, err)
	}

	cloneDocument := func() map[string]any {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var cloned map[string]any
		if err := json.Unmarshal(encoded, &cloned); err != nil {
			t.Fatal(err)
		}
		return cloned
	}
	invalidCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "zero depth", mutate: func(root map[string]any) { root["depth"] = float64(0) }},
		{name: "null stale", mutate: func(root map[string]any) { root["stale"] = nil }},
		{name: "invalid changed file status", mutate: func(root map[string]any) {
			root["changed_files"].([]any)[0].(map[string]any)["status"] = "R"
		}},
		{name: "invalid symbol start line", mutate: func(root map[string]any) {
			root["changed_symbols"].([]any)[0].(map[string]any)["start_line"] = float64(0)
		}},
		{name: "invalid impact depth", mutate: func(root map[string]any) {
			root["blast_radius"].([]any)[0].(map[string]any)["depth"] = float64(-1)
		}},
		{name: "null risk", mutate: func(root map[string]any) { root["risk"] = nil }},
		{name: "invalid risk level", mutate: func(root map[string]any) {
			root["risk"].(map[string]any)["level"] = "critical"
		}},
		{name: "risk score above one", mutate: func(root map[string]any) {
			root["risk"].(map[string]any)["score"] = 1.01
		}},
		{name: "invalid risk factor", mutate: func(root map[string]any) {
			root["risk"].(map[string]any)["factors"] = []any{
				map[string]any{"factor": "other", "severity": 0.5, "detail": "invalid"},
			}
		}},
		{name: "negative risk severity", mutate: func(root map[string]any) {
			root["risk"].(map[string]any)["factors"] = []any{
				map[string]any{"factor": "unresolved", "severity": -0.01, "detail": "invalid"},
			}
		}},
		{name: "camel case schema version alias", mutate: func(root map[string]any) {
			root["schemaVersion"] = root["schema_version"]
			delete(root, "schema_version")
		}},
		{name: "unsupported schema version", mutate: func(root map[string]any) {
			root["schema_version"] = float64(2)
		}},
		{name: "missing schema version", mutate: func(root map[string]any) {
			delete(root, "schema_version")
		}},
		{name: "since mode without ref", mutate: func(root map[string]any) {
			root["mode"] = "since"
			delete(root, "since")
		}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			malformed := cloneDocument()
			tc.mutate(malformed)
			if err := resolved.Validate(malformed); err == nil {
				t.Fatalf("v1 schema accepted malformed document: %#v", malformed)
			}
		})
	}

	root := cloneDocument()
	root["future_optional_field"] = true
	root["changed_symbols"].([]any)[0].(map[string]any)["future_symbol_field"] = "additive"
	root["risk"].(map[string]any)["future_risk_field"] = 1
	if err := resolved.Validate(root); err != nil {
		t.Fatalf("v1 schema rejected additive optional fields: %v", err)
	}
}
