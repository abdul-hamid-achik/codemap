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
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// Extractor wraps an LSP session for one language at one project root.
type Extractor struct {
	ctx    context.Context
	lang   string // codemap language id (e.g. "typescript")
	langID string // LSP languageId (e.g. "typescript")
	client *lsp.Client
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
	return &Extractor{ctx: ctx, lang: lang, langID: langID, client: client}, nil
}

// Language implements the extractor contract.
func (e *Extractor) Language() string { return e.lang }

// ExtractFile opens absPath in the server and maps its document symbols to
// codemap symbols. relPath is stored on the result; src is the file content.
func (e *Extractor) ExtractFile(absPath, relPath string, src []byte) (*extract.FileResult, error) {
	uri := lsp.URI(absPath)
	if err := e.client.DidOpen(uri, e.langID, string(src)); err != nil {
		return nil, err
	}
	syms, err := e.client.DocumentSymbols(e.ctx, uri)
	if err != nil {
		return nil, err
	}
	res := &extract.FileResult{Path: relPath, Language: e.lang}
	lines := strings.Split(string(src), "\n")
	for _, s := range syms {
		appendSymbols(res, lines, e.lang, "", s)
	}
	return res, nil
}

// Close shuts the language server down.
func (e *Extractor) Close() error {
	if e.client == nil {
		return nil
	}
	_ = e.client.Shutdown(e.ctx)
	return e.client.Exit()
}

// appendSymbols recursively maps an LSP DocumentSymbol (and its children) into
// extract.Symbols. parentFQN builds a dotted fully-qualified name from nesting
// (e.g. ClassName.method), which is how class-based languages scope members.
func appendSymbols(res *extract.FileResult, lines []string, lang, parentFQN string, s lsp.DocumentSymbol) {
	kind := mapKind(s.Kind)
	fqn := s.Name
	if parentFQN != "" {
		fqn = parentFQN + "." + s.Name
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
	for _, child := range s.Children {
		appendSymbols(res, lines, lang, fqn, child)
	}
}

// mapKind maps LSP SymbolKind to a codemap node kind, returning "" for kinds we
// don't track as graph nodes (variables, fields, constants, …).
func mapKind(lspKind int) string {
	switch lspKind {
	case lsp.SymbolFunction:
		return extract.KindFunction
	case lsp.SymbolMethod:
		return extract.KindMethod
	case lsp.SymbolClass, lsp.SymbolStruct, lsp.SymbolInterface:
		return extract.KindType
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
