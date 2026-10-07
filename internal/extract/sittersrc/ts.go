package sittersrc

import (
	"regexp"
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// This file ports TypeScript's navigation tree (services/navigationBar.ts,
// TS 5.9: addChildrenRecursively / getItemName / getFunctionOrClassName /
// getNodeKind / getAssignmentDeclarationKind) onto tree-sitter-typescript;
// tsnav.go holds the tree core, the ES5 class merge, and the conversion
// typescript-language-server applies on top. Function names mirror the TS
// originals so the two can be read side by side.
//
// Not ported (no observed parity impact): JSDoc @typedef/@callback leaves and
// plain same-kind merges (see mergeChildren).

// kindProperty is LSP SymbolKind.Property, which codemap never indexes.
const kindProperty = 7

type tsWalker struct {
	lang    *ts.Language
	src     []byte
	isJS    bool
	parent  *navNode
	tracked map[string]bool // trackedEs5Classes of the current scope
}

func tsSymbols(lang *ts.Language, src []byte, root *ts.Node, isJS bool) []lsp.DocumentSymbol {
	top := &navNode{}
	w := &tsWalker{lang: lang, src: src, isJS: isJS, parent: top}
	w.forEachChild(root)
	top.children = mergeChildren(top.children)
	var out []lsp.DocumentSymbol
	for _, c := range top.children {
		convertNav(c, -1, int(^uint(0)>>1), &out)
	}
	return out
}

func (w *tsWalker) typ(n *ts.Node) string { return n.Type(w.lang) }

func (w *tsWalker) field(n *ts.Node, name string) *ts.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldName(name, w.lang)
}

func (w *tsWalker) text(n *ts.Node) string { return text(w.src, n) }

// nm is getItemName's text for a declaration name node: identifier text, or
// the raw node text (string literals keep their quotes, computed names their
// brackets), cleaned.
func (w *tsWalker) nm(n *ts.Node) string { return cleanText(w.text(n)) }

func (w *tsWalker) forEachChild(n *ts.Node) {
	var jsdocs []*ts.Node
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.typ(c) == "comment" {
			if strings.HasPrefix(w.text(c), "/**") && !strings.HasPrefix(w.text(c), "/**/") {
				jsdocs = append(jsdocs, c)
			}
			continue
		}
		if len(jsdocs) > 0 && w.isDefaultCaseStatement(c) {
			w.addJSDocTypeAliases(jsdocs)
		}
		jsdocs = nil
		w.add(c)
	}
}

// isDefaultCaseStatement reports whether TypeScript handles the statement in
// addChildrenRecursively's default case — the only place it reads attached
// JSDoc for @typedef / @callback leaves.
func (w *tsWalker) isDefaultCaseStatement(n *ts.Node) bool {
	switch w.typ(n) {
	case "lexical_declaration", "variable_declaration", "if_statement", "for_statement", "for_in_statement",
		"while_statement", "do_statement", "return_statement", "throw_statement", "try_statement",
		"switch_statement", "labeled_statement", "import_statement", "statement_block", "empty_statement",
		"break_statement", "continue_statement", "debugger_statement":
		return true
	case "expression_statement":
		return n.NamedChildCount() == 0 || w.typ(n.NamedChild(0)) != "internal_module"
	case "export_statement":
		if d := w.field(n, "declaration"); d != nil {
			t := w.typ(d)
			return t == "lexical_declaration" || t == "variable_declaration"
		}
		return w.field(n, "value") == nil // export { … } / export * from …
	}
	return false
}

// jsdocMemberTags continue a @typedef / @callback rather than end it.
var jsdocMemberTags = map[string]bool{
	"property": true, "prop": true, "param": true, "arg": true, "argument": true,
	"returns": true, "return": true, "template": true,
}

var (
	jsdocTagRe     = regexp.MustCompile(`@([A-Za-z]+)`)
	jsdocTypedefRe = regexp.MustCompile(`^@(?:typedef|callback)\s*(?:\{(?:[^{}]|\{[^{}]*\})*\}\s*)?([A-Za-z_$][\w$.]*)`)
)

// addJSDocTypeAliases adds the @typedef / @callback tags of JSDoc blocks as
// leaves (kind "type" → Variable), spanning from the tag to its last content
// line — through any @property / @param tags that belong to it.
func (w *tsWalker) addJSDocTypeAliases(docs []*ts.Node) {
	for _, d := range docs {
		body := w.text(d)
		lines := strings.Split(body, "\n")
		first := line(d.StartPoint())
		for i := 0; i < len(lines); i++ {
			content := jsdocLineContent(lines[i])
			m := jsdocTypedefRe.FindStringSubmatch(content)
			if m == nil {
				continue
			}
			// A tag runs until the next tag that is not one of its members, or
			// to the closing */ — TypeScript ends it on that line.
			end := len(lines) - 1
			for j := i + 1; j < len(lines); j++ {
				c := jsdocLineContent(lines[j])
				if strings.HasPrefix(c, "@") {
					if tag := jsdocTagRe.FindStringSubmatch(c); tag != nil && !jsdocMemberTags[tag[1]] {
						end = j
						break
					}
				}
			}
			n := &navNode{name: m[1], kind: lsp.SymbolVariable, key: m[1],
				spans: []navSpan{{start: first + i, end: first + end, endByte: d.EndByte()}}}
			w.addLeafNode(n)
		}
	}
}

// jsdocLineContent strips a JSDoc line's comment syntax: the opening /**, the
// closing */, and the leading " * ".
func jsdocLineContent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/**")
	s = strings.TrimSuffix(s, "*/")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "*")
	return strings.TrimSpace(s)
}

func span(n *ts.Node) (int, int) { return line(n.StartPoint()), line(n.EndPoint()) }

// declStart widens a declaration to the modifiers TypeScript counts as part of
// the node: an enclosing `export` / `export default` / `declare` statement and,
// for class members, the decorators tree-sitter places as preceding siblings.
func (w *tsWalker) declStart(n *ts.Node) int {
	start := line(n.StartPoint())
	for p := n.Parent(); p != nil; p = p.Parent() {
		t := w.typ(p)
		if t != "export_statement" && t != "ambient_declaration" {
			break
		}
		start = line(p.StartPoint())
	}
	if p := n.Parent(); p != nil && w.typ(p) == "class_body" {
		for s := n.PrevSibling(); s != nil; s = s.PrevSibling() {
			switch w.typ(s) {
			case "decorator":
				start = line(s.StartPoint())
				continue
			case "comment":
				continue
			}
			break
		}
	}
	return start
}

func (w *tsWalker) isFunctionOrClassExpression(n *ts.Node) bool {
	switch w.typ(n) {
	case "arrow_function", "function_expression", "function", "generator_function", "class":
		return true
	}
	return false
}

func (w *tsWalker) isFunctionLike(n *ts.Node) bool {
	if n == nil {
		return false
	}
	switch w.typ(n) {
	case "arrow_function", "function_expression", "function", "generator_function":
		return true
	}
	return false
}

// isFunctionExpression excludes arrows (TypeScript's isFunctionExpression).
func (w *tsWalker) isFunctionExpression(n *ts.Node) bool {
	if n == nil {
		return false
	}
	switch w.typ(n) {
	case "function_expression", "function", "generator_function":
		return true
	}
	return false
}

// hasNavigationBarName: computed member names count only when the expression
// is an entity name, a string, or a number.
func (w *tsWalker) hasNavigationBarName(name *ts.Node) bool {
	if name == nil {
		return false
	}
	if w.typ(name) != "computed_property_name" {
		return true
	}
	if name.NamedChildCount() == 0 {
		return false
	}
	e := name.NamedChild(0)
	switch w.typ(e) {
	case "string", "number":
		return true
	case "template_string":
		return !w.hasSubstitution(e)
	}
	return w.isEntityName(e)
}

func (w *tsWalker) isEntityName(n *ts.Node) bool {
	switch w.typ(n) {
	case "identifier":
		return true
	case "member_expression":
		obj := w.field(n, "object")
		prop := w.field(n, "property")
		return prop != nil && w.typ(prop) == "property_identifier" && obj != nil && w.isEntityName(obj)
	}
	return false
}

func (w *tsWalker) hasSubstitution(n *ts.Node) bool {
	for i := 0; i < n.NamedChildCount(); i++ {
		if w.typ(n.NamedChild(i)) == "template_substitution" {
			return true
		}
	}
	return false
}

// add is addChildrenRecursively.
func (w *tsWalker) add(n *ts.Node) {
	if n == nil || !n.IsNamed() {
		return
	}
	switch t := w.typ(n); t {
	case "comment", "identifier", "property_identifier", "type_identifier", "string", "number",
		"import_statement", "decorator", "export_clause", "import_alias",
		"property_signature", "shorthand_property_identifier":
		// Tokens, aliases, and leaf properties (never indexed, no children).
		return

	case "method_definition", "method_signature", "abstract_method_signature":
		name := w.methodName(n)
		if !w.hasNavigationBarName(name) {
			return
		}
		if t == "method_definition" && w.text(name) == "constructor" && w.inClassBody(n) {
			w.addNodeWithRecursiveChild(item("constructor", lsp.SymbolConstructor, w.declStart(n), n), w.field(n, "body"))
			return
		}
		it := item(w.nm(name), lsp.SymbolMethod, w.declStart(n), n)
		it.key = w.text(name)
		w.addNodeWithRecursiveChild(it, w.field(n, "body"))

	case "public_field_definition", "field_definition":
		name := w.field(n, "name")
		if !w.hasNavigationBarName(name) {
			return
		}
		it := item(w.nm(name), kindProperty, w.declStart(n), n)
		it.key = w.text(name)
		w.addNodeWithRecursiveInitializer(it, w.field(n, "value"))

	case "spread_element":
		if p := n.Parent(); p != nil && w.typ(p) == "object" {
			return // SpreadAssignment is a leaf
		}
		w.forEachChild(n)

	case "variable_declarator":
		name := w.field(n, "name")
		if name == nil {
			return
		}
		if w.isBindingPattern(name) {
			w.addBindingPattern(name)
			return
		}
		it := item(w.nm(name), lsp.SymbolVariable, line(n.StartPoint()), n)
		it.key, it.ctorLike = w.text(name), true
		w.addNodeWithRecursiveInitializer(it, w.field(n, "value"))

	case "pair":
		key := w.field(n, "key")
		if key == nil {
			return
		}
		val := w.unwrapGenericArrow(w.field(n, "value"))
		kind := kindProperty
		if w.isFunctionLike(val) {
			kind = lsp.SymbolMethod
		}
		it := item(w.nm(key), kind, line(n.StartPoint()), n)
		it.key = w.text(key)
		w.addNodeWithRecursiveInitializer(it, val)

	case "function_declaration", "generator_function_declaration", "function_signature":
		nameNode := w.field(n, "name")
		name := w.nm(nameNode)
		if nameNode != nil {
			w.track(w.text(nameNode))
		} else {
			name = "default"
		}
		it := item(name, lsp.SymbolFunction, w.declStart(n), n)
		it.key, it.ctorLike = w.text(nameNode), true
		w.addNodeWithRecursiveChild(it, w.field(n, "body"))

	case "arrow_function", "function_expression", "function", "generator_function":
		if p := n.Parent(); p != nil && w.typ(p) == "export_statement" {
			// `export default function () {}` is a FunctionDeclaration with
			// the default modifier: named, or "default".
			nameNode := w.field(n, "name")
			name := w.nm(nameNode)
			if name == "" {
				name = "default"
			}
			it := item(name, lsp.SymbolFunction, line(p.StartPoint()), n)
			it.key, it.ctorLike = w.text(nameNode), true
			w.addNodeWithRecursiveChild(it, w.field(n, "body"))
			return
		}
		name, key := w.functionOrClassName(n)
		it := item(name, lsp.SymbolFunction, line(n.StartPoint()), n)
		it.key, it.ctorLike = key, t != "arrow_function"
		w.addNodeWithRecursiveChild(it, w.field(n, "body"))

	case "enum_declaration":
		nameNode := w.field(n, "name")
		it := item(w.nm(nameNode), lsp.SymbolEnum, w.declStart(n), n)
		it.key = w.text(nameNode)
		f := w.startNode(it)
		if body := w.field(n, "body"); body != nil {
			for i := 0; i < body.NamedChildCount(); i++ {
				m := body.NamedChild(i)
				mn := m
				switch w.typ(m) {
				case "property_identifier", "string", "number":
				case "enum_assignment":
					mn = w.field(m, "name")
					if mn == nil || w.typ(mn) == "computed_property_name" {
						continue
					}
				default:
					continue
				}
				leaf := item(w.nm(mn), lsp.SymbolConstant, line(m.StartPoint()), m)
				leaf.key = w.text(mn)
				w.addLeafNode(leaf)
			}
		}
		w.endNode(f)

	case "class_declaration", "abstract_class_declaration", "class", "interface_declaration":
		kind := lsp.SymbolClass
		if t == "interface_declaration" {
			kind = lsp.SymbolInterface
		}
		nameNode := w.field(n, "name")
		name, key := w.nm(nameNode), w.text(nameNode)
		start := w.declStart(n)
		exported := false
		if p := n.Parent(); p != nil && w.typ(p) == "export_statement" {
			exported = true
			start = line(p.StartPoint())
		}
		if name == "" {
			if exported {
				name = "default"
			} else {
				name, key = w.functionOrClassName(n)
			}
		}
		it := item(name, kind, start, n)
		it.key = key
		it.classDecl = t == "class_declaration" || t == "abstract_class_declaration" || t == "class" && exported
		f := w.startNode(it)
		if body := w.field(n, "body"); body != nil {
			w.forEachChild(body)
		}
		w.endNode(f)

	case "internal_module", "module":
		name := w.field(n, "name")
		it := item(w.nm(name), lsp.SymbolModule, w.declStart(n), n)
		it.key = w.text(name)
		w.addNodeWithRecursiveChild(it, w.field(n, "body"))

	case "ambient_declaration":
		// `declare global { … }` has no module node in tree-sitter; TypeScript
		// models it as a module declaration named "global".
		if n.NamedChildCount() > 0 && w.typ(n.NamedChild(0)) == "statement_block" && strings.Contains(w.textBefore(n, n.NamedChild(0)), "global") {
			it := item("global", lsp.SymbolModule, w.declStart(n), n)
			it.key = "global"
			w.addNodeWithRecursiveChild(it, n.NamedChild(0))
			return
		}
		w.forEachChild(n)

	case "export_statement":
		w.addExportStatement(n)

	case "index_signature":
		for i := 0; i < n.NamedChildCount(); i++ {
			if w.typ(n.NamedChild(i)) == "mapped_type_clause" {
				w.forEachChild(n) // a mapped type, not an IndexSignature
				return
			}
		}
		w.addLeafNode(item("[]", lsp.SymbolVariable, line(n.StartPoint()), n))

	case "call_signature":
		if p := n.Parent(); p != nil && (w.typ(p) == "interface_body" || w.typ(p) == "object_type") {
			w.addLeafNode(item("()", lsp.SymbolVariable, line(n.StartPoint()), n))
			return
		}
		w.forEachChild(n)

	case "construct_signature":
		w.addLeafNode(item("new()", lsp.SymbolVariable, line(n.StartPoint()), n))

	case "type_alias_declaration":
		nameNode := w.field(n, "name")
		it := item(w.nm(nameNode), lsp.SymbolVariable, w.declStart(n), n)
		it.key = w.text(nameNode)
		w.addLeafNode(it)

	case "formal_parameters":
		// JavaScript's grammar has no parameter wrapper: patterns and
		// defaults sit directly in the list.
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			switch w.typ(c) {
			case "object_pattern", "array_pattern":
				w.addBindingPattern(c)
			case "assignment_pattern":
				if l := w.field(c, "left"); l != nil && w.isBindingPattern(l) {
					w.addBindingPattern(l)
				}
				w.add(w.field(c, "right"))
			case "rest_pattern":
				if c.NamedChildCount() > 0 && w.isBindingPattern(c.NamedChild(0)) {
					w.addBindingPattern(c.NamedChild(0))
				}
			default:
				w.add(c)
			}
		}

	case "required_parameter", "optional_parameter":
		// Reached only through forEachChild of a function initializer (item
		// functions walk their body alone). A destructured parameter binds
		// BindingElements — items, like any destructuring declaration.
		if p := w.field(n, "pattern"); p != nil && w.isBindingPattern(p) {
			w.addBindingPattern(p)
		}
		w.add(w.field(n, "type"))
		w.add(w.field(n, "value"))

	case "for_in_statement":
		// `for (const x of xs)`: TypeScript's initializer is a variable
		// declaration list, so x is an item; a bare `for (x of xs)` is not.
		if left := w.field(n, "left"); left != nil && w.hasDeclarationKeyword(n) {
			w.addDeclaredName(left)
			w.add(w.field(n, "right"))
			w.add(w.field(n, "body"))
			return
		}
		w.forEachChild(n)

	case "catch_clause":
		// The catch binding is a VariableDeclaration in TypeScript's AST.
		if param := w.field(n, "parameter"); param != nil {
			w.addDeclaredName(param)
		}
		w.add(w.field(n, "body"))

	case "assignment_expression":
		if w.addAssignmentDeclaration(n) {
			return
		}
		w.forEachChild(n)

	case "call_expression":
		if w.isJS && w.addDefinePropertyCall(n) {
			return
		}
		w.forEachChild(n)

	case "expression_statement":
		// tree-sitter-typescript predates `using` / `await using`: it parses
		// `using x = init` as the assignment `x = init` and drops the keyword.
		// TypeScript sees a variable declaration.
		if a := w.usingDeclaration(n); a != nil {
			left := w.field(a, "left")
			it := item(w.nm(left), lsp.SymbolVariable, line(a.StartPoint()), a)
			it.key, it.ctorLike = w.text(left), true
			w.addNodeWithRecursiveInitializer(it, w.field(a, "right"))
			return
		}
		w.forEachChild(n)

	default:
		w.forEachChild(n)
	}
}

// methodName returns a method's name node. For `async *gen()` the grammar
// puts "async" in the name field and the real name in a following sibling;
// the real name is the last name-shaped child before the parameters.
func (w *tsWalker) methodName(n *ts.Node) *ts.Node {
	name := w.field(n, "name")
	params := w.field(n, "parameters")
	if name == nil || params == nil {
		return name
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.StartByte() >= params.StartByte() {
			break
		}
		if c.StartByte() <= name.StartByte() {
			continue
		}
		switch w.typ(c) {
		case "property_identifier", "private_property_identifier", "string", "number", "computed_property_name":
			name = c
		}
	}
	return name
}

// genericArrowWrapper reports whether n is tree-sitter's misparse of an async
// generic arrow, `async <T>(…) => …`, as the comparison `(async < T) > (…) =>
// …`; it returns the arrow.
func (w *tsWalker) genericArrowWrapper(n *ts.Node) (*ts.Node, bool) {
	if n == nil || w.typ(n) != "binary_expression" {
		return nil, false
	}
	l, r := w.field(n, "left"), w.field(n, "right")
	if l == nil || r == nil || w.typ(r) != "arrow_function" || w.typ(l) != "binary_expression" {
		return nil, false
	}
	ll := w.field(l, "left")
	if ll == nil || w.typ(ll) != "identifier" || w.text(ll) != "async" {
		return nil, false
	}
	if !strings.Contains(w.textBefore(l, r), "<") {
		return nil, false
	}
	return r, true
}

func (w *tsWalker) unwrapGenericArrow(n *ts.Node) *ts.Node {
	if a, ok := w.genericArrowWrapper(n); ok {
		return a
	}
	return n
}

// usingDeclaration returns the assignment of a `using x = …` / `await using x
// = …` statement, or nil.
func (w *tsWalker) usingDeclaration(stmt *ts.Node) *ts.Node {
	s := strings.TrimSpace(w.text(stmt))
	if !strings.HasPrefix(s, "using ") && !strings.HasPrefix(s, "await using ") {
		return nil
	}
	if stmt.NamedChildCount() == 0 {
		return nil
	}
	a := stmt.NamedChild(0)
	if w.typ(a) == "await_expression" && a.NamedChildCount() > 0 {
		a = a.NamedChild(0)
	}
	if w.typ(a) != "assignment_expression" {
		return nil
	}
	if l := w.field(a, "left"); l == nil || w.typ(l) != "identifier" {
		return nil
	}
	return a
}

func (w *tsWalker) inClassBody(n *ts.Node) bool {
	p := n.Parent()
	return p != nil && w.typ(p) == "class_body"
}

// textBefore returns the source between n's start and child's start.
func (w *tsWalker) textBefore(n, child *ts.Node) string {
	return string(w.src[n.StartByte():child.StartByte()])
}

// addExportStatement routes the export forms: a wrapped declaration
// (`export class …`), `export default <expr>` (ExportAssignment), and
// `export = <expr>`. Specifier lists and re-exports are aliases.
func (w *tsWalker) addExportStatement(n *ts.Node) {
	if decl := w.field(n, "declaration"); decl != nil {
		w.add(decl)
		return
	}
	val := w.field(n, "value")
	if val == nil {
		// `export = x;` has no value field; find the expression after "=".
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if !c.IsNamed() && w.text(c) == "=" && i+1 < n.ChildCount() {
				w.addExportAssignment(n, n.Child(i+1), "export=")
				return
			}
		}
		return
	}
	switch w.typ(val) {
	case "function_expression", "function", "generator_function", "class":
		w.add(val) // a default-exported declaration
		return
	}
	w.addExportAssignment(n, val, "default")
}

// addExportAssignment ports the ExportAssignment case: object or call
// expressions contribute children, a function contributes its body, anything
// else is a leaf. Its kind is getNodeKind(expression), "" → const.
func (w *tsWalker) addExportAssignment(stmt, expr *ts.Node, name string) {
	it := item(name, lsp.SymbolConstant, line(stmt.StartPoint()), stmt)
	if w.typ(expr) == "identifier" {
		it.key = w.text(expr) // getNameOfDeclaration(ExportAssignment)
	}
	switch w.typ(expr) {
	case "arrow_function", "function_expression", "function", "generator_function":
		it.kind = lsp.SymbolFunction
		w.addNodeWithRecursiveChild(it, w.field(expr, "body"))
		return
	case "class":
		it.kind = lsp.SymbolClass
	case "object", "call_expression":
		w.addNodeWithRecursiveChild(it, expr)
		return
	}
	w.addLeafNode(it)
}

func (w *tsWalker) hasDeclarationKeyword(n *ts.Node) bool {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.IsNamed() {
			continue
		}
		switch w.text(c) {
		case "const", "let", "var", "using":
			return true
		}
	}
	return false
}

// addDeclaredName adds an initializer-less declaration (a for-of/for-in
// binding, a catch parameter): an identifier leaf or a destructuring pattern.
func (w *tsWalker) addDeclaredName(n *ts.Node) {
	if w.isBindingPattern(n) {
		w.addBindingPattern(n)
		return
	}
	if w.typ(n) == "identifier" {
		w.addBinding(n, n, nil)
	}
}

func (w *tsWalker) isBindingPattern(n *ts.Node) bool {
	switch w.typ(n) {
	case "object_pattern", "array_pattern":
		return true
	}
	return false
}

// addBinding adds one BindingElement item (the kind of its root declaration —
// a variable) spanning elem, with init as its default-value initializer.
func (w *tsWalker) addBinding(name, elem, init *ts.Node) {
	it := item(w.nm(name), lsp.SymbolVariable, line(elem.StartPoint()), elem)
	it.key = w.text(name)
	if init == nil {
		w.addLeafNode(it)
		return
	}
	w.addNodeWithRecursiveInitializer(it, init)
}

// addBindingPattern walks a destructuring pattern: every bound name is a
// BindingElement item, with a default value treated as its initializer.
func (w *tsWalker) addBindingPattern(p *ts.Node) {
	for i := 0; i < p.NamedChildCount(); i++ {
		c := p.NamedChild(i)
		switch w.typ(c) {
		case "shorthand_property_identifier_pattern", "identifier":
			w.addBinding(c, c, nil)
		case "pair_pattern":
			w.addBindingTarget(c, w.field(c, "value"))
		case "object_assignment_pattern", "assignment_pattern":
			w.addBindingTarget(c, c)
		case "rest_pattern":
			if c.NamedChildCount() > 0 {
				inner := c.NamedChild(0)
				if w.isBindingPattern(inner) {
					w.addBindingPattern(inner)
				} else {
					w.addBinding(inner, c, nil)
				}
			}
		case "object_pattern", "array_pattern":
			w.addBindingPattern(c)
		}
	}
}

func (w *tsWalker) addBindingTarget(elem, v *ts.Node) {
	if v == nil {
		return
	}
	switch w.typ(v) {
	case "object_pattern", "array_pattern":
		w.addBindingPattern(v)
	case "assignment_pattern", "object_assignment_pattern":
		l := w.field(v, "left")
		if l == nil {
			return
		}
		if w.isBindingPattern(l) {
			w.addBindingPattern(l)
			return
		}
		w.addBinding(l, elem, w.field(v, "right"))
	default:
		w.addBinding(v, elem, nil)
	}
}

// assignedName is getAssignedName: the name a function or class expression
// takes from where it is assigned (display text, merge key).
func (w *tsWalker) assignedName(n *ts.Node) (string, string) {
	p := n.Parent()
	if a, ok := w.genericArrowWrapper(p); ok && a.StartByte() == n.StartByte() {
		n, p = p, p.Parent() // see through the misparsed generic-arrow wrapper
	}
	if p == nil {
		return "", ""
	}
	switch w.typ(p) {
	case "pair":
		if k := w.field(p, "key"); k != nil {
			return w.text(k), w.text(k)
		}
	case "variable_declarator":
		if nm := w.field(p, "name"); nm != nil && w.typ(nm) == "identifier" {
			return w.text(nm), w.text(nm)
		}
	case "assignment_expression", "augmented_assignment_expression", "binary_expression":
		// getAssignedName checks only that n is the right operand of a
		// BinaryExpression — any operator: `opts.cb || function () {}` is "cb".
		if r := w.field(p, "right"); r == nil || r.StartByte() != n.StartByte() {
			return "", ""
		}
		l := w.field(p, "left")
		if l == nil {
			return "", ""
		}
		switch w.typ(l) {
		case "identifier":
			return w.text(l), w.text(l)
		case "member_expression":
			prop := w.text(w.field(l, "property"))
			return prop, prop
		case "subscript_expression":
			// getElementOrPropertyAccessArgumentExpressionOrName: a literal
			// index names it; anything else names it "[<index>]".
			idx := w.field(l, "index")
			if idx == nil {
				return "", ""
			}
			switch w.typ(idx) {
			case "string", "number":
				return w.text(idx), w.text(idx)
			}
			return "[" + w.text(idx) + "]", w.text(l)
		}
	case "assignment_pattern", "object_assignment_pattern":
		// A BindingElement default only: a parameter default (`(cb = () => {})`)
		// is a Parameter initializer, which assigns no name.
		if gp := p.Parent(); gp == nil || !w.isBindingPattern(gp) {
			return "", ""
		}
		if r := w.field(p, "right"); r != nil && r.StartByte() == n.StartByte() {
			if l := w.field(p, "left"); l != nil && !w.isBindingPattern(l) {
				return w.text(l), w.text(l)
			}
		}
	}
	return "", ""
}

const maxNavLength = 150

// functionOrClassName is getItemName for a function or class expression item:
// its own name, its assigned name, or — for an anonymous call argument — the
// "<callee>(<literal args>) callback" placeholder codemap treats as
// anonymous. The second result is the merge key (getNameOfDeclaration).
func (w *tsWalker) functionOrClassName(n *ts.Node) (string, string) {
	if name := w.field(n, "name"); name != nil && name.EndByte() > name.StartByte() {
		return w.nm(name), w.text(name)
	}
	if name, key := w.assignedName(n); name != "" {
		return cleanText(name), key
	}
	if w.typ(n) == "class" {
		return "<class>", ""
	}
	args := n.Parent()
	if _, ok := w.genericArrowWrapper(args); ok {
		args = args.Parent()
	}
	if args != nil && w.typ(args) == "arguments" {
		if call := args.Parent(); call != nil && w.typ(call) == "call_expression" {
			if name, ok := w.calledExpressionName(w.field(call, "function")); ok {
				name = cleanText(name)
				if len(name) > maxNavLength {
					return name + " callback", ""
				}
				var lits []string
				for i := 0; i < args.NamedChildCount(); i++ {
					a := args.NamedChild(i)
					switch w.typ(a) {
					case "string", "template_string":
						lits = append(lits, w.text(a))
					}
				}
				return name + "(" + cleanText(strings.Join(lits, ", ")) + ") callback", ""
			}
		}
	}
	return "<function>", ""
}

// calledExpressionName is getCalledExpressionName: a dotted identifier chain.
func (w *tsWalker) calledExpressionName(n *ts.Node) (string, bool) {
	if n == nil {
		return "", false
	}
	switch w.typ(n) {
	case "identifier":
		return w.text(n), true
	case "member_expression":
		prop := w.field(n, "property")
		if prop == nil || w.typ(prop) != "property_identifier" {
			return "", false
		}
		left, ok := w.calledExpressionName(w.field(n, "object"))
		if !ok {
			return w.text(prop), true
		}
		return left + "." + w.text(prop), true
	}
	return "", false
}

var navNewlines = regexp.MustCompile(`\\?(?:\r?\n|[\r\x{2028}\x{2029}])`)

// cleanText truncates to 150 characters (plus "...") and drops line breaks.
func cleanText(s string) string {
	if len(s) > maxNavLength {
		s = s[:maxNavLength] + "..."
	}
	return navNewlines.ReplaceAllString(s, "")
}
