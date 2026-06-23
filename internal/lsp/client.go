package lsp

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
)

// LSP symbol kinds (subset we map to codemap node kinds).
const (
	SymbolClass     = 5
	SymbolMethod    = 6
	SymbolInterface = 11
	SymbolFunction  = 12
	SymbolVariable  = 13
	SymbolStruct    = 23
)

// Position is a 0-based line/character location.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range spans two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a URI plus a range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// DocumentSymbol is a hierarchical symbol from textDocument/documentSymbol.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// Client is a headless LSP client over one language-server connection.
type Client struct {
	conn *conn
	cmd  *exec.Cmd
}

func newClient(r io.Reader, w io.Writer, closer func() error) *Client {
	c := &Client{}
	c.conn = newConn(r, w, closer, c.handle)
	return c
}

// handle replies to server→client requests so the server doesn't stall. We have
// no workspace config to offer, so we return empty/null.
func (c *Client) handle(method string, _ json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		return []any{map[string]any{}}, nil
	default:
		return nil, nil
	}
}

// Spawn starts a language-server subprocess and wires a client to its stdio.
func Spawn(ctx context.Context, name string, args ...string) (*Client, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	closer := func() error {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}
	cl := newClient(stdout, stdin, closer)
	cl.cmd = cmd
	return cl, nil
}

// URI converts a filesystem path to a file:// URI.
func URI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs
}

// Initialize performs the initialize/initialized handshake rooted at rootPath.
func (c *Client) Initialize(ctx context.Context, rootPath string) error {
	params := map[string]any{
		"processId": nil,
		"rootUri":   URI(rootPath),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
			},
		},
	}
	if _, err := c.conn.Call(ctx, "initialize", params); err != nil {
		return err
	}
	return c.conn.Notify("initialized", map[string]any{})
}

// DidOpen tells the server about a document's content.
func (c *Client) DidOpen(uri, languageID, text string) error {
	return c.conn.Notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

// DocumentSymbols returns the symbols declared in a document.
func (c *Client) DocumentSymbols(ctx context.Context, uri string) ([]DocumentSymbol, error) {
	raw, err := c.conn.Call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var syms []DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		return nil, err
	}
	return syms, nil
}

// References returns all references to the symbol at a position.
func (c *Client) References(ctx context.Context, uri string, pos Position, includeDecl bool) ([]Location, error) {
	raw, err := c.conn.Call(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     pos,
		"context":      map[string]any{"includeDeclaration": includeDecl},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		return nil, err
	}
	return locs, nil
}

// Shutdown asks the server to shut down.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.conn.Call(ctx, "shutdown", nil)
	return err
}

// Exit notifies the server to exit and closes the connection.
func (c *Client) Exit() error {
	_ = c.conn.Notify("exit", nil)
	return c.conn.Close()
}

// Close terminates the connection (and the subprocess, if any).
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
