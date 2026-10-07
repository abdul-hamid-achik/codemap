package lsp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestFakeTSServerProcess is not a test: when CODEMAP_FAKE_LSP_STATE is set it
// runs as a stdio language server whose FIRST launch reports a dead tsserver
// backend (the SIGABRT logMessage typescript-language-server sends) and then
// answers empty, while every later launch answers normally.
func TestFakeTSServerProcess(t *testing.T) {
	state := os.Getenv("CODEMAP_FAKE_LSP_STATE")
	if state == "" {
		t.Skip("helper process for TestClientRestartAfterBackendCrash")
	}
	prior, _ := os.ReadFile(state)
	launch := len(prior)
	_ = os.WriteFile(state, append(prior, 'x'), 0o644)

	var srv *conn
	srv = newConn(os.Stdin, os.Stdout, nil, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return map[string]any{"capabilities": map[string]any{"documentSymbolProvider": true}}, nil
		case "textDocument/documentSymbol":
			if launch == 0 {
				_ = srv.Notify("window/logMessage", map[string]any{
					"type": 1, "message": "[lspserver] [tsclient] [tsserver] Exited. Code: null. Signal: SIGABRT",
				})
				return []DocumentSymbol{}, nil
			}
			return []DocumentSymbol{{Name: "Foo", Kind: SymbolFunction, Range: Range{End: Position{Line: 2}}}}, nil
		}
		return nil, nil
	})
	select {} // killed by the client
}

func TestClientRestartAfterBackendCrash(t *testing.T) {
	t.Setenv("CODEMAP_FAKE_LSP_STATE", t.TempDir()+"/launches")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cl, err := Spawn(ctx, os.Args[0], "-test.run=^TestFakeTSServerProcess$")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Initialize(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	syms, err := cl.DocumentSymbols(ctx, "file:///a.ts")
	if err != nil || len(syms) != 0 {
		t.Fatalf("first launch = %v, %v; want an empty answer", syms, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if down, msg := cl.Crashed(); down {
			if msg == "" {
				t.Fatal("crash recorded without the server's message")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend exit logMessage was not recorded as a crash")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := cl.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if down, _ := cl.Crashed(); down {
		t.Fatal("Restart must clear the crash")
	}
	if cl.Restarts() != 1 {
		t.Fatalf("Restarts = %d, want 1", cl.Restarts())
	}
	syms, err = cl.DocumentSymbols(ctx, "file:///a.ts")
	if err != nil || len(syms) != 1 || syms[0].Name != "Foo" {
		t.Fatalf("after restart = %v, %v; want Foo from the fresh process", syms, err)
	}
}

func TestInitializationOptionsOnlyForTypeScriptServer(t *testing.T) {
	if opts := initializationOptions("/usr/local/bin/typescript-language-server"); opts["maxTsServerMemory"] != tsServerMaxMemoryMB {
		t.Fatalf("typescript-language-server options = %v", opts)
	}
	for _, name := range []string{"pyright-langserver", "gopls", ""} {
		if opts := initializationOptions(name); opts != nil {
			t.Fatalf("%q must get no initializationOptions, got %v", name, opts)
		}
	}
}

func TestResetRestartsStartsANewBudget(t *testing.T) {
	c := &Client{restarts: 5}
	c.ResetRestarts()
	if c.Restarts() != 0 {
		t.Fatalf("Restarts = %d after reset", c.Restarts())
	}
}
