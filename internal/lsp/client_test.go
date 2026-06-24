package lsp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestURI(t *testing.T) {
	if got := URI("/abs/path"); got != "file:///abs/path" {
		t.Errorf("URI = %q, want file:///abs/path", got)
	}
}

// TestClientFakeServer exercises the full framed JSON-RPC round-trip against an
// in-memory fake server (no external dependency).
func TestClientFakeServer(t *testing.T) {
	c2sR, c2sW := io.Pipe() // client -> server
	s2cR, s2cW := io.Pipe() // server -> client
	defer c2sW.Close()
	defer s2cW.Close()

	serverHandler := func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return map[string]any{"capabilities": map[string]any{}}, nil
		case "textDocument/documentSymbol":
			return []DocumentSymbol{
				{Name: "Foo", Kind: SymbolFunction, Range: Range{End: Position{Line: 2}}},
				{Name: "Bar", Kind: SymbolStruct, Range: Range{Start: Position{Line: 4}, End: Position{Line: 6}}},
			}, nil
		case "textDocument/references":
			return []Location{{URI: "file:///a.go", Range: Range{Start: Position{Line: 1}}}}, nil
		}
		return nil, nil
	}
	srv := newConn(c2sR, s2cW, nil, serverHandler)
	defer srv.Close()

	cl := newClient(s2cR, c2sW, nil)
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := cl.Initialize(ctx, "/tmp/proj"); err != nil {
		t.Fatal(err)
	}
	syms, err := cl.DocumentSymbols(ctx, "file:///tmp/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 || syms[0].Name != "Foo" || syms[1].Name != "Bar" {
		t.Fatalf("symbols = %+v", syms)
	}
	if syms[0].Kind != SymbolFunction || syms[1].Kind != SymbolStruct {
		t.Errorf("kinds = %d,%d", syms[0].Kind, syms[1].Kind)
	}

	refs, err := cl.References(ctx, "file:///tmp/a.go", Position{Line: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Errorf("references = %+v, want 1", refs)
	}
}

// TestRequestTimeout verifies a request to a stalled server (one that never
// replies) is bounded by the connection's per-request timeout instead of hanging
// forever — so one hung language server can't freeze the whole index.
func TestRequestTimeout(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	defer c2sW.Close()
	defer s2cW.Close()

	block := make(chan struct{})
	defer close(block)
	serverHandler := func(method string, _ json.RawMessage) (any, error) {
		if method == "textDocument/documentSymbol" {
			<-block // never responds while the test runs
		}
		return nil, nil
	}
	srv := newConn(c2sR, s2cW, nil, serverHandler)
	defer srv.Close()

	cl := newClient(s2cR, c2sW, nil)
	defer cl.Close()
	cl.conn.reqTimeout = 100 * time.Millisecond // tiny bound for the test

	start := time.Now()
	// context.Background() has no deadline → the per-request bound applies.
	if _, err := cl.DocumentSymbols(context.Background(), "file:///a.go"); err == nil {
		t.Fatal("expected a deadline error from a stalled server, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("request should have been bounded by reqTimeout (~100ms), took %v", elapsed)
	}
}

// TestGoplsIntegration validates against the real gopls if it is installed
// (skipped in CI, where gopls is absent).
func TestGoplsIntegration(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package m\n\nfunc Foo() {}\n\ntype Bar struct{}\n\nfunc (b Bar) Baz() {}\n"
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cl, err := Spawn(ctx, "gopls")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	if err := cl.Initialize(ctx, dir); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := cl.DidOpen(URI(file), "go", src); err != nil {
		t.Fatal(err)
	}
	syms, err := cl.DocumentSymbols(ctx, URI(file))
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	if !names["Foo"] || !names["Bar"] {
		t.Errorf("gopls symbols = %v, want Foo and Bar", names)
	}
	_ = cl.Shutdown(ctx)
	_ = cl.Exit()
}

func TestGoplsCallHierarchy(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package m\n\nfunc Helper() {}\n\nfunc Run() { Helper() }\n"
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cl, err := Spawn(ctx, "gopls")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.Initialize(ctx, dir); err != nil {
		t.Fatal(err)
	}
	uri := URI(file)
	if err := cl.DidOpen(uri, "go", src); err != nil {
		t.Fatal(err)
	}

	// Find Helper's name position from documentSymbol.
	syms, err := cl.DocumentSymbols(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	var pos Position
	for _, s := range syms {
		if s.Name == "Helper" {
			pos = s.SelectionRange.Start
		}
	}
	items, err := cl.PrepareCallHierarchy(ctx, uri, pos)
	if err != nil {
		t.Fatalf("prepareCallHierarchy: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no call hierarchy item for Helper")
	}
	calls, err := cl.IncomingCalls(ctx, items[0])
	if err != nil {
		t.Fatalf("incomingCalls: %v", err)
	}
	names := map[string]bool{}
	for _, c := range calls {
		names[c.From.Name] = true
	}
	if !names["Run"] {
		t.Errorf("incoming calls = %v, want Run (which calls Helper)", names)
	}
}
