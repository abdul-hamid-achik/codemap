package sittersrc

import (
	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// The navigation-tree core: items with one or more spans, the per-scope merge
// pass (mergeChildren / tryMergeEs5Class), and typescript-language-server's
// conversion of the tree into documentSymbols.

// navSpan is one source range of an item; merged items carry several.
type navSpan struct {
	start, end int // 0-based lines
	endByte    uint32
}

// navNode is one navigation-tree item.
type navNode struct {
	name     string
	kind     int
	spans    []navSpan
	children []*navNode

	// Merge inputs (tryMergeEs5Class): the declaration-name text items are
	// grouped by, whether the item's node could be an ES5 constructor
	// (function declaration/expression or variable declaration), whether it
	// is a class declaration (and whether a merge synthesized it), and the
	// assignment-declaration kind of a binary/call item.
	key         string
	ctorLike    bool
	classDecl   bool
	synthesized bool
	asg         int
}

// Assignment declaration kinds (TypeScript's AssignmentDeclarationKind).
const (
	asgNone = iota
	asgExportsProperty
	asgModuleExports
	asgPrototypeProperty
	asgThisProperty
	asgProperty
	asgPrototype
	asgDefinePropertyValue
	asgDefinePropertyExports
	asgDefinePrototypeProperty
)

func isEs5ClassMember(k int) bool {
	switch k {
	case asgProperty, asgPrototypeProperty, asgDefinePropertyValue, asgDefinePrototypeProperty, asgPrototype:
		return true
	}
	return false
}

// item builds an item spanning from startLine to the end of n.
func item(name string, kind, startLine int, n *ts.Node) *navNode {
	return &navNode{name: name, kind: kind, spans: []navSpan{{start: startLine, end: line(n.EndPoint()), endByte: n.EndByte()}}}
}

type navFrame struct {
	parent  *navNode
	tracked map[string]bool
}

// startNode / endNode / addLeafNode mirror navigationBar's stack discipline,
// including the per-scope trackedEs5Classes set.
func (w *tsWalker) startNode(n *navNode) navFrame {
	w.parent.children = append(w.parent.children, n)
	f := navFrame{parent: w.parent, tracked: w.tracked}
	w.parent = n
	w.tracked = nil
	return f
}

func (w *tsWalker) endNode(f navFrame) {
	w.parent.children = mergeChildren(w.parent.children)
	w.parent = f.parent
	w.tracked = f.tracked
}

func (w *tsWalker) addLeafNode(n *navNode) {
	w.parent.children = append(w.parent.children, n)
}

func (w *tsWalker) addNodeWithRecursiveChild(n *navNode, child *ts.Node) {
	f := w.startNode(n)
	if child != nil {
		w.add(child)
	}
	w.endNode(f)
}

// addNodeWithRecursiveInitializer: a function/class expression initializer is
// not an item of its own — its parts become children of the declaration.
func (w *tsWalker) addNodeWithRecursiveInitializer(n *navNode, init *ts.Node) {
	init = w.unwrapGenericArrow(init)
	if init != nil && w.isFunctionOrClassExpression(init) {
		f := w.startNode(n)
		w.forEachChild(init)
		w.endNode(f)
		return
	}
	w.addNodeWithRecursiveChild(n, init)
}

func (w *tsWalker) track(name string) {
	if w.tracked == nil {
		w.tracked = map[string]bool{}
	}
	w.tracked[name] = true
}

// mergeChildren groups siblings by declaration name and folds ES5 class
// members into their constructor (tryMergeEs5Class). A same-name assignment
// declaration that cannot merge is dropped, as in TypeScript. Plain same-kind
// merges (overloads, reopened namespaces) are not performed: they only
// gather spans, which the per-span conversion splits back apart.
func mergeChildren(children []*navNode) []*navNode {
	if len(children) < 2 {
		return children
	}
	orig := append([]*navNode(nil), children...)
	byKey := map[string][]*navNode{}
	out := children[:0]
	for i, b := range orig {
		if b.key == "" {
			out = append(out, b)
			continue
		}
		merged := false
		for _, a := range byKey[b.key] {
			if tryMergeEs5Class(a, b, i, orig) {
				merged = true
				break
			}
		}
		if !merged {
			byKey[b.key] = append(byKey[b.key], b)
			out = append(out, b)
		}
	}
	return out
}

func tryMergeEs5Class(a, b *navNode, bIndex int, siblings []*navNode) bool {
	aK, bK := a.asg, b.asg
	if !(isEs5ClassMember(bK) && isEs5ClassMember(aK) ||
		a.ctorLike && isEs5ClassMember(bK) ||
		b.ctorLike && isEs5ClassMember(aK) ||
		a.classDecl && a.synthesized && isEs5ClassMember(bK) ||
		b.classDecl && isEs5ClassMember(aK) ||
		a.classDecl && a.synthesized && b.ctorLike ||
		b.classDecl && a.ctorLike && a.synthesized) {
		return bK != asgNone
	}
	last := len(a.spans) - 1 // lastANode
	if !a.classDecl && !b.classDecl || a.ctorLike || b.ctorLike {
		var ctorFn *navNode
		switch {
		case a.ctorLike:
			ctorFn = a
		case b.ctorLike:
			ctorFn = b
		}
		if ctorFn != nil {
			ctor := &navNode{name: "constructor", kind: lsp.SymbolConstructor, spans: []navSpan{ctorFn.spans[0]}, children: ctorFn.children}
			if a == ctorFn {
				a.children = append([]*navNode{ctor}, orSelf(b)...)
			} else {
				a.children = append(orSelfCopy(a), ctor)
			}
		} else if a.children != nil || b.children != nil {
			a.children = mergeChildren(append(orSelfCopy(a), orSelf(b)...))
		}
		if a.name == "" {
			a.name = "__class__"
		}
		a.kind = lsp.SymbolClass
		a.classDecl, a.synthesized, a.ctorLike, a.asg = true, true, false, asgNone
		last = 0 // a.node is now the synthesized class over a's own range
	} else {
		a.children = mergeChildren(append(a.children, b.children...))
	}
	if bIndex > 0 && siblings[bIndex-1].spans[0].endByte == a.spans[last].endByte {
		a.spans[last].end = b.spans[0].end
		a.spans[last].endByte = b.spans[0].endByte
	} else {
		a.spans = append(a.spans, b.spans[0])
	}
	return true
}

func orSelf(n *navNode) []*navNode {
	if n.children != nil {
		return n.children
	}
	return []*navNode{n}
}

func orSelfCopy(n *navNode) []*navNode {
	if n.children != nil {
		return n.children
	}
	c := *n
	return []*navNode{&c}
}

// convertNav is tsl's collectDocumentSymbolsInRange: one entry per span that
// intersects [lo, hi], included when shouldIncludeEntry holds or any
// descendant was included (the flag carries across spans, as in tsl).
func convertNav(n *navNode, lo, hi int, out *[]lsp.DocumentSymbol) bool {
	include := n.name != "" && n.name != "<function>" && n.name != "<class>"
	for _, sp := range n.spans {
		if sp.end < lo || sp.start > hi {
			continue
		}
		var children []lsp.DocumentSymbol
		for _, c := range n.children {
			if c.intersects(sp) && convertNav(c, sp.start, sp.end, &children) {
				include = true
			}
		}
		if include {
			*out = append(*out, symbol(n.name, n.kind, sp.start, sp.end, children))
		}
	}
	return include
}

func (n *navNode) intersects(sp navSpan) bool {
	for _, s := range n.spans {
		if s.start <= sp.end && s.end >= sp.start {
			return true
		}
	}
	return false
}
