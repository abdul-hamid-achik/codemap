package lspsrc

import (
	"context"
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
