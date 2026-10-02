package tsscan

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Call candidates.
//
// The LSP documentSymbol backend yields definitions only, so without --precise
// a TS/JS function call never became an edge: `codemap context resolveUser`
// showed callers:0 callees:0 and orphans listed helpers that are obviously
// called two lines away. CallRefs adds the HIGH-PRECISION subset of calls that
// can be attributed from text alone, in one linear pass over the
// comments/strings-blanked view:
//
//  1. same-file calls — `name(`, `new Name(`, `await name(` where name is a
//     function / class / variable symbol defined in the SAME file and visible
//     from the call site (a symbol nested in another function is only visible
//     inside it; class members are never reachable by a bare name);
//     `Owner.member(` where "Owner.member" is a symbol of the same file
//     (static methods, enum/namespace members);
//  2. imported bindings — `name(` for a named/default import, `ns.member(` for
//     a namespace import, `Obj.member(` for a default/named import. The
//     reference carries the import specifier; the indexer resolves it to a
//     project file (relative paths, @/ ~/ aliases, workspace packages) and
//     looks the member up INSIDE that file only. A package import (react,
//     node:fs) resolves to no project file and so yields no edge;
//  3. `this.method(` inside a class member → the same class's member when the
//     class defines it in this file.
//
// Everything else (arbitrary `obj.method(`, globals, callbacks passed by
// value, inherited members, calls through barrel re-exports) is deliberately
// out of scope: precision over recall. The edges are name-provenance
// candidates (weight 0.7) that the --precise pass supersedes per file.
//
// Known, accepted false positives (documented, not modeled): a parameter or
// local variable that shadows a same-file function / import name and is then
// called (`function run(cb) { cb() }` next to a top-level `cb`), JSX prose that
// reads like a call, and regex literals. Each requires a name collision with a
// symbol that is callable in the same file or imported, so they are rare.

// callableKindBare reports whether a symbol kind can be the target of a bare
// `name(` call.
func callableKindBare(kind string) bool {
	switch kind {
	case extract.KindFunction, extract.KindClass, extract.KindVariable, extract.KindTest:
		return true
	}
	return false
}

// callableKindMember reports whether a symbol kind can be the target of a
// `Owner.member(` / `this.member(` call.
func callableKindMember(kind string) bool {
	switch kind {
	case extract.KindFunction, extract.KindMethod, extract.KindVariable, extract.KindTest:
		return true
	}
	return false
}

// importBinding is one local name introduced by an import / require.
type importBinding struct {
	spec     string
	kind     bindingKind
	exported string // exported name for a named binding
}

type bindingKind uint8

const (
	bindNamed bindingKind = iota
	bindDefault
	bindNamespace
)

var (
	importClauseRe = regexp.MustCompile(`(?m)\bimport\s+(type\s+)?([^'";]*?)\s*\bfrom\s*['"]([^'"]+)['"]`)
	// const x = require('m') / const {a, b: c} = require('m') / import x = require('m')
	requireBindRe = regexp.MustCompile(`(?:\b(?:const|let|var)\s+(\{[^}]*\}|[A-Za-z_$][\w$]*)|\bimport\s+([A-Za-z_$][\w$]*))\s*=\s*(?:await\s+)?require\(\s*['"]([^'"]+)['"]\s*\)`)
	nsClauseRe    = regexp.MustCompile(`^\*\s*as\s+([A-Za-z_$][\w$]*)`)
)

// parseImportBindings extracts local binding names from import statements and
// require() declarations in the comments-blanked view (string contents kept,
// since specifiers live inside strings). Type-only imports (`import type`,
// `{ type X }`) are skipped: they never execute.
func parseImportBindings(code string) map[string]importBinding {
	out := map[string]importBinding{}
	for _, m := range importClauseRe.FindAllStringSubmatch(code, -1) {
		if m[1] != "" {
			continue // import type …
		}
		if strings.HasPrefix(m[3], "node:") {
			continue // runtime builtin: never a project file
		}
		addClauseBindings(out, strings.TrimSpace(m[2]), m[3])
	}
	for _, m := range requireBindRe.FindAllStringSubmatch(code, -1) {
		spec := m[3]
		if strings.HasPrefix(spec, "node:") {
			continue
		}
		target := m[1]
		if target == "" {
			target = m[2]
		}
		if strings.HasPrefix(target, "{") {
			for _, item := range strings.Split(strings.Trim(target, "{} \t\r\n"), ",") {
				item = strings.TrimSpace(item)
				if item == "" || strings.HasPrefix(item, "...") {
					continue
				}
				exported, local := item, item
				if i := strings.IndexByte(item, ':'); i > 0 {
					exported, local = strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+1:])
				}
				if i := strings.IndexByte(local, '='); i > 0 {
					local = strings.TrimSpace(local[:i])
				}
				if isPlainIdent(exported) && isPlainIdent(local) {
					out[local] = importBinding{spec: spec, kind: bindNamed, exported: exported}
				}
			}
			continue
		}
		if isPlainIdent(target) {
			out[target] = importBinding{spec: spec, kind: bindNamespace}
		}
	}
	return out
}

func addClauseBindings(out map[string]importBinding, clause, spec string) {
	if clause == "" {
		return
	}
	rest := clause
	if rest[0] != '{' && rest[0] != '*' {
		end := 0
		for end < len(rest) && isIdentChar(rest[end]) {
			end++
		}
		if end > 0 {
			out[rest[:end]] = importBinding{spec: spec, kind: bindDefault}
		}
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest[end:]), ","))
	}
	if rest == "" {
		return
	}
	if rest[0] == '*' {
		if m := nsClauseRe.FindStringSubmatch(rest); m != nil {
			out[m[1]] = importBinding{spec: spec, kind: bindNamespace}
		}
		return
	}
	if rest[0] != '{' {
		return
	}
	body := rest[1:]
	if i := strings.IndexByte(body, '}'); i >= 0 {
		body = body[:i]
	}
	for _, item := range strings.Split(body, ",") {
		item = strings.TrimSpace(item)
		if item == "" || strings.HasPrefix(item, "type ") {
			continue
		}
		exported, local := item, item
		if i := strings.Index(item, " as "); i > 0 {
			exported, local = strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+4:])
		}
		if !isPlainIdent(exported) || !isPlainIdent(local) {
			continue
		}
		if exported == "default" {
			out[local] = importBinding{spec: spec, kind: bindDefault}
			continue
		}
		out[local] = importBinding{spec: spec, kind: bindNamed, exported: exported}
	}
}

func isPlainIdent(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

// symInfo is a same-file symbol plus its lexical parent (nil at top level).
type symInfo struct {
	sym    extract.Symbol
	parent *extract.Symbol
}

// callModifiers are the keywords that may precede a method/field NAME at the
// start of a class-member or object-literal line (`private async foo() {`).
var callModifiers = map[string]bool{
	"public": true, "private": true, "protected": true, "static": true,
	"async": true, "get": true, "set": true, "readonly": true,
	"override": true, "abstract": true, "declare": true,
}

// notACallBefore are keywords after which `name(` declares rather than calls.
var notACallBefore = map[string]bool{
	"function": true, "class": true, "interface": true, "enum": true,
	"namespace": true, "module": true, "type": true,
}

// CallRefs returns name-based call candidates for one TS/JS file (see the
// package comment above). symbols supplies enclosing-scope attribution and the
// same-file target table; the scan is a single pass over the sanitized text.
func CallRefs(relPath string, src []byte, symbols []extract.Symbol) []extract.Reference {
	view, code := sanitize(src)
	imports := parseImportBindings(string(code))
	if len(symbols) == 0 && len(imports) == 0 {
		return nil
	}
	text := string(view)

	byFQN := make(map[string]*extract.Symbol, len(symbols))
	for i := range symbols {
		s := &symbols[i]
		if s.FQN == "" {
			continue
		}
		if _, dup := byFQN[s.FQN]; !dup {
			byFQN[s.FQN] = s
		}
	}
	bare := map[string][]symInfo{}
	memberOwners := map[string]bool{}
	hasMethods := false
	for i := range symbols {
		s := symbols[i]
		if s.FQN == "" || s.Name == "" {
			continue
		}
		var parent *extract.Symbol
		if dot := strings.LastIndexByte(s.FQN, '.'); dot > 0 {
			parent = byFQN[s.FQN[:dot]]
			if strings.Count(s.FQN, ".") == 1 {
				memberOwners[s.FQN[:dot]] = true
			}
		}
		if s.Kind == extract.KindMethod {
			hasMethods = true
		}
		if callableKindBare(s.Kind) && isPlainIdent(s.Name) {
			// A dotted FQN with an unresolvable parent is a flat-documentSymbol
			// quirk we cannot scope — keep it out of bare matching.
			if strings.Contains(s.FQN, ".") && parent == nil {
				continue
			}
			bare[s.Name] = append(bare[s.Name], symInfo{sym: s, parent: parent})
		}
	}

	interesting := make(map[string]bool, len(bare)+len(memberOwners)+len(imports)+1)
	for name := range bare {
		interesting[name] = true
	}
	for owner := range memberOwners {
		interesting[owner] = true
	}
	for name := range imports {
		interesting[name] = true
	}
	if hasMethods {
		interesting["this"] = true
	}
	if len(interesting) == 0 {
		return nil
	}

	lineOf := lineOffsets(text)
	encloseSym := newSymEncloser(symbols)
	seen := map[string]bool{}
	var refs []extract.Reference

	emit := func(start int, ref extract.Reference) {
		line := lineOf(start)
		from, fromLine := relPath, 0 // module-level call → file node (JSX convention)
		if s := encloseSym(line); s != nil {
			from, fromLine = s.FQN, s.StartLine
		}
		if ref.ToFile == relPath && ref.To == from && (ref.ToLine == 0 || ref.ToLine == fromLine) {
			return // direct recursion adds nothing
		}
		key := from + "\x00" + strconv.Itoa(fromLine) + "\x00" + ref.ToFile + "\x00" + ref.ImportSpec + "\x00" + ref.To
		if ref.DefaultExport {
			key += "\x00d"
		}
		if seen[key] {
			return
		}
		seen[key] = true
		ref.From, ref.FromLine, ref.FromFile = from, fromLine, relPath
		ref.Kind, ref.Line, ref.Qualified = extract.RefCalls, line, true
		refs = append(refs, ref)
	}

	n := len(text)
	for i := 0; i < n; {
		c := text[i]
		if c >= '0' && c <= '9' { // a numeric literal: skip so `1e3foo` never yields `foo`
			for i < n && tokChar(text[i]) {
				i++
			}
			continue
		}
		if !tokChar(c) {
			i++
			continue
		}
		start := i
		for i < n && tokChar(text[i]) {
			i++
		}
		tok := text[start:i]
		if !interesting[tok] {
			continue
		}
		// A property name (`x.name(`) is never the bare binding — except a
		// spread (`...name(`), which is a plain call.
		pb := prevNonSpace(text, start)
		if pb >= 0 && text[pb] == '.' && !(pb >= 2 && text[pb-1] == '.' && text[pb-2] == '.') {
			continue
		}

		k := skipBlanks(text, i)
		switch {
		case k < n && text[k] == '.':
			// Member call: tok.member( — only one level deep.
			ms := k + 1
			me := ms
			for me < n && tokChar(text[me]) {
				me++
			}
			if me == ms {
				continue
			}
			member := text[ms:me]
			if _, ok := openParenAfter(text, me); !ok {
				continue
			}
			if tok == "this" {
				if cls := enclosingClass(symFQN(encloseSym(lineOf(start))), byFQN); cls != "" {
					fqn := cls + "." + member
					if t := byFQN[fqn]; t != nil && callableKindMember(t.Kind) {
						emit(start, extract.Reference{To: fqn, ToFile: relPath, ToLine: t.StartLine})
					}
				}
				continue
			}
			if memberOwners[tok] && isTopLevelOwner(bare[tok]) {
				fqn := tok + "." + member
				if t := byFQN[fqn]; t != nil && callableKindMember(t.Kind) {
					emit(start, extract.Reference{To: fqn, ToFile: relPath, ToLine: t.StartLine})
					continue
				}
			}
			if b, ok := imports[tok]; ok && !shadowed(bare[tok], lineOf(start)) {
				switch b.kind {
				case bindNamespace:
					emit(start, extract.Reference{To: member, ImportSpec: b.spec})
				case bindDefault:
					emit(start, extract.Reference{To: member, ImportSpec: b.spec, DefaultExport: true})
				case bindNamed:
					emit(start, extract.Reference{To: b.exported + "." + member, ImportSpec: b.spec})
				}
			}
		case k < n && (text[k] == '(' || text[k] == '<'):
			if text[k] == '<' {
				g := skipGeneric(text, k)
				if g < 0 {
					continue
				}
				k = skipBlanks(text, g)
				if k >= n || text[k] != '(' {
					continue
				}
			}
			if tok == "this" {
				continue
			}
			if isDeclarationSite(text, start, k) {
				continue
			}
			line := lineOf(start)
			if t := visibleBare(bare[tok], line); t != nil {
				emit(start, extract.Reference{To: t.FQN, ToFile: relPath, ToLine: t.StartLine})
				continue
			}
			if b, ok := imports[tok]; ok {
				switch b.kind {
				case bindNamed:
					emit(start, extract.Reference{To: b.exported, ImportSpec: b.spec})
				case bindDefault:
					emit(start, extract.Reference{ImportSpec: b.spec, DefaultExport: true})
				}
			}
		}
	}
	return refs
}

// DefaultExportName returns the identifier a module's default export names, or
// "" when the default export is anonymous or absent. The indexer uses it to
// bind a default-import call to the symbol the imported file exports.
func DefaultExportName(raw []byte) string {
	_, code := sanitize(raw)
	return defaultExportName(code)
}

// tokChar is isIdentChar extended to non-ASCII bytes, so a unicode identifier
// is one token and never leaks an ASCII tail that could spuriously match.
func tokChar(c byte) bool { return c >= 0x80 || isIdentChar(c) }

// prevNonSpace returns the index of the closest non-whitespace byte before i,
// or -1.
func prevNonSpace(text string, i int) int {
	for j := i - 1; j >= 0; j-- {
		switch text[j] {
		case ' ', '\t', '\n', '\r':
		default:
			return j
		}
	}
	return -1
}

// skipBlanks skips spaces and tabs (not newlines: a call's `(` is on the same
// line as its name in all but pathological code).
func skipBlanks(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return i
}

// openParenAfter reports the index of the `(` that follows position i,
// skipping blanks and one simple generic argument list (`name<T>(`).
func openParenAfter(text string, i int) (int, bool) {
	k := skipBlanks(text, i)
	if k < len(text) && text[k] == '<' {
		g := skipGeneric(text, k)
		if g < 0 {
			return 0, false
		}
		k = skipBlanks(text, g)
	}
	if k < len(text) && text[k] == '(' {
		return k, true
	}
	return 0, false
}

// skipGeneric skips a simple type-argument list starting at text[i]=='<' and
// returns the index after its closing '>', or -1. Only characters that occur
// in type arguments are allowed (identifiers, dots, commas, brackets, unions)
// and the scan is length-bounded, so a comparison like `a < b; c > (d)` never
// reads as a generic call.
func skipGeneric(text string, i int) int {
	depth := 0
	limit := min(len(text), i+256)
	for j := i; j < limit; j++ {
		switch c := text[j]; {
		case c == '<':
			depth++
		case c == '>':
			depth--
			if depth == 0 {
				return j + 1
			}
		case tokChar(c), c == ' ', c == '\t', c == ',', c == '.', c == '[', c == ']', c == '|':
		default:
			return -1
		}
	}
	return -1
}

// visibleBare picks the symbol a bare `name(` at the given line refers to: a
// top-level symbol is visible everywhere; a symbol nested in a function /
// module is visible only inside its parent's span; a class member is never
// reachable by a bare name. Among several visible candidates the one declared
// in the innermost scope wins (lexical shadowing).
func visibleBare(cands []symInfo, line int) *extract.Symbol {
	var best *extract.Symbol
	bestSpan := int(^uint(0) >> 1)
	for i := range cands {
		c := &cands[i]
		span := int(^uint(0)>>1) - 1 // top level: lowest priority
		if c.parent != nil {
			if c.parent.Kind == extract.KindClass {
				continue
			}
			if line < c.parent.StartLine || line > c.parent.EndLine {
				continue
			}
			span = c.parent.EndLine - c.parent.StartLine
		}
		if best == nil || span < bestSpan {
			best, bestSpan = &c.sym, span
		}
	}
	return best
}

// shadowed reports whether a same-file symbol of the same name is visible at
// line (it would shadow an import).
func shadowed(cands []symInfo, line int) bool {
	return visibleBare(cands, line) != nil
}

// isTopLevelOwner reports whether some top-level same-file symbol of this name
// exists (the only kind of owner `Owner.member(` resolves against).
func isTopLevelOwner(cands []symInfo) bool {
	for _, c := range cands {
		if c.parent == nil {
			return true
		}
	}
	return false
}

// newSymEncloser is newEncloser returning the innermost enclosing symbol itself
// (its FQN AND declaration line), so a reference can name the exact definition
// when the same FQN is declared more than once in a file.
func newSymEncloser(symbols []extract.Symbol) func(int) *extract.Symbol {
	syms := make([]extract.Symbol, 0, len(symbols))
	for _, s := range symbols {
		if s.FQN != "" {
			syms = append(syms, s)
		}
	}
	sort.SliceStable(syms, func(i, j int) bool {
		return (syms[i].EndLine - syms[i].StartLine) > (syms[j].EndLine - syms[j].StartLine)
	})
	return func(line int) *extract.Symbol {
		var best *extract.Symbol
		for i := range syms {
			if syms[i].StartLine <= line && line <= syms[i].EndLine {
				best = &syms[i]
			}
		}
		return best
	}
}

func symFQN(s *extract.Symbol) string {
	if s == nil {
		return ""
	}
	return s.FQN
}

// enclosingClass returns the FQN of the closest class that encloses the
// symbol fqn (including itself), or "".
func enclosingClass(fqn string, byFQN map[string]*extract.Symbol) string {
	for fqn != "" {
		if s := byFQN[fqn]; s != nil && s.Kind == extract.KindClass {
			return fqn
		}
		dot := strings.LastIndexByte(fqn, '.')
		if dot <= 0 {
			return ""
		}
		fqn = fqn[:dot]
	}
	return ""
}

// isDeclarationSite reports whether the `name(` at text[start:] (open paren at
// text[paren]) declares something instead of calling it:
//
//   - preceded by function / class / interface / enum / namespace / type
//     (`function name(`, including `function* name(`);
//   - a method or signature at the start of a line (modifiers allowed):
//     the parameter list is followed by `{`, or by `:` on the same line
//     (`name(x: T): R {`, `name(x): R;`), neither of which a call can be.
//
// A genuine call is never followed by a block `{`; the closing-paren search is
// bounded so a huge callback argument is simply treated as a call.
func isDeclarationSite(text string, start, paren int) bool {
	j := prevNonSpace(text, start)
	if j >= 0 && text[j] == '*' { // function* name(
		j = prevNonSpace(text, j)
	}
	if j >= 0 && isIdentChar(text[j]) {
		e := j + 1
		for j >= 0 && isIdentChar(text[j]) {
			j--
		}
		if notACallBefore[text[j+1:e]] {
			return true
		}
	}
	if !atMemberStart(text, start) {
		// Not at the start of a line: only a block `{` right after the
		// parameter list proves a declaration (`{ name() {} }`, `}, name() {`).
		close := matchingParen(text, paren)
		if close < 0 {
			return false
		}
		p := close + 1
		for p < len(text) && (text[p] == ' ' || text[p] == '\t' || text[p] == '\n' || text[p] == '\r') {
			p++
		}
		return p < len(text) && text[p] == '{'
	}
	close := matchingParen(text, paren)
	if close < 0 {
		return false
	}
	p := close + 1
	for p < len(text) && (text[p] == ' ' || text[p] == '\t') {
		p++
	}
	if p < len(text) && text[p] == ':' {
		return true // signature / typed method on one line
	}
	for p < len(text) && (text[p] == ' ' || text[p] == '\t' || text[p] == '\n' || text[p] == '\r') {
		p++
	}
	return p < len(text) && text[p] == '{'
}

// atMemberStart reports whether only whitespace, modifier keywords and a
// generator `*` separate text[start] from the beginning of its line.
func atMemberStart(text string, start int) bool {
	j := start - 1
	for {
		for j >= 0 && (text[j] == ' ' || text[j] == '\t') {
			j--
		}
		if j < 0 || text[j] == '\n' || text[j] == '\r' {
			return true
		}
		if text[j] == '*' {
			j--
			continue
		}
		if !isIdentChar(text[j]) {
			return false
		}
		e := j + 1
		for j >= 0 && isIdentChar(text[j]) {
			j--
		}
		if !callModifiers[text[j+1:e]] {
			return false
		}
	}
}

// matchingParen returns the index of the `)` closing the `(` at text[open], or
// -1 when it is not found within a bounded window (a very large argument list
// is a call, never a declaration).
func matchingParen(text string, open int) int {
	depth := 0
	limit := min(len(text), open+4096)
	for j := open; j < limit; j++ {
		switch text[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}
