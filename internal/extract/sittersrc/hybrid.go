package sittersrc

import (
	"context"
	"sync"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// ConcurrentSafe marks an extractor the indexer may call from several
// goroutines at once (a language-server extractor must stay one in flight).
func (e *Extractor) ConcurrentSafe() bool { return true }

// Hybrid pairs the tree-sitter extractor with a live language server, which
// the indexer spawns only for --precise. Structure still comes from
// tree-sitter — so a precise run indexes exactly what a plain one does — while
// the server contributes what only it can: callHierarchy call edges
// (extract.CallResolver) and a second opinion for the rare file whose parse
// has syntax errors.
type Hybrid struct {
	*Extractor
	lsp extract.Extractor
	mu  sync.Mutex // the server connection serves one request at a time
}

// WithServer wraps e with the language-server extractor srv.
func WithServer(e *Extractor, srv extract.Extractor) *Hybrid {
	return &Hybrid{Extractor: e, lsp: srv}
}

// ExtractFile implements extract.Extractor: tree-sitter first; the server
// only when the parse reported syntax errors.
func (h *Hybrid) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res, clean, err := h.extract(relPath, src)
	if err == nil && clean {
		return res, nil
	}
	h.mu.Lock()
	fr, lerr := h.lsp.ExtractFile(relPath, src)
	h.mu.Unlock()
	if lerr == nil {
		return fr, nil
	}
	if err == nil {
		return res, nil // the partial tree beats nothing
	}
	return nil, err
}

// CallEdges implements extract.CallResolver by delegating to the server.
func (h *Hybrid) CallEdges(ctx context.Context, relPath string) ([]extract.CallEdge, error) {
	cr, ok := h.lsp.(extract.CallResolver)
	if !ok {
		return nil, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return cr.CallEdges(ctx, relPath)
}

// Degraded forwards the server's health (see index.noteDegradedServers).
func (h *Hybrid) Degraded() (bool, string) {
	if d, ok := h.lsp.(interface{ Degraded() (bool, string) }); ok {
		return d.Degraded()
	}
	return false, ""
}

// ConcurrentSafe implements the indexer's concurrency marker: server calls
// are serialized by h.mu.
func (h *Hybrid) ConcurrentSafe() bool { return true }
