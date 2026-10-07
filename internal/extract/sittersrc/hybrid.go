package sittersrc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
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

// CallEdges implements extract.CallResolver by delegating to the server. When
// the file parses cleanly, the server is handed tree-sitter's emulation of
// its own documentSymbol answer and asked only for callHierarchy: one round
// trip less per file, and no exposure to the server answering documentSymbol
// empty while it is still loading the file's project.
func (h *Hybrid) CallEdges(ctx context.Context, relPath string) ([]extract.CallEdge, error) {
	cr, ok := h.lsp.(extract.CallResolver)
	if !ok {
		return nil, nil
	}
	var known []lsp.DocumentSymbol
	if ws, ok := h.lsp.(symbolsCallResolver); ok && ws.Root() != "" {
		if src, err := os.ReadFile(filepath.Join(ws.Root(), relPath)); err == nil {
			if p, err := h.parse(relPath, src); err == nil && p.clean {
				known = p.syms
				if known == nil {
					known = []lsp.DocumentSymbol{}
				}
			}
		}
		if known != nil {
			h.mu.Lock()
			defer h.mu.Unlock()
			return ws.CallEdgesWithSymbols(ctx, relPath, known)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return cr.CallEdges(ctx, relPath)
}

// symbolsCallResolver is the server capability Hybrid feeds: callHierarchy
// over a documentSymbol tree supplied by the caller (lspsrc.Extractor).
type symbolsCallResolver interface {
	CallEdgesWithSymbols(ctx context.Context, relPath string, known []lsp.DocumentSymbol) ([]extract.CallEdge, error)
	Root() string
}

// Degraded forwards the server's health (see index.noteDegradedServers).
func (h *Hybrid) Degraded() (bool, string) {
	if d, ok := h.lsp.(interface{ Degraded() (bool, string) }); ok {
		return d.Degraded()
	}
	return false, ""
}

// ForkServer returns a Hybrid with the same tree-sitter extractor over a newly
// spawned process of its server, and that process for the caller to Close.
// Only a language-server backend that can fork (lspsrc) supports it.
func (h *Hybrid) ForkServer(ctx context.Context) (*Hybrid, io.Closer, error) {
	f, ok := h.lsp.(interface {
		Fork(context.Context) (*lspsrc.Extractor, error)
	})
	if !ok {
		return nil, nil, fmt.Errorf("%s server cannot be forked", h.Language())
	}
	srv, err := f.Fork(ctx)
	if err != nil {
		return nil, nil, err
	}
	return WithServer(h.Extractor, srv), srv, nil
}

// Share returns a Hybrid for h's language served by other's server process
// (one typescript-language-server serves TS and JS). ok is false when other's
// server cannot bind another language.
func (h *Hybrid) Share(other *Hybrid) (*Hybrid, bool) {
	srv, ok := other.lsp.(*lspsrc.Extractor)
	mine, ok2 := h.lsp.(*lspsrc.Extractor)
	if !ok || !ok2 {
		return nil, false
	}
	return WithServer(h.Extractor, srv.Bind(h.Language(), mine.LangID())), true
}

// ServerID identifies the server process behind h, so callers can tell which
// languages share one connection.
func (h *Hybrid) ServerID() string {
	if srv, ok := h.lsp.(*lspsrc.Extractor); ok {
		return srv.ServerID()
	}
	return fmt.Sprintf("%p", h.lsp)
}

// BackendRestarts forwards the server's restart count.
func (h *Hybrid) BackendRestarts() (int, string) {
	if r, ok := h.lsp.(interface{ BackendRestarts() (int, string) }); ok {
		return r.BackendRestarts()
	}
	return 0, ""
}

// ResetBackendRestarts forwards a new per-run restart budget.
func (h *Hybrid) ResetBackendRestarts() {
	if r, ok := h.lsp.(interface{ ResetBackendRestarts() }); ok {
		r.ResetBackendRestarts()
	}
}

// BackendCrash forwards the server's backend-crash report.
func (h *Hybrid) BackendCrash() (bool, string) {
	if c, ok := h.lsp.(interface{ BackendCrash() (bool, string) }); ok {
		return c.BackendCrash()
	}
	return false, ""
}

// ConcurrentSafe implements the indexer's concurrency marker: server calls
// are serialized by h.mu.
func (h *Hybrid) ConcurrentSafe() bool { return true }
