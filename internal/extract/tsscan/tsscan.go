// Package tsscan adds cheap, name-based structure to LSP-extracted TypeScript/
// JavaScript FileResults: import specifiers, JSX component-usage call
// references, and Next.js framework-wiring references. The LSP documentSymbol
// backend (lspsrc) yields precise symbols but no references at all — so in a
// React codebase, where composition happens through JSX elements rather than
// plain call expressions, the base (non --precise) graph had zero call edges:
// every component looked like an orphan root and codemap_map had no hubs.
//
// tsscan is the TS/JS analogue of gosrc's name-based reference extraction:
//   - Imports:  `import ... from "spec"`, `export ... from "spec"`,
//     `require("spec")`, `import("spec")` — resolved to file→file
//     EdgeImports by the indexer's existing import pass (which already
//     understood TS relative specifiers but had no TS writer).
//   - JSX:      `<Foo/>`, `<Foo.Bar/>` create a RefCalls reference from the
//     enclosing component/function to the component's name — rendering IS
//     invocation for a function component. Lowercase intrinsic elements
//     (`<div>`) never create edges; dotted lowercase namespaces
//     (`<motion.div/>`) are recognized as components but resolve (or not)
//     by name like any other reference.
//   - Wiring:   Next.js App/Pages Router convention files (page.tsx,
//     layout.tsx, route.ts GET/POST, middleware, …) get a RefReferences
//     from the file to the framework-invoked exports, the same mechanism
//     gosrc uses for cobra RunE / mux.HandleFunc handlers — so orphan
//     detection stops flagging framework entrypoints as dead code.
//
// Like all name-based extraction the references over-match same-named symbols
// (edges carry candidate weight, not precise provenance); the --precise LSP
// callHierarchy pass still supersedes per-file coverage honestly.
package tsscan

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Enrich populates res.Imports and appends name-based references for a TS/JS
// file. res.Symbols must already be extracted (references are attributed to
// the innermost enclosing symbol). Safe on any content — a scan that matches
// nothing simply adds nothing.
//
// Each scan sanitizes internally (see sanitize): comments are always blanked
// (commented-out JSX/imports are not structure), and the JSX scan additionally
// blanks string/template contents (a "<Foo/>" inside a string literal is
// data, not a render). Import specifiers live inside string literals, so the
// import scan keeps string contents.
func Enrich(res *extract.FileResult, relPath string, src []byte) {
	res.Imports = append(res.Imports, Imports(src)...)
	if isJSXPath(relPath) {
		res.References = append(res.References, JSXRefs(relPath, src, res.Symbols)...)
		res.References = append(res.References, ClassNameRefs(relPath, src, res.Symbols)...)
	}
	res.References = append(res.References, FrameworkRefs(relPath, src)...)
}

// isJSXPath reports whether a path may legally contain JSX elements. Plain .ts
// files cannot (angle brackets there are generics/type assertions), which is
// what keeps the scan free of `<Foo>value` cast false positives.
func isJSXPath(relPath string) bool {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".tsx", ".jsx":
		return true
	}
	return false
}

// Import statement forms. Specifiers never contain quotes, so [^'"]+ inside a
// quoted capture cannot cross strings; the non-greedy clause bridges multi-line
// named-import lists.
var importRes = []*regexp.Regexp{
	// import X from 'spec' / import {a, b} from 'spec' / import * as ns from 'spec' / import type {T} from 'spec'
	regexp.MustCompile(`(?m)\bimport\s+(?:type\s+)?[^'";]*?\bfrom\s*['"]([^'"]+)['"]`),
	// side-effect import: import 'spec'
	regexp.MustCompile(`(?m)\bimport\s*['"]([^'"]+)['"]`),
	// re-exports: export * from 'spec' / export {a} from 'spec' / export * as ns from 'spec'
	regexp.MustCompile(`(?m)\bexport\s+(?:type\s+)?(?:\*(?:\s+as\s+[\w$]+)?|\{[^}]*\})\s*from\s*['"]([^'"]+)['"]`),
	// CommonJS + dynamic import
	regexp.MustCompile(`\b(?:require|import)\(\s*['"]([^'"]+)['"]\s*\)`),
}

// Imports returns every import/re-export/require specifier in src, deduped in
// first-seen order. The scan runs over the comments-blanked view so
// commented-out imports don't match; a specifier inside an unrelated string
// can still match (the resolver drops anything that isn't a project file).
func Imports(raw []byte) []string {
	_, src := sanitize(raw)
	seen := map[string]bool{}
	var out []string
	for _, re := range importRes {
		for _, m := range re.FindAllSubmatch(src, -1) {
			spec := string(m[1])
			if spec == "" || seen[spec] {
				continue
			}
			seen[spec] = true
			out = append(out, spec)
		}
	}
	return out
}

// jsxTagRe matches a JSX element name right after `<`: either a capitalized
// identifier (`<Foo`, optionally dotted: `<Foo.Bar`) or a dotted lowercase
// member (`<motion.div`). A bare lowercase name (`<div`) deliberately does
// not match — intrinsic host elements are not components. The name must be
// terminated by whitespace, `/`, or `>` (so `<T,>` generic parameter lists
// don't match).
var jsxTagRe = regexp.MustCompile(`<([A-Za-z_$][\w$]*(?:\.[\w$]+)*)(?:\s|/|>)`)

// JSXRefs scans a .tsx/.jsx file for JSX component usages and returns one
// RefCalls reference per (enclosing symbol, component) pair. symbols supplies
// enclosing-scope attribution by line range; a usage outside any symbol is
// attributed to the file path (the indexer resolves that to the file node,
// exactly like gosrc's package-level value references). The scan runs over
// the fully sanitized view: commented-out JSX and "<Foo/>" string data never
// become references.
func JSXRefs(relPath string, src []byte, symbols []extract.Symbol) []extract.Reference {
	jsxView, _ := sanitize(src)
	text := string(jsxView)
	lineOf := lineOffsets(text)
	enclose := newEncloser(symbols)

	seen := map[string]bool{}
	var refs []extract.Reference
	for _, m := range jsxTagRe.FindAllStringSubmatchIndex(text, -1) {
		start := m[0] // position of '<'
		name := text[m[2]:m[3]]
		if !isComponentName(name) {
			continue // intrinsic element (<div>) or not a component
		}
		// Reject generics/comparisons: JSX's `<` never directly follows an
		// identifier char (useState<T>, a<B), a `.` (obj.<T> is invalid anyway),
		// or another `<` (left shift).
		if start > 0 {
			prev := text[start-1]
			if isIdentChar(prev) || prev == '<' || prev == '.' {
				continue
			}
		}
		// Reject `<T extends ...>` generic parameter lists in .tsx arrows.
		if rest := strings.TrimLeft(text[m[3]:min(len(text), m[3]+16)], " \t"); strings.HasPrefix(rest, "extends ") {
			continue
		}
		target := refTarget(name)
		line := lineOf(start)
		from := enclose(line)
		if from == "" {
			from = relPath // top-level JSX (e.g. a const element) → file node
		}
		key := from + "\x00" + target
		if target == "" || seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, extract.Reference{
			From: from,
			To:   target,
			Kind: extract.RefCalls,
			Line: line,
			// Qualified: JSX names may resolve cross-file/package; name matching
			// over-matches, so edges carry candidate weight — mirroring gosrc's
			// selector-call convention.
			Qualified: true,
		})
	}
	return refs
}

// isComponentName reports whether a JSX tag name denotes a component rather
// than an intrinsic host element. JSX semantics: only a bare all-lowercase-
// leading name is a host element; a capitalized, `_`-, or `$`-leading name and
// ANY dotted member expression are component lookups (<Foo/>, <_Private/>,
// <$Styled/>, <Foo.Bar/>, <motion.div/>).
func isComponentName(name string) bool {
	if strings.Contains(name, ".") {
		return true // <Foo.Bar/>, <motion.div/>
	}
	return !(name[0] >= 'a' && name[0] <= 'z')
}

// refTarget maps a JSX tag name to the reference target name. Member
// expressions take the leading object — `<Foo.Bar/>` and `<motion.div/>`
// reference the binding `Foo`/`motion` in scope (the property lookup happens
// on that object at runtime; the property name usually has no standalone
// definition node while the object does — a namespace import, a compound
// component root, or an external library that resolves to nothing).
func refTarget(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// classNameRe anchors a className attribute (`className=`) or object property
// (`className:` — props objects, cva/clsx maps) whose value follows.
var classNameRe = regexp.MustCompile(`\bclassName\s*[=:]\s*`)

// classNameTokenExcluded rejects tokens that cannot be plain class names:
// Tailwind variants/arbitrary values (`hover:underline`, `w-[10px]`) and
// interpolation remnants. Plain utilities (`flex`, `mt-4`) pass — they simply
// resolve to no selector node and produce no edge, like external imports.
func classNameTokenExcluded(tok string) bool {
	return tok == "" || strings.ContainsAny(tok, ":[]()/${}")
}

// ClassNameRefs scans a .tsx/.jsx file for className values and returns one
// RefStyles reference per (enclosing symbol, class token) pair, targeting the
// selector name `.token` — the namespace the CSS backends define nodes under.
// The scan runs on the comments-blanked view (string contents kept: class
// names live in strings). Values are read three ways:
//
//   - className="btn active"    — the literal's tokens;
//   - className={…}             — every string literal and template-literal
//     static segment inside the balanced-brace span, covering cn()/clsx(),
//     ternaries, and `${}` templates without modeling the call graph;
//   - className: "btn"          — same, for props objects.
//
// Dynamic segments contribute nothing; tokens that cannot be class names
// (Tailwind variants/arbitrary values) are filtered. Like all name-based
// extraction the edges carry candidate weight.
func ClassNameRefs(relPath string, src []byte, symbols []extract.Symbol) []extract.Reference {
	_, code := sanitize(src)
	text := string(code)
	lineOf := lineOffsets(text)
	enclose := newEncloser(symbols)

	seen := map[string]bool{}
	var refs []extract.Reference
	for _, m := range classNameRe.FindAllStringIndex(text, -1) {
		start := m[1]
		if start >= len(code) {
			continue
		}
		var tokens []string
		switch code[start] {
		case '"', '\'':
			if end := closingQuote(code, start); end > start {
				tokens = strings.Fields(text[start+1 : end])
			}
		case '{', '`':
			tokens = stringTokensInSpan(code, src, start)
		default:
			continue // an identifier or expression with no literal head
		}
		line := lineOf(m[0])
		from := enclose(line)
		if from == "" {
			from = relPath
		}
		for _, tok := range tokens {
			if classNameTokenExcluded(tok) {
				continue
			}
			key := from + "\x00" + tok
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, extract.Reference{
				From: from,
				To:   "." + tok,
				Kind: extract.RefStyles,
				Line: line,
				// Qualified: the defining stylesheet is another file; name
				// matching may over-match → candidate weight.
				Qualified: true,
			})
		}
	}
	return refs
}

// stringTokensInSpan collects the whitespace-split tokens of every string
// literal and template-literal static segment inside the balanced expression
// starting at code[start] (`{` — a JSX expression container — or a bare
// template literal). A tiny mode machine, not a JS parser: quotes and
// backticks toggle collection; `${…}` interpolation bodies are code again
// (nested string literals inside them still collect — `${cond ? "on" : "off"}`
// names real classes); everything else only balances braces. Structure is
// read from the sanitized code view (comments blanked — a quote inside a
// comment never opens collection) while token TEXT is read from raw at the
// same offsets, because the code view blanks template-literal contents.
func stringTokensInSpan(code, raw []byte, start int) []string {
	var tokens []string
	collect := func(from, to int) {
		if from < to && to <= len(raw) {
			tokens = append(tokens, strings.Fields(string(raw[from:to]))...)
		}
	}

	outerDepth := 0     // {…} nesting of the JSX expression container
	var tmplBrace []int // ${…} brace depth per open template; top = innermost
	inTemplate := false
	segStart := -1

	i := start
	if code[i] == '{' {
		outerDepth = 1
		i++
	}
	for ; i < len(code); i++ {
		c := code[i]
		if inTemplate {
			switch {
			case c == '\\' && i+1 < len(code):
				i++
			case c == '`': // closes the innermost template
				collect(segStart, i)
				tmplBrace = tmplBrace[:len(tmplBrace)-1]
				inTemplate = false
				if outerDepth == 0 && len(tmplBrace) == 0 {
					return tokens // bare template value ended
				}
			case c == '$' && i+1 < len(code) && code[i+1] == '{':
				collect(segStart, i)
				inTemplate = false // interpolation body is code again
				i++
			}
			continue
		}
		switch c {
		case '\'', '"':
			if end := closingQuote(code, i); end > i {
				collect(i+1, end)
				i = end
			}
		case '`':
			tmplBrace = append(tmplBrace, 0)
			inTemplate = true
			segStart = i + 1
		case '{':
			if len(tmplBrace) > 0 {
				tmplBrace[len(tmplBrace)-1]++
			} else {
				outerDepth++
			}
		case '}':
			if len(tmplBrace) > 0 {
				if tmplBrace[len(tmplBrace)-1] == 0 {
					inTemplate = true // interpolation closed → template text resumes
					segStart = i + 1
				} else {
					tmplBrace[len(tmplBrace)-1]--
				}
			} else if outerDepth > 0 {
				outerDepth--
				if outerDepth == 0 {
					return tokens
				}
			}
		}
	}
	return tokens
}

// sanitize produces two same-length views of src (byte offsets and line
// numbers stay valid in both):
//
//   - jsxView:  comments AND string/template-literal contents blanked to
//     spaces. Feeds the JSX scan, so `{/* <Old/> */}`, `// <Old/>`, and
//     `"<Foo/>"` never become references. Template `${...}` interpolation
//     bodies are kept — JSX inside an interpolation is real rendering.
//   - codeView: only comments blanked. Feeds the import and framework-export
//     scans, whose tokens (import specifiers) live inside string literals.
//
// The lexer tracks line/block comments, ” and "" strings, and nested
// template literals. Two deliberate heuristics: a quote with no closing mate
// on the same line is treated as prose (JSX text like `Don't`, which would
// otherwise swallow following lines — JS strings cannot span lines), and
// regex literals are not modeled (a `/` divides or starts a comment only).
func sanitize(src []byte) (jsxView, codeView []byte) {
	jsx := make([]byte, len(src))
	code := make([]byte, len(src))
	copy(jsx, src)
	copy(code, src)
	blank := func(view []byte, i int) {
		if view[i] != '\n' && view[i] != '\r' {
			view[i] = ' '
		}
	}
	// tmplBrace holds the ${…} brace depth per enclosing template literal;
	// empty means we're in plain code.
	var tmplBrace []int
	inTemplate := false // currently inside template TEXT (not an interpolation)

	for i := 0; i < len(src); i++ {
		c := src[i]
		if inTemplate {
			switch {
			case c == '\\' && i+1 < len(src):
				blank(jsx, i)
				blank(jsx, i+1)
				i++
			case c == '`': // closes the innermost template
				tmplBrace = tmplBrace[:len(tmplBrace)-1]
				inTemplate = false
			case c == '$' && i+1 < len(src) && src[i+1] == '{':
				inTemplate = false // interpolation body is code
				i++
			default:
				blank(jsx, i)
				blank(code, i)
			}
			continue
		}
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/': // line comment
			for ; i < len(src) && src[i] != '\n'; i++ {
				blank(jsx, i)
				blank(code, i)
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*': // block comment
			blank(jsx, i)
			blank(code, i)
			i++
			for ; i < len(src); i++ {
				blank(jsx, i)
				blank(code, i)
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					i++
					blank(jsx, i)
					blank(code, i)
					break
				}
			}
		case c == '\'' || c == '"':
			end := closingQuote(src, i)
			if end < 0 {
				continue // unpaired on this line: JSX prose, not a string
			}
			for j := i + 1; j < end; j++ {
				blank(jsx, j)
			}
			i = end
		case c == '`':
			tmplBrace = append(tmplBrace, 0)
			inTemplate = true
		case len(tmplBrace) > 0 && c == '{':
			tmplBrace[len(tmplBrace)-1]++
		case len(tmplBrace) > 0 && c == '}':
			if tmplBrace[len(tmplBrace)-1] == 0 {
				inTemplate = true // interpolation closed, back to template text
			} else {
				tmplBrace[len(tmplBrace)-1]--
			}
		}
	}
	return jsx, code
}

// closingQuote returns the index of the unescaped quote closing the string
// opened at src[open], or -1 when no mate exists before end of line — JS
// single/double-quoted strings are single-line, so an unpaired quote is JSX
// prose (an apostrophe in text), not a string opener.
func closingQuote(src []byte, open int) int {
	q := src[open]
	for j := open + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '\n':
			return -1
		case q:
			return j
		}
	}
	return -1
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// lineOffsets returns a function mapping a byte offset to a 1-based line.
func lineOffsets(text string) func(int) int {
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

// newEncloser returns a function mapping a 1-based line to the FQN of the
// innermost symbol whose range contains it ("" when none does). Innermost =
// the smallest containing span, so a component defined inside a module or
// class attributes usages to itself, not the container.
func newEncloser(symbols []extract.Symbol) func(int) string {
	syms := make([]extract.Symbol, 0, len(symbols))
	for _, s := range symbols {
		if s.FQN != "" {
			syms = append(syms, s)
		}
	}
	// Sort by span size descending so the LAST containing match is innermost.
	sort.SliceStable(syms, func(i, j int) bool {
		return (syms[i].EndLine - syms[i].StartLine) > (syms[j].EndLine - syms[j].StartLine)
	})
	return func(line int) string {
		fqn := ""
		for _, s := range syms {
			if s.StartLine <= line && line <= s.EndLine {
				fqn = s.FQN
			}
		}
		return fqn
	}
}

// Next.js framework conventions. A convention file's framework-invoked exports
// get a RefReferences edge from the file node so orphan detection treats them
// as wired — the same mechanism that keeps cobra RunE handlers off the
// dead-code list. Names follow the App Router (and classic Pages Router) docs.
var (
	// Special files whose DEFAULT export the framework renders. Recognized by
	// basename under an `app` or `pages` path segment.
	nextDefaultExportFiles = map[string]bool{
		"page": true, "layout": true, "template": true, "loading": true,
		"error": true, "global-error": true, "not-found": true, "default": true,
		"unauthorized": true, "forbidden": true,
		"opengraph-image": true, "twitter-image": true, "icon": true, "apple-icon": true,
		// Metadata routes: the framework invokes the default export.
		"manifest": true, "robots": true, "sitemap": true,
	}
	// route.ts handlers: the framework invokes exported HTTP-verb functions.
	nextRouteExports = []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
	// Extra framework-invoked named exports of page/layout/route files.
	nextMetadataExports = []string{
		"generateMetadata", "generateStaticParams", "generateViewport",
		"generateImageMetadata", "generateSitemaps",
		// Pages Router data hooks.
		"getServerSideProps", "getStaticProps", "getStaticPaths", "getInitialProps",
	}
)

var (
	defaultExportFuncRe = regexp.MustCompile(`\bexport\s+default\s+(?:async\s+)?(?:function|class)\s+([A-Za-z_$][\w$]*)`)
	// Wrapped default exports: `export default memo(Page)`, forwardRef(Input),
	// memo(forwardRef(Page)) — the `(?:ident\s*\(\s*)+` group skips one or more
	// wrapper calls and captures the innermost call's first identifier argument
	// (the component the framework ultimately invokes).
	defaultExportCallRe  = regexp.MustCompile(`\bexport\s+default\s+(?:[A-Za-z_$][\w$]*\s*\(\s*)+([A-Za-z_$][\w$]*)`)
	defaultExportIdentRe = regexp.MustCompile(`(?m)\bexport\s+default\s+([A-Za-z_$][\w$]*)\s*;?\s*$`)
	defaultExportAsRe    = regexp.MustCompile(`\bexport\s*\{[^}]*?\b([A-Za-z_$][\w$]*)\s+as\s+default\b`)
	exportedDeclRe       = regexp.MustCompile(`(?m)\bexport\s+(?:async\s+)?(?:function|const|let|var|class)\s+([A-Za-z_$][\w$]*)`)
	exportListRe         = regexp.MustCompile(`\bexport\s*\{([^}]*)\}`)
)

// FrameworkRefs returns RefReferences from the file to every framework-invoked
// export of a Next.js convention file, or nil for a non-convention file. The
// reference resolves within the file's own directory first (Qualified=false),
// so per-route GET/POST handlers bind to their own route.ts. Export scanning
// runs over the comments-blanked view (string contents are kept — export
// names never live in strings, but blanking them is unnecessary).
func FrameworkRefs(relPath string, raw []byte) []extract.Reference {
	_, src := sanitize(raw)
	wired := frameworkWiredNames(relPath, src)
	if len(wired) == 0 {
		return nil
	}
	exported := exportedNames(src)
	seen := map[string]bool{}
	var refs []extract.Reference
	for _, name := range wired {
		if name == "" || seen[name] || !exported[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, extract.Reference{
			From: relPath, To: name, Kind: extract.RefReferences, Line: 1, Qualified: false,
		})
	}
	return refs
}

// frameworkWiredNames returns the export names the framework would invoke for
// this path, including the resolved default-export identifier when the
// convention wires the default export.
func frameworkWiredNames(relPath string, src []byte) []string {
	slash := filepath.ToSlash(relPath)
	base := strings.ToLower(filepath.Base(slash))
	ext := filepath.Ext(base)
	switch ext {
	case ".tsx", ".jsx", ".ts", ".js", ".mjs":
	default:
		return nil
	}
	stem := strings.TrimSuffix(base, ext)

	var wired []string
	switch {
	case stem == "middleware" || stem == "proxy":
		wired = append(wired, "middleware", "proxy", "config", defaultExportName(src))
	case stem == "instrumentation":
		wired = append(wired, "register", "onRequestError")
	case !underSegment(slash, "app") && !underSegment(slash, "pages"):
		// Every remaining convention is scoped to the App/Pages Router trees so a
		// stray component named error.tsx elsewhere isn't silently exempted.
		return nil
	case stem == "route":
		wired = append(wired, nextRouteExports...)
	case nextDefaultExportFiles[stem]:
		wired = append(wired, defaultExportName(src))
		wired = append(wired, nextMetadataExports...)
	case underSegment(slash, "pages"):
		// Pages Router: every module under pages/ is a page/api route whose
		// default export the framework invokes.
		wired = append(wired, defaultExportName(src))
		wired = append(wired, nextMetadataExports...)
	default:
		return nil
	}
	return wired
}

// defaultExportName resolves the identifier a module's default export names,
// or "" for an anonymous or absent default export.
func defaultExportName(src []byte) string {
	// Order matters: the func/class form first (so `export default function Page`
	// never reads as a call), then the wrapped-call form BEFORE the bare-ident
	// form — identRe's end-of-line anchor can't match `memo(Page)`, but keep the
	// ordering defensive rather than load-bearing.
	for _, re := range []*regexp.Regexp{defaultExportFuncRe, defaultExportCallRe, defaultExportIdentRe, defaultExportAsRe} {
		if m := re.FindSubmatch(src); m != nil {
			name := string(m[1])
			switch name { // keywords the ident form could catch (`export default async ...`)
			case "async", "function", "class", "new", "await":
				continue
			}
			return name
		}
	}
	return ""
}

// exportedNames returns every identifier the module visibly exports —
// declarations (`export function GET`), export lists (`export { GET, POST }`),
// and the default-export identifier. Framework refs only wire names that are
// actually exported, so a private helper named GET is not falsely protected.
func exportedNames(src []byte) map[string]bool {
	out := map[string]bool{}
	for _, m := range exportedDeclRe.FindAllSubmatch(src, -1) {
		out[string(m[1])] = true
	}
	for _, m := range exportListRe.FindAllSubmatch(src, -1) {
		for _, item := range strings.Split(string(m[1]), ",") {
			name := strings.TrimSpace(item)
			// `A as B` exports B naming A: the local definition A is what's wired.
			if i := strings.Index(name, " as "); i > 0 {
				name = strings.TrimSpace(name[:i])
			}
			if name != "" && !strings.ContainsAny(name, " \t\n") {
				out[name] = true
			}
		}
	}
	if d := defaultExportName(src); d != "" {
		out[d] = true
	}
	return out
}

// underSegment reports whether a slash path contains dir as a whole segment
// (e.g. "apps/web/src/app/(marketing)/page.tsx" is under "app").
func underSegment(slash, dir string) bool {
	for _, seg := range strings.Split(slash, "/") {
		if seg == dir {
			return true
		}
	}
	return false
}
