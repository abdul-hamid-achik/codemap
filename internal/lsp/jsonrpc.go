// Package lsp is a minimal headless Language Server Protocol client: it speaks
// JSON-RPC 2.0 over Content-Length-framed stdio to a language server subprocess
// (gopls, typescript-language-server, …) to extract precise code structure.
//
// IMPORTANT: LSP uses Content-Length framing. This is the LSP convention only —
// it must never be confused with codemap's MCP server, which uses
// newline-delimited JSON-RPC. The two transports are deliberately separate.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultRequestTimeout bounds a single JSON-RPC request when the caller's
// context carries no deadline of its own — so a hung or misbehaving language
// server can't stall indexing indefinitely. A request that exceeds it returns a
// deadline error, which the indexer treats as "skip this file/symbol" and
// continues. Callers that set their own deadline keep it.
const defaultRequestTimeout = 30 * time.Second

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handlerFunc handles an inbound request (its result is sent back) or
// notification (result ignored). Returning (nil, nil) replies with null.
type handlerFunc func(method string, params json.RawMessage) (any, error)

// conn is a Content-Length-framed JSON-RPC 2.0 connection. It serves both client
// and server roles (the test fake-server reuses it), with a background read loop
// that routes responses to pending callers and requests/notifications to the
// handler.
type conn struct {
	w      io.Writer
	r      *bufio.Reader
	closer func() error

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan message

	handler handlerFunc

	reqTimeout time.Duration // per-request bound applied when the caller sets no deadline

	closeOnce sync.Once
	done      chan struct{}
}

func newConn(r io.Reader, w io.Writer, closer func() error, handler handlerFunc) *conn {
	c := &conn{
		w:          w,
		r:          bufio.NewReader(r),
		closer:     closer,
		pending:    make(map[int64]chan message),
		handler:    handler,
		reqTimeout: defaultRequestTimeout,
		done:       make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *conn) writeMessage(m message) error {
	m.JSONRPC = "2.0"
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// Call sends a request and waits for its response.
func (c *conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Bound an otherwise-unbounded request so a stalled server can't hang forever.
	if c.reqTimeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.reqTimeout)
			defer cancel()
		}
	}
	id := c.nextID.Add(1)
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan message, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeMessage(message{ID: &id, Method: method, Params: raw}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a notification (no response expected).
func (c *conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.writeMessage(message{Method: method, Params: raw})
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

func (c *conn) readLoop() {
	for {
		m, err := c.readMessage()
		if err != nil {
			c.closeWithError()
			return
		}
		switch {
		case m.ID != nil && m.Method == "":
			// response to one of our calls
			c.pendingMu.Lock()
			ch := c.pending[*m.ID]
			c.pendingMu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.Method != "":
			c.dispatch(m)
		}
	}
}

func (c *conn) dispatch(m message) {
	var result any
	var herr error
	if c.handler != nil {
		result, herr = c.handler(m.Method, m.Params)
	}
	if m.ID == nil {
		return // notification
	}
	if herr != nil {
		_ = c.writeMessage(message{ID: m.ID, Error: &rpcError{Code: -32603, Message: herr.Error()}})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		raw = json.RawMessage("null")
	}
	_ = c.writeMessage(message{ID: m.ID, Result: raw})
}

func (c *conn) readMessage() (message, error) {
	contentLength := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return message{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(v)
			if err != nil {
				return message{}, fmt.Errorf("bad Content-Length %q: %w", v, err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return message{}, fmt.Errorf("missing Content-Length header")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return message{}, err
	}
	var m message
	if err := json.Unmarshal(buf, &m); err != nil {
		return message{}, err
	}
	return m, nil
}

func (c *conn) closeWithError() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.closer != nil {
			_ = c.closer()
		}
	})
}

// Close shuts down the connection.
func (c *conn) Close() error {
	c.closeWithError()
	return nil
}
