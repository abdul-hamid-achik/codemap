package lspsrc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

type stubLanguageClient struct {
	documentSymbols bool
	callHierarchy   bool
	syms            []lsp.DocumentSymbol
	prepareItems    []lsp.CallHierarchyItem
	prepareErr      error
	prepareFn       func(lsp.Position) ([]lsp.CallHierarchyItem, error)
	prepared        []lsp.Position
	outgoingFn      func(lsp.CallHierarchyItem) ([]lsp.CallHierarchyOutgoingCall, error)
	outgoingErr     error
	opened          []string
	closed          []string
}

func (s *stubLanguageClient) SupportsDocumentSymbols() bool { return s.documentSymbols }
func (s *stubLanguageClient) SupportsCallHierarchy() bool   { return s.callHierarchy }
func (s *stubLanguageClient) DidOpen(uri, _, _ string) error {
	s.opened = append(s.opened, uri)
	return nil
}
func (s *stubLanguageClient) DidClose(uri string) error {
	s.closed = append(s.closed, uri)
	return nil
}
func (s *stubLanguageClient) DocumentSymbols(context.Context, string) ([]lsp.DocumentSymbol, error) {
	return s.syms, nil
}

func (s *stubLanguageClient) PrepareCallHierarchy(_ context.Context, _ string, pos lsp.Position) ([]lsp.CallHierarchyItem, error) {
	s.prepared = append(s.prepared, pos)
	if s.prepareFn != nil {
		return s.prepareFn(pos)
	}
	return s.prepareItems, s.prepareErr
}
func (s *stubLanguageClient) OutgoingCalls(_ context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyOutgoingCall, error) {
	if s.outgoingFn != nil {
		return s.outgoingFn(item)
	}
	return nil, s.outgoingErr
}
func (s *stubLanguageClient) Shutdown(context.Context) error { return nil }
func (s *stubLanguageClient) Exit() error                    { return nil }

func TestMapKind(t *testing.T) {
	ds := func(kind int, detail string) lsp.DocumentSymbol {
		return lsp.DocumentSymbol{Kind: kind, Detail: detail}
	}
	cases := []struct {
		name string
		sym  lsp.DocumentSymbol
		want string
	}{
		{"function", ds(lsp.SymbolFunction, ""), extract.KindFunction},
		{"method", ds(lsp.SymbolMethod, ""), extract.KindMethod},
		{"constructor", ds(lsp.SymbolConstructor, ""), extract.KindMethod},
		{"class", ds(lsp.SymbolClass, ""), extract.KindClass},
		{"interface", ds(lsp.SymbolInterface, ""), extract.KindType},
		{"enum", ds(lsp.SymbolEnum, ""), extract.KindType},
		{"struct", ds(lsp.SymbolStruct, ""), extract.KindType},
		{"namespace", ds(lsp.SymbolNamespace, ""), extract.KindModule},
		{"module", ds(lsp.SymbolModule, ""), extract.KindModule},
		{"arrow-fn const", ds(lsp.SymbolConstant, "() => void"), extract.KindFunction},
		{"plain var", ds(lsp.SymbolVariable, "number"), extract.KindVariable},
		{"untracked (property)", ds(7, ""), ""},
	}
	for _, c := range cases {
		if got := mapKind(c.sym); got != c.want {
			t.Errorf("mapKind(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAppendSymbolsNesting(t *testing.T) {
	res := &extract.FileResult{Path: "a.ts", Language: "typescript"}
	lines := []string{"class Foo {", "  bar() {}", "}"}
	sym := lsp.DocumentSymbol{
		Name: "Foo", Kind: lsp.SymbolClass,
		Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 2}},
		Children: []lsp.DocumentSymbol{
			{Name: "bar", Kind: lsp.SymbolMethod, Range: lsp.Range{Start: lsp.Position{Line: 1}, End: lsp.Position{Line: 1}}},
		},
	}
	appendSymbols(res, lines, "typescript", "", false, "a.ts", sym)

	if len(res.Symbols) != 2 {
		t.Fatalf("symbols = %d, want 2", len(res.Symbols))
	}
	by := map[string]extract.Symbol{}
	for _, s := range res.Symbols {
		by[s.FQN] = s
	}
	if by["Foo"].Kind != extract.KindClass {
		t.Errorf("Foo kind = %q, want class", by["Foo"].Kind)
	}
	if by["Foo.bar"].Kind != extract.KindMethod {
		t.Errorf("Foo.bar kind = %q, want method (nested FQN)", by["Foo.bar"].Kind)
	}
	if by["Foo.bar"].StartLine != 2 {
		t.Errorf("Foo.bar StartLine = %d, want 2 (1-based)", by["Foo.bar"].StartLine)
	}
}

func TestIsAnonymousCallable(t *testing.T) {
	for _, n := range []string{"", "<function>", "<anonymous>", "<lambda>",
		"map() callback", "defineEventHandler() callback", "xs.filter() callback"} {
		if !isAnonymousCallable(n) {
			t.Errorf("%q should be an anonymous callable", n)
		}
	}
	for _, n := range []string{"namedFunc", "handler", "defineEventHandler",
		"Foo.Bar", "useAuth", "callback", "myCallback", "onCallback"} {
		if isAnonymousCallable(n) {
			t.Errorf("%q is a real identifier and must NOT be treated as anonymous", n)
		}
	}
}

// TestAppendSymbolsSkipsAnonymousCallbacks verifies inline anonymous functions the
// language server names after their call site ("map() callback") or "<function>"
// are NOT indexed (they drown callback-heavy code in noise), while real
// declarations are kept and a real nested symbol is reparented to the enclosing
// scope rather than the junk callback name.
func TestAppendSymbolsSkipsAnonymousCallbacks(t *testing.T) {
	at := func(name string, kind, line int, kids ...lsp.DocumentSymbol) lsp.DocumentSymbol {
		return lsp.DocumentSymbol{Name: name, Kind: kind,
			Range:    lsp.Range{Start: lsp.Position{Line: line}, End: lsp.Position{Line: line}},
			Children: kids}
	}
	res := &extract.FileResult{Path: "a.ts", Language: "typescript"}
	roots := []lsp.DocumentSymbol{
		at("namedFunc", lsp.SymbolFunction, 0,
			at("map() callback", lsp.SymbolFunction, 1),
			at("filter() callback", lsp.SymbolFunction, 1)),
		// handler is a real module-level const; its inline arrow is anonymous, but a
		// genuinely-named helper nested under that arrow must survive (reparented).
		at("handler", lsp.SymbolVariable, 3,
			at("defineEventHandler() callback", lsp.SymbolFunction, 3,
				at("realHelper", lsp.SymbolFunction, 4))),
		at("<function>", lsp.SymbolFunction, 6),
	}
	for _, s := range roots {
		appendSymbols(res, nil, "typescript", "", false, "a.ts", s)
	}
	got := map[string]bool{}
	for _, s := range res.Symbols {
		got[s.FQN] = true
		if isAnonymousCallable(s.Name) {
			t.Errorf("anonymous callable %q was indexed but should be skipped", s.Name)
		}
	}
	for _, want := range []string{"namedFunc", "handler", "handler.realHelper"} {
		if !got[want] {
			t.Errorf("expected real symbol %q, got %v", want, got)
		}
	}
	if got["handler.defineEventHandler() callback.realHelper"] {
		t.Error("realHelper should be reparented to handler, not the junk callback FQN")
	}
}

func TestCallEdgesUsesExactlyTheIndexedCallableSet(t *testing.T) {
	at := func(name string, kind, line int, detail string, kids ...lsp.DocumentSymbol) lsp.DocumentSymbol {
		pos := lsp.Position{Line: line}
		return lsp.DocumentSymbol{
			Name: name, Kind: kind, Detail: detail,
			Range: lsp.Range{Start: pos, End: pos}, SelectionRange: lsp.Range{Start: pos, End: pos},
			Children: kids,
		}
	}
	callback := at("map() callback", lsp.SymbolFunction, 1, "")
	syms := []lsp.DocumentSymbol{
		at("handler", lsp.SymbolConstant, 0, "() => number", callback),
		at("helper", lsp.SymbolFunction, 3, ""),
	}
	client := &stubLanguageClient{
		documentSymbols: true,
		callHierarchy:   true,
		syms:            syms,
		prepareFn: func(pos lsp.Position) ([]lsp.CallHierarchyItem, error) {
			if pos.Line == 1 {
				return nil, errors.New("anonymous callback must not be queried")
			}
			name := "helper"
			if pos.Line == 0 {
				name = "handler"
			}
			return []lsp.CallHierarchyItem{{Name: name}}, nil
		},
		outgoingFn: func(item lsp.CallHierarchyItem) ([]lsp.CallHierarchyOutgoingCall, error) {
			if item.Name != "handler" {
				return nil, nil
			}
			return []lsp.CallHierarchyOutgoingCall{{To: lsp.CallHierarchyItem{
				Name: "helper", URI: "file:///project/src/app.ts",
				Range: lsp.Range{Start: lsp.Position{Line: 3}, End: lsp.Position{Line: 3}},
			}}}, nil
		},
	}
	e := &Extractor{
		ctx: context.Background(), lang: "typescript", langID: "typescript",
		root: "/project", client: client,
	}

	res, err := e.ExtractFile("src/app.ts", []byte("const handler = () => 1\n// callback\n\nfunction helper() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	indexed := map[string]string{}
	for _, sym := range res.Symbols {
		indexed[sym.FQN] = sym.Kind
	}
	if indexed["handler"] != extract.KindFunction || indexed["helper"] != extract.KindFunction {
		t.Fatalf("indexed callables = %v, want handler arrow + helper", indexed)
	}
	if indexed["handler.map() callback"] != "" {
		t.Fatalf("anonymous callback was indexed: %v", indexed)
	}

	edges, err := e.CallEdges(context.Background(), "src/app.ts")
	if err != nil {
		t.Fatalf("CallEdges queried a non-indexed callback or missed an indexed arrow: %v", err)
	}
	if len(client.prepared) != 2 || client.prepared[0].Line != 0 || client.prepared[1].Line != 3 {
		t.Fatalf("prepared positions = %+v, want exactly indexed callables at lines 0 and 3", client.prepared)
	}
	if len(edges) != 1 || edges[0].FromFQN != "handler" || edges[0].FromFile != "src/app.ts" || edges[0].FromLine != 1 || edges[0].ToFile != "src/app.ts" || edges[0].ToLine != 4 || edges[0].External {
		t.Fatalf("arrow-function call edge = %+v, want handler:1 -> helper:4", edges)
	}
}

// TestAppendSymbolsSkipsParams verifies a function's nested Variable children
// (parameters/locals, as pyright reports them) are dropped, while the function
// and a module-level variable are kept — so the graph isn't cluttered with param
// nodes like add.a / add.b.
func TestAppendSymbolsSkipsParams(t *testing.T) {
	res := &extract.FileResult{Path: "m.py", Language: "python"}
	lines := []string{"def add(a, b):", "    return a + b"}
	fn := lsp.DocumentSymbol{
		Name: "add", Kind: lsp.SymbolFunction,
		Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 1}},
		Children: []lsp.DocumentSymbol{
			{Name: "a", Kind: lsp.SymbolVariable, Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0}}},
			{Name: "b", Kind: lsp.SymbolVariable, Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0}}},
		},
	}
	modVar := lsp.DocumentSymbol{
		Name: "CONFIG", Kind: lsp.SymbolVariable,
		Range: lsp.Range{Start: lsp.Position{Line: 2}, End: lsp.Position{Line: 2}},
	}
	appendSymbols(res, lines, "python", "", false, "a.py", fn)
	appendSymbols(res, lines, "python", "", false, "a.py", modVar)

	by := map[string]string{}
	for _, s := range res.Symbols {
		by[s.FQN] = s.Kind
	}
	if by["add"] != extract.KindFunction {
		t.Errorf("add should be a function, got %q", by["add"])
	}
	if _, ok := by["add.a"]; ok {
		t.Error("function parameter add.a must be dropped, not a node")
	}
	if _, ok := by["add.b"]; ok {
		t.Error("function parameter add.b must be dropped, not a node")
	}
	if by["CONFIG"] != extract.KindVariable {
		t.Errorf("module-level variable CONFIG should be kept, got %q", by["CONFIG"])
	}
}

// TestBind verifies a bound extractor takes a new language/languageId while
// sharing the original's root/client and is marked shared (so it won't close the
// server it doesn't own) — the mechanism that lets one typescript-language-server
// serve both TypeScript and JavaScript.
func TestBind(t *testing.T) {
	ts := &Extractor{lang: "typescript", langID: "typescript", root: "/proj"}
	js := ts.Bind("javascript", "javascript")
	if js.Language() != "javascript" || js.langID != "javascript" {
		t.Errorf("Bind lang/langID = %q/%q, want javascript/javascript", js.Language(), js.langID)
	}
	if js.root != ts.root {
		t.Errorf("bound extractor root = %q, want shared %q", js.root, ts.root)
	}
	if !js.shared {
		t.Error("bound extractor must be marked shared (it doesn't own the server)")
	}
	if err := js.Close(); err != nil {
		t.Errorf("a shared extractor's Close should be a no-op, got %v", err)
	}
}

// TestRelOf verifies relOf percent-decodes the callee URI before computing the
// root-relative path (P1-02 residual): a raw TrimPrefix(uri, "file://") left
// "%20" in the path, which never matched a project root containing a space and
// silently marked every such callee External, dropping the precise call edge.
func TestRelOf(t *testing.T) {
	root := "/Users/dev/My Project"
	e := &Extractor{root: root}

	t.Run("percent-encoded path inside root resolves and is not external", func(t *testing.T) {
		uri := "file:///Users/dev/My%20Project/src/util%20file.ts"
		rel, external := e.relOf(uri)
		if external {
			t.Fatalf("relOf(%q) external = true, want false", uri)
		}
		want := filepath.Join("src", "util file.ts")
		if rel != want {
			t.Errorf("relOf(%q) = %q, want %q", uri, rel, want)
		}
	})

	t.Run("path outside root is external", func(t *testing.T) {
		uri := "file:///Users/dev/Other%20Project/dep.ts"
		if _, external := e.relOf(uri); !external {
			t.Errorf("relOf(%q) external = false, want true (outside root)", uri)
		}
	})
}

// TestWrapExtractErr verifies a request timeout becomes an actionable message
// (not a bare "context deadline exceeded"), while other errors pass through.
func TestWrapExtractErr(t *testing.T) {
	got := wrapExtractErr("typescript", "src/App.tsx", context.DeadlineExceeded).Error()
	if !strings.Contains(got, "timed out") || !strings.Contains(got, "App.tsx") || !strings.Contains(got, "typescript") {
		t.Errorf("timeout error should name the language/file and say timed out, got %q", got)
	}
	other := errors.New("some other failure")
	if wrapExtractErr("go", "a.go", other) != other {
		t.Error("a non-timeout error should pass through unchanged")
	}
}

// TestHasDeclarations pins the heuristic that decides whether an empty
// documentSymbol is worth retrying (a parse race on a file that should yield
// symbols) versus accepting immediately (a re-export / import-only file).
func TestHasDeclarations(t *testing.T) {
	yes := []string{
		"export function getTableImportColumns() {}",
		"export const sort = () => {}",
		"class Foo {}",
		"interface Bar {}",
		"type T = number",
		"def add(a, b):\n    return a + b", // Python
	}
	no := []string{
		"export { x } from './y'",
		"import x from 'y'\nexport default x",
		"// just a comment\n",
		"",
	}
	for _, s := range yes {
		if !hasDeclarations([]byte(s)) {
			t.Errorf("hasDeclarations should be true for %q", s)
		}
	}
	for _, s := range no {
		if hasDeclarations([]byte(s)) {
			t.Errorf("hasDeclarations should be false for %q", s)
		}
	}
}

// TestLSPLanguageID pins the per-extension languageId override: .tsx/.jsx must
// map to the *react ids (so tsserver parses JSX and resolves <Component/> calls),
// while other files keep the language's default.
func TestLSPLanguageID(t *testing.T) {
	cases := []struct{ path, fallback, want string }{
		{"src/App.tsx", "typescript", "typescriptreact"},
		{"src/app.jsx", "javascript", "javascriptreact"},
		{"app.ts", "typescript", "typescript"},
		{"x.js", "javascript", "javascript"},
		{"m.py", "python", "python"},
	}
	for _, c := range cases {
		if got := lspLanguageID(c.path, c.fallback); got != c.want {
			t.Errorf("lspLanguageID(%q, %q) = %q, want %q", c.path, c.fallback, got, c.want)
		}
	}
}

// TestExtractFileEnrichesTSX pins the name-based enrichment layer: a .tsx
// extraction must carry import specifiers, JSX component-usage call
// references attributed to the enclosing component, and framework-wiring
// references for Next.js convention files — with no language server beyond
// documentSymbol involved.
func TestExtractFileEnrichesTSX(t *testing.T) {
	src := "import Hero from './hero';\n" +
		"export default function Page() {\n" +
		"  return <main><Hero title=\"x\" /></main>;\n" +
		"}\n"
	stub := &stubLanguageClient{documentSymbols: true, syms: []lsp.DocumentSymbol{{
		Name: "Page", Kind: lsp.SymbolFunction,
		Range: lsp.Range{Start: lsp.Position{Line: 1}, End: lsp.Position{Line: 3}},
	}}}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: "/proj", client: stub}
	res, err := e.ExtractFile("app/page.tsx", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imports) != 1 || res.Imports[0] != "./hero" {
		t.Errorf("imports = %v, want [./hero]", res.Imports)
	}
	var jsx, wired, intrinsic bool
	for _, r := range res.References {
		if r.From == "Page" && r.To == "Hero" && r.Kind == extract.RefCalls {
			jsx = true
		}
		if r.From == "app/page.tsx" && r.To == "Page" && r.Kind == extract.RefReferences {
			wired = true
		}
		if r.To == "main" {
			intrinsic = true
		}
	}
	if !jsx {
		t.Errorf("missing JSX call reference Page → Hero: %v", res.References)
	}
	if !wired {
		t.Errorf("missing framework wiring reference file → Page: %v", res.References)
	}
	if intrinsic {
		t.Errorf("intrinsic <main> must not produce a reference: %v", res.References)
	}
}

func TestExtractFileClosesDocument(t *testing.T) {
	stub := &stubLanguageClient{
		documentSymbols: true,
		syms: []lsp.DocumentSymbol{{
			Name: "Page", Kind: lsp.SymbolFunction,
			Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0}},
		}},
	}
	e := &Extractor{
		ctx: context.Background(), lang: "typescript", langID: "typescript",
		root: "/proj", client: stub,
	}
	if _, err := e.ExtractFile("app/page.tsx", []byte("export function Page() {}\n")); err != nil {
		t.Fatal(err)
	}
	if len(stub.opened) != 1 || len(stub.closed) != 1 {
		t.Fatalf("opened=%v closed=%v, want one DidOpen and one DidClose", stub.opened, stub.closed)
	}
	if stub.opened[0] != stub.closed[0] {
		t.Fatalf("open/close URI mismatch: %q vs %q", stub.opened[0], stub.closed[0])
	}
}

func TestCallEdgesReopensDocumentOnDisk(t *testing.T) {
	dir := t.TempDir()
	rel := "src/app.ts"
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("export function helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &stubLanguageClient{
		documentSymbols: true,
		callHierarchy:   true,
		syms: []lsp.DocumentSymbol{{
			Name: "helper", Kind: lsp.SymbolFunction,
			Range: lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0}},
		}},
		prepareItems: []lsp.CallHierarchyItem{{Name: "helper"}},
	}
	e := &Extractor{
		ctx: context.Background(), lang: "typescript", langID: "typescript",
		root: dir, client: stub,
	}
	if _, err := e.CallEdges(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if len(stub.opened) != 1 || len(stub.closed) != 1 {
		t.Fatalf("CallEdges should DidOpen/DidClose once, got opened=%v closed=%v", stub.opened, stub.closed)
	}
}

func TestCallEdgesTypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "callee.ts"),
		[]byte("export function callee() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "caller.ts"),
		[]byte("import { callee } from \"./callee\";\n\nexport function caller() { return callee(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := New(ctx, "typescript", "typescript", dir, "typescript-language-server", "--stdio")
	if err != nil {
		t.Skipf("typescript-language-server unavailable: %v", err)
	}
	defer e.Close()
	// Open both files so callHierarchy resolves cross-file.
	for _, f := range []string{"callee.ts", "caller.ts"} {
		src, _ := os.ReadFile(filepath.Join(dir, f))
		if _, err := e.ExtractFile(f, src); err != nil {
			t.Fatal(err)
		}
	}

	edges, err := e.CallEdges(ctx, "caller.ts")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ed := range edges {
		if ed.FromFQN == "caller" && !ed.External && ed.ToFile == "callee.ts" {
			found = true
			if ed.FromFile != "caller.ts" || ed.FromLine != 3 || ed.ToLine != 1 {
				t.Errorf("call edge positions = %+v, want caller.ts:3 -> callee.ts:1", ed)
			}
		}
	}
	if !found {
		t.Errorf("expected caller -> callee.ts edge, got %+v", edges)
	}
}

func TestCallEdgesRequireAdvertisedCapability(t *testing.T) {
	e := &Extractor{
		lang:   "rust",
		root:   "/project",
		client: &stubLanguageClient{documentSymbols: true},
	}
	_, err := e.CallEdges(context.Background(), "src/lib.rs")
	if err == nil || !strings.Contains(err.Error(), "does not advertise callHierarchy") {
		t.Fatalf("CallEdges error = %v, want capability admission failure", err)
	}
}

func TestCallEdgesPropagatePerSymbolFailure(t *testing.T) {
	want := errors.New("server could not prepare hierarchy")
	e := &Extractor{
		lang: "rust",
		root: "/project",
		client: &stubLanguageClient{
			documentSymbols: true,
			callHierarchy:   true,
			syms: []lsp.DocumentSymbol{{
				Name: "run",
				Kind: lsp.SymbolFunction,
			}},
			prepareErr: want,
		},
	}
	_, err := e.CallEdges(context.Background(), "src/lib.rs")
	if !errors.Is(err, want) {
		t.Fatalf("CallEdges error = %v, want wrapped per-symbol failure", err)
	}
}

func TestCallEdgesRejectEmptyPrepareForCallable(t *testing.T) {
	e := &Extractor{
		lang: "rust",
		root: "/project",
		client: &stubLanguageClient{
			documentSymbols: true,
			callHierarchy:   true,
			syms: []lsp.DocumentSymbol{{
				Name: "leaf",
				Kind: lsp.SymbolFunction,
			}},
		},
	}
	_, err := e.CallEdges(context.Background(), "src/lib.rs")
	if err == nil || !strings.Contains(err.Error(), "returned no item") {
		t.Fatalf("CallEdges error = %v, want incomplete coverage failure", err)
	}
}

func TestCallEdgesRejectEmptyDocumentSymbols(t *testing.T) {
	e := &Extractor{
		lang: "rust",
		root: "/project",
		client: &stubLanguageClient{
			documentSymbols: true,
			callHierarchy:   true,
		},
	}
	_, err := e.CallEdges(context.Background(), "src/lib.rs")
	if err == nil || !strings.Contains(err.Error(), "documentSymbol returned no symbols") {
		t.Fatalf("CallEdges error = %v, want conservative empty-document failure", err)
	}
}

func TestLineSlice(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	if got := lineSlice(lines, 1, 2); got != "b\nc" {
		t.Errorf("lineSlice = %q, want b\\nc", got)
	}
	if got := lineSlice(lines, 0, 99); got != "a\nb\nc\nd" {
		t.Errorf("lineSlice clamp = %q", got)
	}
	if got := lineSlice(lines, 5, 6); got != "" {
		t.Errorf("lineSlice out-of-range = %q, want empty", got)
	}
}

func TestLSPExtractGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package m\n\nfunc Foo() {}\n\ntype Bar struct{}\n"
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	e, err := New(ctx, "go", "go", dir, "gopls")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	res, err := e.ExtractFile("a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range res.Symbols {
		kinds[s.Name] = s.Kind
	}
	if kinds["Foo"] != extract.KindFunction {
		t.Errorf("Foo kind = %q, want function (got symbols %v)", kinds["Foo"], kinds)
	}
	if kinds["Bar"] != extract.KindType {
		t.Errorf("Bar kind = %q, want type", kinds["Bar"])
	}
}
