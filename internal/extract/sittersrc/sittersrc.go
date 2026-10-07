// Package sittersrc is the default structural backend for TypeScript,
// JavaScript, and Python: a pure-Go tree-sitter parse (odvcencio/gotreesitter,
// no CGO) that emulates the documentSymbol tree the language servers would
// return — typescript-language-server's navigation tree and pyright's symbol
// indexer — and hands it to lspsrc.FromDocumentSymbols, the same
// normalization the LSP backend uses. Kinds, FQNs, test classification,
// docstrings, and source slices therefore cannot drift between the backends,
// and position-keyed joins with the LSP's --precise callHierarchy edges keep
// working. Parsing is ~20x faster than waiting on documentSymbol and needs no
// installed language server.
//
// The emulation is deliberately faithful, quirks included (an arrow-function
// const is a variable, a type alias is a variable, `module.exports = {}` is
// "<unknown>"): this backend replaces how symbols are found, not what the graph
// means. internal/extract/sittersrc/parity_test.go diffs it against the live
// servers on a real repository.
package sittersrc

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// MaxFileBytes bounds what the backend parses. Larger files are almost always
// generated or minified bundles, where a parse costs seconds and hundreds of
// MB for symbols nobody navigates; they are reported, not indexed.
const MaxFileBytes = 1 << 20

// grammar identifies one tree-sitter grammar. TypeScript needs two (TSX is a
// separate grammar); JavaScript's grammar parses JSX natively.
type grammar int

const (
	grammarTS grammar = iota
	grammarTSX
	grammarJS
	grammarPython
)

var (
	poolsOnce   sync.Once
	pools       map[grammar]*ts.ParserPool
	langs       map[grammar]*ts.Language
	grammarErrs map[grammar]error
)

// loadGrammars builds the parser pools once per process. Grammar tables load
// lazily (~40ms for all four) so a Go-only index never pays for them.
func loadGrammars() {
	poolsOnce.Do(func() {
		loaders := map[grammar]func() *ts.Language{
			grammarTS:     grammars.TypescriptLanguage,
			grammarTSX:    grammars.TsxLanguage,
			grammarJS:     grammars.JavascriptLanguage,
			grammarPython: grammars.PythonLanguage,
		}
		langs = map[grammar]*ts.Language{}
		pools = map[grammar]*ts.ParserPool{}
		grammarErrs = map[grammar]error{}
		for g, load := range loaders {
			l, err := loadGrammar(load)
			if err != nil {
				grammarErrs[g] = err
				continue
			}
			langs[g] = l
			pools[g] = ts.NewParserPool(l)
		}
	})
}

// loadGrammar turns gotreesitter's load panic (a grammar left out of a
// grammar_subset build) into an error for the files that need it.
func loadGrammar(load func() *ts.Language) (l *ts.Language, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	if l = load(); l == nil {
		return nil, fmt.Errorf("grammar unavailable in this build")
	}
	return l, nil
}

// Languages are the codemap language ids this backend serves.
var Languages = []string{"typescript", "javascript", "python"}

// Extractor parses one codemap language. It is safe for concurrent use.
type Extractor struct {
	lang string
}

// New returns the extractor for a codemap language id ("typescript",
// "javascript", or "python").
func New(lang string) (*Extractor, error) {
	switch lang {
	case "typescript", "javascript", "python":
		return &Extractor{lang: lang}, nil
	}
	return nil, fmt.Errorf("sittersrc: unsupported language %q", lang)
}

// Language implements extract.Extractor.
func (e *Extractor) Language() string { return e.lang }

// grammarFor picks the grammar by extension (vuesrc passes synthetic paths
// ending in .ts/.js), falling back to the extractor's language.
func (e *Extractor) grammarFor(relPath string) grammar {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".tsx":
		return grammarTSX
	case ".ts", ".mts", ".cts":
		return grammarTS
	case ".js", ".jsx", ".mjs", ".cjs":
		return grammarJS
	case ".py", ".pyw", ".pyi":
		return grammarPython
	}
	switch e.lang {
	case "python":
		return grammarPython
	case "javascript":
		return grammarJS
	}
	return grammarTS
}

// ExtractFile implements extract.Extractor. A file whose parse reported
// syntax errors still yields the symbols of the recovered tree.
func (e *Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res, _, err := e.extract(relPath, src)
	return res, err
}

// extract also reports whether the parse was free of syntax errors.
func (e *Extractor) extract(relPath string, src []byte) (*extract.FileResult, bool, error) {
	p, err := e.parse(relPath, src)
	if err != nil {
		return nil, false, err
	}
	res := lspsrc.FromDocumentSymbols(e.lang, relPath, src, p.syms)
	res.References = append(res.References, p.refs...)
	res.Imports = append(res.Imports, p.imports...)
	return res, p.clean, nil
}

// DocumentSymbols parses src and returns the documentSymbol tree the language
// server would have produced for it. Exposed for the parity harness.
func (e *Extractor) DocumentSymbols(relPath string, src []byte) ([]lsp.DocumentSymbol, error) {
	p, err := e.parse(relPath, src)
	return p.syms, err
}

// parsed is one file's parse: the emulated symbol tree plus, for Python,
// the call candidates and import specs the binder emulation yields (TS/JS
// get theirs from tsscan inside lspsrc.FromDocumentSymbols).
type parsed struct {
	syms    []lsp.DocumentSymbol
	refs    []extract.Reference
	imports []string
	clean   bool
}

func (e *Extractor) parse(relPath string, src []byte) (parsed, error) {
	if len(src) > MaxFileBytes {
		return parsed{}, fmt.Errorf("%s: %d bytes exceeds the %d-byte structural parse limit (generated or minified?)", relPath, len(src), MaxFileBytes)
	}
	loadGrammars()
	g := e.grammarFor(relPath)
	if gerr := grammarErrs[g]; gerr != nil {
		return parsed{}, fmt.Errorf("%s: %w", relPath, gerr)
	}
	tree, err := pools[g].Parse(src)
	if err != nil {
		return parsed{}, fmt.Errorf("%s: parse: %w", relPath, err)
	}
	if tree == nil {
		return parsed{}, fmt.Errorf("%s: parse returned no tree", relPath)
	}
	defer tree.Release()
	root := tree.RootNode()
	if root == nil {
		return parsed{}, fmt.Errorf("%s: parse returned no root", relPath)
	}
	clean := !root.HasError()
	if g == grammarPython {
		w := bindPython(langs[g], src, root)
		return parsed{syms: w.emit(nil), refs: pythonCallRefs(w, root, relPath), imports: dedupeStrings(w.imports), clean: clean}, nil
	}
	return parsed{syms: tsSymbols(langs[g], src, root, g == grammarJS), clean: clean}, nil
}

// node helpers shared by both emulators.

func line(p ts.Point) int { return int(p.Row) }

func text(src []byte, n *ts.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

func symbol(name string, kind, start, end int, children []lsp.DocumentSymbol) lsp.DocumentSymbol {
	r := lsp.Range{Start: lsp.Position{Line: start}, End: lsp.Position{Line: end}}
	return lsp.DocumentSymbol{Name: name, Kind: kind, Range: r, SelectionRange: r, Children: children}
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
