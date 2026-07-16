package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestCallGraphEnum pins the stable machine-readable call_graph enum a consumer
// switches on (vs the free-form Resolution sentence). The enum must be:
//
//	resolved   — every matching definition file completed precise resolution
//	name       — name-based call graph (Go default)
//	unresolved — no name-based call edges & not precise (TS/JS/Python/Vue)
//	none       — no matching definitions / empty
func TestCallGraphEnum(t *testing.T) {
	cases := []struct {
		langs    []string
		resolved []bool
		want     string
	}{
		{[]string{"go"}, []bool{true}, CallGraphResolved},
		{[]string{"typescript"}, []bool{true}, CallGraphResolved},
		{nil, nil, CallGraphNone},
		{[]string{"go"}, []bool{false}, CallGraphName},
		{[]string{"typescript"}, []bool{false}, CallGraphUnresolved},
		{[]string{"python"}, []bool{false}, CallGraphUnresolved},
		{[]string{"vue"}, []bool{false}, CallGraphUnresolved},
		// worst-case wins: a mixed-language symbol with an unresolved def → unresolved
		{[]string{"go", "typescript"}, []bool{true, false}, CallGraphUnresolved},
		// An uncovered Go definition keeps a mixed result name-based even when
		// another definition is exact.
		{[]string{"go", "go"}, []bool{true, false}, CallGraphName},
	}
	for _, c := range cases {
		nodes := make([]graph.Node, len(c.langs))
		resolvedFiles := map[string]bool{}
		for i, l := range c.langs {
			file := fmt.Sprintf("file-%d", i)
			nodes[i] = graph.Node{Language: l, FilePath: file}
			if c.resolved[i] {
				resolvedFiles[file] = true
			}
		}
		if got := callGraphEnum(resolvedFiles, nodes); got != c.want {
			t.Errorf("callGraphEnum(langs=%v, resolved=%v) = %q, want %q", c.langs, c.resolved, got, c.want)
		}
	}
}

// TestWorstCallGraph pins the review aggregation: a diff's band is only as
// confident as its least-resolved changed symbol (min rank wins).
func TestWorstCallGraph(t *testing.T) {
	if got := worstCallGraph(nil); got != CallGraphNone {
		t.Errorf("worstCallGraph(nil) = %q, want none", got)
	}
	imps := []*ImpactReport{{CallGraph: CallGraphResolved}, {CallGraph: CallGraphName}}
	if got := worstCallGraph(imps); got != CallGraphName {
		t.Errorf("resolved+name → %q, want name", got)
	}
	imps = append(imps, &ImpactReport{CallGraph: CallGraphUnresolved})
	if got := worstCallGraph(imps); got != CallGraphUnresolved {
		t.Errorf("resolved+name+unresolved → %q, want unresolved", got)
	}
}

// TestImpactCallGraphNameBased pins that a Go (name-based) index reports
// call_graph="name", flips to "resolved" once its file has precise coverage,
// and stays "none" for a symbol that isn't in the graph.
func TestImpactCallGraphNameBased(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(src), 0o644); err != nil {
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

	// A real Go symbol on a name-based index → "name". Helper is called by Run.
	imp, err := svc.Impact(proj, "Helper", 2)
	if err != nil {
		t.Fatal(err)
	}
	if imp.CallGraph != CallGraphName {
		t.Errorf("Go name-based impact call_graph = %q, want %q", imp.CallGraph, CallGraphName)
	}

	// An unknown symbol → "none" (no matching definitions).
	miss, err := svc.Impact(proj, "DoesNotExist", 2)
	if err != nil {
		t.Fatal(err)
	}
	if miss.CallGraph != CallGraphNone {
		t.Errorf("unknown symbol call_graph = %q, want %q", miss.CallGraph, CallGraphNone)
	}

	// Mark the definition file successfully resolved. Coverage, not the existence
	// of an arbitrary precise edge, is what flips Helper to resolved.
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(filepath.Base(proj))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(p.ID, "a.go", "test"); err != nil {
		t.Fatal(err)
	}
	imp2, err := svc.Impact(proj, "Helper", 2)
	if err != nil {
		t.Fatal(err)
	}
	if imp2.CallGraph != CallGraphResolved {
		t.Errorf("precise index impact call_graph = %q, want %q", imp2.CallGraph, CallGraphResolved)
	}
}

func TestImpactCallGraphMixedUsesPerFileCoverage(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(filepath.Base(proj), proj, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	goID, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "worker.go", Symbol: "Shared", FQN: "app.Shared",
		Kind: graph.KindFunction, Language: "go", SourceHash: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	tsID, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "worker.ts", Symbol: "Shared", FQN: "Shared",
		Kind: graph.KindFunction, Language: "typescript", SourceHash: "ts",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Even a real precise edge and exact Go coverage cannot upgrade the
	// unresolved TypeScript definition sharing this query name.
	if _, err := g.AddEdgeProv(goID, tsID, graph.EdgeCalls, 1, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(pid, "worker.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Impact(proj, "Shared", 1)
	if err != nil {
		t.Fatal(err)
	}
	if imp.CallGraph != CallGraphUnresolved {
		t.Fatalf("mixed Go-resolved/TS-unresolved impact = %q, want unresolved", imp.CallGraph)
	}

	// A successful leaf callHierarchy request produces coverage even without an
	// outgoing edge; once both definition files are covered, the union is exact.
	if err := g.MarkCallGraphResolved(pid, "worker.ts", "lsp"); err != nil {
		t.Fatal(err)
	}
	imp, err = svc.Impact(proj, "Shared", 1)
	if err != nil {
		t.Fatal(err)
	}
	if imp.CallGraph != CallGraphResolved {
		t.Fatalf("fully covered mixed impact = %q, want resolved", imp.CallGraph)
	}
}

// TestRelationCallGraph pins callers/callees carry the same enum, and that a
// blank symbol short-circuits to "none".
func TestRelationCallGraph(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
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
	ca, err := svc.Callers(proj, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if ca.CallGraph != CallGraphName {
		t.Errorf("callers call_graph = %q, want %q", ca.CallGraph, CallGraphName)
	}
	ce, err := svc.Callees(proj, "Run")
	if err != nil {
		t.Fatal(err)
	}
	if ce.CallGraph != CallGraphName {
		t.Errorf("callees call_graph = %q, want %q", ce.CallGraph, CallGraphName)
	}
	// Unknown symbol → "none" (no defs to classify).
	miss, err := svc.Callers(proj, "Ghost")
	if err != nil {
		t.Fatal(err)
	}
	if miss.CallGraph != CallGraphNone {
		t.Errorf("unknown callers call_graph = %q, want %q", miss.CallGraph, CallGraphNone)
	}
	// Blank symbol short-circuit → "none".
	blank, err := svc.Callers(proj, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if blank.CallGraph != CallGraphNone {
		t.Errorf("blank callers call_graph = %q, want %q", blank.CallGraph, CallGraphNone)
	}
}

func TestRelationKeepsUnresolvedSignalWithPartialResults(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := g.UpsertProject(filepath.Base(proj), proj, "typescript")
	caller, _ := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "caller.ts", Symbol: "caller", FQN: "caller",
		Kind: graph.KindFunction, Language: "typescript", SourceHash: "caller",
	})
	target, _ := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "target.ts", Symbol: "target", FQN: "target",
		Kind: graph.KindFunction, Language: "typescript", SourceHash: "target",
	})
	// A stale/partial precise edge may still produce a non-empty answer. Without
	// coverage for target.ts, the report must remain unresolved so auto-upgrade
	// can replace it rather than treating the partial result as authoritative.
	if _, err := g.AddEdgeProv(caller, target, graph.EdgeCalls, 1, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.relation(proj, "target", (*graph.Store).Callers)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("fixture should return one partial caller, got %+v", rep.Results)
	}
	if rep.CallGraph != CallGraphUnresolved || rep.Resolution == "" {
		t.Fatalf("partial uncovered relation = %+v, want unresolved signal", rep)
	}
}

// TestContextCallGraph pins the bundle carries call_graph from its bundled
// Impact (none for an unknown symbol, name for a Go symbol).
func TestContextCallGraph(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
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
	ctx, err := svc.Context(proj, "Run", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.CallGraph != CallGraphName {
		t.Errorf("context call_graph = %q, want %q", ctx.CallGraph, CallGraphName)
	}
	miss, err := svc.Context(proj, "Ghost", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if miss.CallGraph != CallGraphNone {
		t.Errorf("unknown context call_graph = %q, want %q", miss.CallGraph, CallGraphNone)
	}
}

// TestPreciseCoverageHint pins the coverage-aware qualifier appended to
// "run 'codemap index --precise'" Resolution sentences: silent when the
// project never had precise coverage (the bare advice is the whole story),
// an N/M decay warning when coverage is partial (the daemon-watching-
// without---precise footgun), and silent again on full coverage (where
// callGraphUnavailable can't fire anyway).
func TestPreciseCoverageHint(t *testing.T) {
	callables := []graph.Node{
		{FilePath: "a.ts"}, {FilePath: "a.ts"}, // same file twice — counted once
		{FilePath: "b.ts"},
	}
	if got := preciseCoverageHint(map[string]bool{}, callables); got != "" {
		t.Errorf("no coverage should yield no hint, got %q", got)
	}
	got := preciseCoverageHint(map[string]bool{"a.ts": true}, callables)
	if !strings.Contains(got, "1/2 callable files") || !strings.Contains(got, "--precise") {
		t.Errorf("partial coverage hint = %q, want a 1/2 decay warning naming --precise", got)
	}
	if got := preciseCoverageHint(map[string]bool{"a.ts": true, "b.ts": true}, callables); got != "" {
		t.Errorf("full coverage should yield no hint, got %q", got)
	}
}

// TestImpactResolutionCarriesCoverageHint pins the end-to-end wiring: a TS
// project whose coverage is partial (one callable file covered, one not) gets
// the N/M decay suffix appended to impact's unresolved Resolution sentence.
func TestImpactResolutionCarriesCoverageHint(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(filepath.Base(proj), proj, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "a.ts", Symbol: "alpha", FQN: "alpha",
		Kind: graph.KindFunction, Language: "typescript", SourceHash: "a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "b.ts", Symbol: "beta", FQN: "beta",
		Kind: graph.KindFunction, Language: "typescript", SourceHash: "b",
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(pid, "a.ts", "lsp"); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Impact(proj, "beta", 1)
	if err != nil {
		t.Fatal(err)
	}
	if imp.CallGraph != CallGraphUnresolved {
		t.Fatalf("impact call_graph = %q, want unresolved", imp.CallGraph)
	}
	if !strings.Contains(imp.Resolution, "1/2 callable files") {
		t.Errorf("Resolution = %q, want the 1/2 partial-coverage decay hint appended", imp.Resolution)
	}
}
