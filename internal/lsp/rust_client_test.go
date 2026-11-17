package lsp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// TestRustAnalyzerCapabilityGating pins how a rust-analyzer initialize result
// gates the precise pass: callHierarchyProvider as a registration-options
// OBJECT means supported (that is how rust-analyzer advertises it); its absence
// means the --precise call path must not be attempted.
func TestRustAnalyzerCapabilityGating(t *testing.T) {
	withCallHierarchy := func(handler func(method string, _ json.RawMessage) (any, error)) *Client {
		c2sR, c2sW := io.Pipe()
		s2cR, s2cW := io.Pipe()
		t.Cleanup(func() { c2sW.Close(); s2cW.Close() })
		srv := newConn(c2sR, s2cW, nil, handler)
		t.Cleanup(func() { _ = srv.Close() })
		cl := newClient(s2cR, c2sW, nil)
		t.Cleanup(func() { _ = cl.Close() })
		return cl
	}

	// rust-analyzer-style capabilities.
	handler := func(method string, _ json.RawMessage) (any, error) {
		if method == "initialize" {
			return map[string]any{"capabilities": map[string]any{
				"documentSymbolProvider": true,
				"callHierarchyProvider":  map[string]any{"workDoneProgress": true},
				"textDocumentSync":       map[string]any{"openClose": true, "change": 1},
			}}, nil
		}
		return nil, nil
	}
	cl := withCallHierarchy(handler)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cl.Initialize(ctx, "/tmp/cargo-proj"); err != nil {
		t.Fatal(err)
	}
	if !cl.SupportsDocumentSymbols() {
		t.Error("rust-analyzer advertises documentSymbolProvider but it was not retained")
	}
	if !cl.SupportsCallHierarchy() {
		t.Error("object-valued callHierarchyProvider (rust-analyzer style) must read as supported")
	}

	// Without the capability the precise path must be refused downstream.
	bare := func(method string, _ json.RawMessage) (any, error) {
		if method == "initialize" {
			return map[string]any{"capabilities": map[string]any{"documentSymbolProvider": true}}, nil
		}
		return nil, nil
	}
	cl2 := withCallHierarchy(bare)
	if err := cl2.Initialize(ctx, "/tmp/cargo-proj"); err != nil {
		t.Fatal(err)
	}
	if cl2.SupportsCallHierarchy() {
		t.Error("server without callHierarchyProvider must not report the capability")
	}
}

// TestRustAnalyzerRequestsAreBounded proves a rust-analyzer that accepts a
// request but never answers cannot hang the indexer: the client returns a
// deadline error at the caller's bound (and the 30s default applies when the
// caller sets none — see defaultRequestTimeout).
func TestRustAnalyzerRequestsAreBounded(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	defer c2sW.Close()
	defer s2cW.Close()

	handler := func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return map[string]any{"capabilities": map[string]any{"documentSymbolProvider": true}}, nil
		case "textDocument/documentSymbol":
			time.Sleep(10 * time.Second) // a stalled server: never answers
		}
		return nil, nil
	}
	srv := newConn(c2sR, s2cW, nil, handler)
	defer srv.Close()
	cl := newClient(s2cR, c2sW, nil)
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cl.Initialize(ctx, "/tmp/cargo-proj"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	rctx, rcancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer rcancel()
	_, err := cl.DocumentSymbols(rctx, "file:///tmp/cargo-proj/src/main.rs")
	if err == nil {
		t.Fatal("stalled server must yield an error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && rctx.Err() == nil {
		t.Fatalf("expected the caller's deadline to bound the request, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("request was not bounded by the caller's deadline: took %s", elapsed)
	}
}
