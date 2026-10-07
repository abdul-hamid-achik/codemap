package sittersrc

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// This file emulates pyright's documentSymbol (SymbolIndexer.indexSymbols over
// the binder's scopes, pyright 1.1.408) on tree-sitter-python:
//
//   - every scope (module, class, function) lists its symbols; for each, the
//     LAST declaration with a type (def, class, annotated variable, type
//     alias) wins, else the FIRST declaration; an alias (import) winner is
//     dropped, as is a variable named "_";
//   - defs and classes recurse into their own scope, spanning from their first
//     decorator to the end of their body; variables span their name;
//   - like pyright's binder, function bodies are bound after their enclosing
//     scope (a deferred FIFO), so `self.x = …` declarations land in the class
//     scope after the class body's own, and `global X` assignments in the
//     module scope after module-level code.
//
// Parameters are not modeled: they only ever appear inside a callable, where
// codemap drops variables, and they can never win over a def of the same name.

type pyDeclKind int

const (
	pyVar pyDeclKind = iota
	pyFunc
	pyClass
	pyAlias
	pyTypeAlias
)

type pyDecl struct {
	kind       pyDeclKind
	typed      bool
	start, end int
	scope      *pyScope // the def/class body's own scope

	// Import bindings (pyAlias): the module spec as written ("a.b", ".pkg")
	// and the imported name (empty when the binding is the module itself).
	module, orig string
}

type pyScope struct {
	kind    pyDeclKind // pyFunc / pyClass for those bodies; pyVar marks the module
	order   []string
	decls   map[string][]pyDecl
	parent  *pyScope
	globals map[string]bool
	selfVar string // first parameter of a method: `self.x` targets bind in parent
	static  bool

	// For call references: the scope's FQN as codemap indexes it ("" for the
	// module), and a function's parameter names (they shadow outer bindings).
	fqn    string
	params map[string]bool
}

func newPyScope(kind pyDeclKind, parent *pyScope) *pyScope {
	return &pyScope{kind: kind, parent: parent, decls: map[string][]pyDecl{}}
}

func (s *pyScope) declare(name string, d pyDecl) {
	if name == "" {
		return
	}
	if _, ok := s.decls[name]; !ok {
		s.order = append(s.order, name)
	}
	s.decls[name] = append(s.decls[name], d)
}

type pyWalker struct {
	lang     *ts.Language
	src      []byte
	module   *pyScope
	deferred []pyDeferred

	scopeOf map[uint32]*pyScope // def/class node start byte → its body scope
	imports []string            // module specs for file→file import edges
}

type pyDeferred struct {
	body  *ts.Node
	scope *pyScope
}

func pythonSymbols(lang *ts.Language, src []byte, root *ts.Node) []lsp.DocumentSymbol {
	return bindPython(lang, src, root).emit(nil)
}

// bindPython runs the binder emulation over a whole file.
func bindPython(lang *ts.Language, src []byte, root *ts.Node) *pyWalker {
	w := &pyWalker{lang: lang, src: src, scopeOf: map[uint32]*pyScope{}}
	w.module = newPyScope(pyVar, nil)
	w.bindBlock(root, w.module)
	for len(w.deferred) > 0 {
		d := w.deferred[0]
		w.deferred = w.deferred[1:]
		w.bindBlock(d.body, d.scope)
	}
	return w
}

func (w *pyWalker) typ(n *ts.Node) string { return n.Type(w.lang) }

func (w *pyWalker) field(n *ts.Node, name string) *ts.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldName(name, w.lang)
}

func (w *pyWalker) text(n *ts.Node) string { return text(w.src, n) }

// emit applies pyright's per-symbol declaration choice and builds the tree
// (nil = the module scope).
func (w *pyWalker) emit(s *pyScope) []lsp.DocumentSymbol {
	if s == nil {
		s = w.module
	}
	var out []lsp.DocumentSymbol
	for _, name := range s.order {
		decls := s.decls[name]
		var chosen *pyDecl
		for i := len(decls) - 1; i >= 0; i-- {
			if decls[i].typed {
				chosen = &decls[i]
				break
			}
		}
		if chosen == nil {
			chosen = &decls[0]
		}
		switch chosen.kind {
		case pyAlias:
			continue
		case pyFunc:
			kind := lsp.SymbolFunction
			if s.kind == pyClass {
				kind = lsp.SymbolMethod
			}
			out = append(out, symbol(name, kind, chosen.start, chosen.end, w.emit(chosen.scope)))
		case pyClass:
			out = append(out, symbol(name, lsp.SymbolClass, chosen.start, chosen.end, w.emit(chosen.scope)))
		default:
			if name == "_" {
				continue
			}
			out = append(out, symbol(name, lsp.SymbolVariable, chosen.start, chosen.end, nil))
		}
	}
	return out
}

// bindBlock binds the statements of one scope body (module, block).
func (w *pyWalker) bindBlock(n *ts.Node, s *pyScope) {
	if n == nil {
		return
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		w.bindStatement(n.NamedChild(i), s)
	}
}

func (w *pyWalker) bindStatement(n *ts.Node, s *pyScope) {
	switch w.typ(n) {
	case "decorated_definition":
		def := w.field(n, "definition")
		if def != nil {
			w.bindDefinition(def, n, s)
		}
	case "function_definition", "class_definition":
		w.bindDefinition(n, n, s)
	case "expression_statement":
		for i := 0; i < n.NamedChildCount(); i++ {
			w.bindExpressionStatement(n.NamedChild(i), s)
		}
	case "if_statement":
		w.bindWalrus(w.field(n, "condition"), s)
		w.bindBlock(w.field(n, "consequence"), s)
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			switch w.typ(c) {
			case "elif_clause":
				w.bindWalrus(w.field(c, "condition"), s)
				w.bindBlock(w.field(c, "consequence"), s)
			case "else_clause":
				w.bindBlock(w.field(c, "body"), s)
			}
		}
	case "for_statement":
		w.bindTarget(w.field(n, "left"), s, false)
		w.bindBlock(w.field(n, "body"), s)
		if alt := w.field(n, "alternative"); alt != nil {
			w.bindBlock(w.field(alt, "body"), s)
		}
	case "while_statement":
		w.bindWalrus(w.field(n, "condition"), s)
		w.bindBlock(w.field(n, "body"), s)
		if alt := w.field(n, "alternative"); alt != nil {
			w.bindBlock(w.field(alt, "body"), s)
		}
	case "try_statement":
		// pyright's binder walks try, else, the except clauses, then finally —
		// the else suite binds before the handlers it textually follows.
		w.bindBlock(w.field(n, "body"), s)
		for _, want := range []string{"else_clause", "except_clause", "finally_clause"} {
			for i := 0; i < n.NamedChildCount(); i++ {
				c := n.NamedChild(i)
				t := w.typ(c)
				if t == "except_group_clause" {
					t = "except_clause"
				}
				if t != want {
					continue
				}
				if t == "except_clause" {
					w.bindExceptClause(c, s)
				} else if b := w.field(c, "body"); b != nil {
					w.bindBlock(b, s)
				} else {
					w.bindBlockChildren(c, s)
				}
			}
		}
	case "with_statement":
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			if w.typ(c) == "with_clause" {
				w.bindWithClause(c, s)
			}
		}
		w.bindBlock(w.field(n, "body"), s)
	case "match_statement":
		if body := w.field(n, "body"); body != nil {
			for i := 0; i < body.NamedChildCount(); i++ {
				c := body.NamedChild(i)
				if w.typ(c) == "case_clause" {
					w.bindBlock(w.field(c, "consequence"), s)
				}
			}
		}
	case "import_statement":
		w.bindImport(n, s)
	case "import_from_statement", "future_import_statement":
		w.bindImportFrom(n, s)
	case "global_statement":
		for i := 0; i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); w.typ(c) == "identifier" && s.globals != nil {
				s.globals[w.text(c)] = true
			}
		}
	case "type_alias_statement":
		if left := w.field(n, "left"); left != nil {
			name := left
			if w.typ(left) != "identifier" && left.NamedChildCount() > 0 {
				name = left.NamedChild(0)
			}
			st, en := span(name)
			s.declare(w.text(name), pyDecl{kind: pyTypeAlias, typed: true, start: st, end: en})
		}
	case "block":
		w.bindBlock(n, s)
	case "assignment", "augmented_assignment", "named_expression":
		// This grammar inlines expression_statement: assignments sit directly
		// in the block.
		w.bindExpressionStatement(n, s)
	}
}

func (w *pyWalker) bindBlockChildren(n *ts.Node, s *pyScope) {
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.typ(c) == "block" {
			w.bindBlock(c, s)
		}
	}
}

// bindDefinition declares a def or class in s. A class body binds inline (it
// is its own scope); a def body is deferred, as in pyright's binder.
func (w *pyWalker) bindDefinition(def, outer *ts.Node, s *pyScope) {
	name := w.text(w.field(def, "name"))
	start, end := line(outer.StartPoint()), pyEndLine(w, def)
	body := w.field(def, "body")
	if w.typ(def) == "class_definition" {
		cs := newPyScope(pyClass, s)
		cs.fqn = joinFQN(s.fqn, name)
		w.scopeOf[def.StartByte()] = cs
		s.declare(name, pyDecl{kind: pyClass, typed: true, start: start, end: end, scope: cs})
		w.bindBlock(body, cs)
		w.bindSlots(body, cs)
		return
	}
	fs := newPyScope(pyFunc, s)
	fs.globals = map[string]bool{}
	fs.fqn = joinFQN(s.fqn, name)
	fs.params = w.paramNames(def)
	w.scopeOf[def.StartByte()] = fs
	if s.kind == pyClass {
		fs.selfVar = w.firstParam(def)
		fs.static = w.hasDecorator(outer, "staticmethod")
	}
	// `global` applies to the whole body, wherever it appears.
	w.collectGlobals(body, fs)
	s.declare(name, pyDecl{kind: pyFunc, typed: true, start: start, end: end, scope: fs})
	w.deferred = append(w.deferred, pyDeferred{body: body, scope: fs})
}

// bindSlots mirrors pyright's __slots__ handling: when a class body assigns
// __slots__ a string, or a tuple/list made only of strings, each slot name is
// declared in the class scope (at its string) after the body's own
// declarations — so it precedes any `self.x = …` in a method.
func (w *pyWalker) bindSlots(body *ts.Node, cs *pyScope) {
	if body == nil {
		return
	}
	// pyright keeps the pending slots in one binder field that every nested
	// class resets on entry, so a class statement after __slots__ discards
	// them. Replay that state machine in source order (method bodies are
	// bound later and never interfere).
	var value *ts.Node
	var scan func(n *ts.Node)
	scan = func(n *ts.Node) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			switch w.typ(c) {
			case "class_definition":
				value = nil
			case "decorated_definition":
				if d := w.field(c, "definition"); d != nil && w.typ(d) == "class_definition" {
					value = nil
				}
			case "function_definition", "lambda":
			case "assignment":
				if l := w.field(c, "left"); l != nil && w.typ(l) == "identifier" && w.text(l) == "__slots__" {
					value = w.field(c, "right")
				}
			default:
				scan(c)
			}
		}
	}
	scan(body)
	if value == nil {
		return
	}
	var entries []*ts.Node
	switch w.typ(value) {
	case "string":
		entries = append(entries, value)
	case "tuple", "list", "parenthesized_expression":
		for i := 0; i < value.NamedChildCount(); i++ {
			e := value.NamedChild(i)
			if w.typ(e) != "string" {
				return // not understood: pyright declares none
			}
			entries = append(entries, e)
		}
	default:
		return
	}
	for _, e := range entries {
		name := w.stringValue(e)
		if name == "" || name == "__dict__" || name == "__weakref__" {
			continue
		}
		st, en := span(e)
		cs.declare(name, pyDecl{kind: pyVar, start: st, end: en})
	}
}

// stringValue returns a plain string literal's contents ("" when it has
// interpolations or prefixes pyright would not treat as a slot name).
func (w *pyWalker) stringValue(n *ts.Node) string {
	var b []byte
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.typ(c) {
		case "string_content":
			b = append(b, w.text(c)...)
		case "interpolation":
			return ""
		}
	}
	return string(b)
}

func (w *pyWalker) collectGlobals(n *ts.Node, fs *pyScope) {
	if n == nil {
		return
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.typ(c) {
		case "global_statement":
			for j := 0; j < c.NamedChildCount(); j++ {
				if id := c.NamedChild(j); w.typ(id) == "identifier" {
					fs.globals[w.text(id)] = true
				}
			}
		case "function_definition", "class_definition", "decorated_definition", "lambda":
			continue
		default:
			w.collectGlobals(c, fs)
		}
	}
}

func (w *pyWalker) firstParam(def *ts.Node) string {
	params := w.field(def, "parameters")
	if params == nil || params.NamedChildCount() == 0 {
		return ""
	}
	p := params.NamedChild(0)
	switch w.typ(p) {
	case "identifier":
		return w.text(p)
	case "typed_parameter":
		if p.NamedChildCount() > 0 && w.typ(p.NamedChild(0)) == "identifier" {
			return w.text(p.NamedChild(0))
		}
	case "default_parameter", "typed_default_parameter":
		if nm := w.field(p, "name"); nm != nil {
			return w.text(nm)
		}
	}
	return ""
}

func (w *pyWalker) hasDecorator(outer *ts.Node, name string) bool {
	if w.typ(outer) != "decorated_definition" {
		return false
	}
	for i := 0; i < outer.NamedChildCount(); i++ {
		d := outer.NamedChild(i)
		if w.typ(d) == "decorator" && d.NamedChildCount() > 0 && w.text(d.NamedChild(0)) == name {
			return true
		}
	}
	return false
}

func (w *pyWalker) bindExpressionStatement(n *ts.Node, s *pyScope) {
	switch w.typ(n) {
	case "assignment":
		typed := w.field(n, "type") != nil
		w.bindTarget(w.field(n, "left"), s, typed)
		right := w.field(n, "right")
		if right != nil && w.typ(right) == "assignment" {
			w.bindExpressionStatement(right, s) // a = b = 1
		} else {
			w.bindWalrus(right, s)
		}
	case "augmented_assignment":
		w.bindTarget(w.field(n, "left"), s, false)
		w.bindWalrus(w.field(n, "right"), s)
	default:
		w.bindWalrus(n, s)
	}
}

// bindTarget declares the names an assignment target binds.
func (w *pyWalker) bindTarget(n *ts.Node, s *pyScope, typed bool) {
	if n == nil {
		return
	}
	switch w.typ(n) {
	case "identifier":
		name := w.text(n)
		st, en := span(n)
		d := pyDecl{kind: pyVar, typed: typed, start: st, end: en}
		if s.globals != nil && s.globals[name] {
			w.module.declare(name, d)
			return
		}
		s.declare(name, d)
	case "attribute":
		// self.x / cls.x in a method declares a member of the class scope.
		obj := w.field(n, "object")
		attr := w.field(n, "attribute")
		if obj == nil || attr == nil || s.kind != pyFunc || s.selfVar == "" || s.static {
			return
		}
		if w.typ(obj) == "identifier" && w.text(obj) == s.selfVar && s.parent != nil && s.parent.kind == pyClass {
			st, en := span(attr)
			s.parent.declare(w.text(attr), pyDecl{kind: pyVar, typed: typed, start: st, end: en})
		}
	case "pattern_list", "tuple_pattern", "list_pattern", "tuple", "list", "expression_list",
		"parenthesized_expression", "list_splat_pattern", "list_splat":
		for i := 0; i < n.NamedChildCount(); i++ {
			w.bindTarget(n.NamedChild(i), s, typed)
		}
	}
}

func (w *pyWalker) bindExceptClause(n *ts.Node, s *pyScope) {
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.typ(c) {
		case "as_pattern":
			if alias := w.field(c, "alias"); alias != nil {
				w.bindTarget(w.unwrapAsTarget(alias), s, false)
			}
		case "block":
			w.bindBlock(c, s)
		}
	}
	// Older grammars: `except E as e` → [expr, "as", identifier].
	for i := 0; i+1 < n.ChildCount(); i++ {
		if c := n.Child(i); !c.IsNamed() && w.text(c) == "as" {
			if id := n.Child(i + 1); w.typ(id) == "identifier" {
				w.bindTarget(id, s, false)
			}
		}
	}
}

func (w *pyWalker) bindWithClause(n *ts.Node, s *pyScope) {
	for i := 0; i < n.NamedChildCount(); i++ {
		item := n.NamedChild(i)
		if w.typ(item) != "with_item" {
			continue
		}
		v := w.field(item, "value")
		if v != nil && w.typ(v) == "as_pattern" {
			if alias := w.field(v, "alias"); alias != nil {
				w.bindTarget(w.unwrapAsTarget(alias), s, false)
			}
		}
	}
}

func (w *pyWalker) unwrapAsTarget(n *ts.Node) *ts.Node {
	if w.typ(n) == "as_pattern_target" && n.NamedChildCount() > 0 {
		return n.NamedChild(0)
	}
	return n
}

// bindWalrus declares `name := …` targets found in an expression, without
// entering lambdas or nested scopes.
func (w *pyWalker) bindWalrus(n *ts.Node, s *pyScope) {
	if n == nil {
		return
	}
	switch w.typ(n) {
	case "lambda", "function_definition", "class_definition":
		return
	case "named_expression":
		w.bindTarget(w.field(n, "name"), s, false)
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		w.bindWalrus(n.NamedChild(i), s)
	}
}

func (w *pyWalker) bindImport(n *ts.Node, s *pyScope) {
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		var name *ts.Node
		module := ""
		switch w.typ(c) {
		case "dotted_name":
			if c.NamedChildCount() > 0 {
				name = c.NamedChild(0) // `import a.b` binds a
				module = w.text(name)
				w.imports = append(w.imports, w.text(c))
			}
		case "aliased_import":
			name = w.field(c, "alias")
			module = w.text(w.field(c, "name"))
			w.imports = append(w.imports, module)
		}
		if name != nil {
			st, en := span(name)
			s.declare(w.text(name), pyDecl{kind: pyAlias, start: st, end: en, module: module})
		}
	}
}

func (w *pyWalker) bindImportFrom(n *ts.Node, s *pyScope) {
	module := w.field(n, "module_name")
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if module != nil && c.StartByte() == module.StartByte() {
			continue
		}
		var name *ts.Node
		orig := ""
		switch w.typ(c) {
		case "dotted_name":
			if c.NamedChildCount() > 0 {
				name = c.NamedChild(c.NamedChildCount() - 1)
				orig = w.text(c)
			}
		case "aliased_import":
			name = w.field(c, "alias")
			orig = w.text(w.field(c, "name"))
		}
		if name != nil {
			st, en := span(name)
			spec := w.text(module)
			s.declare(w.text(name), pyDecl{kind: pyAlias, start: st, end: en, module: spec, orig: orig})
			// The module itself, and the name as a possible submodule.
			w.imports = append(w.imports, spec, pyJoinModule(spec, orig))
		}
	}
}

// pyEndLine is where pyright's node range ends: the last token of the
// definition, ignoring trailing comments tree-sitter may attach to a block.
func pyEndLine(w *pyWalker, n *ts.Node) int {
	for {
		var last *ts.Node
		for i := n.ChildCount() - 1; i >= 0; i-- {
			c := n.Child(i)
			if w.typ(c) == "comment" || c.EndByte() == c.StartByte() {
				continue
			}
			last = c
			break
		}
		if last == nil {
			return line(n.EndPoint())
		}
		n = last
	}
}

// paramNames returns a def's parameter names (they shadow outer bindings for
// call resolution; they are never indexed).
func (w *pyWalker) paramNames(def *ts.Node) map[string]bool {
	out := map[string]bool{}
	params := w.field(def, "parameters")
	if params == nil {
		return out
	}
	for i := 0; i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		var id *ts.Node
		switch w.typ(p) {
		case "identifier":
			id = p
		case "typed_parameter", "list_splat_pattern", "dictionary_splat_pattern":
			if p.NamedChildCount() > 0 {
				id = p.NamedChild(0)
			}
		case "default_parameter", "typed_default_parameter":
			id = w.field(p, "name")
		}
		if id != nil && w.typ(id) == "identifier" {
			out[w.text(id)] = true
		}
	}
	return out
}

func joinFQN(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

// pyJoinModule appends a dotted name to a module spec, minding relative
// specs ("." + "x" is ".x", not "..x").
func pyJoinModule(spec, name string) string {
	if name == "" {
		return spec
	}
	if spec == "" || strings.HasSuffix(spec, ".") {
		return spec + name
	}
	return spec + "." + name
}
