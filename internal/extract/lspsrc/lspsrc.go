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
	client languageClient
	shared bool // true for a Bind()'d extractor sharing another's server; it must not close it
}

// languageClient is the narrow LSP port the extractor needs. Keeping it as an
// interface makes capability admission and per-symbol failure handling testable
// without spawning a real language server.
type languageClient interface {
	SupportsDocumentSymbols() bool
	SupportsCallHierarchy() bool
	DidOpen(uri, languageID, text string) error
	DocumentSymbols(ctx context.Context, uri string) ([]lsp.DocumentSymbol, error)
	PrepareCallHierarchy(ctx context.Context, uri string, pos lsp.Position) ([]lsp.CallHierarchyItem, error)
	OutgoingCalls(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyOutgoingCall, error)
	Shutdown(ctx context.Context) error
	Exit() error
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
	if !client.SupportsDocumentSymbols() {
		_ = client.Close()
		return nil, fmt.Errorf("%s does not advertise textDocument/documentSymbol", command)
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
		appendSymbols(res, lines, e.lang, "", false, relPath, s)
	}
	return res, nil
}

// CallEdges resolves the outgoing calls of every function/method in relPath via
// the server's callHierarchy, returning one edge per resolved call (the callee
// located by its declaration position). Implements extract.CallResolver. The file
// must already be open in the server (ExtractFile did didOpen); callHierarchy
// resolves cross-file because the whole project's files were opened first.
func (e *Extractor) CallEdges(ctx context.Context, relPath string) ([]extract.CallEdge, error) {
	if !e.client.SupportsCallHierarchy() {
		return nil, fmt.Errorf("%s language server does not advertise callHierarchy", e.lang)
	}
	uri, _ := lsp.URI(filepath.Join(e.root, relPath))
	syms, err := e.client.DocumentSymbols(ctx, uri)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		// A successful callable leaf still appears here and prepares one hierarchy
		// item with zero outgoing calls. An empty documentSymbol response cannot
		// prove that definitions observed during extraction were analyzed, so keep
		// the file uncovered rather than upgrading an unknown graph to resolved.
		return nil, fmt.Errorf("%s documentSymbol returned no symbols for %s", e.lang, relPath)
	}
	var out []extract.CallEdge
	if err := e.walkCallEdges(ctx, uri, relPath, "", false, syms, &out); err != nil {
		return nil, wrapExtractErr(e.lang, relPath, err)
	}
	return out, nil
}

func (e *Extractor) walkCallEdges(ctx context.Context, uri, relPath, parentFQN string, insideCallable bool, syms []lsp.DocumentSymbol, out *[]extract.CallEdge) error {
	for _, s := range syms {
		class := classifyIndexedSymbol(s, insideCallable, relPath)
		fqn := parentFQN
		if !class.anonymous {
			fqn = symbolFQN(parentFQN, s)
		}
		if class.callable {
			items, err := e.client.PrepareCallHierarchy(ctx, uri, s.SelectionRange.Start)
			if err != nil {
				return fmt.Errorf("prepare call hierarchy for %s: %w", fqn, err)
			}
			if len(items) == 0 {
				// A leaf callable still has a call-hierarchy item and zero outgoing
				// calls. A null/empty prepare response means the server could not
				// resolve this declaration, so marking the whole file covered would
				// turn an unknown relation into a confidently-empty one.
				return fmt.Errorf("prepare call hierarchy for %s returned no item", fqn)
			}
			calls, err := e.client.OutgoingCalls(ctx, items[0])
			if err != nil {
				return fmt.Errorf("outgoing calls for %s: %w", fqn, err)
			}
			for _, c := range calls {
				file, external := e.relOf(c.To.URI)
				*out = append(*out, extract.CallEdge{
					FromFQN:  fqn,
					FromFile: relPath,
					FromLine: s.Range.Start.Line + 1, // 1-based, matches caller node StartLine
					ToFile:   file,
					ToLine:   c.To.Range.Start.Line + 1, // 1-based, matches node StartLine
					External: external,
				})
			}
		}
		childInside := insideCallable || class.anonymous || class.callable
		if err := e.walkCallEdges(ctx, uri, relPath, fqn, childInside, s.Children, out); err != nil {
			return err
		}
	}
	return nil
}

// symbolFQN normalizes ownership across both legal documentSymbol response
// shapes. Hierarchical DocumentSymbol children use parentFQN; flat
// SymbolInformation entries carry their owner in ContainerName.
func symbolFQN(parentFQN string, s lsp.DocumentSymbol) string {
	if parentFQN != "" {
		return parentFQN + "." + s.Name
	}
	if s.ContainerName != "" {
		return s.ContainerName + "." + s.Name
	}
	return s.Name
}

// relOf turns a callee's file:// URI into a root-relative path; external=true when
// the callee is outside the project (a dependency / lib, with no graph node).
//
// P1-02 (residual): this used to do TrimPrefix(uri, "file://") with no
// percent-decoding, so any callee under a path containing a space (or any
// other %-escaped character — common on macOS) never matched e.root and was
// silently marked External, dropping the precise call edge. lsp.PathFromURI
// decodes the URI properly before computing the relative path.
func (e *Extractor) relOf(uri string) (rel string, external bool) {
	p, err := lsp.PathFromURI(uri)
	if err != nil || p == "" {
		p = strings.TrimPrefix(uri, "file://") // best-effort fallback for a malformed URI
	}
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
func appendSymbols(res *extract.FileResult, lines []string, lang, parentFQN string, insideCallable bool, relPath string, s lsp.DocumentSymbol) {
	class := classifyIndexedSymbol(s, insideCallable, relPath)
	fqn := parentFQN
	if !class.anonymous {
		fqn = symbolFQN(parentFQN, s)
	}
	if class.kind != "" {
		res.Symbols = append(res.Symbols, extract.Symbol{
			Name:      s.Name,
			FQN:       fqn,
			Kind:      class.kind,
			Language:  lang,
			StartLine: s.Range.Start.Line + 1, // LSP is 0-based; codemap 1-based
			EndLine:   s.Range.End.Line + 1,
			Signature: signature(s),
			Docstring: extractDocstring(lines, s.Range.Start.Line, lang),
			Source:    lineSlice(lines, s.Range.Start.Line, s.Range.End.Line),
		})
	}
	// A function/method (incl. an anonymous callback) scopes its params and locals;
	// mark children inside-callable so nested Variable symbols are dropped (above).
	childInside := insideCallable || class.anonymous || class.callable
	for _, child := range s.Children {
		appendSymbols(res, lines, lang, fqn, childInside, relPath, child)
	}
}

// indexedSymbolClass is the single normalization boundary shared by structural
// extraction and callHierarchy. If these paths disagree, a callable can be
// indexed without being queried (false resolved coverage), or an anonymous
// callback that is deliberately absent from the graph can fail the whole file.
type indexedSymbolClass struct {
	kind      string
	callable  bool
	anonymous bool
}

func classifyIndexedSymbol(s lsp.DocumentSymbol, insideCallable bool, relPath string) indexedSymbolClass {
	kind := mapKind(s)
	// Some servers (notably pyright) report a function's parameters and locals as
	// nested Variable symbols. Module- and class-level variables remain indexable.
	if kind == extract.KindVariable && insideCallable {
		kind = ""
	}

	callable := isIndexedCallableKind(kind)
	// Language servers report inline anonymous functions as symbols, named after
	// their call site ("map() callback") or as angle-bracket placeholders. They
	// have no durable graph identity, so neither extraction nor callHierarchy may
	// treat them as indexed definitions. We still recurse through them below.
	anonymous := callable && isAnonymousCallable(s.Name)
	if anonymous {
		kind = ""
		callable = false
	} else if callable && isTestFilePath(relPath) {
		// P2-03 (O29): callable declarations in test files are test nodes, but they
		// remain callHierarchy roots just like ordinary functions and methods.
		kind = extract.KindTest
	}
	return indexedSymbolClass{kind: kind, callable: callable, anonymous: anonymous}
}

func isIndexedCallableKind(kind string) bool {
	return kind == extract.KindFunction || kind == extract.KindMethod || kind == extract.KindTest
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

// isTestFilePath reports whether a project-relative path is a test file
// for LSP-backed languages. P2-03 (O29): mirrors gosrc's _test.go check
// but for TS/JS/Python conventions.
func isTestFilePath(relPath string) bool {
	base := strings.ToLower(filepath.Base(relPath))
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") {
		return true
	}
	return false
}

// extractDocstring scans the source lines above a symbol's start line for
// a JSDoc block or // comments (TS/JS/Vue), or takes the first string
// literal inside the range (Python docstring). P2-03 (O28).
func extractDocstring(lines []string, startLine int, lang string) string {
	if startLine < 0 || startLine >= len(lines) {
		return ""
	}
	if lang == "python" {
		for i := startLine + 1; i < len(lines) && i <= startLine+5; i++ {
			line := strings.TrimSpace(lines[i])
			if strings.HasPrefix(line, "\"\"\"") || strings.HasPrefix(line, "'''") {
				if strings.HasSuffix(line, "\"\"\"") && len(line) > 6 {
					return strings.Trim(line, "\"'")
				}
				var parts []string
				parts = append(parts, strings.TrimPrefix(strings.TrimPrefix(line, "\"\"\""), "'''"))
				for j := i + 1; j < len(lines); j++ {
					next := strings.TrimSpace(lines[j])
					parts = append(parts, next)
					if strings.HasSuffix(next, "\"\"\"") || strings.HasSuffix(next, "'''") {
						break
					}
				}
				return strings.Join(parts, " ")
			}
		}
		return ""
	}
	// TS/JS/Vue: scan upward for JSDoc or // comments.
	var comments []string
	for i := startLine - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "//") {
			comments = append([]string{strings.TrimPrefix(line, "//")}, comments...)
			continue
		}
		break
	}
	if len(comments) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(comments, " "))
}
