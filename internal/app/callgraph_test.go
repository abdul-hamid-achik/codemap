package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestCallGraphEnum pins the stable machine-readable call_graph enum a consumer
// switches on (vs the free-form Resolution sentence). The enum must be:
//
//	resolved   — precise edges present
//	name       — name-based call graph (Go default)
//	unresolved — no name-based call edges & not precise (TS/JS/Python/Vue)
//	none       — no matching definitions / empty
func TestCallGraphEnum(t *testing.T) {
	cases := []struct {
		precise bool
		langs   []string
		want    string
	}{
		{true, []string{"go"}, CallGraphResolved},
		{true, []string{"typescript"}, CallGraphResolved},
		{false, nil, CallGraphNone},
		{false, []string{"go"}, CallGraphName},
		{false, []string{"typescript"}, CallGraphUnresolved},
		{false, []string{"python"}, CallGraphUnresolved},
		{false, []string{"vue"}, CallGraphUnresolved},
		// worst-case wins: a mixed-language symbol with an unresolved def → unresolved
		{false, []string{"go", "typescript"}, CallGraphUnresolved},
	}
	for _, c := range cases {
		nodes := make([]graph.Node, len(c.langs))
		for i, l := range c.langs {
			nodes[i] = graph.Node{Language: l}
		}
		if got := callGraphEnum(c.precise, nodes); got != c.want {
			t.Errorf("callGraphEnum(precise=%v, langs=%v) = %q, want %q", c.precise, c.langs, got, c.want)
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
// call_graph="name", flips to "resolved" once a precise edge exists, and stays
// "none" for a symbol that isn't in the graph.
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

	// Inject one precise edge → the project now reports call_graph="resolved".
	// Run calls Helper: wire that edge as precise, then impact of Helper flips.
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(filepath.Base(proj))
	if err != nil {
		t.Fatal(err)
	}
	callers, _ := g.Callers(p.ID, "Helper")
	if len(callers) == 0 {
		t.Fatal("expected Helper to have a caller (Run) to wire a precise edge")
	}
	target, _ := g.FindNodesBySymbol(p.ID, "Helper")
	if len(target) == 0 {
		t.Fatal("expected Helper definition")
	}
	if _, err := g.AddEdgeProv(callers[0].ID, target[0].ID, graph.EdgeCalls, 1.0, graph.ProvPrecise); err != nil {
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
	ctx, err := svc.Context(proj, "Run", 3)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.CallGraph != CallGraphName {
		t.Errorf("context call_graph = %q, want %q", ctx.CallGraph, CallGraphName)
	}
	miss, err := svc.Context(proj, "Ghost", 3)
	if err != nil {
		t.Fatal(err)
	}
	if miss.CallGraph != CallGraphNone {
		t.Errorf("unknown context call_graph = %q, want %q", miss.CallGraph, CallGraphNone)
	}
}
