package lspsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// crashingClient answers like stubLanguageClient, except that its backend
// "dies" while answering the first documentSymbol (an empty answer plus a
// recorded crash, as typescript-language-server does when tsserver aborts),
// until Restart swaps in a fresh process.
type crashingClient struct {
	stubLanguageClient
	crashed     bool
	crashOnNext bool
	restarts    int
}

func (c *crashingClient) DocumentSymbols(ctx context.Context, uri string) ([]lsp.DocumentSymbol, error) {
	if c.crashOnNext {
		c.crashOnNext, c.crashed = false, true
	}
	if c.crashed {
		return nil, nil
	}
	return c.stubLanguageClient.DocumentSymbols(ctx, uri)
}
func (c *crashingClient) Crashed() (bool, string) {
	return c.crashed, "[tsserver] Exited. Code: null. Signal: SIGABRT"
}
func (c *crashingClient) Restarts() int { return c.restarts }
func (c *crashingClient) Restart(context.Context) error {
	c.restarts++
	c.crashed = false
	return nil
}

func callableStub() stubLanguageClient {
	return stubLanguageClient{
		documentSymbols: true, callHierarchy: true,
		syms: []lsp.DocumentSymbol{{
			Name: "run", Kind: lsp.SymbolFunction,
			Range:          lsp.Range{End: lsp.Position{Line: 2}},
			SelectionRange: lsp.Range{Start: lsp.Position{Character: 9}},
		}},
		prepareItems: []lsp.CallHierarchyItem{{Name: "run"}},
	}
}

func TestCallEdgesRestartsCrashedBackendAndRetriesTheFile(t *testing.T) {
	c := &crashingClient{stubLanguageClient: callableStub(), crashOnNext: true}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: t.TempDir(), cmd: "typescript-language-server", client: c, health: &serverHealth{}}

	if _, err := e.CallEdges(context.Background(), "a.ts"); err != nil {
		t.Fatalf("the file whose answer the crash ate must be retried on a fresh process: %v", err)
	}
	if c.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", c.restarts)
	}
	if n, bin := e.BackendRestarts(); n != 1 || bin != "typescript-language-server" {
		t.Fatalf("BackendRestarts = %d %q", n, bin)
	}
	if down, _ := e.Degraded(); down {
		t.Fatal("a recovered server is not degraded")
	}
}

func TestCallEdgesGivesUpAfterRestartBudget(t *testing.T) {
	c := &crashingClient{stubLanguageClient: callableStub(), crashed: true, restarts: maxBackendRestarts}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: t.TempDir(), cmd: "typescript-language-server", client: c, health: &serverHealth{}}

	_, err := e.CallEdges(context.Background(), "a.ts")
	if err == nil || !strings.Contains(err.Error(), "backend exited") || !strings.Contains(err.Error(), "SIGABRT") {
		t.Fatalf("err = %v, want the backend-exit cause with the server's message", err)
	}
	if c.restarts != maxBackendRestarts {
		t.Fatalf("restarted past the budget: %d", c.restarts)
	}
	if down, _ := e.Degraded(); !down {
		t.Fatal("a dead backend must read as degraded")
	}
}

// countingClient records documentSymbol requests.
type countingClient struct {
	stubLanguageClient
	docSymbolCalls int
}

func (c *countingClient) DocumentSymbols(ctx context.Context, uri string) ([]lsp.DocumentSymbol, error) {
	c.docSymbolCalls++
	return c.stubLanguageClient.DocumentSymbols(ctx, uri)
}

func TestCallEdgesWithSymbolsSkipsDocumentSymbol(t *testing.T) {
	stub := callableStub()
	known := stub.syms
	stub.syms = nil // the server would answer empty — it must not be asked
	c := &countingClient{stubLanguageClient: stub}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: t.TempDir(), client: c, health: &serverHealth{}}

	if _, err := e.CallEdgesWithSymbols(context.Background(), "a.ts", known); err != nil {
		t.Fatal(err)
	}
	if c.docSymbolCalls != 0 || len(c.prepared) != 1 {
		t.Fatalf("documentSymbol calls = %d, prepares = %d; want 0 and 1", c.docSymbolCalls, len(c.prepared))
	}

	edges, err := e.CallEdgesWithSymbols(context.Background(), "b.ts", []lsp.DocumentSymbol{})
	if err != nil || edges != nil || c.docSymbolCalls != 0 {
		t.Fatalf("empty known tree = %v, %v (docSymbol calls %d); want a complete empty answer", edges, err, c.docSymbolCalls)
	}

	if _, err := e.CallEdgesWithSymbols(context.Background(), "c.ts", nil); err == nil || c.docSymbolCalls == 0 {
		t.Fatalf("a nil tree must ask the server (and its empty answer stays an error): err=%v calls=%d", err, c.docSymbolCalls)
	}
}

func TestNamePositionSkipsModifiers(t *testing.T) {
	lines := []string{"class A {", "  private constructor(x: number) {}", "}"}
	s := lsp.DocumentSymbol{Name: "constructor", SelectionRange: lsp.Range{Start: lsp.Position{Line: 1, Character: 2}}}
	if pos, ok := namePosition(lines, s); !ok || pos.Character != 10 || pos.Line != 1 {
		t.Fatalf("namePosition = %+v %v, want line 1 col 10", pos, ok)
	}
	s.SelectionRange.Start.Character = 10
	if _, ok := namePosition(lines, s); ok {
		t.Fatal("a selection already on the name needs no retry")
	}
	if _, ok := namePosition(nil, s); ok {
		t.Fatal("no source, no retry")
	}
}

func TestPrepareRetriesAtNameColumn(t *testing.T) {
	stub := stubLanguageClient{
		documentSymbols: true, callHierarchy: true,
		prepareFn: func(pos lsp.Position) ([]lsp.CallHierarchyItem, error) {
			if pos.Character == 10 {
				return []lsp.CallHierarchyItem{{Name: "constructor"}}, nil
			}
			return nil, nil
		},
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.ts"), []byte("class A {\n  private constructor() {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: root, client: &stub, health: &serverHealth{}}
	known := []lsp.DocumentSymbol{{Name: "A", Kind: lsp.SymbolClass, Range: lsp.Range{End: lsp.Position{Line: 2}},
		Children: []lsp.DocumentSymbol{{Name: "constructor", Kind: lsp.SymbolConstructor,
			Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 2}, End: lsp.Position{Line: 1, Character: 26}}, SelectionRange: lsp.Range{Start: lsp.Position{Line: 1, Character: 2}}}}}}
	if _, err := e.CallEdgesWithSymbols(context.Background(), "a.ts", known); err != nil {
		t.Fatalf("constructor selected at its modifier must prepare at its name: %v", err)
	}
}

func TestPropertyArrowNoItemIsCoveredOnlyUnderAPreparedAncestor(t *testing.T) {
	src := "function outer() {\n  const api = {\n    getLogger: () => log(),\n  };\n}\nconst top = {\n  up: async function () {},\n};\n"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := stubLanguageClient{
		documentSymbols: true, callHierarchy: true,
		prepareFn: func(pos lsp.Position) ([]lsp.CallHierarchyItem, error) {
			if pos.Line == 0 {
				return []lsp.CallHierarchyItem{{Name: "outer"}}, nil
			}
			return nil, nil // property assignments are never call-hierarchy declarations
		},
	}
	e := &Extractor{ctx: context.Background(), lang: "typescript", langID: "typescript", root: root, client: &stub, health: &serverHealth{}}
	sel := func(line, col int) lsp.Range { return lsp.Range{Start: lsp.Position{Line: line, Character: col}} }
	getLogger := lsp.DocumentSymbol{Name: "getLogger", Kind: lsp.SymbolMethod, Range: sel(2, 4), SelectionRange: sel(2, 4)}
	inner := []lsp.DocumentSymbol{{Name: "outer", Kind: lsp.SymbolFunction, Range: lsp.Range{End: lsp.Position{Line: 4}}, SelectionRange: sel(0, 9),
		Children: []lsp.DocumentSymbol{{Name: "api", Kind: lsp.SymbolVariable, Range: sel(1, 8), SelectionRange: sel(1, 8), Children: []lsp.DocumentSymbol{getLogger}}}}}
	if _, err := e.CallEdgesWithSymbols(context.Background(), "a.ts", inner); err != nil {
		t.Fatalf("a property arrow inside a prepared function is covered by it: %v", err)
	}

	up := lsp.DocumentSymbol{Name: "up", Kind: lsp.SymbolMethod, Range: sel(6, 2), SelectionRange: sel(6, 2)}
	module := []lsp.DocumentSymbol{{Name: "top", Kind: lsp.SymbolVariable, Range: sel(5, 6), SelectionRange: sel(5, 6), Children: []lsp.DocumentSymbol{up}}}
	if _, err := e.CallEdgesWithSymbols(context.Background(), "a.ts", module); err == nil {
		t.Fatal("a module-level property function has no prepared ancestor: it must stay a coverage gap")
	}
}

func TestIsPropertyAssignment(t *testing.T) {
	lines := []string{`  getLogger: () => x,`, `  handler?: () => void;`, `  "quoted": function () {},`, `  up() {}`, `  const f = () => 1`}
	cases := []struct {
		line int
		name string
		want bool
	}{{0, "getLogger", true}, {1, "handler", true}, {2, "quoted", true}, {3, "up", false}, {4, "f", false}}
	for _, c := range cases {
		s := lsp.DocumentSymbol{Name: c.name, SelectionRange: lsp.Range{Start: lsp.Position{Line: c.line, Character: 2}}}
		if got := isPropertyAssignment(lines, s); got != c.want {
			t.Errorf("isPropertyAssignment(%q) = %v, want %v", lines[c.line], got, c.want)
		}
	}
}
