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
	cases := map[int]string{
		lsp.SymbolFunction:  extract.KindFunction,
		lsp.SymbolMethod:    extract.KindMethod,
		lsp.SymbolClass:     extract.KindType,
		lsp.SymbolStruct:    extract.KindType,
		lsp.SymbolInterface: extract.KindType,
		lsp.SymbolVariable:  "",
	}
	for in, want := range cases {
		if got := mapKind(in); got != want {
			t.Errorf("mapKind(%d) = %q, want %q", in, got, want)
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
	if by["Foo"].Kind != extract.KindType {
		t.Errorf("Foo kind = %q, want type", by["Foo"].Kind)
	}
	if by["Foo.bar"].Kind != extract.KindMethod {
		t.Errorf("Foo.bar kind = %q, want method (nested FQN)", by["Foo.bar"].Kind)
	}
	if by["Foo.bar"].StartLine != 2 {
		t.Errorf("Foo.bar StartLine = %d, want 2 (1-based)", by["Foo.bar"].StartLine)
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

	res, err := e.ExtractFile(file, "a.go", []byte(src))
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
