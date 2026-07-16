package index

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// TestIndexTypeScriptSymbols proves the LSP backend indexes TypeScript into the
// same node/edge model Go uses. Server-gated: it self-skips where
// typescript-language-server isn't on PATH (e.g. CI), and runs locally.
func TestIndexTypeScriptSymbols(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", `export class UserService {
  getUser(id: string) { return id; }
}

export function makeService() { return new UserService(); }
`)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes == 0 {
		t.Fatal("expected TypeScript nodes, got 0 (is typescript resolvable in this env?)")
	}

	kindOf := func(symbol, fqn string) string {
		nodes, err := g.FindNodesBySymbol(pid, symbol)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if n.FQN == fqn {
				return n.Kind
			}
		}
		return "(absent)"
	}
	if k := kindOf("UserService", "UserService"); k != graph.KindClass {
		t.Errorf("UserService kind = %q, want class", k)
	}
	if k := kindOf("getUser", "UserService.getUser"); k != graph.KindMethod {
		t.Errorf("getUser kind = %q, want method (nested FQN)", k)
	}
	if k := kindOf("makeService", "makeService"); k != graph.KindFunction {
		t.Errorf("makeService kind = %q, want function", k)
	}

	// A `defines` edge file -> class lands in Pass 1, so structure browsing works.
	nodes, _ := g.FindNodesBySymbol(pid, "UserService")
	if len(nodes) == 0 {
		t.Fatal("UserService node missing")
	}
	var in int
	_ = g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='defines'", nodes[0].ID).Scan(&in)
	if in == 0 {
		t.Error("expected a defines edge into the UserService node")
	}
}

// TestIndexTypeScriptCallEdges proves --precise adds exact TS call edges via
// callHierarchy, so callers/impact work for TypeScript. Server-gated.
func TestIndexTypeScriptCallEdges(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "callee.ts", "export function callee() { return 1; }\n")
	writeFile(t, dir, "caller.ts", "import { callee } from \"./callee\";\n\nexport function caller() { return callee(); }\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatal("expected precise TS call edges, got 0 (callHierarchy join failed?)")
	}
	callers, err := g.Callers(pid, "callee")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range callers {
		if c.Symbol == "caller" {
			found = true
		}
	}
	if !found {
		t.Errorf("callers of callee should include caller (precise TS edge), got %+v", callers)
	}
	// Without --precise, TS has no call edges (callHierarchy is the only source).
	g2, _ := newStores(t)
	pid2, _ := g2.UpsertProject("ts", dir, "typescript")
	ix2 := New(g2, nil, nil, config.DefaultConfig().Index)
	defer ix2.Close()
	if _, err := ix2.IndexProject(context.Background(), pid2, "ts", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if c, _ := g2.Callers(pid2, "callee"); len(c) != 0 {
		t.Errorf("name-based TS index should have no call edges, got %+v", c)
	}
}

// TestIndexTSXCallEdges proves JSX is resolved: a .tsx component rendering
// another (<Button/>) becomes a call edge under --precise — which only works
// because the file is opened with the typescriptreact languageId. Server-gated.
func TestIndexTSXCallEdges(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "App.tsx", "export function Button(props: { label: string }) {\n  return <button>{props.label}</button>;\n}\nexport function App() {\n  return <Button label=\"hi\" />;\n}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("tsx", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "tsx", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}
	callers, err := g.Callers(pid, "Button")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range callers {
		if c.Symbol == "App" {
			found = true
		}
	}
	if !found {
		t.Errorf("callers of Button should include App (JSX <Button/> usage), got %+v", callers)
	}
}

// TestIndexJSXNameBasedEdges proves React composition is visible WITHOUT
// --precise: JSX component usage (<Button/>) becomes a name-based call edge,
// Next.js convention files get framework-wiring references (so orphans stops
// flagging pages), and .jsx files index at all. Server-gated (documentSymbol
// still comes from typescript-language-server), but no callHierarchy runs.
func TestIndexJSXNameBasedEdges(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "components/button.tsx", "export function Button(props: { label: string }) {\n  return <button>{props.label}</button>;\n}\n")
	writeFile(t, dir, "app/page.tsx", "import { Button } from \"../components/button\";\nexport default function HomePage() {\n  return <main><Button label=\"hi\" /></main>;\n}\n")
	writeFile(t, dir, "components/legacy.jsx", "export function Legacy() {\n  return <Button label=\"old\" />;\n}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("jsx-name", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "jsx-name", dir, Options{}) // NOT precise
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["javascript"] == 0 {
		t.Errorf(".jsx file not indexed: languages = %v", res.Languages)
	}

	// Name-based JSX call edges: HomePage (.tsx) and Legacy (.jsx) → Button.
	callers, err := g.Callers(pid, "Button")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range callers {
		got[c.Symbol] = true
	}
	if !got["HomePage"] || !got["Legacy"] {
		t.Errorf("callers of Button = %v, want HomePage (.tsx) and Legacy (.jsx)", got)
	}
	// Intrinsic elements never create edges.
	if c, _ := g.Callers(pid, "button"); len(c) != 0 {
		t.Errorf("<button> intrinsic must not have callers: %+v", c)
	}
	if c, _ := g.Callers(pid, "main"); len(c) != 0 {
		t.Errorf("<main> intrinsic must not have callers: %+v", c)
	}

	// Framework wiring: app/page.tsx's default export is referenced by its
	// file node, so orphan detection keeps it.
	orphans, err := g.Orphans(pid, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range orphans {
		if n.Symbol == "HomePage" {
			t.Errorf("HomePage (Next.js page default export) flagged as orphan")
		}
		if n.Symbol == "Button" {
			t.Errorf("Button (JSX-used component) flagged as orphan")
		}
	}

	// Relative import persisted as a file→file imports edge.
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	fileID := map[string]int64{}
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			fileID[n.FilePath] = n.ID
		}
	}
	var importFound bool
	for _, e := range edges {
		if e.EdgeType == graph.EdgeImports &&
			e.SourceID == fileID["app/page.tsx"] && e.TargetID == fileID["components/button.tsx"] {
			importFound = true
		}
	}
	if !importFound {
		t.Errorf("missing imports edge app/page.tsx → components/button.tsx")
	}
}

// TestIndexJavaScriptMixed proves one typescript-language-server serves BOTH
// TypeScript and JavaScript: a mixed project indexes .js and .ts together, and
// --precise resolves call edges across the language boundary (a .ts function
// calling a .js function). Server-gated.
func TestIndexJavaScriptMixed(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "math.js", "export function add(a, b) { return a + b; }\n")
	writeFile(t, dir, "app.ts", "import { add } from \"./math.js\";\n\nexport function compute(x: number): number { return add(x, 1); }\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("mix", dir, "javascript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "mix", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	// Both languages were indexed by the single shared server.
	if res.Languages["javascript"] == 0 || res.Languages["typescript"] == 0 {
		t.Errorf("expected both JS and TS indexed, got languages %v", res.Languages)
	}
	// The JS function is a real node.
	if ns, _ := g.FindNodesBySymbol(pid, "add"); len(ns) == 0 {
		t.Fatal("JavaScript function add was not indexed")
	}
	// Cross-language precise call edge: compute (.ts) -> add (.js).
	callers, err := g.Callers(pid, "add")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range callers {
		if c.Symbol == "compute" {
			found = true
		}
	}
	if !found {
		t.Errorf("callers of add (JS) should include compute (TS) — cross-language precise edge, got %+v", callers)
	}
}

// TestIndexPython proves pyright-langserver indexes Python into the same node/
// edge model: functions become nodes (no parameter-variable noise) and --precise
// resolves the call graph via callHierarchy. Server-gated.
func TestIndexPython(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		t.Skip("pyright-langserver not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "calc.py", "def add(a, b):\n    return a + b\n\ndef compute(x):\n    return add(x, 1)\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("py", dir, "python")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "py", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["python"] == 0 {
		t.Errorf("expected Python indexed, got languages %v", res.Languages)
	}
	// Functions are nodes; parameters (a, b, x) are not.
	if ns, _ := g.FindNodesBySymbol(pid, "add"); len(ns) == 0 {
		t.Fatal("Python function add was not indexed")
	}
	if ns, _ := g.FindNodesBySymbol(pid, "a"); len(ns) != 0 {
		t.Error("function parameter 'a' should not be a graph node")
	}
	// Precise call edge: compute -> add.
	callers, err := g.Callers(pid, "add")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range callers {
		if c.Symbol == "compute" {
			found = true
		}
	}
	if !found {
		t.Errorf("callers of add should include compute (precise Python edge), got %+v", callers)
	}
}

// TestIndexTypeScriptDisabledByNoLSP confirms --no-lsp keeps TS unindexed
// regardless of installed servers (deterministic, never spawns a server).
func TestIndexTypeScriptDisabledByNoLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", "export function f() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("NoLSP should index no TS nodes, got %d", res.Nodes)
	}
	if res.Unsupported["typescript"] != 1 {
		t.Errorf("expected 1 unsupported typescript file under NoLSP, got %v", res.Unsupported)
	}
}

// TestMissingServerReportedNotSilent proves that a project with supported
// LSP-language files but no server binary reports MissingServers (which drives the
// actionable "install typescript-language-server …" warning) instead of silently
// producing an empty graph. Deterministic regardless of what's installed locally:
// it points the server spec at a binary that cannot resolve on PATH, so the
// missing-server branch is taken even on a dev machine that has the real server.
func TestMissingServerReportedNotSilent(t *testing.T) {
	saved := lspsrc.DefaultServers
	t.Cleanup(func() { lspsrc.DefaultServers = saved })
	lspsrc.DefaultServers = []lspsrc.ServerSpec{{
		Cmd:   "codemap-no-such-language-server",
		Args:  []string{"--stdio"},
		Langs: []lspsrc.LangBinding{{Lang: "typescript", LangID: "typescript"}},
	}}

	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", "export function f() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{}) // LSP enabled
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("server absent: expected no TS nodes indexed, got %d", res.Nodes)
	}
	if res.MissingServers["typescript"] == "" {
		t.Errorf("server absent: expected MissingServers[typescript] to be set, got %v", res.MissingServers)
	}
}
