package sittersrc

import (
	ts "github.com/odvcencio/gotreesitter"

	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// Assignment declarations: TypeScript's getAssignmentDeclarationKind and the
// BinaryExpression / CallExpression cases of addChildrenRecursively. In a
// TypeScript file only the Property kind (`F.x = …` on a tracked function)
// applies; the CommonJS, prototype, and Object.defineProperty forms are
// JavaScript-only, exactly as in the compiler.

func (w *tsWalker) isAccess(n *ts.Node) bool {
	if n == nil {
		return false
	}
	t := w.typ(n)
	return t == "member_expression" || t == "subscript_expression"
}

func (w *tsWalker) accessObject(n *ts.Node) *ts.Node { return w.field(n, "object") }

// accessNameNode is getNameOrArgument: the property name, or the index.
func (w *tsWalker) accessNameNode(n *ts.Node) *ts.Node {
	if w.typ(n) == "member_expression" {
		return w.field(n, "property")
	}
	return w.field(n, "index")
}

// accessName is getElementOrPropertyAccessName: the property name, or a
// literal index's value ("" for a dynamic index).
func (w *tsWalker) accessName(n *ts.Node) string {
	nn := w.accessNameNode(n)
	if nn == nil {
		return ""
	}
	switch w.typ(nn) {
	case "property_identifier", "private_property_identifier":
		return w.text(nn)
	case "string":
		return w.stringValue(nn)
	case "number":
		return w.text(nn)
	}
	return ""
}

func (w *tsWalker) stringValue(n *ts.Node) string {
	s := w.text(n)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') {
		return s[1 : len(s)-1]
	}
	return s
}

func (w *tsWalker) isLiteralIndex(n *ts.Node) bool {
	idx := w.field(n, "index")
	return idx != nil && (w.typ(idx) == "string" || w.typ(idx) == "number")
}

// isBindableStaticName: an identifier (or `this`) followed by property
// accesses / literal element accesses.
func (w *tsWalker) isBindableStaticName(n *ts.Node, excludeThis bool) bool {
	if n == nil {
		return false
	}
	switch w.typ(n) {
	case "identifier":
		return true
	case "this":
		return !excludeThis
	}
	return w.isBindableStaticAccess(n, excludeThis)
}

func (w *tsWalker) isBindableStaticAccess(n *ts.Node, excludeThis bool) bool {
	switch w.typ(n) {
	case "member_expression":
		obj := w.accessObject(n)
		if obj != nil && w.typ(obj) == "this" {
			return !excludeThis
		}
		prop := w.field(n, "property")
		return prop != nil && w.typ(prop) == "property_identifier" && w.isBindableStaticName(obj, true)
	case "subscript_expression":
		return w.isLiteralIndex(n) && w.isBindableStaticName(w.accessObject(n), excludeThis)
	}
	return false
}

// rightmostAssigned skips chained assignments (`a = b = expr` → expr).
func (w *tsWalker) rightmostAssigned(n *ts.Node) *ts.Node {
	r := w.field(n, "right")
	for r != nil && w.typ(r) == "assignment_expression" {
		r = w.field(r, "right")
	}
	return r
}

// assignmentKind is getAssignmentDeclarationKind for `left = right`.
func (w *tsWalker) assignmentKind(n *ts.Node) int {
	left := w.field(n, "left")
	right := w.rightmostAssigned(n)
	if !w.isAccess(left) || right == nil {
		return asgNone
	}
	if w.typ(right) == "unary_expression" && w.text(right) == "void 0" {
		return asgNone
	}
	var k int
	obj := w.accessObject(left)
	if w.isBindableStaticName(obj, true) && w.accessName(left) == "prototype" && w.typ(right) == "object" {
		k = asgPrototype
	} else {
		k = w.propertyAccessKind(left)
	}
	if k != asgProperty && !w.isJS {
		return asgNone
	}
	return k
}

// propertyAccessKind is getAssignmentDeclarationPropertyAccessKind.
func (w *tsWalker) propertyAccessKind(lhs *ts.Node) int {
	obj := w.accessObject(lhs)
	if obj == nil {
		return asgNone
	}
	if w.typ(obj) == "this" {
		return asgThisProperty
	}
	if w.typ(obj) == "identifier" && w.text(obj) == "module" && w.accessName(lhs) == "exports" {
		return asgModuleExports
	}
	if !w.isBindableStaticName(obj, true) {
		return asgNone
	}
	if w.isAccess(obj) && w.accessName(obj) == "prototype" {
		return asgPrototypeProperty
	}
	nextToLast := lhs
	for {
		o := w.accessObject(nextToLast)
		if o == nil || w.typ(o) == "identifier" {
			break
		}
		nextToLast = o
	}
	id := w.accessObject(nextToLast)
	if id != nil && (w.text(id) == "exports" || w.text(id) == "module" && w.accessName(nextToLast) == "exports") &&
		w.isBindableStaticAccess(lhs, false) {
		return asgExportsProperty
	}
	if w.isBindableStaticName(lhs, true) || w.typ(lhs) == "subscript_expression" && !w.isLiteralIndex(lhs) {
		return asgProperty
	}
	return asgNone
}

// binaryNodeKind is getNodeKind for an assignment-declaration binary.
func (w *tsWalker) binaryNodeKind(k int, right *ts.Node) int {
	switch k {
	case asgExportsProperty, asgModuleExports:
		switch w.typ(right) {
		case "arrow_function", "function_expression", "function", "generator_function":
			return lsp.SymbolFunction
		case "class":
			return lsp.SymbolClass
		}
		return lsp.SymbolConstant
	case asgPrototypeProperty, asgProperty:
		if w.isFunctionExpression(right) {
			return lsp.SymbolMethod
		}
		return kindProperty
	case asgPrototype:
		return lsp.SymbolClass // "local class"
	}
	return lsp.SymbolVariable
}

// addAssignmentDeclaration ports the BinaryExpression case. Reports whether
// it consumed n (otherwise the caller traverses it by default).
func (w *tsWalker) addAssignmentDeclaration(n *ts.Node) bool {
	k := w.assignmentKind(n)
	if k == asgNone || k == asgThisProperty {
		return false
	}
	left := w.field(n, "left")
	right := w.field(n, "right")
	start := line(n.StartPoint())
	kind := w.binaryNodeKind(k, w.rightmostAssigned(n))
	switch k {
	case asgExportsProperty, asgModuleExports:
		name, key := "<unknown>", ""
		if k == asgExportsProperty {
			nn := w.accessNameNode(left)
			name, key = w.nm(nn), w.text(nn)
		}
		it := item(name, kind, start, n)
		it.key, it.asg = key, k
		w.addNodeWithRecursiveChild(it, right)
		return true

	case asgPrototype, asgPrototypeProperty:
		protoAccess := left
		if k == asgPrototypeProperty {
			protoAccess = w.accessObject(left)
		}
		target := w.accessObject(protoAccess)
		var frames []navFrame
		var className *ts.Node
		if w.typ(target) == "identifier" {
			w.track(w.text(target))
			className = target
		} else {
			frames, className = w.startNestedNodes(n, target, kind, k)
		}
		named := func(name *ts.Node) *navNode {
			it := item(w.nm(name), kind, start, n)
			it.key, it.asg = w.text(name), k
			return it
		}
		switch {
		case k == asgPrototype:
			if right.NamedChildCount() > 0 {
				f := w.startNode(named(className))
				w.forEachChild(right)
				w.endNode(f)
			}
		case w.isFunctionLike(right):
			w.addNodeWithRecursiveChild(named(className), right)
		default:
			f := w.startNode(named(className))
			inner := item("", kind, start, n)
			inner.asg = k
			if w.typ(left) == "member_expression" {
				nn := w.field(left, "property")
				inner.name, inner.key = w.nm(nn), w.text(nn)
			}
			w.addNodeWithRecursiveChild(inner, right)
			w.endNode(f)
		}
		w.endNestedNodes(frames)
		return true

	case asgProperty:
		target := w.accessObject(left)
		if target == nil || w.typ(target) != "identifier" || w.accessName(left) == "prototype" || !w.tracked[w.text(target)] {
			return false
		}
		outer := item(w.nm(target), kind, start, n)
		outer.key, outer.asg = w.text(target), k
		if w.isFunctionLike(right) {
			w.addNodeWithRecursiveChild(outer, right)
			return true
		}
		if w.isBindableStaticAccess(left, false) {
			f := w.startNode(outer)
			nn := w.accessNameNode(left)
			inner := item(w.nm(nn), lsp.SymbolVariable, line(left.StartPoint()), left)
			inner.key = w.text(nn)
			w.addNodeWithRecursiveChild(inner, right)
			w.endNode(f)
		}
		return true
	}
	return false
}

// addDefinePropertyCall ports the ObjectDefineProperty cases:
// Object.defineProperty(target, "name", descriptor) with a bindable target
// becomes target > name > (descriptor members), the target being a class
// member for the ES5 merge.
func (w *tsWalker) addDefinePropertyCall(n *ts.Node) bool {
	fn := w.field(n, "function")
	if fn == nil || w.text(fn) != "Object.defineProperty" {
		return false
	}
	args := w.field(n, "arguments")
	if args == nil || args.NamedChildCount() != 3 {
		return false
	}
	target, member, desc := args.NamedChild(0), args.NamedChild(1), args.NamedChild(2)
	if !w.isBindableStaticName(target, false) || (w.typ(member) != "string" && w.typ(member) != "number") {
		return false
	}
	if (w.typ(target) == "identifier" && w.text(target) == "exports") ||
		(w.isAccess(target) && w.text(w.accessObject(target)) == "module" && w.accessName(target) == "exports") {
		return false // ObjectDefinePropertyExports: traversed by default
	}
	k := asgDefinePropertyValue
	className := target
	if w.isBindableStaticAccess(target, false) && w.accessName(target) == "prototype" {
		k = asgDefinePrototypeProperty
		className = w.accessObject(target)
	}
	start := line(n.StartPoint())
	frames, id := w.startNestedNodes(n, className, lsp.SymbolVariable, k)
	outer := item(w.nm(id), lsp.SymbolVariable, start, n)
	outer.key, outer.asg = w.text(id), k
	f1 := w.startNode(outer)
	name := w.stringValue(member)
	inner := item(cleanText(name), lsp.SymbolVariable, start, n)
	inner.key, inner.asg = name, k
	f2 := w.startNode(inner)
	w.add(desc)
	w.endNode(f2)
	w.endNode(f1)
	w.endNestedNodes(frames)
	return true
}

// startNestedNodes opens one item per segment of a dotted target (skipping
// "prototype") and returns the frames to close plus the innermost name.
func (w *tsWalker) startNestedNodes(target, entity *ts.Node, kind, asg int) ([]navFrame, *ts.Node) {
	var names []*ts.Node
	for w.isAccess(entity) {
		nameNode := w.accessNameNode(entity)
		nameText := w.accessName(entity)
		entity = w.accessObject(entity)
		if nameText == "prototype" || (nameNode != nil && w.typ(nameNode) == "private_property_identifier") {
			continue
		}
		names = append(names, nameNode)
	}
	names = append(names, entity)
	var frames []navFrame
	for i := len(names) - 1; i > 0; i-- {
		it := item(w.nm(names[i]), kind, line(target.StartPoint()), target)
		it.key, it.asg = w.text(names[i]), asg
		frames = append(frames, w.startNode(it))
	}
	return frames, names[0]
}

func (w *tsWalker) endNestedNodes(frames []navFrame) {
	for i := len(frames) - 1; i >= 0; i-- {
		w.endNode(frames[i])
	}
}
