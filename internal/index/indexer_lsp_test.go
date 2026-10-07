package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
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
	ix := New(g, nil, nil, lspModeConfig())

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
	ix := New(g, nil, nil, lspModeConfig())
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
	// Without --precise, TS has only name-based CANDIDATE edges (tsscan: calls to
	// an imported binding resolved through the import) — never precise ones.
	g2, _ := newStores(t)
	pid2, _ := g2.UpsertProject("ts", dir, "typescript")
	ix2 := New(g2, nil, nil, lspModeConfig())
	defer ix2.Close()
	if _, err := ix2.IndexProject(context.Background(), pid2, "ts", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	c2, _ := g2.Callers(pid2, "callee")
	if len(c2) != 1 || c2[0].Symbol != "caller" {
		t.Errorf("name-based TS index should have exactly the caller→callee candidate, got %+v", c2)
	}
	if prov := callEdgeProvenance(t, g2, pid2, nodeIDBySymbol(t, g2, pid2, "caller"), nodeIDBySymbol(t, g2, pid2, "callee")); prov != graph.ProvName {
		t.Errorf("non-precise TS call edge provenance = %q, want %q", prov, graph.ProvName)
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
	ix := New(g, nil, nil, lspModeConfig())
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
	ix := New(g, nil, nil, lspModeConfig())
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
	ix := New(g, nil, nil, lspModeConfig())
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
	ix := New(g, nil, nil, lspModeConfig())
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
	ix := New(g, nil, nil, lspModeConfig())
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
	ix := New(g, nil, nil, lspModeConfig())
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
	if len(res.ServerIssues) != 1 || res.ServerIssues[0].Code != "lsp_not_found" {
		t.Errorf("server absent: expected ServerIssues lsp_not_found, got %+v", res.ServerIssues)
	}
	if res.ServerIssues[0].AgentFix == nil || len(res.ServerIssues[0].AgentFix.Steps) == 0 {
		t.Errorf("server absent: expected agent_fix steps, got %+v", res.ServerIssues[0].AgentFix)
	}
}

// TestDeadVersionManagerShimReportsStructuredIssue proves that a binary which
// exists on PATH (LookPath succeeds) but fails under the project cwd — the
// classic asdf/mise "No version is set for command" shim — is classified as
// lsp_version_manager_gap with stderr and pins, not collapsed to "install X".
func TestDeadVersionManagerShimReportsStructuredIssue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	binDir := t.TempDir()
	shim := filepath.Join(binDir, "fake-ts-ls")
	script := "#!/bin/sh\necho 'No version is set for command fake-ts-ls' >&2\necho 'Consider adding one of the following versions in your config file at .tool-versions' >&2\nexit 126\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	saved := lspsrc.DefaultServers
	t.Cleanup(func() { lspsrc.DefaultServers = saved })
	lspsrc.DefaultServers = []lspsrc.ServerSpec{{
		Cmd:   "fake-ts-ls",
		Args:  []string{"--stdio"},
		Langs: []lspsrc.LangBinding{{Lang: "typescript", LangID: "typescript"}},
	}}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tool-versions"), []byte("nodejs 24.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "svc.ts", "export function f() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, lspModeConfig())
	res, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.MissingServers["typescript"] == "" {
		t.Fatalf("expected MissingServers, got %v", res.MissingServers)
	}
	if len(res.ServerIssues) != 1 {
		t.Fatalf("ServerIssues = %+v", res.ServerIssues)
	}
	iss := res.ServerIssues[0]
	if iss.Code != "lsp_version_manager_gap" {
		t.Errorf("code = %q, want lsp_version_manager_gap; stderr=%q", iss.Code, iss.Stderr)
	}
	if iss.ResolvedPath == "" {
		t.Error("expected resolved_path of the dead shim")
	}
	if iss.ExitCode == nil || *iss.ExitCode != 126 {
		t.Errorf("exit_code = %v", iss.ExitCode)
	}
	if iss.VersionManager == nil || iss.VersionManager.Kind != "asdf" {
		t.Errorf("version_manager = %+v", iss.VersionManager)
	}
	if !strings.Contains(iss.Stderr, "No version is set") {
		t.Errorf("stderr should carry asdf message, got %q", iss.Stderr)
	}
}

// lspModeConfig selects the previous structural backend — language servers
// for TS/JS/Python symbols — which the tests in this file (and the hermetic
// fake-server tests) exercise. The default tree-sitter mode is covered by
// TestTreeSitter* below and internal/extract/sittersrc.
func lspModeConfig() config.IndexConfig {
	cfg := config.DefaultConfig().Index
	cfg.StructuralBackend = config.StructuralLSP
	return cfg
}

// TestTreeSitterIndexesTypeScriptWithoutServers pins the default backend's
// contract: TS/JS/Python index with no language server — under --no-lsp, and
// with every configured server missing — and a missing server is reported
// only when --precise needs it, scoped to the precise call graph.
func TestTreeSitterIndexesTypeScriptWithoutServers(t *testing.T) {
	saved := lspsrc.DefaultServers
	t.Cleanup(func() { lspsrc.DefaultServers = saved })
	lspsrc.DefaultServers = []lspsrc.ServerSpec{{
		Cmd:   "codemap-no-such-language-server",
		Args:  []string{"--stdio"},
		Langs: []lspsrc.LangBinding{{Lang: "typescript", LangID: "typescript"}},
	}}

	for _, opts := range []Options{{NoLSP: true}, {}} {
		dir := t.TempDir()
		writeFile(t, dir, "svc.ts", "export function f() {}\nexport class Svc { run() { f(); } }\n")
		g, _ := newStores(t)
		pid, _ := g.UpsertProject("ts", dir, "typescript")
		ix := New(g, nil, nil, config.DefaultConfig().Index)
		res, err := ix.IndexProject(context.Background(), pid, "ts", dir, opts)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := g.FindNodesBySymbol(pid, "run"); len(n) != 1 || n[0].Kind != graph.KindMethod {
			t.Errorf("opts %+v: Svc.run not indexed by tree-sitter: %+v", opts, n)
		}
		if len(res.Unsupported) != 0 || len(res.ServerIssues) != 0 || len(res.MissingServers) != 0 {
			t.Errorf("opts %+v: no server should be needed, got unsupported=%v issues=%+v", opts, res.Unsupported, res.ServerIssues)
		}
	}

	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", "export function f() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := g.FindNodesBySymbol(pid, "f"); len(n) != 1 {
		t.Errorf("--precise without a server must still index symbols, got %+v", n)
	}
	if len(res.ServerIssues) != 1 || res.ServerIssues[0].Capability != tooling.CapabilityPrecise {
		t.Fatalf("--precise without a server should report one precise-scoped issue, got %+v", res.ServerIssues)
	}
	if line := tooling.WarningLine(res.ServerIssues[0]); !strings.Contains(line, "precise call graph unavailable") || strings.Contains(line, "skipped") {
		t.Errorf("warning should scope the gap to --precise, got %q", line)
	}
}

// TestTreeSitterPythonCallCandidates indexes a small package with no language
// server and proves the base graph now carries Python call candidates through
// same-file calls, self-methods, and imports (relative, absolute, src/
// layout), plus file→file import edges — none of which existed before.
func TestTreeSitterPythonCallCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/shop/__init__.py", "")
	writeFile(t, dir, "src/shop/pricing.py", "def price(item):\n    return discount(item)\n\n\ndef discount(item):\n    return item\n")
	writeFile(t, dir, "src/shop/cart.py", strings.Join([]string{
		"from .pricing import price",
		"import shop.pricing as pr",
		"",
		"",
		"class Cart:",
		"    def total(self):",
		"        return self.sum_items()",
		"",
		"    def sum_items(self):",
		"        return price(1) + pr.discount(2)",
		"",
	}, "\n"))
	writeFile(t, dir, "tests/test_cart.py", "from shop.cart import Cart\n\n\ndef test_total():\n    Cart().total()\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("py", dir, "python")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "py", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	callers := func(fqn string) []string {
		t.Helper()
		nodes, _ := g.FindNodesBySymbol(pid, fqn[strings.LastIndex(fqn, ".")+1:])
		var out []string
		for _, n := range nodes {
			if n.FQN != fqn {
				continue
			}
			cs, err := g.CallersOfNode(pid, n.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cs {
				out = append(out, c.FQN)
			}
		}
		sort.Strings(out)
		return out
	}
	for fqn, want := range map[string]string{
		"discount":       "Cart.sum_items,price", // same-file + `import … as pr` module call
		"price":          "Cart.sum_items",       // `from .pricing import price`
		"Cart.sum_items": "Cart.total",           // self.method()
		"Cart.total":     "test_total",           // Cart().total() is obj.method(): unlinked…
		"Cart":           "test_total",           // …but Cart() links through the src/ import
	} {
		got := strings.Join(callers(fqn), ",")
		if fqn == "Cart.total" {
			if got != "" {
				t.Errorf("Cart().total() needs types; want no candidate, got %q", got)
			}
			continue
		}
		if got != want {
			t.Errorf("callers(%s) = %q, want %q", fqn, got, want)
		}
	}
	nodes, _ := g.ProjectNodes(pid)
	byID := map[int64]graph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	edges, _ := g.ProjectEdges(pid)
	var imports []string
	for _, e := range edges {
		if e.EdgeType == graph.EdgeImports && byID[e.SourceID].FilePath == "src/shop/cart.py" {
			imports = append(imports, byID[e.TargetID].FilePath)
		}
	}
	if strings.Join(imports, ",") != "src/shop/pricing.py" {
		t.Errorf("cart.py imports = %v, want [src/shop/pricing.py]", imports)
	}
}

// TestTreeSitterInheritanceEdges pins declared inheritance: TS/JS extends and
// implements (through relative, type-only, and same-file bindings) and Python
// bases (through imports), plus the derived method overrides — while a base
// from a package (react) yields no edge.
func TestTreeSitterInheritanceEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/repo.ts", "export interface Repo {\n  save(): void;\n  load(): void;\n}\n")
	writeFile(t, dir, "src/base.ts", "export class Base {\n  save() {}\n  describe() {}\n}\n")
	writeFile(t, dir, "src/user.ts", strings.Join([]string{
		`import { Base } from "./base";`,
		`import type { Repo } from "./repo";`,
		`import React from "react";`,
		``,
		`export class UserRepo extends Base implements Repo {`,
		`  save() {}`,
		`  load() {}`,
		`  constructor() { super(); }`,
		`}`,
		`class Local {}`,
		`class Widget extends React.Component {}`,
		`class Special extends Local {}`,
		``,
	}, "\n"))
	writeFile(t, dir, "py/animals.py", "class Animal:\n    def speak(self):\n        pass\n")
	writeFile(t, dir, "py/dogs.py", "from .animals import Animal\n\n\nclass Dog(Animal):\n    def speak(self):\n        return 1\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("inh", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "inh", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := g.ProjectNodes(pid)
	byID := map[int64]graph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	edges, _ := g.ProjectEdges(pid)
	var got []string
	for _, e := range edges {
		switch e.EdgeType {
		case graph.EdgeExtends, graph.EdgeImplements, graph.EdgeOverrides:
			s, d := byID[e.SourceID], byID[e.TargetID]
			got = append(got, e.EdgeType+" "+s.FilePath+":"+s.FQN+" -> "+d.FilePath+":"+d.FQN)
		}
	}
	sort.Strings(got)
	want := []string{
		"extends py/dogs.py:Dog -> py/animals.py:Animal",
		"extends src/user.ts:Special -> src/user.ts:Local",
		"extends src/user.ts:UserRepo -> src/base.ts:Base",
		"implements src/user.ts:UserRepo -> src/repo.ts:Repo",
		"overrides py/dogs.py:Dog.speak -> py/animals.py:Animal.speak",
		"overrides src/user.ts:UserRepo.load -> src/repo.ts:Repo.load",
		"overrides src/user.ts:UserRepo.save -> src/base.ts:Base.save",
		"overrides src/user.ts:UserRepo.save -> src/repo.ts:Repo.save",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("inheritance edges:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Re-deriving after an edit keeps overrides consistent (no duplicates,
	// removed methods drop out).
	writeFile(t, dir, "src/user.ts", strings.Replace(mustRead(t, dir, "src/user.ts"), "  load() {}\n", "", 1))
	if _, err := ix.IndexProject(context.Background(), pid, "inh", dir, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}
	n, _ := g.CountEdgesByType(pid, graph.EdgeOverrides)
	if n != 3 {
		t.Errorf("overrides after removing load() = %d, want 3", n)
	}
}

func mustRead(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
