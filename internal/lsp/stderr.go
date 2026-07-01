package lsp

import (
	"sync"
)

// cappedBuffer is a thread-safe ring buffer of bytes, dropping the oldest
// content when the cap is hit. Used to capture the tail of a language
// server's stderr so a crash surfaces actionable diagnostics instead of
// a bare io.EOF (P1-03 / O34).
type cappedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newCappedBuffer(cap int) *cappedBuffer {
	return &cappedBuffer{buf: make([]byte, 0, cap), cap: cap}
}

// Write implements io.Writer — appends p to the buffer, evicting the
// oldest bytes if the cap is hit. Never returns an error.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) >= c.cap {
		// p alone exceeds the cap — keep only its tail.
		c.buf = append(c.buf[:0], p[len(p)-c.cap:]...)
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.cap {
		// Drop oldest bytes.
		c.buf = c.buf[len(c.buf)-c.cap:]
	}
	return len(p), nil
}

// String returns the buffered content (thread-safe copy).
func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}
