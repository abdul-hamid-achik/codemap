/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestServeStdioNewlineDelimitedFraming drives the REAL `codemap serve`
// subprocess over OS pipes and pins the MCP stdio framing contract that
// AGENTS.md marks CRITICAL: responses must be newline-delimited JSON-RPC,
// never Content-Length framed. A sibling tool (glyph) broke in Claude Code
// purely by emitting Content-Length framing on stdio; the in-memory transport
// used by every other MCP test cannot catch a regression that re-frames stdio
// output, so this test exercises the actual transport end to end.
func TestServeStdioNewlineDelimitedFraming(t *testing.T) {
	root := t.TempDir()
	binName := "codemap"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(root, binName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	// Isolate all codemap dirs so `serve` never touches real state. serve
	// lazy-opens the store, so no index is needed to answer initialize/tools-list.
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".data"),
		"CODEMAP_DATA="+filepath.Join(home, "data"),
		"CODEMAP_CONFIG=",
	)

	cmd := exec.Command(bin, "serve")
	cmd.Dir = root
	cmd.Env = env
	cmd.Stderr = io.Discard // server logs go to stderr; stdout must stay pure JSON-RPC
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Feed each newline-delimited response line onto a channel so reads can be
	// bounded by a timeout (a hung server must fail the test, not stall it).
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // tools/list with 40+ schemas can exceed 64KB
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	// assertFraming is the heart of the contract: a response line must be a
	// single newline-delimited JSON-RPC object — never a "Content-Length:"
	// header (the LSP framing that must NOT leak into the MCP transport).
	assertFraming := func(line string) {
		t.Helper()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return
		}
		if strings.HasPrefix(trimmed, "Content-Length") {
			t.Fatalf("MCP stdio response is Content-Length framed (must be newline-delimited JSON): %q", line)
		}
		if !strings.HasPrefix(trimmed, "{") {
			t.Fatalf("MCP stdio response line is not a JSON object: %q", line)
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
			t.Fatalf("MCP stdio response line is not valid JSON: %v: %q", err, line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Fatalf("MCP stdio response is not JSON-RPC 2.0: %q", line)
		}
	}

	// readUntil reads newline-delimited responses until one contains wantID
	// (matched as a raw substring on the compact JSON), asserting the framing
	// contract on every line seen along the way.
	readUntil := func(wantID string) string {
		t.Helper()
		deadline := time.After(30 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("serve closed stdout before a response containing %s", wantID)
				}
				assertFraming(line)
				if strings.Contains(line, wantID) {
					return line
				}
			case <-deadline:
				t.Fatalf("timed out waiting for a response containing %s", wantID)
			}
		}
	}

	send := func(v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(stdin, "%s\n", b); err != nil {
			t.Fatalf("write to serve stdin: %v", err)
		}
	}

	// 1. initialize → the server's first response must already be
	//    newline-delimited JSON-RPC (this is where a Content-Length regression
	//    would show up first).
	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "stdio-framing-test", "version": "0"},
		},
	})
	initResp := readUntil(`"id":1`)
	if !strings.Contains(initResp, "protocolVersion") {
		t.Fatalf("initialize response missing protocolVersion: %s", initResp)
	}

	// 2. Complete the handshake, then list tools over the real transport.
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	toolsResp := readUntil(`"id":2`)

	// The tools/list response proves the whole handshake worked over real stdio
	// and that the registered codemap tools actually serialize onto the wire.
	if !strings.Contains(toolsResp, "codemap_impact") || !strings.Contains(toolsResp, "codemap_context") {
		t.Fatalf("tools/list over stdio missing core codemap tools: %s", toolsResp)
	}
}
