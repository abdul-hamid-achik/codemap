package sittersrc

import (
	"strconv"
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Declared inheritance for TypeScript/JavaScript: `class A extends B
// implements I, J` and `interface I extends J`. Each named base becomes an
// extends/implements reference from the class (or interface) to the base,
// resolved like a call candidate — same file, or through an import binding —
// so `extends React.Component` from a package yields no edge, while a project
// base class or interface does. Overrides between their methods are derived at
// index time (graph.RecomputeOverrides).

// tsHeritage is one declared base, captured while the tree is alive.
type tsHeritage struct {
	kind      string // extract.RefExtends / RefImplements
	className string
	startLine int // 1-based, as the class symbol is emitted
	base      []string
	at        int
}

type tsImport struct {
	spec, exported string
	def, ns        bool
}

func collectTSHeritage(lang *ts.Language, src []byte, root *ts.Node) ([]tsHeritage, map[string]tsImport) {
	w := &tsWalker{lang: lang, src: src}
	var out []tsHeritage
	imports := map[string]tsImport{}
	var visit func(n *ts.Node)
	visit = func(n *ts.Node) {
		switch w.typ(n) {
		case "import_statement":
			w.collectImport(n, imports)
			return
		case "class_declaration", "abstract_class_declaration", "class", "interface_declaration":
			name := w.text(w.field(n, "name"))
			if name != "" {
				start := w.declStart(n)
				if p := n.Parent(); p != nil && w.typ(p) == "export_statement" {
					start = line(p.StartPoint())
				}
				for _, h := range w.heritageOf(n) {
					h.className, h.startLine = name, start+1
					out = append(out, h)
				}
			}
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			visit(n.NamedChild(i))
		}
	}
	visit(root)
	return out, imports
}

// heritageOf returns the declared bases of a class or interface.
func (w *tsWalker) heritageOf(n *ts.Node) []tsHeritage {
	var out []tsHeritage
	add := func(kind string, expr *ts.Node) {
		if parts := w.baseParts(expr); len(parts) > 0 {
			out = append(out, tsHeritage{kind: kind, base: parts, at: line(expr.StartPoint()) + 1})
		}
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.typ(c) {
		case "class_heritage":
			for j := 0; j < c.NamedChildCount(); j++ {
				h := c.NamedChild(j)
				switch w.typ(h) {
				case "extends_clause":
					if v := w.field(h, "value"); v != nil {
						add(extract.RefExtends, v)
					} else if h.NamedChildCount() > 0 {
						add(extract.RefExtends, h.NamedChild(0))
					}
				case "implements_clause":
					for k := 0; k < h.NamedChildCount(); k++ {
						add(extract.RefImplements, h.NamedChild(k))
					}
				default: // JavaScript: `extends <expression>` sits directly here
					add(extract.RefExtends, h)
				}
			}
		case "extends_type_clause":
			for j := 0; j < c.NamedChildCount(); j++ {
				add(extract.RefExtends, c.NamedChild(j))
			}
		}
	}
	return out
}

// baseParts returns a base expression as an identifier chain (Base,
// ns.Base, Base<T>), or nil for anything computed (mixin(Base), …).
func (w *tsWalker) baseParts(n *ts.Node) []string {
	if n == nil {
		return nil
	}
	switch w.typ(n) {
	case "identifier", "type_identifier":
		return []string{w.text(n)}
	case "generic_type":
		return w.baseParts(w.field(n, "name"))
	case "member_expression":
		left := w.baseParts(w.field(n, "object"))
		prop := w.field(n, "property")
		if left == nil || prop == nil {
			return nil
		}
		return append(left, w.text(prop))
	case "nested_type_identifier":
		left := w.baseParts(w.field(n, "module"))
		name := w.field(n, "name")
		if left == nil || name == nil {
			return nil
		}
		return append(left, w.text(name))
	case "nested_identifier":
		return strings.Split(w.text(n), ".")
	}
	return nil
}

func (w *tsWalker) collectImport(n *ts.Node, out map[string]tsImport) {
	// Type-only imports count here: `import type { Repo }` is exactly how an
	// implemented interface usually arrives.
	source := w.field(n, "source")
	if source == nil {
		return
	}
	spec := strings.Trim(w.text(source), "'\"`")
	for i := 0; i < n.NamedChildCount(); i++ {
		clause := n.NamedChild(i)
		if w.typ(clause) != "import_clause" {
			continue
		}
		for j := 0; j < clause.NamedChildCount(); j++ {
			c := clause.NamedChild(j)
			switch w.typ(c) {
			case "identifier":
				out[w.text(c)] = tsImport{spec: spec, def: true}
			case "namespace_import":
				for k := 0; k < c.NamedChildCount(); k++ {
					if id := c.NamedChild(k); w.typ(id) == "identifier" {
						out[w.text(id)] = tsImport{spec: spec, ns: true}
					}
				}
			case "named_imports":
				for k := 0; k < c.NamedChildCount(); k++ {
					s := c.NamedChild(k)
					if w.typ(s) != "import_specifier" {
						continue
					}
					name := w.text(w.field(s, "name"))
					local := name
					if a := w.field(s, "alias"); a != nil {
						local = w.text(a)
					}
					if name != "" {
						out[local] = tsImport{spec: spec, exported: name}
					}
				}
			}
		}
	}
}

// tsHeritageRefs resolves collected bases into references, sourcing each from
// the class symbol emitted at its start line.
func tsHeritageRefs(relPath string, hs []tsHeritage, imports map[string]tsImport, symbols []extract.Symbol) []extract.Reference {
	if len(hs) == 0 {
		return nil
	}
	fqnAt := map[string]string{}
	for _, s := range symbols {
		if s.Kind == extract.KindClass || s.Kind == extract.KindType {
			fqnAt[s.Name+"\x00"+strconv.Itoa(s.StartLine)] = s.FQN
		}
	}
	var out []extract.Reference
	for _, h := range hs {
		from, ok := fqnAt[h.className+"\x00"+strconv.Itoa(h.startLine)]
		if !ok {
			continue
		}
		ref := extract.Reference{From: from, FromFile: relPath, FromLine: h.startLine, Kind: h.kind, Line: h.at}
		rest := strings.Join(h.base[1:], ".")
		switch b, imported := imports[h.base[0]]; {
		case imported && b.def:
			ref.ImportSpec, ref.DefaultExport, ref.To = b.spec, true, rest
		case imported && b.ns:
			if rest == "" {
				continue
			}
			ref.ImportSpec, ref.To = b.spec, rest
		case imported:
			ref.ImportSpec, ref.To = b.spec, b.exported
			if rest != "" {
				ref.To += "." + rest
			}
		default:
			ref.ToFile, ref.To = relPath, strings.Join(h.base, ".")
		}
		out = append(out, ref)
	}
	return out
}
