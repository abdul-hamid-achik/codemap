// Package csssrc extracts CSS selector definitions and stylesheet imports
// from CSS, SCSS, Sass (indented), and Less sources with a pure-Go scanner
// (no CGO, no language server) — the stylesheet analogue of the other
// name-based backends. It emits one "selector" node per distinct class/id
// token defined in a file (Name keeps the leading `.`/`#` sigil, which
// namespaces selector symbols away from every code identifier), so the
// indexer's existing name-resolution pass can link JSX className / HTML class
// references to their defining rules with zero indexer changes.
//
// Nested SCSS/Sass/Less rules are flattened against their parent selectors
// (`&` substitution, descendant joining, comma cartesian capped at 16
// combinations) so `.card { &.active {} }` defines `.active`, discoverable
// at its real flattened form `.card.active`. Interpolated selector parts
// (`#{$x}`, `@{x}`) are not statically resolvable and are skipped. At-rule
// wrappers (@media/@supports/@layer/@container/@scope) are transparent;
// everything else at-rule-shaped (@keyframes, @mixin, @font-face, …) is
// opaque and contributes no selector nodes. Like the other line scanners,
// pathological layouts degrade to slightly-off line spans — never dropped
// symbols.
package csssrc

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the pure-Go stylesheet backend. One instance serves exactly one
// codemap language id; the indexer registers four (css, scss, sass, less).
type Extractor struct{ lang string }

// New returns a stylesheet extractor for lang ∈ {"css","scss","sass","less"}.
func New(lang string) *Extractor { return &Extractor{lang: lang} }

// Language implements extract.Extractor.
func (e *Extractor) Language() string { return e.lang }

// Rule is one style rule after nesting flattening: the absolute selector
// variants it applies to, its 1-based line span, and its raw source text
// (prelude + body — feeds node Source for embedding).
type Rule struct {
	Selectors []string
	StartLine int
	EndLine   int
	Text      string
}

// ScanRules scans stylesheet source into flattened rules. indented selects
// the Sass indented syntax; otherwise brace syntax (CSS/SCSS/Less) with `//`
// line comments enabled. Exported so an HTML backend can later delegate
// <style> block contents here.
func ScanRules(src []byte, indented bool) []Rule { return scanRules(src, indented, true) }

func scanRules(src []byte, indented, lineComments bool) []Rule {
	if indented {
		return scanIndentedRules(src)
	}
	return scanBraceRules(src, lineComments)
}

// ExtractFile implements extract.Extractor.
func (e *Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: e.lang}
	// Plain CSS has no `//` line comments; treating them as comments there
	// could eat `url(//host/…)` tokens mid-declaration.
	lineComments := e.lang != "css"
	rules := scanRules(src, e.lang == "sass", lineComments)
	emitSelectorSymbols(res, e.lang, rules)
	res.Imports = scanImports(src, lineComments)
	return res, nil
}

// sentinel replaces interpolation spans (`#{…}` scss, `@{…}` less) in the
// structural view: a selector token adjacent to it is dynamic and skipped.
const sentinel = '\x00'

// frame classification for nesting contexts.
type frameKind int

const (
	ruleFrame        frameKind = iota // a selector rule — emits, participates in flattening
	transparentFrame                  // @media-like wrapper — invisible to flattening
	opaqueFrame                       // @keyframes/@mixin/property-namespace — nothing inside emits
)

// transparentAtRules wrap rules without changing selector meaning (media
// context tracking is out of scope v1).
var transparentAtRules = map[string]bool{
	"@media": true, "@supports": true, "@layer": true, "@container": true, "@scope": true,
}

type cssFrame struct {
	kind      frameKind
	selectors []string // flattened; ruleFrame only
	startLine int
	startOff  int // byte offset of the prelude's first non-space char in src
}

// scanBraceRules scans brace-syntax stylesheets (css/scss/less) with a brace
// stack over the comment/string-sanitized view; each rule frame carries its
// flattened selector list and emits a Rule when its `}` closes.
func scanBraceRules(src []byte, lineComments bool) []Rule {
	code, _ := sanitizeCSS(src, lineComments)
	text := string(code)
	lineOf := newLineOffsets(text)

	var rules []Rule
	var stack []cssFrame
	preludeStart := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ';':
			preludeStart = i + 1 // statement (import, declaration, &:extend) — not a prelude
		case '{':
			prelude := text[preludeStart:i]
			lead := len(prelude) - len(strings.TrimLeft(prelude, " \t\r\n"))
			f := classifyFrame(strings.TrimSpace(prelude), stack)
			f.startOff = preludeStart + lead
			f.startLine = lineOf(f.startOff)
			stack = append(stack, f)
			preludeStart = i + 1
		case '}':
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if f.kind == ruleFrame {
					rules = append(rules, Rule{
						Selectors: f.selectors,
						StartLine: f.startLine,
						EndLine:   lineOf(i),
						Text:      string(src[f.startOff : i+1]),
					})
				}
			}
			preludeStart = i + 1
		}
	}
	// Unbalanced braces: close remaining rule frames at EOF — degraded spans,
	// never dropped symbols.
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.kind == ruleFrame && len(text) > 0 {
			rules = append(rules, Rule{
				Selectors: f.selectors,
				StartLine: f.startLine,
				EndLine:   lineOf(len(text) - 1),
				Text:      string(src[f.startOff:]),
			})
		}
	}
	return rules
}

// classifyFrame decides what kind of nesting context a `{` opens, given the
// trimmed prelude text and the enclosing stack.
func classifyFrame(prelude string, stack []cssFrame) cssFrame {
	// Everything inside an opaque frame stays opaque (a rule-shaped line in a
	// @mixin body is a template, not a definition).
	for _, f := range stack {
		if f.kind == opaqueFrame {
			return cssFrame{kind: opaqueFrame}
		}
	}
	if strings.HasPrefix(prelude, "@") {
		word := prelude
		if i := strings.IndexAny(prelude, " \t\r\n("); i >= 0 {
			word = prelude[:i]
		}
		if transparentAtRules[strings.ToLower(word)] {
			return cssFrame{kind: transparentFrame}
		}
		// @keyframes/@font-face/@page/@mixin/@function/@include/unknown @ —
		// their bodies define no linkable selectors.
		return cssFrame{kind: opaqueFrame}
	}
	// SCSS property-namespace block (`font: { weight: bold }`): a trailing `:`
	// before `{` marks a declaration, not a selector. Empty preludes likewise.
	if prelude == "" || strings.HasSuffix(prelude, ":") {
		return cssFrame{kind: opaqueFrame}
	}
	return cssFrame{
		kind:      ruleFrame,
		selectors: flattenSelectors(nearestRuleSelectors(stack), splitTopLevelCommas(prelude)),
	}
}

// nearestRuleSelectors returns the flattened selectors of the innermost
// enclosing rule frame, skipping transparent at-rule wrappers.
func nearestRuleSelectors(stack []cssFrame) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == ruleFrame {
			return stack[i].selectors
		}
	}
	return nil
}

// Selector token shapes. -?[_a-zA-Z] leads (CSS ident rules, `-moz`-style
// vendor names included); \w and - continue. Unicode/escaped identifiers are
// out of scope.
var (
	classTokenRe = regexp.MustCompile(`\.(-?[_a-zA-Z][\w-]*)`)
	idTokenRe    = regexp.MustCompile(`#(-?[_a-zA-Z][\w-]*)`)
	attrSpanRe   = regexp.MustCompile(`\[[^\]]*\]`)
)

// selectorTokens extracts the distinct class (`.btn`) and id (`#hero`) tokens
// a flattened selector list defines, in first-seen order. Attribute selector
// spans are removed first (an unquoted `[data-x=a.b]` must not read as a
// class); a token cut short by an interpolation sentinel or an escape (`\`,
// e.g. Tailwind's compiled `.hover\:underline`) is dynamic/partial → skipped.
// Element/pseudo-only selectors yield nothing — by design they are not
// indexed (nothing references them by name).
func selectorTokens(selectors []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sel := range selectors {
		sel = attrSpanRe.ReplaceAllString(sel, "")
		for _, re := range []*regexp.Regexp{classTokenRe, idTokenRe} {
			for _, m := range re.FindAllStringSubmatchIndex(sel, -1) {
				if m[1] < len(sel) && (sel[m[1]] == sentinel || sel[m[1]] == '\\') {
					continue // `.mod-#{$name}`, `.hover\:x` — not a static full token
				}
				tok := sel[m[0]:m[1]]
				if !seen[tok] {
					seen[tok] = true
					out = append(out, tok)
				}
			}
		}
	}
	return out
}

// emitSelectorSymbols dedupes rule tokens per file and appends one selector
// Symbol per distinct token. The first defining rule (by line) wins the node's
// position, Signature (its full flattened selector list), and Source (its raw
// text — what embeddings index); later rules only bump the Docstring count.
func emitSelectorSymbols(res *extract.FileResult, lang string, rules []Rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].StartLine != rules[j].StartLine {
			return rules[i].StartLine < rules[j].StartLine
		}
		return rules[i].EndLine < rules[j].EndLine
	})
	index := map[string]int{} // token → res.Symbols index
	extra := map[string]int{} // token → additional defining rules
	for _, r := range rules {
		for _, name := range selectorTokens(r.Selectors) {
			if _, ok := index[name]; ok {
				extra[name]++
				continue
			}
			index[name] = len(res.Symbols)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name:      name,
				FQN:       res.Path + "#" + name, // slash-containing → never collides with dotted FQNs
				Kind:      extract.KindSelector,
				Language:  lang,
				StartLine: r.StartLine,
				EndLine:   r.EndLine,
				Signature: strings.Join(r.Selectors, ", "),
				Source:    r.Text,
			})
		}
	}
	for name, n := range extra {
		res.Symbols[index[name]].Docstring = fmt.Sprintf("also defined in %d more rule(s) in this file", n)
	}
}

// Import statements. The body capture stops at `;` or `{` so a rule prelude
// never bleeds in; specifiers are the quoted strings (or url() argument)
// inside the body.
var (
	atImportRe   = regexp.MustCompile(`(?i)@(import|use|forward)\b([^;{]*)`)
	quotedSpecRe = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+?)['"]?\s*\)|['"]([^'"]+)['"]`)
)

// scanImports returns @import/@use/@forward specifiers, deduped in first-seen
// order, over the comments-blanked strings-kept view. Sass builtins
// (`sass:math`), absolute URLs, and interpolated specs are external — skipped.
// @import takes every spec in its comma list; @use/@forward take exactly one
// (the rest of their body is `as`/`with` configuration).
func scanImports(src []byte, lineComments bool) []string {
	_, nc := sanitizeCSS(src, lineComments)
	seen := map[string]bool{}
	var out []string
	for _, m := range atImportRe.FindAllStringSubmatch(string(nc), -1) {
		kind := strings.ToLower(m[1])
		specs := quotedSpecRe.FindAllStringSubmatch(m[2], -1)
		if kind != "import" && len(specs) > 1 {
			specs = specs[:1]
		}
		for _, sm := range specs {
			spec := sm[1]
			if spec == "" {
				spec = sm[2]
			}
			spec = strings.TrimSpace(spec)
			if skipImportSpec(spec) || seen[spec] {
				continue
			}
			seen[spec] = true
			out = append(out, spec)
		}
	}
	return out
}

func skipImportSpec(spec string) bool {
	return spec == "" ||
		strings.HasPrefix(spec, "sass:") ||
		strings.HasPrefix(spec, "http://") ||
		strings.HasPrefix(spec, "https://") ||
		strings.HasPrefix(spec, "//") ||
		strings.Contains(spec, "#{") ||
		strings.Contains(spec, "@{") ||
		strings.ContainsRune(spec, sentinel)
}

// sanitizeCSS produces two equal-length views of src (byte offsets and line
// numbers stay valid in both):
//
//   - code: comments blanked, string contents blanked, interpolation spans
//     (`#{…}`/`@{…}`) collapsed to a sentinel byte. Feeds structural scans —
//     a `}` inside `content: "}"` must not close a frame.
//   - noComment: comments blanked, strings KEPT — import specifiers live
//     inside strings.
//
// `//` line comments are recognized only when lineComments is set (scss/less/
// sass); block `/* … */` comments always. CSS strings are single-line, so an
// unterminated string stops blanking at the newline.
func sanitizeCSS(src []byte, lineComments bool) (code, noComment []byte) {
	code = make([]byte, len(src))
	nc := make([]byte, len(src))
	copy(code, src)
	copy(nc, src)
	blank := func(view []byte, i int) {
		if view[i] != '\n' && view[i] != '\r' {
			view[i] = ' '
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '*': // block comment
			for ; i < len(src); i++ {
				blank(code, i)
				blank(nc, i)
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					i++
					blank(code, i)
					blank(nc, i)
					break
				}
			}
		case lineComments && c == '/' && i+1 < len(src) && src[i+1] == '/': // line comment
			for ; i < len(src) && src[i] != '\n'; i++ {
				blank(code, i)
				blank(nc, i)
			}
		case c == '\'' || c == '"':
			q := c
			j := i + 1
			for ; j < len(src); j++ {
				if src[j] == '\\' {
					j++
					continue
				}
				if src[j] == q || src[j] == '\n' {
					break
				}
			}
			for k := i + 1; k < j && k < len(src); k++ {
				blank(code, k)
			}
			i = j
		case (c == '#' || c == '@') && i+1 < len(src) && src[i+1] == '{': // interpolation
			depth := 0
			j := i + 1
			for ; j < len(src); j++ {
				if src[j] == '{' {
					depth++
				} else if src[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			code[i] = sentinel
			last := j
			if last >= len(src) {
				last = len(src) - 1
			}
			for k := i + 1; k <= last; k++ {
				blank(code, k)
				blank(nc, k)
			}
			i = last
		}
	}
	return code, nc
}

// newLineOffsets returns a function mapping a byte offset to a 1-based line.
func newLineOffsets(text string) func(int) int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1
	}
}
