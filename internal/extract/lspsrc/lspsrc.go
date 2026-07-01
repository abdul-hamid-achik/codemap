// Package lspsrc is an LSP-backed structure extractor. It drives a language
// server (via internal/lsp) to turn a file's documentSymbol response into
// codemap symbols — giving precise, multi-language coverage (TypeScript,
// Python, …) where the pure-Go go/parser backend doesn't apply.
//
// Unlike the go/parser backend, this is stateful: it owns a language-server
// session (one subprocess, initialized once at a project root) and is closed
// when indexing finishes.
package lspsrc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// parseWait caps a per-file retry of an empty documentSymbol. The server answers
// documentSymbol BEFORE it finishes parsing a freshly-opened file (instant when
// idle, but it races ahead under load) and returns EMPTY — which silently dropped
// ~half the files on a large repo. Retrying recovers them and paces codemap to the
// server. Bounded, and gated by hasDeclarations so symbol-less files aren't retried.
const parseWait = 8 * time.Second

// Extractor satisfies extract.Extractor (and CallResolver) by driving a language server.
var (
	_ extract.Extractor    = (*Extractor)(nil)
	_ extract.CallResolver = (*Extractor)(nil)
)

// Extractor wraps an LSP session for one language at one project root.
type Extractor struct {
	ctx    context.Context
	lang   string // codemap language id (e.g. "typescript")
	langID string // LSP languageId (e.g. "typescript")
	root   string // project root, to resolve a relative path to a file:// URI
	client *lsp.Client
	shared bool // true for a Bind()'d extractor sharing another's server; it must not close it
}

// New spawns the language server `command args...`, initializes it at root, and
// returns an extractor for the given codemap language id and LSP languageId.
func New(ctx context.Context, lang, langID, root, command string, args ...string) (*Extractor, error) {
	client, err := lsp.Spawn(ctx, command, args...)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx, root); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Extractor{ctx: ctx, lang: lang, langID: langID, root: root, client: client}, nil
}

// wrapExtractErr turns the bare context-deadline error a stalled language server
// produces (via the per-request LSP timeout) into a message a user can act on,
// instead of a cryptic "context deadline exceeded" in the index summary.
func wrapExtractErr(lang, relPath string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s language server timed out on %s — file skipped", lang, relPath)
	}
	return err
}

// lspLanguageID refines the extractor's default LSP languageId by file
// extension where it changes behavior — notably JSX/TSX, which
// typescript-language-server only parses (and whose `<Component/>` usages it only
// resolves as call edges) under the *react languageIds. Other files use fallback.
func lspLanguageID(relPath, fallback string) string {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	}
	return fallback
}

// Bind returns an extractor for another language served by the SAME server
// process — typescript-language-server handles both TypeScript and JavaScript, so
// codemap spawns it once and binds each language with its own LSP languageId. The
// returned extractor shares the client and does NOT own it: only the original
// (from New) shuts the server down, so Close on a bound extractor is a no-op.
func (e *Extractor) Bind(lang, langID string) *Extractor {
	return &Extractor{ctx: e.ctx, lang: lang, langID: langID, root: e.root, client: e.client, shared: true}
}

// Language implements the extractor contract.
func (e *Extractor) Language() string { return e.lang }

// documentSymbolsParsed queries a file's symbols, retrying an EMPTY result while
// the file plausibly has declarations. The server answers documentSymbol before
// it finishes parsing a freshly-opened file under load and returns empty — which
// silently dropped ~half the files on a big repo. A file with no declaration
// keyword (a barrel / import-only file) is accepted empty immediately, so the
// retry cost falls only on files that should yield symbols; retrying also paces
// codemap to the server's parse rate instead of flooding it.
func (e *Extractor) documentSymbolsParsed(uri string, src []byte) ([]lsp.DocumentSymbol, error) {
	syms, err := e.client.DocumentSymbols(e.ctx, uri)
	if err != nil || len(syms) > 0 || !hasDeclarations(src) {
		return syms, err
	}
	deadline := time.Now().Add(parseWait)
	for backoff := 40 * time.Millisecond; time.Now().Before(deadline); {
		select {
		case <-e.ctx.Done():
			return nil, e.ctx.Err()
		case <-time.After(backoff):
		}
		if syms, err = e.client.DocumentSymbols(e.ctx, uri); err != nil || len(syms) > 0 {
			return syms, err
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	return syms, nil // still empty after waiting — accept it
}

// hasDeclarations is a cheap heuristic: does the source contain a declaration
// construct that should produce a documentSymbol? Used to decide whether an empty
// result is worth retrying (a parse race) or genuine (a re-export/import-only
// file). Covers TS/JS and Python keywords.
func hasDeclarations(src []byte) bool {
	s := string(src)
	for _, kw := range []string{
		"function", "class ", "interface ", "enum ", "=>",
		"const ", "let ", "var ", "type ", "namespace ", "module ", "def ",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// ExtractFile opens relPath (resolved against the project root) in the server and
// maps its document symbols to codemap symbols. src is the file content. The
// 2-arg signature matches extract.Extractor; the abs file:// URI is derived here.
func (e *Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	uri, _ := lsp.URI(filepath.Join(e.root, relPath))
	if err := e.client.DidOpen(uri, lspLanguageID(relPath, e.langID), string(src)); err != nil {
		return nil, err
	}
	syms, err := e.documentSymbolsParsed(uri, src)
	if err != nil {
		return nil, wrapExtractErr(e.lang, relPath, err)
	}
	res := &extract.FileResult{Path: relPath, Language: e.lang}
	lines := strings.Split(string(src), "\n")
	for _, s := range syms {
		appendSymbols(res, lines, e.lang, "", false, s)
	}
	return res, nil
}

// CallEdges resolves the outgoing calls of every function/method in relPath via
// the server's callHierarchy, returning one edge per resolved call (the callee
// located by its declaration position). Implements extract.CallResolver. The file
// must already be open in the server (ExtractFile did didOpen); callHierarchy
// resolves cross-file because the whole project's files were opened first.
func (e *Extractor) CallEdges(ctx context.Context, relPath string) ([]extract.CallEdge, error) {
	uri, _ := lsp.URI(filepath.Join(e.root, relPath))
	syms, err := e.client.DocumentSymbols(ctx, uri)
	if err != nil {
		return nil, err
	}
	var out []extract.CallEdge
	e.walkCallEdges(ctx, uri, "", syms, &out)
	return out, nil
}

func (e *Extractor) walkCallEdges(ctx context.Context, uri, parentFQN string, syms []lsp.DocumentSymbol, out *[]extract.CallEdge) {
	for _, s := range syms {
		fqn := s.Name
		if parentFQN != "" {
			fqn = parentFQN + "." + s.Name
		}
		if isCallable(s.Kind) {
			items, err := e.client.PrepareCallHierarchy(ctx, uri, s.SelectionRange.Start)
			if err == nil && len(items) > 0 {
				calls, _ := e.client.OutgoingCalls(ctx, items[0])
				for _, c := range calls {
					file, external := e.relOf(c.To.URI)
					*out = append(*out, extract.CallEdge{
						FromFQN:  fqn,
						ToFile:   file,
						ToLine:   c.To.Range.Start.Line + 1, // 1-based, matches node StartLine
						External: external,
					})
				}
			}
		}
		e.walkCallEdges(ctx, uri, fqn, s.Children, out)
	}
}

func isCallable(kind int) bool {
	return kind == lsp.SymbolFunction || kind == lsp.SymbolMethod || kind == lsp.SymbolConstructor
}

// relOf turns a callee's file:// URI into a root-relative path; external=true when
// the callee is outside the project (a dependency / lib, with no graph node).
func (e *Extractor) relOf(uri string) (rel string, external bool) {
	p := strings.TrimPrefix(uri, "file://")
	r, err := filepath.Rel(e.root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", true
	}
	return r, false
}

// Close shuts the language server down.
func (e *Extractor) Close() error {
	if e.client == nil || e.shared {
		return nil // a bound extractor doesn't own the server; the original closes it
	}
	_ = e.client.Shutdown(e.ctx)
	return e.client.Exit()
}

// appendSymbols recursively maps an LSP DocumentSymbol (and its children) into
// extract.Symbols. parentFQN builds a dotted fully-qualified name from nesting
// (e.g. ClassName.method), which is how class-based languages scope members.
func appendSymbols(res *extract.FileResult, lines []string, lang, parentFQN string, insideCallable bool, s lsp.DocumentSymbol) {
	kind := mapKind(s)
	// Some servers (notably pyright) report a function's parameters and locals as
	// nested Variable symbols — skip those so the graph isn't cluttered with param
	// nodes. Module- and class-level variables (not inside a callable) are kept.
	if kind == extract.KindVariable && insideCallable {
		kind = ""
	}
	// Language servers report inline anonymous functions as symbols, named after
	// their call site ("map() callback", "defineEventHandler() callback") or
	// "<function>"/"<anonymous>"/"<lambda>". They aren't real, queryable
	// declarations, and on callback-heavy code (Nuxt/React/Vue, array methods, zod)
	// they drown the graph — ~a third of a real app's symbols, and the bulk of its
	// dead-code candidates. Don't index them; still recurse so a genuinely-named
	// nested declaration is kept, parented to the real enclosing scope (not the
	// synthesized junk name).
	anon := isAnonymousCallable(s.Name)
	if anon {
		kind = ""
	}
	fqn := parentFQN
	if !anon {
		if parentFQN == "" {
			fqn = s.Name
		} else {
			fqn = parentFQN + "." + s.Name
		}
	}
	if kind != "" {
		res.Symbols = append(res.Symbols, extract.Symbol{
			Name:      s.Name,
			FQN:       fqn,
			Kind:      kind,
			Language:  lang,
			StartLine: s.Range.Start.Line + 1, // LSP is 0-based; codemap 1-based
			EndLine:   s.Range.End.Line + 1,
			Signature: signature(s),
			Source:    lineSlice(lines, s.Range.Start.Line, s.Range.End.Line),
		})
	}
	// A function/method (incl. an anonymous callback) scopes its params and locals;
	// mark children inside-callable so nested Variable symbols are dropped (above).
	childInside := insideCallable || anon || kind == extract.KindFunction || kind == extract.KindMethod
	for _, child := range s.Children {
		appendSymbols(res, lines, lang, fqn, childInside, child)
	}
}

// isAnonymousCallable reports whether a language-server symbol name is a synthesized
// placeholder for an inline anonymous function rather than a real declaration:
// "<function>"/"<anonymous>"/"<lambda>"/empty, or the "<callee>() callback" form
// servers use for arrows passed inline (e.g. "map() callback"). Such names can never
// be a real identifier (they contain "()"/spaces/angle brackets), so this never drops
// a genuine symbol.
func isAnonymousCallable(name string) bool {
	name = strings.TrimSpace(name)
	switch name {
	case "", "<function>", "<anonymous>", "<lambda>":
		return true
	}
	return strings.HasSuffix(name, ") callback")
}

// mapKind maps an LSP DocumentSymbol to a codemap node kind, returning "" for
// kinds we don't track as graph nodes. It takes the whole symbol (not just the
// kind int) so a Variable/Constant whose Detail looks callable — e.g. a TS
// `const f = () => {}` arrow function — is promoted to a function node.
func mapKind(s lsp.DocumentSymbol) string {
	switch s.Kind {
	case lsp.SymbolFunction:
		return extract.KindFunction
	case lsp.SymbolMethod, lsp.SymbolConstructor:
		return extract.KindMethod
	case lsp.SymbolClass:
		return extract.KindClass
	case lsp.SymbolStruct, lsp.SymbolInterface, lsp.SymbolEnum:
		return extract.KindType
	case lsp.SymbolModule, lsp.SymbolNamespace:
		return extract.KindModule
	case lsp.SymbolVariable, lsp.SymbolConstant:
		if strings.Contains(s.Detail, "=>") || strings.Contains(s.Detail, "(") {
			return extract.KindFunction // arrow function / callable binding
		}
		return extract.KindVariable
	default:
		return ""
	}
}

func signature(s lsp.DocumentSymbol) string {
	if s.Detail != "" {
		return strings.TrimSpace(s.Name + " " + s.Detail)
	}
	return s.Name
}

func lineSlice(lines []string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}
	if start > end || start >= len(lines) {
		return ""
	}
	return strings.Join(lines[start:end+1], "\n")
}
