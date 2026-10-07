package sittersrc

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Python call candidates. Before this backend Python had no call edges at all
// without --precise. The binder emulation already knows every scope's
// bindings, so calls whose target is text-attributable become name-provenance
// candidates (weight 0.7), resolved by the indexer exactly like
// tsscan.CallRefs: file-scoped, never by project-wide name.
//
// Linked: f() to a def/class bound in an enclosing scope of the same file
// (LEGB, with parameters, lambda parameters and comprehension targets
// shadowing); self.m() / cls.m() (also from closures inside the method) to a
// method of the enclosing class; C.m() to a method of a same-file class; and
// calls through import bindings — from m import f; f() / import pkg.mod as m;
// m.f() — resolved by the indexer's Python import resolver. Not linked:
// arbitrary obj.method() calls (that needs types; --precise), builtins,
// inherited methods, and calls through rebound variables.

type pyRefCtx struct {
	scope    *pyScope
	from     string // FQN of the innermost def/class, "" = the module
	fromLine int    // its 1-based start line (decorators included)
	shadow   map[string]bool
}

// pythonCallRefs returns the call candidates and import specs of a file.
func pythonCallRefs(w *pyWalker, root *ts.Node, relPath string) []extract.Reference {
	r := &pyRefWalker{w: w, rel: relPath, seen: map[string]bool{}}
	r.walk(root, pyRefCtx{scope: w.module})
	return r.out
}

type pyRefWalker struct {
	w    *pyWalker
	rel  string
	out  []extract.Reference
	seen map[string]bool
}

func (r *pyRefWalker) walk(n *ts.Node, ctx pyRefCtx) {
	if n == nil {
		return
	}
	w := r.w
	switch w.typ(n) {
	case "decorated_definition":
		def := w.field(n, "definition")
		for i := 0; i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); w.typ(c) == "decorator" {
				r.walk(c, ctx)
			}
		}
		if def != nil {
			r.walkDefinition(def, n, ctx)
		}
		return
	case "function_definition", "class_definition":
		r.walkDefinition(n, n, ctx)
		return
	case "lambda":
		inner := ctx
		inner.shadow = withNames(ctx.shadow, r.lambdaParams(n))
		r.walk(w.field(n, "body"), inner)
		return
	case "list_comprehension", "set_comprehension", "dictionary_comprehension", "generator_expression":
		inner := ctx
		inner.shadow = withNames(ctx.shadow, r.comprehensionTargets(n))
		r.walkChildren(n, inner)
		return
	case "call":
		r.call(w.field(n, "function"), n, ctx)
	}
	r.walkChildren(n, ctx)
}

func (r *pyRefWalker) walkChildren(n *ts.Node, ctx pyRefCtx) {
	for i := 0; i < n.NamedChildCount(); i++ {
		r.walk(n.NamedChild(i), ctx)
	}
}

// walkDefinition evaluates a def's parameters (defaults, annotations) and a
// class's bases in the enclosing scope, then its body in its own.
func (r *pyRefWalker) walkDefinition(def, outer *ts.Node, ctx pyRefCtx) {
	w := r.w
	body := w.field(def, "body")
	for i := 0; i < def.NamedChildCount(); i++ {
		c := def.NamedChild(i)
		if body != nil && c.StartByte() == body.StartByte() {
			continue
		}
		r.walk(c, ctx)
	}
	s := w.scopeOf[def.StartByte()]
	if s == nil || body == nil {
		return
	}
	r.walk(body, pyRefCtx{scope: s, from: s.fqn, fromLine: line(outer.StartPoint()) + 1})
}

func (r *pyRefWalker) call(callee, call *ts.Node, ctx pyRefCtx) {
	if callee == nil {
		return
	}
	parts := r.dotted(callee)
	if len(parts) == 0 || ctx.shadow[parts[0]] {
		return
	}
	at := line(call.StartPoint()) + 1
	if len(parts) == 2 && r.isSelf(parts[0], ctx.scope) {
		if cs := r.methodClass(ctx.scope, parts[0]); cs != nil && r.definesCallable(cs, parts[1]) {
			r.emit(ctx, at, joinFQN(cs.fqn, parts[1]), "")
		}
		return
	}
	decl, scope := r.lookup(parts[0], ctx.scope)
	if decl == nil {
		return
	}
	switch decl.kind {
	case pyFunc, pyClass:
		if len(parts) == 1 {
			r.emit(ctx, at, joinFQN(scope.fqn, parts[0]), "")
		} else if len(parts) == 2 && decl.kind == pyClass && decl.scope != nil && r.definesCallable(decl.scope, parts[1]) {
			r.emit(ctx, at, joinFQN(decl.scope.fqn, parts[1]), "")
		}
	case pyAlias:
		if decl.orig != "" {
			// from X import n [as k]: k() / k.attr() is X's n[.attr] — or,
			// when n is a submodule, attr inside X.n.
			r.emit(ctx, at, strings.Join(append([]string{decl.orig}, parts[1:]...), "."), decl.module)
			if len(parts) > 1 {
				r.emitModuleSplits(ctx, at, pyJoinModule(decl.module, decl.orig), parts[1:])
			}
		} else if len(parts) > 1 {
			// import a.b [as m]: m.f() / a.b.f() — the module is some prefix.
			r.emitModuleSplits(ctx, at, decl.module, parts[1:])
		}
	}
}

// emitModuleSplits emits one candidate per way rest can split into a
// submodule path and a symbol path: for import a then a.b.c.f(), (a.b.c, f),
// (a.b, c.f) and (a, b.c.f). Only a split naming a project file and an
// indexed symbol in it becomes an edge.
func (r *pyRefWalker) emitModuleSplits(ctx pyRefCtx, at int, module string, rest []string) {
	for k := len(rest) - 1; k >= 0; k-- {
		spec := module
		for _, p := range rest[:k] {
			spec = pyJoinModule(spec, p)
		}
		r.emit(ctx, at, strings.Join(rest[k:], "."), spec)
	}
}

func (r *pyRefWalker) emit(ctx pyRefCtx, at int, to, importSpec string) {
	from, fromFile := ctx.from, r.rel
	if from == "" {
		from = r.rel // module-level code: sourced from the file node
	}
	key := from + "\x00" + to + "\x00" + importSpec
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	ref := extract.Reference{From: from, FromFile: fromFile, FromLine: ctx.fromLine, To: to, Kind: extract.RefCalls, Line: at}
	if importSpec != "" {
		ref.ImportSpec = importSpec
	} else {
		ref.ToFile = r.rel
	}
	r.out = append(r.out, ref)
}

// dotted returns an identifier chain (a.b.c) or nil for any other callee.
func (r *pyRefWalker) dotted(n *ts.Node) []string {
	w := r.w
	switch w.typ(n) {
	case "identifier":
		return []string{w.text(n)}
	case "attribute":
		left := r.dotted(w.field(n, "object"))
		attr := w.field(n, "attribute")
		if left == nil || attr == nil {
			return nil
		}
		return append(left, w.text(attr))
	}
	return nil
}

// lookup resolves a name LEGB-style from scope s and returns the binding the
// emulation chose (last typed declaration, else the first) and its scope. A
// parameter, a variable, or a `global` miss returns nil.
func (r *pyRefWalker) lookup(name string, s *pyScope) (*pyDecl, *pyScope) {
	first := true
	for cur := s; cur != nil; cur = cur.parent {
		// Class bodies are visible only to code directly inside them.
		if cur.kind == pyClass && !first {
			continue
		}
		first = false
		if cur.globals[name] {
			return chosenDecl(r.w.module.decls[name]), r.w.module
		}
		if cur.params[name] {
			return nil, nil
		}
		if ds := cur.decls[name]; len(ds) > 0 {
			return chosenDecl(ds), cur
		}
	}
	return nil, nil
}

func chosenDecl(ds []pyDecl) *pyDecl {
	if len(ds) == 0 {
		return nil
	}
	for i := len(ds) - 1; i >= 0; i-- {
		if ds[i].typed {
			return &ds[i]
		}
	}
	return &ds[0]
}

// isSelf reports whether name is the self/cls parameter of the enclosing
// method, seen from s (possibly a closure inside it) without being rebound.
func (r *pyRefWalker) isSelf(name string, s *pyScope) bool {
	return r.methodClass(s, name) != nil
}

func (r *pyRefWalker) methodClass(s *pyScope, name string) *pyScope {
	for cur := s; cur != nil && cur.kind == pyFunc; cur = cur.parent {
		if cur.selfVar == name && !cur.static && cur.parent != nil && cur.parent.kind == pyClass {
			return cur.parent
		}
		if cur.params[name] || len(cur.decls[name]) > 0 {
			return nil // rebound before reaching the method
		}
	}
	return nil
}

func (r *pyRefWalker) definesCallable(cs *pyScope, name string) bool {
	d := chosenDecl(cs.decls[name])
	return d != nil && (d.kind == pyFunc || d.kind == pyClass)
}

func (r *pyRefWalker) lambdaParams(n *ts.Node) []string {
	w := r.w
	params := w.field(n, "parameters")
	if params == nil {
		return nil
	}
	var out []string
	for i := 0; i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		id := p
		if w.typ(p) != "identifier" {
			id = w.field(p, "name")
			if id == nil && p.NamedChildCount() > 0 {
				id = p.NamedChild(0)
			}
		}
		if id != nil && w.typ(id) == "identifier" {
			out = append(out, w.text(id))
		}
	}
	return out
}

func (r *pyRefWalker) comprehensionTargets(n *ts.Node) []string {
	w := r.w
	var out []string
	var collect func(t *ts.Node)
	collect = func(t *ts.Node) {
		if t == nil {
			return
		}
		if w.typ(t) == "identifier" {
			out = append(out, w.text(t))
			return
		}
		for i := 0; i < t.NamedChildCount(); i++ {
			collect(t.NamedChild(i))
		}
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); w.typ(c) == "for_in_clause" {
			collect(w.field(c, "left"))
		}
	}
	return out
}

func withNames(base map[string]bool, names []string) map[string]bool {
	if len(names) == 0 {
		return base
	}
	out := make(map[string]bool, len(base)+len(names))
	for k := range base {
		out[k] = true
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}
