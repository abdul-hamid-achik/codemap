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
	appendSymbols(res, lines, "typescript", "", false, sym)

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
	appendSymbols(res, lines, "python", "", false, fn)
	appendSymbols(res, lines, "python", "", false, modVar)

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
		t.Fatal(err)
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
		}
	}
	if !found {
		t.Errorf("expected caller -> callee.ts edge, got %+v", edges)
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
