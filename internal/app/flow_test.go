package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// flowProj indexes (name-based, no --precise) a small Go module made of the
// given files and returns the service and project root.
func flowProj(t *testing.T, files map[string]string) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/fl\n\ngo 1.25\n"
	}
	for name, src := range files {
		path := filepath.Join(proj, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
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

func flowChild(s *FlowStep, symbol string) *FlowStep {
	for _, c := range s.Children {
		if c.Symbol == symbol {
			return c
		}
	}
	return nil
}

func flowSymbols(s *FlowStep) []string {
	out := make([]string, 0, len(s.Children))
	for _, c := range s.Children {
		out = append(out, c.Symbol)
	}
	return out
}

func flowCount(s *FlowStep) int {
	n := 1
	for _, c := range s.Children {
		n += flowCount(c)
	}
	return n
}

const flowAmbiguityMain = `package main

import (
	"example.com/fl/internal/netx"
	"example.com/fl/internal/storex"
)

// Run wires the pieces together. It is the entry point of the flow.
func Run() {
	conn := netx.Dial()
	conn.Close()
	st := storex.New()
	st.Close()
	helper()
}

func helper() {}
`

func flowAmbiguityFiles() map[string]string {
	return map[string]string{
		"cmd/app/main.go": flowAmbiguityMain,
		"internal/netx/netx.go": `package netx

type Conn struct{}

// Dial opens a connection. Extra detail follows.
func Dial() *Conn { return &Conn{} }

func (c *Conn) Close() {}
`,
		"internal/storex/storex.go": `package storex

type Store struct{}

func New() *Store { return &Store{} }

func (s *Store) Close() {}
`,
		"internal/dbx/dbx.go": `package dbx

type DB struct{}

func (d *DB) Close() {}
`,
		"internal/cachex/cachex.go": `package cachex

type Cache struct{}

func (c *Cache) Close() {}
`,
	}
}

func TestFlowCallOrderDocsAndAmbiguityCollapse(t *testing.T) {
	svc, proj := flowProj(t, flowAmbiguityFiles())
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "Run"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.Root == nil || rep.SchemaVersion != 1 || !rep.Indexed {
		t.Fatalf("flow identity = %+v", rep)
	}
	root := rep.Root
	if root.ID != "s0" || root.Confidence != "confirmed" || root.Depth != 0 || root.File != "cmd/app/main.go" {
		t.Fatalf("root = %+v", root)
	}
	if root.Doc != "Run wires the pieces together." {
		t.Errorf("root doc = %q, want the first sentence only", root.Doc)
	}
	if root.Subsystem != "cmd/app" {
		t.Errorf("root subsystem = %q", root.Subsystem)
	}

	// Children appear in source order, not name order: Dial, conn.Close, New, st.Close, helper.
	got := flowSymbols(root)
	want := []string{"Dial", "Close", "New", "Close", "helper"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("children = %v, want %v", got, want)
	}
	for i, c := range root.Children {
		if c.CallOrder != i+1 {
			t.Errorf("child %d (%s) call_order = %d, want %d", i, c.Symbol, c.CallOrder, i+1)
		}
		if c.Confidence != "candidate" {
			t.Errorf("name-based child %s confidence = %q, want candidate", c.Symbol, c.Confidence)
		}
	}

	dial := root.Children[0]
	if dial.Doc != "Dial opens a connection." || dial.Subsystem != "internal/netx" || dial.StartLine == 0 {
		t.Errorf("Dial step = %+v", dial)
	}

	// conn.Close() relates to Conn by name: collapsed to Conn.Close with 3 alternatives.
	closeConn := root.Children[1]
	if closeConn.FQN != "netx.Conn.Close" || closeConn.Alternatives != 3 || closeConn.LeafReason != "" {
		t.Errorf("conn.Close = %+v, want netx.Conn.Close with 3 alternatives", closeConn)
	}
	// st.Close() has no evidence among 4 definitions: an unexpanded placeholder.
	placeholder := root.Children[3]
	if placeholder.LeafReason != "ambiguous" || placeholder.File != "" || placeholder.FQN != "" || placeholder.Alternatives != 4 {
		t.Errorf("st.Close = %+v, want an ambiguous placeholder over 4 definitions", placeholder)
	}
	if len(placeholder.Candidates) == 0 || placeholder.Candidates[0].Selector == nil {
		t.Errorf("placeholder should list candidate selectors: %+v", placeholder.Candidates)
	}
	if rep.AmbiguousCalls != 2 {
		t.Errorf("ambiguous_calls = %d, want 2", rep.AmbiguousCalls)
	}
	if rep.CallGraph == "" || rep.CallGraph == CallGraphNone {
		t.Errorf("call_graph = %q", rep.CallGraph)
	}
	if rep.Notes == nil || rep.PartialErrors == nil || rep.Subsystems == nil || rep.Files == nil {
		t.Errorf("slices must serialize as [] not null: %+v", rep)
	}
	if rep.Subsystems[0].Name != "cmd/app" || rep.Subsystems[0].FirstDepth != 0 {
		t.Errorf("subsystems = %+v, want cmd/app first", rep.Subsystems)
	}
	if rep.StepsEmitted != flowCount(root) || rep.StepsTotal != rep.StepsEmitted || rep.Truncated {
		t.Errorf("counts = total %d emitted %d truncated %v, tree has %d", rep.StepsTotal, rep.StepsEmitted, rep.Truncated, flowCount(root))
	}
}

func TestFlowDeterministicJSON(t *testing.T) {
	svc, proj := flowProj(t, flowAmbiguityFiles())
	var first []byte
	for i := 0; i < 3; i++ {
		rep, err := svc.Flow(proj, FlowOptions{Symbol: "Run"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = data
		} else if string(first) != string(data) {
			t.Fatalf("flow output changed between identical runs")
		}
	}
	if strings.Contains(string(first), `"children":null`) {
		t.Errorf("children must serialize as [] not null")
	}
}

func TestFlowRejectsLocalClosureSharingAFunctionName(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"a/a.go": `package a

func parse() {
	flush := func() {}
	flush()
	keep()
}

func keep() {}
`,
		"b/b.go": `package b

type Watcher struct{}

func (w *Watcher) flush() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "parse"})
	if err != nil {
		t.Fatal(err)
	}
	if flowChild(rep.Root, "flush") != nil {
		t.Errorf("a bare flush() cannot reach another package's method: %v", flowSymbols(rep.Root))
	}
	if flowChild(rep.Root, "keep") == nil {
		t.Errorf("keep() must remain: %v", flowSymbols(rep.Root))
	}
	found := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "rejected") {
			found = true
		}
	}
	if !found {
		t.Errorf("a rejected edge must be counted in notes, got %v", rep.Notes)
	}
}

func TestFlowCycleAndRepeat(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"x/x.go": `package x

func a() {
	b()
	c()
}

func b() {
	c()
	a()
}

func c() { d() }

func d() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := flowChild(rep.Root, "b")
	if b == nil {
		t.Fatalf("a must call b: %v", flowSymbols(rep.Root))
	}
	backEdge := flowChild(b, "a")
	if backEdge == nil || !backEdge.Cycle || backEdge.LeafReason != "cycle" || len(backEdge.Children) != 0 {
		t.Errorf("b->a must be a cycle leaf, got %+v", backEdge)
	}
	cUnderB := flowChild(b, "c")
	cUnderA := flowChild(rep.Root, "c")
	if cUnderB == nil || cUnderA == nil {
		t.Fatalf("c must appear under both: %v / %v", flowSymbols(b), flowSymbols(rep.Root))
	}
	if cUnderB.RepeatOf != "" || cUnderB.LeafReason != "" {
		t.Errorf("first c is the expansion: %+v", cUnderB)
	}
	if cUnderA.RepeatOf != cUnderB.ID || cUnderA.LeafReason != "repeat" || len(cUnderA.Children) != 0 {
		t.Errorf("second c must repeat %s, got %+v", cUnderB.ID, cUnderA)
	}
	if cUnderA.CallOrder != 2 || b.CallOrder != 1 {
		t.Errorf("call orders = b %d, c %d", b.CallOrder, cUnderA.CallOrder)
	}
}

func TestFlowDepthAndMaxNodesBounds(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"x/x.go": `package x

func root() {
	l1()
	l2()
	l3()
	l4()
	l5()
	l6()
	l7()
	l8()
	l9()
	l10()
}

func l1() { deep1() }
func l2()  {}
func l3()  {}
func l4()  {}
func l5()  {}
func l6()  {}
func l7()  {}
func l8()  {}
func l9()  {}
func l10() {}

func deep1() { deep2() }
func deep2() { deep3() }
func deep3() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "root", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	l1 := flowChild(rep.Root, "l1")
	deep1 := flowChild(l1, "deep1")
	if deep1 == nil || deep1.LeafReason != "depth" || deep1.ChildrenTotal != 1 || len(deep1.Children) != 0 {
		t.Errorf("deep1 must be a depth leaf with children_total 1: %+v", deep1)
	}
	if !rep.DepthTruncated {
		t.Errorf("depth_truncated must be set")
	}

	rep, err = svc.Flow(proj, FlowOptions{Symbol: "root", MaxNodes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Truncated || rep.StepsEmitted != 5 || flowCount(rep.Root) != 5 {
		t.Fatalf("max_nodes=5: truncated=%v emitted=%d tree=%d", rep.Truncated, rep.StepsEmitted, flowCount(rep.Root))
	}
	if rep.StepsTotal <= rep.StepsEmitted {
		t.Errorf("steps_total %d must exceed steps_emitted %d when trimmed", rep.StepsTotal, rep.StepsEmitted)
	}
	if rep.Root.ChildrenTotal != 10 || len(rep.Root.Children) != 4 || rep.Root.LeafReason != "max_nodes" {
		t.Errorf("root = children_total %d, children %d, leaf_reason %q; want 10/4/max_nodes", rep.Root.ChildrenTotal, len(rep.Root.Children), rep.Root.LeafReason)
	}
	// siblings stay visible in call order even though the first one has a deep subtree
	if got := flowSymbols(rep.Root); strings.Join(got, ",") != "l1,l2,l3,l4" {
		t.Errorf("children = %v", got)
	}
	if l1 := flowChild(rep.Root, "l1"); l1.LeafReason != "max_nodes" || l1.ChildrenTotal != 1 || len(l1.Children) != 0 {
		t.Errorf("l1 must be cut by the node budget: %+v", l1)
	}
}

func TestFlowExcludesTestCalleesUnlessAsked(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"x/x.go": `package x

func root() {
	real()
	fixture()
}

func real() {}
`,
		"x/x_test.go": `package x

func fixture() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if flowChild(rep.Root, "fixture") != nil {
		t.Errorf("test-file helpers are excluded by default: %v", flowSymbols(rep.Root))
	}
	noted := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "test callee") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("omitted test callees must be reported in notes: %v", rep.Notes)
	}
	rep, err = svc.Flow(proj, FlowOptions{Symbol: "root", IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if flowChild(rep.Root, "fixture") == nil {
		t.Errorf("include_tests must show the helper: %v", flowSymbols(rep.Root))
	}
}

func TestFlowValueReferencesAreLabelledAndFiltered(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"x/x.go": `package x

type cfg struct{ Path string }

func register(c cfg) {
	mount(handler)
	_ = c.Path
}

func mount(fn func()) {}

func handler() {}
`,
		"y/y.go": `package y

func Path() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "register"})
	if err != nil {
		t.Fatal(err)
	}
	h := flowChild(rep.Root, "handler")
	if h == nil || h.LeafReason != "reference" || len(h.Children) != 0 {
		t.Errorf("handler passed as a value must be a reference leaf: %v / %+v", flowSymbols(rep.Root), h)
	}
	if flowChild(rep.Root, "Path") != nil {
		t.Errorf("a field read c.Path must not surface the unrelated Path function: %v", flowSymbols(rep.Root))
	}
}

func TestFlowResolvesSelectorAndReportsAmbiguity(t *testing.T) {
	svc, proj := flowProj(t, flowAmbiguityFiles())

	amb, err := svc.Flow(proj, FlowOptions{Symbol: "Close"})
	if err != nil {
		t.Fatal(err)
	}
	if amb.Found || amb.Root != nil || len(amb.Candidates) != 4 {
		t.Fatalf("ambiguous symbol = found %v root %v candidates %d, want not found with 4 candidates", amb.Found, amb.Root, len(amb.Candidates))
	}
	if amb.Candidates[0].Selector == nil {
		t.Fatalf("candidates must carry selectors")
	}
	again, err := svc.Flow(proj, FlowOptions{Selector: amb.Candidates[0].Selector})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Found || again.Root.File != amb.Candidates[0].File {
		t.Errorf("selector flow = %+v", again.Root)
	}

	byFQN, err := svc.Flow(proj, FlowOptions{Symbol: "netx.Conn.Close"})
	if err != nil {
		t.Fatal(err)
	}
	if !byFQN.Found || byFQN.Root.FQN != "netx.Conn.Close" {
		t.Errorf("fqn flow = %+v", byFQN.Root)
	}

	missing, err := svc.Flow(proj, FlowOptions{Symbol: "NoSuchThing"})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Found || len(missing.Notes) == 0 {
		t.Errorf("missing symbol = %+v", missing)
	}
}

func TestFlowValidatesOptions(t *testing.T) {
	svc, proj := flowProj(t, flowAmbiguityFiles())
	cases := map[string]FlowOptions{
		"neither":        {},
		"both":           {Symbol: "Run", Selector: &SymbolSelector{File: "cmd/app/main.go", StartLine: 9}},
		"depth too big":  {Symbol: "Run", Depth: MaxFlowDepth + 1},
		"depth negative": {Symbol: "Run", Depth: -1},
		"nodes too big":  {Symbol: "Run", MaxNodes: MaxFlowMaxNodes + 1},
	}
	for name, opts := range cases {
		if _, err := svc.Flow(proj, opts); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if CodeOf(err) != CodeInvalidInput {
			t.Errorf("%s: code = %q, want invalid_input", name, CodeOf(err))
		}
	}
}

func TestFlowNotIndexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	rep, err := NewService(sess).Flow(t.TempDir(), FlowOptions{Symbol: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Found || rep.Root != nil || rep.CallGraph != CallGraphNone {
		t.Errorf("unindexed project = %+v", rep)
	}
}

func TestFlowUnreadableSourceFallsBackWithPartialError(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"x/x.go": `package x

func root() {
	zeta()
	alpha()
}

func alpha() {}
func zeta()  {}
`,
	})
	if err := os.Remove(filepath.Join(proj, "x/x.go")); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if got := flowSymbols(rep.Root); strings.Join(got, ",") != "alpha,zeta" {
		t.Errorf("without source the order falls back to name order: %v", got)
	}
	for _, c := range rep.Root.Children {
		if c.CallOrder != 0 {
			t.Errorf("call_order must be 0 when unknown, got %d for %s", c.CallOrder, c.Symbol)
		}
	}
	if len(rep.PartialErrors) == 0 {
		t.Errorf("an unreadable file must be recorded in partial_errors")
	}
	if !rep.Stale {
		t.Errorf("a deleted file means the index is stale")
	}
}

func TestFlowUsesImportsToTellInternalExternalAndAliasedPackages(t *testing.T) {
	svc, proj := flowProj(t, map[string]string{
		"a/a.go": `package a

import (
	"github.com/spf13/viper"

	bb "example.com/fl/b"
)

func setup() {
	v := viper.New()
	_ = v
	bb.New()
}
`,
		"b/b.go": `package b

func New() {}
`,
		"c/c.go": `package c

func New() {}
`,
	})
	rep, err := svc.Flow(proj, FlowOptions{Symbol: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Root.Children) != 1 || rep.Root.Children[0].FQN != "b.New" {
		t.Fatalf("only the aliased internal import can reach b.New (viper.New is external, c is never imported): %v", flowSymbols(rep.Root))
	}
	if rep.Root.Children[0].Alternatives != 1 || rep.Root.Children[0].Confidence != "candidate" {
		t.Errorf("b.New = %+v, want a candidate pick among 2 definitions", rep.Root.Children[0])
	}
}

func TestFlowTextHelpers(t *testing.T) {
	src := "foo(1) // bar(2)\n/* baz(3) */ qux(4)\ns := \"quux(5)\"; `corge(6)`\n"
	masked := flowMask(src, flowSyntaxFor("go"))
	for _, name := range []string{"bar", "baz", "quux", "corge"} {
		if strings.Contains(masked, name) {
			t.Errorf("%s should be masked: %q", name, masked)
		}
	}
	for _, name := range []string{"foo", "qux"} {
		if !strings.Contains(masked, name) {
			t.Errorf("%s should stay visible: %q", name, masked)
		}
	}
	if len(masked) != len(src) {
		t.Errorf("masking must preserve offsets")
	}

	sites := flowCallSites("x.Foo (1); Foo(2); xFoo(3); Foo4(4); a.b.Foo(5)", 0, "Foo", false)
	if len(sites) != 3 {
		t.Fatalf("sites = %+v, want 3 (xFoo and Foo4 are different identifiers)", sites)
	}
	if !sites[0].qualified || sites[0].qual != "x" || sites[1].qualified || sites[2].qual != "b" {
		t.Errorf("qualifiers = %+v", sites)
	}
	if got := flowCallSites("<Card x={1} />", 0, "Card", true); len(got) != 1 {
		t.Errorf("JSX usage must count: %+v", got)
	}

	if start := flowBodyStart("func (s *S) F(a interface{}) error {\n\tG()\n}", "go"); start == 0 || !strings.HasPrefix("func (s *S) F(a interface{}) error {\n\tG()\n}"[start:], "\n\tG()") {
		t.Errorf("body start must skip the declaration header, got %d", start)
	}

	docs := map[string]string{
		"Run does a thing. It also does more.":           "Run does a thing.",
		"// Run does a thing, e.g. this one. Then more.": "Run does a thing, e.g. this one.",
		"First line\nsecond line.\n\nSecond paragraph.":  "First line second line.",
		"   ":                       "",
		strings.Repeat("word ", 60): "",
		"Load resolves configuration with the precedence (low": "Load resolves configuration with the precedence (low",
	}
	for in, want := range docs {
		got := flowDoc(in, flowDocMax)
		if want == "" && in != "   " {
			if len([]rune(got)) > flowDocMax || !strings.HasSuffix(got, "…") {
				t.Errorf("long doc must be capped with an ellipsis: %q", got)
			}
			continue
		}
		if got != want {
			t.Errorf("flowDoc(%q) = %q, want %q", in, got, want)
		}
	}
}
