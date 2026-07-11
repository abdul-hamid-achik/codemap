package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestOrphansResolutionPin pins P1-08 (B71): pre-fix Orphans on a
// project with no call graph (TS/JS/Python without --precise)
// silently returned Found:false for every function as "dead code" —
// a confidently-wrong answer for the agent audience. The fix: the
// report carries Resolution="none" + a Note explaining what to run
// to make the list reliable.
func TestOrphansResolutionPin(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustExec(t, "git", "-C", proj, "init", "-q")
	mustExec(t, "git", "-C", proj, "config", "user.email", "t@t")
	mustExec(t, "git", "-C", proj, "config", "user.name", "t")
	mustWrite(t, proj, "a.go", "package a\n\nfunc Run() {}\nfunc Other() { Run() }\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := NewService(sess).Orphans(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	// Go has name-based call resolution by default, so the orphan
	// list is meaningful (Run is called by Other → not orphan).
	// Resolution should be "" (or "name") — the list is reliable.
	if rep.Resolution == "none" {
		t.Errorf("P1-08: Go project with name-based edges shouldn't have Resolution='none' (no call graph unavailable)")
	}
}

func TestOrphansReportsUnavailableCallGraph(t *testing.T) {
	svc, proj := unresolvedGraphProject(t)
	rep, err := svc.Orphans(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CallGraph != CallGraphUnresolved || !strings.Contains(rep.Resolution, "typescript") || rep.Note == "" {
		t.Fatalf("unresolved TypeScript orphans = %+v, want resolution and reliability note", rep)
	}
}

func TestPathReportsUnavailableCallGraph(t *testing.T) {
	svc, proj := unresolvedGraphProject(t)
	rep, err := svc.Path(proj, "start", "finish")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || rep.CallGraph != CallGraphUnresolved || !strings.Contains(rep.Resolution, "typescript") {
		t.Fatalf("unresolved TypeScript path = %+v, want found:false resolution:typescript", rep)
	}
}

func TestPathMissingEndpointIsNotMisreportedAsGraphFailure(t *testing.T) {
	svc, proj := unresolvedGraphProject(t)
	rep, err := svc.Path(proj, "start", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if rep.CallGraph != CallGraphNone || !strings.Contains(rep.Note, "not a symbol") {
		t.Fatalf("missing endpoint should be the primary answer, got %+v", rep)
	}
}

func TestPathConfidenceIncludesUncoveredIntermediate(t *testing.T) {
	svc, proj, g, pid, ids := pathCoverageProject(t, 3, true)
	for _, i := range []int{0, 2} {
		if err := g.MarkCallGraphResolved(pid, fmt.Sprintf("n%d.ts", i), "lsp"); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := svc.Path(proj, "n0", "n2")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || len(ids) != 3 || rep.CallGraph != CallGraphUnresolved || rep.Resolution == "" {
		t.Fatalf("covered endpoints must not hide an uncovered intermediate: %+v", rep)
	}
	if err := g.MarkCallGraphResolved(pid, "n1.ts", "lsp"); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Path(proj, "n0", "n2")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.CallGraph != CallGraphResolved || rep.Resolution != "" {
		t.Fatalf("fully covered path should be resolved: %+v", rep)
	}
}

func TestNoPathConfidenceCoversWholeProject(t *testing.T) {
	svc, proj, g, pid, _ := pathCoverageProject(t, 3, false)
	for _, i := range []int{0, 2} {
		if err := g.MarkCallGraphResolved(pid, fmt.Sprintf("n%d.ts", i), "lsp"); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := svc.Path(proj, "n0", "n2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || rep.CallGraph != CallGraphUnresolved || rep.Resolution == "" {
		t.Fatalf("an uncovered project callable leaves a negative path assertion unresolved: %+v", rep)
	}
	if err := g.MarkCallGraphResolved(pid, "n1.ts", "lsp"); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Path(proj, "n0", "n2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || rep.CallGraph != CallGraphResolved || rep.Resolution != "" {
		t.Fatalf("fully covered project can make a resolved no-path assertion: %+v", rep)
	}
}

func TestPathDefaultDepthIsUnboundedAndResolved(t *testing.T) {
	svc, proj, g, pid, _ := pathCoverageProject(t, 13, true) // 12 hops: longer than the old hidden cap.
	for i := 0; i < 13; i++ {
		if err := g.MarkCallGraphResolved(pid, fmt.Sprintf("n%d.ts", i), "lsp"); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := svc.Path(proj, "n0", "n12")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || len(rep.Path) != 13 || rep.CallGraph != CallGraphResolved {
		t.Fatalf("12-hop covered path should be found and resolved: %+v", rep)
	}
}

func pathCoverageProject(t *testing.T, count int, connect bool) (*Service, string, *graph.Store, int64, []int64) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(config.DeriveProjectName(proj), proj, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		symbol := fmt.Sprintf("n%d", i)
		id, addErr := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: symbol + ".ts", Symbol: symbol, FQN: symbol,
			Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1, SourceHash: "h",
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
	}
	if connect {
		for i := 0; i+1 < len(ids); i++ {
			if _, err := g.AddEdgeProv(ids[i], ids[i+1], graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
				t.Fatal(err)
			}
		}
	}
	return NewService(sess), proj, g, pid, ids
}

func TestHotspotsReportsUnavailableCallGraphWhenEmpty(t *testing.T) {
	svc, proj := unresolvedGraphProject(t)
	rep, err := svc.Hotspots(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hotspots) != 0 {
		t.Fatalf("fixture should have no call edges/hotspots, got %+v", rep.Hotspots)
	}
	if rep.CallGraph != CallGraphUnresolved || rep.Resolution == "" || rep.Note == "" {
		t.Fatalf("empty unresolved TypeScript hotspots = %+v, want explicit unresolved confidence", rep)
	}
}

func unresolvedGraphProject(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(config.DeriveProjectName(proj), proj, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"start", "finish"} {
		if _, err := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: "a.ts", Symbol: symbol, FQN: symbol,
			Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1, SourceHash: "h",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(sess), proj
}

// mustExec runs a shell command in dir, failing the test on error.
func mustExec(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// mustWrite writes rel=content in dir, failing the test on error.
func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := dir + "/" + rel
	if err := os.MkdirAll(parentDir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
