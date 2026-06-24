package lspsrc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	appendSymbols(res, lines, "typescript", "", sym)

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
