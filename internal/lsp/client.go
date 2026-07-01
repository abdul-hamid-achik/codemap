package lsp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// LSP symbol kinds (subset we map to codemap node kinds). Values are from the
// LSP spec (SymbolKind), not gopls-specific.
const (
	SymbolModule      = 2
	SymbolNamespace   = 3
	SymbolClass       = 5
	SymbolMethod      = 6
	SymbolConstructor = 9
	SymbolEnum        = 10
	SymbolInterface   = 11
	SymbolFunction    = 12
	SymbolVariable    = 13
	SymbolConstant    = 14
	SymbolStruct      = 23
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

// CallHierarchyItem identifies a symbol in the call hierarchy.
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
}

// CallHierarchyIncomingCall is a caller of the prepared item.
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

// CallHierarchyOutgoingCall is a callee of the prepared item.
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// Client is a headless LSP client over one language-server connection.
type Client struct {
	conn      *conn
	cmd       *exec.Cmd
	ready     chan struct{} // signalled when a $/progress "end" arrives
	stderrBuf *cappedBuffer // last 8KB of the server's stderr; surfaced on connection-close errors
}

func newClient(r io.Reader, w io.Writer, closer func() error) *Client {
	c := &Client{ready: make(chan struct{}, 8)}
	c.conn = newConn(r, w, closer, c.handle)
	return c
}

// handle replies to server→client requests so the server doesn't stall, and
// watches $/progress so callers can wait for the server to finish loading.
func (c *Client) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		return []any{map[string]any{}}, nil
	case "$/progress":
		var p struct {
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if json.Unmarshal(params, &p) == nil && p.Value.Kind == "end" {
			select {
			case c.ready <- struct{}{}:
			default:
			}
		}
	}
	return nil, nil
}

// WaitReady blocks until the server reports a $/progress "end" (its initial
// workspace load completing) or the timeout elapses — needed before whole-
// workspace queries like callHierarchy on large projects.
func (c *Client) WaitReady(ctx context.Context, timeout time.Duration) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-c.ready:
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
	// P1-03 (O34): capture the last 8KB of stderr into a ring buffer
	// so a server crash surfaces actionable diagnostics instead of
	// bare io.EOF. The cap bounds memory for noisy servers; only the
	// tail is kept (most useful signal).
	stderrBuf := newCappedBuffer(8 << 10)
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Reap the subprocess on close: Kill then Wait, so a stopped language server
	// doesn't linger as a zombie. sync.Once keeps Close idempotent (Wait must not
	// be called twice).
	var once sync.Once
	closer := func() error {
		once.Do(func() {
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		return nil
	}
	cl := newClient(stdout, stdin, closer)
	cl.cmd = cmd
	cl.stderrBuf = stderrBuf
	return cl, nil
}

// URI converts a filesystem path to a percent-encoded file:// URI
// (P1-02: was bare string concatenation; spaces / non-ASCII characters
// now round-trip through the language server).
func URI(path string) (string, error) {
	return PathToURI(path)
}

// Initialize performs the initialize/initialized handshake rooted at rootPath.
func (c *Client) Initialize(ctx context.Context, rootPath string) error {
	params := map[string]any{
		"processId": os.Getpid(), // P1-03 (B31): real PID so the language server's parent-watchdog can kill orphans on codemap SIGKILL
		"rootUri":   func() string { u, _ := URI(rootPath); return u }(),
		"capabilities": map[string]any{
			"window": map[string]any{"workDoneProgress": true},
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"callHierarchy": map[string]any{"dynamicRegistration": true},
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

// PrepareCallHierarchy returns the call-hierarchy item(s) at a position.
func (c *Client) PrepareCallHierarchy(ctx context.Context, uri string, pos Position) ([]CallHierarchyItem, error) {
	raw, err := c.conn.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     pos,
	})
	if err != nil {
		return nil, err
	}
	if isNull(raw) {
		return nil, nil
	}
	var items []CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// IncomingCalls returns the callers of a prepared call-hierarchy item.
func (c *Client) IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error) {
	raw, err := c.conn.Call(ctx, "callHierarchy/incomingCalls", map[string]any{"item": item})
	if err != nil {
		return nil, err
	}
	if isNull(raw) {
		return nil, nil
	}
	var calls []CallHierarchyIncomingCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, err
	}
	return calls, nil
}

// OutgoingCalls returns the callees of a prepared call-hierarchy item.
func (c *Client) OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyOutgoingCall, error) {
	raw, err := c.conn.Call(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": item})
	if err != nil {
		return nil, err
	}
	if isNull(raw) {
		return nil, nil
	}
	var calls []CallHierarchyOutgoingCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, err
	}
	return calls, nil
}

func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

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
