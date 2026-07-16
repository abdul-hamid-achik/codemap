// Package rubysrc extracts structural symbols and name-based references from
// Ruby source with a pure-Go line scanner (no CGO, no language server) —
// the same T1 fidelity the TS/JS base path has: definition nodes, call
// references that the indexer resolves by name, and require imports.
//
// The scanner is indentation-and-`end`-aware rather than a full parser:
// module/class/def blocks open a frame; a frame closes on the matching
// dedented `end` (or a same-line `end` / endless `def x = expr`). That is
// exact for conventionally formatted Ruby and degrades to slightly-off
// EndLines (never crashes, never drops symbols) on hand-crammed layouts —
// the accepted trade-off for a dependency-free backend, mirroring vuesrc's
// documented regex-over-parser choice.
package rubysrc

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the pure-Go Ruby backend.
type Extractor struct{}

// New returns a Ruby extractor.
func New() *Extractor { return &Extractor{} }

// Language implements extract.Extractor.
func (*Extractor) Language() string { return "ruby" }

var (
	moduleRe = regexp.MustCompile(`^module\s+([A-Z][\w:]*)`)
	classRe  = regexp.MustCompile(`^class\s+([A-Z][\w:]*)`)
	sclassRe = regexp.MustCompile(`^class\s*<<\s*self\b`)
	defRe    = regexp.MustCompile(`^def\s+(self\.)?([A-Za-z_]\w*[!?=]?)`)
	// Endless method (Ruby 3): def foo(...) = expr / def foo = expr. The `=`
	// must not be immediately followed by `(` — that's a setter (def name=(v)),
	// which is a normal block method.
	endlessRe = regexp.MustCompile(`^def\s+(?:self\.)?[A-Za-z_]\w*[!?]?\s*(?:\([^)]*\))?\s*=\s*[^=(\s][^=]*`)

	requireRe = regexp.MustCompile(`^\s*(require_relative|require)\s*\(?\s*['"]([^'"]+)['"]`)

	// Method-visibility/scope modifiers that may prefix a def on one line
	// (`private def x` — idiomatic Rails). The def is extracted as if bare.
	defModifierRe = regexp.MustCompile(`^(?:private|protected|public|module_function|private_class_method|public_class_method)\s+(def\s.*)$`)

	// Heredoc openers. The bare form mirrors Ruby's own heuristic: `<<IDENT`
	// with no space is a heredoc when the identifier starts uppercase
	// (`x = <<~SQL`, `a <<HEREDOC`); a spaced `a << b` shove never matches.
	// Detected on the string-blanked line so `"a <<FOO"` (data) cannot match;
	// the quoted form's terminator is recovered from the unblanked line at
	// the same offsets (blanking is length-preserving).
	heredocBareRe   = regexp.MustCompile(`<<[~-]?([A-Z_][A-Za-z0-9_]*)`)
	heredocQuotedRe = regexp.MustCompile(`<<[~-]?(['"])`)

	// name-based references inside a body
	callParenRe = regexp.MustCompile(`(\.?)\s*\b([a-z_]\w*[!?]?)\s*\(`)
	dotCallRe   = regexp.MustCompile(`[\w\)\]]\.\s*([a-z_]\w*[!?]?)\b`)
	constNewRe  = regexp.MustCompile(`\b([A-Z]\w*(?:::[A-Z]\w*)*)\.new\b`)
	mixinRe     = regexp.MustCompile(`^(?:include|extend|prepend)\s+([A-Z][\w:]*)`)
)

// rubyKeywords never resolve to project methods; excluding them keeps the
// name graph from linking `if`/`raise`/`yield` sites.
var rubyKeywords = map[string]bool{
	"if": true, "unless": true, "while": true, "until": true, "for": true,
	"case": true, "when": true, "then": true, "do": true, "end": true,
	"begin": true, "rescue": true, "ensure": true, "else": true, "elsif": true,
	"def": true, "class": true, "module": true, "return": true, "yield": true,
	"super": true, "self": true, "nil": true, "true": true, "false": true,
	"and": true, "or": true, "not": true, "raise": true, "lambda": true,
	"proc": true, "loop": true, "require": true, "require_relative": true,
	"new": true, "attr_accessor": true, "attr_reader": true, "attr_writer": true,
	"private": true, "public": true, "protected": true, "puts": true, "p": true,
}

type frame struct {
	kind      string // "module", "class", "sclass", "def"
	name      string // declared name ("" for class << self)
	fqn       string
	indent    int
	startLine int
	symIndex  int // index into result symbols, -1 for anonymous frames
}

// ExtractFile implements extract.Extractor.
func (*Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "ruby"}
	lines := strings.Split(string(src), "\n")
	isTest := isTestPath(relPath)

	var stack []frame
	var pendingDoc []string
	var heredocs []string // pending heredoc terminators, in opener order
	inCommentBlock := false

	closeFrame := func(f frame, endLine int) {
		if f.symIndex >= 0 {
			res.Symbols[f.symIndex].EndLine = endLine
			res.Symbols[f.symIndex].Source = lineSlice(lines, f.startLine, endLine)
		}
	}
	containerFQN := func() string {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].fqn != "" {
				return stack[i].fqn
			}
		}
		return ""
	}
	insideDef := func() *frame {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == "def" {
				return &stack[i]
			}
		}
		return nil
	}

	for i, raw := range lines {
		lineNo := i + 1
		// Heredoc bodies and =begin/=end comment blocks are content, not code:
		// a `def`, `end`, or `helper(x)` inside them must not shape the graph.
		if len(heredocs) > 0 {
			if strings.TrimSpace(raw) == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		if inCommentBlock {
			if strings.HasPrefix(raw, "=end") {
				inCommentBlock = false
			}
			continue
		}
		if strings.HasPrefix(raw, "=begin") {
			inCommentBlock = true
			continue
		}
		code := stripComment(raw)
		sanitized := blankStrings(code)
		heredocs = append(heredocs, heredocTerminators(sanitized, code)...)
		trimmed := strings.TrimSpace(code)
		if trimmed == "" {
			if strings.HasPrefix(strings.TrimSpace(raw), "#") {
				pendingDoc = append(pendingDoc, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#")))
			} else {
				pendingDoc = nil
			}
			continue
		}
		indent := indentOf(raw)
		// `private def x` declares x; parse the decl as if unprefixed.
		decl := stripDefModifier(trimmed)

		// A dedented declaration or `end` closes every deeper frame.
		if trimmed == "end" || strings.HasPrefix(trimmed, "end ") || strings.HasPrefix(trimmed, "end.") {
			for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
				closeFrame(stack[len(stack)-1], lineNo)
				stack = stack[:len(stack)-1]
				break // one `end` closes one frame
			}
			pendingDoc = nil
			continue
		}
		for len(stack) > 0 && isDecl(decl) && stack[len(stack)-1].indent >= indent {
			// Malformed/cramped layout: a new declaration at or above the top
			// frame's indent means that frame already ended.
			closeFrame(stack[len(stack)-1], lineNo-1)
			stack = stack[:len(stack)-1]
		}

		doc := strings.Join(pendingDoc, " ")
		pendingDoc = nil

		if m := requireRe.FindStringSubmatch(code); m != nil {
			spec := m[2]
			if m[1] == "require_relative" {
				spec = "./" + spec
			}
			res.Imports = append(res.Imports, spec)
			continue
		}

		switch {
		case sclassRe.MatchString(trimmed):
			stack = append(stack, frame{kind: "sclass", indent: indent, startLine: lineNo, symIndex: -1})
			continue
		case moduleRe.MatchString(trimmed), classRe.MatchString(trimmed):
			var name, kind string
			if m := moduleRe.FindStringSubmatch(trimmed); m != nil {
				name, kind = m[1], extract.KindModule
			} else {
				name, kind = classRe.FindStringSubmatch(trimmed)[1], extract.KindClass
			}
			name = strings.ReplaceAll(name, "::", ".")
			fqn := joinFQN(containerFQN(), name)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: lastSegment(name), FQN: fqn, Kind: kind, Language: "ruby",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			if !oneLineDef(trimmed) { // `class Foo; end` needs no frame
				stack = append(stack, frame{kind: kindWord(kind), name: name, fqn: fqn, indent: indent, startLine: lineNo, symIndex: len(res.Symbols) - 1})
			}
			continue
		case defRe.MatchString(decl):
			m := defRe.FindStringSubmatch(decl)
			name := m[2]
			fqn := joinFQN(containerFQN(), name)
			kind := extract.KindFunction
			if len(stack) > 0 || m[1] != "" {
				kind = extract.KindMethod
			}
			if isTest {
				kind = extract.KindTest
			}
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: kind, Language: "ruby",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			symIdx := len(res.Symbols) - 1
			// Endless/one-line detection runs on the string-blanked decl so a
			// `=` or `; end` INSIDE a default-argument string can't fake one.
			sdecl := blankStrings(decl)
			if endlessRe.MatchString(sdecl) || oneLineDef(sdecl) {
				res.Symbols[symIdx].Source = raw
				// The body shares the def line: collect its refs (skip the
				// signature portion so the method itself isn't a callee).
				if _, body, ok := strings.Cut(sdecl, "="); ok {
					collectRefs(res, fqn, body, lineNo)
				} else if _, body, ok := strings.Cut(sdecl, ";"); ok {
					collectRefs(res, fqn, body, lineNo)
				}
			} else {
				stack = append(stack, frame{kind: "def", name: name, fqn: fqn, indent: indent, startLine: lineNo, symIndex: symIdx})
			}
			continue
		}

		// Body line: attribute references to the innermost def, else the
		// enclosing container, else the file (framework-style top-level wiring,
		// e.g. a Rails routes file or Sinatra DSL).
		from := relPath
		if d := insideDef(); d != nil {
			from = d.fqn
		} else if c := containerFQN(); c != "" {
			from = c
		}
		if m := mixinRe.FindStringSubmatch(trimmed); m != nil {
			res.References = append(res.References, extract.Reference{
				From: from, To: lastSegment(strings.ReplaceAll(m[1], "::", ".")),
				Kind: extract.RefReferences, Line: lineNo, Qualified: true,
			})
			continue
		}
		// Scan the string-blanked line: `log("foo(1)")` must reference log,
		// never foo — while `"#{format_name(user)}"` interpolation is code
		// and keeps its reference.
		collectRefs(res, from, sanitized, lineNo)
	}
	for len(stack) > 0 {
		closeFrame(stack[len(stack)-1], len(lines))
		stack = stack[:len(stack)-1]
	}
	return res, nil
}

// collectRefs appends call/instantiation references found in one code line.
func collectRefs(res *extract.FileResult, from, code string, lineNo int) {
	seen := map[string]bool{}
	add := func(to string, kind string, qualified bool) {
		if to == "" || rubyKeywords[to] || seen[kind+to] || to == from {
			return
		}
		seen[kind+to] = true
		res.References = append(res.References, extract.Reference{
			From: from, To: to, Kind: kind, Line: lineNo, Qualified: qualified,
		})
	}
	for _, m := range callParenRe.FindAllStringSubmatch(code, -1) {
		add(m[2], extract.RefCalls, m[1] == ".")
	}
	for _, m := range dotCallRe.FindAllStringSubmatch(code, -1) {
		add(m[1], extract.RefCalls, true)
	}
	for _, m := range constNewRe.FindAllStringSubmatch(code, -1) {
		add(lastSegment(strings.ReplaceAll(m[1], "::", ".")), extract.RefReferences, true)
	}
}

// stripDefModifier unwraps `private def x`-style one-line visibility
// modifiers so the def parses as if bare.
func stripDefModifier(trimmed string) string {
	if m := defModifierRe.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	return trimmed
}

// blankStrings returns a same-length copy of a comment-stripped line with
// string-literal contents blanked to spaces — except `#{...}` interpolation
// bodies inside double-quoted strings, which are code and keep their text.
// Length preservation keeps byte offsets aligned with the input, which the
// heredoc scanner relies on.
func blankStrings(line string) string {
	out := []byte(line)
	var quote byte
	depth := 0 // > 0 while inside a #{...} interpolation body
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0 && depth > 0: // interpolation body: code, keep text
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
			}
		case quote != 0:
			switch {
			case c == '\\':
				out[i] = ' '
				if i+1 < len(line) {
					out[i+1] = ' '
				}
				i++
			case c == quote:
				quote = 0
			case quote == '"' && c == '#' && i+1 < len(line) && line[i+1] == '{':
				depth = 1
				i++ // keep "#{"
			default:
				out[i] = ' '
			}
		case c == '\'' || c == '"':
			quote = c
		}
	}
	return string(out)
}

// heredocTerminators returns the terminators of every heredoc opened on this
// line, in opener order. sanitized is the string-blanked line (so `<<` inside
// string data never matches); code is the aligned unblanked line, used to
// recover a quoted terminator's name (its letters are blanked in sanitized).
func heredocTerminators(sanitized, code string) []string {
	type opener struct {
		pos  int
		term string
	}
	var ops []opener
	for _, m := range heredocBareRe.FindAllStringSubmatchIndex(sanitized, -1) {
		ops = append(ops, opener{m[0], sanitized[m[2]:m[3]]})
	}
	for _, m := range heredocQuotedRe.FindAllStringSubmatchIndex(sanitized, -1) {
		q := sanitized[m[2]]
		rest := code[m[3]:]
		if end := strings.IndexByte(rest, q); end > 0 {
			ops = append(ops, opener{m[0], rest[:end]})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].pos < ops[j].pos })
	terms := make([]string, 0, len(ops))
	for _, o := range ops {
		terms = append(terms, o.term)
	}
	return terms
}

// stripComment removes a trailing # comment, respecting single/double quotes
// (a # inside a string or a #{} interpolation stays).
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#':
			return line[:i] // interpolation (#{...}) only exists inside quotes, handled above
		}
	}
	return line
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 8
		default:
			return n
		}
	}
	return n
}

func isDecl(trimmed string) bool {
	return moduleRe.MatchString(trimmed) || classRe.MatchString(trimmed) ||
		sclassRe.MatchString(trimmed) || defRe.MatchString(trimmed)
}

// oneLineDef reports a `def x; ...; end` single-line definition.
func oneLineDef(trimmed string) bool {
	return strings.Contains(trimmed, ";") && (strings.HasSuffix(trimmed, "end") || strings.Contains(trimmed, " end"))
}

func joinFQN(container, name string) string {
	if container == "" {
		return name
	}
	return container + "." + name
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func kindWord(kind string) string {
	if kind == extract.KindModule {
		return "module"
	}
	return "class"
}

// isTestPath mirrors Ruby test conventions: *_test.rb, *_spec.rb, test_*.rb.
func isTestPath(relPath string) bool {
	base := strings.ToLower(filepath.Base(relPath))
	return strings.HasSuffix(base, "_test.rb") || strings.HasSuffix(base, "_spec.rb") ||
		(strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".rb"))
}

func lineSlice(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}
