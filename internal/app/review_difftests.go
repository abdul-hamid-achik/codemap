package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// diffTestFile is a test file touched by the diff under review. content is the
// file's CODE with comments and string literals blanked out (see
// stripCommentsAndStrings), so a name mentioned only in prose never links.
type diffTestFile struct {
	path    string
	content []byte
}

// loadDiffTestFiles reads the changed or untracked test files of the diff (not
// deleted ones) from the working tree. These may be missing from the index —
// the common post-edit flow reviews before reindexing — so the indexed
// heuristic scan alone cannot see a brand-new test. Files in a language the
// comment/string lexer does not know are skipped: an unscannable file is never
// treated as evidence of coverage.
func loadDiffTestFiles(root string, changed []git.ChangedFile) []diffTestFile {
	var out []diffTestFile
	for _, cf := range changed {
		if cf.Status == "D" || !isTestFilePath(cf.Path) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, cf.Path))
		if err != nil {
			continue
		}
		code := stripCommentsAndStrings(filepath.Ext(cf.Path), b)
		if code == nil {
			continue
		}
		out = append(out, diffTestFile{path: cf.Path, content: code})
	}
	return out
}

// diffBareName is the final dotted component of a symbol name (Type.Method ->
// Method), the form a test file would reference.
func diffBareName(symbol string) string {
	if i := strings.LastIndex(symbol, "."); i >= 0 {
		return symbol[i+1:]
	}
	return symbol
}

// diffTestCoverage links a changed symbol to the diff's own test files: a
// changed/new test file whose CODE (comments and string literals excluded)
// references the symbol by bare name (word boundary) counts as covering it for
// the coverage verdict. Same-name false positives are bounded by requiring, for
// a Go symbol, that the test file lives in the symbol's directory (same
// package, including the _test external package).
func diffTestCoverage(files []diffTestFile, sym SymbolRef) []ImpactNode {
	name := diffBareName(sym.Symbol)
	if name == "" || len(files) == 0 {
		return nil
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return nil
	}
	goSym := strings.EqualFold(filepath.Ext(sym.File), ".go")
	var out []ImpactNode
	for _, f := range files {
		if goSym && (!strings.EqualFold(filepath.Ext(f.path), ".go") || filepath.Dir(f.path) != filepath.Dir(sym.File)) {
			continue
		}
		if !re.Match(f.content) {
			continue
		}
		out = append(out, ImpactNode{
			Symbol: filepath.Base(f.path), FQN: f.path, Kind: graph.KindTest,
			File: f.path, StartLine: 1, Heuristic: true, Confidence: ConfidenceCandidate,
		})
	}
	return out
}

// definitionCounter returns a function reporting how many definitions in the
// project's index share a symbol name. When the index cannot be consulted it
// reports 2 for every name, so callers that need a unique name fail closed.
func (svc *Service) definitionCounter(cwd string) func(string) int {
	unknown := func(string) int { return 2 }
	g, err := svc.s.Graph()
	if err != nil {
		return unknown
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return unknown
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return unknown
	}
	return func(symbol string) int {
		nodes, err := g.FindNodesBySymbol(p.ID, symbol)
		if err != nil {
			return 2
		}
		return len(nodes)
	}
}

// withDiffTests wraps a per-symbol impact analyzer so the diff's own test files
// can count toward the review COVERAGE verdict for the changed symbols they
// reference. The link is deliberately weaker than a call-graph or indexed
// heuristic test:
//
//   - it is recorded on the report's private diffTests only — never in Tests,
//     Untested, untested_symbols, the risk untested factor, or the
//     --fail-on-untested gate, which stay call-graph/index based;
//   - it is skipped when the symbol's name has more than one definition in the
//     project (defCount > 1), since a text scan cannot tell them apart — the same
//     guard impactFromLocations applies to its heuristic test scan;
//   - test files are scanned as code, with comments and string literals removed.
func withDiffTests(analyze func(SymbolRef) (*ImpactReport, error), files []diffTestFile, defCount func(string) int) func(SymbolRef) (*ImpactReport, error) {
	if len(files) == 0 {
		return analyze
	}
	return func(s SymbolRef) (*ImpactReport, error) {
		imp, err := analyze(s)
		if err != nil || imp == nil || !imp.Found {
			return imp, err
		}
		if defCount == nil || defCount(diffBareName(s.Symbol)) > 1 {
			return imp, nil
		}
		have := map[string]bool{}
		for _, t := range imp.Tests {
			have[t.File] = true
		}
		var names []string
		for _, t := range diffTestCoverage(files, s) {
			if have[t.File] {
				continue
			}
			have[t.File] = true
			imp.diffTests = append(imp.diffTests, t)
			names = append(names, t.File)
		}
		if len(names) > 0 {
			imp.Note = joinNote(imp.Note, fmt.Sprintf("%d test file(s) in the same diff reference %q (%s) — counted toward coverage only; untested/risk/--fail-on-untested still follow the call graph", len(names), s.Symbol, strings.Join(names, ", ")))
		}
		return imp, nil
	}
}

// lexSpec describes the comment and string syntax of one language family for
// stripCommentsAndStrings.
type lexSpec struct {
	lineComments []string // e.g. "//", "#"
	blockOpen    string   // "" when the language has no block comment
	blockClose   string
	quotes       string // single-line string delimiters with backslash escapes
	rawMulti     string // multi-line string delimiters (Go raw strings, JS templates)
	rawEscapes   bool   // whether rawMulti strings honour backslash escapes
	triple       bool   // Python-style triple-quoted strings
}

func lexSpecFor(ext string) *lexSpec {
	switch strings.ToLower(ext) {
	case ".go":
		return &lexSpec{lineComments: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: `"'`, rawMulti: "`"}
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return &lexSpec{lineComments: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: `"'`, rawMulti: "`", rawEscapes: true}
	case ".py":
		return &lexSpec{lineComments: []string{"#"}, quotes: `"'`, triple: true}
	case ".rb":
		return &lexSpec{lineComments: []string{"#"}, quotes: `"'`}
	case ".lua":
		return &lexSpec{lineComments: []string{"--"}, blockOpen: "--[[", blockClose: "]]", quotes: `"'`}
	}
	return nil
}

// stripCommentsAndStrings returns src with every comment and string literal
// blanked to spaces (newlines are kept, so offsets and line counts are
// preserved). It is a deliberately simple lexer — enough to stop a test file's
// prose or message strings from looking like a reference to a symbol — and
// returns nil for a language it does not know.
func stripCommentsAndStrings(ext string, src []byte) []byte {
	spec := lexSpecFor(ext)
	if spec == nil {
		return nil
	}
	out := make([]byte, len(src))
	copy(out, src)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	has := func(i int, tok string) bool {
		return tok != "" && i+len(tok) <= len(src) && string(src[i:i+len(tok)]) == tok
	}
	i := 0
scan:
	for i < len(src) {
		// Block comment (checked before line comments: Lua's "--[[" starts with "--").
		if has(i, spec.blockOpen) {
			end := strings.Index(string(src[i+len(spec.blockOpen):]), spec.blockClose)
			stop := len(src)
			if end >= 0 {
				stop = i + len(spec.blockOpen) + end + len(spec.blockClose)
			}
			blank(i, stop)
			i = stop
			continue
		}
		for _, lc := range spec.lineComments {
			if has(i, lc) {
				stop := i
				for stop < len(src) && src[stop] != '\n' {
					stop++
				}
				blank(i, stop)
				i = stop
				continue scan
			}
		}
		c := src[i]
		if spec.triple && (has(i, `"""`) || has(i, `'''`)) {
			q := string(src[i : i+3])
			end := strings.Index(string(src[i+3:]), q)
			stop := len(src)
			if end >= 0 {
				stop = i + 3 + end + 3
			}
			blank(i, stop)
			i = stop
			continue
		}
		if strings.IndexByte(spec.quotes, c) >= 0 {
			stop := i + 1
			for stop < len(src) && src[stop] != c && src[stop] != '\n' {
				if src[stop] == '\\' {
					stop++
				}
				stop++
			}
			if stop < len(src) && src[stop] == c {
				stop++
			}
			if stop > len(src) {
				stop = len(src)
			}
			blank(i, stop)
			i = stop
			continue
		}
		if spec.rawMulti != "" && strings.IndexByte(spec.rawMulti, c) >= 0 {
			stop := i + 1
			for stop < len(src) && src[stop] != c {
				if spec.rawEscapes && src[stop] == '\\' {
					stop++
				}
				stop++
			}
			if stop < len(src) {
				stop++
			}
			if stop > len(src) {
				stop = len(src)
			}
			blank(i, stop)
			i = stop
			continue
		}
		i++
	}
	return out
}
