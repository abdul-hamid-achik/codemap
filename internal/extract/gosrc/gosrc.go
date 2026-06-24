// Package gosrc extracts Go structural symbols using the standard library
// go/parser (pure Go, no CGO). It resolves intra-file structure precisely;
// cross-package call resolution is left to the indexer (by name match, weight
// 0.7) or the LSP backend (precise, weight 1.0).
package gosrc

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the go/parser-based Go backend.
type Extractor struct{}

// New returns a Go extractor.
func New() *Extractor { return &Extractor{} }

// Language implements extract.Extractor.
func (Extractor) Language() string { return "go" }

// ExtractFile implements extract.Extractor.
func (e Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	res := &extract.FileResult{Path: relPath, Language: "go"}
	pkg := f.Name.Name
	isTest := strings.HasSuffix(relPath, "_test.go")

	for _, imp := range f.Imports {
		res.Imports = append(res.Imports, strings.Trim(imp.Path.Value, `"`))
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := e.funcSymbol(fset, src, pkg, d, isTest)
			res.Symbols = append(res.Symbols, sym)
			res.References = append(res.References, callRefs(fset, sym.FQN, d)...)
			res.References = append(res.References, valueRefs(fset, sym.FQN, d)...)
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						res.Symbols = append(res.Symbols, typeSymbol(fset, src, pkg, d, ts))
					}
				}
			case token.VAR, token.CONST:
				res.Symbols = append(res.Symbols, valueSymbols(fset, src, pkg, d)...)
			}
		}
	}
	// Function values wired in package-level declarations (e.g. a cobra command
	// table) — attributed to the file so the handlers aren't flagged as dead code.
	res.References = append(res.References, fileValueRefs(fset, relPath, f)...)
	return res, nil
}

func (e Extractor) funcSymbol(fset *token.FileSet, src []byte, pkg string, d *ast.FuncDecl, isTest bool) extract.Symbol {
	name := d.Name.Name
	kind := extract.KindFunction
	fqn := pkg + "." + name

	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recv := recvTypeName(d.Recv.List[0].Type); recv != "" {
			fqn = pkg + "." + recv + "." + name
			kind = extract.KindMethod
		}
	}
	if isTest && isTestFuncName(name) {
		kind = extract.KindTest
	}

	start := fset.Position(d.Pos())
	end := fset.Position(d.End())
	return extract.Symbol{
		Name:      name,
		FQN:       fqn,
		Kind:      kind,
		Language:  "go",
		StartLine: start.Line,
		EndLine:   end.Line,
		Signature: signature(fset, d),
		Docstring: strings.TrimSpace(d.Doc.Text()),
		Source:    sliceSrc(src, start.Offset, end.Offset),
	}
}

func typeSymbol(fset *token.FileSet, src []byte, pkg string, gd *ast.GenDecl, ts *ast.TypeSpec) extract.Symbol {
	start := fset.Position(ts.Pos())
	end := fset.Position(ts.End())
	doc := strings.TrimSpace(ts.Doc.Text())
	if doc == "" && gd.Doc != nil {
		doc = strings.TrimSpace(gd.Doc.Text())
	}
	source := sliceSrc(src, start.Offset, end.Offset)
	return extract.Symbol{
		Name:      ts.Name.Name,
		FQN:       pkg + "." + ts.Name.Name,
		Kind:      extract.KindType,
		Language:  "go",
		StartLine: start.Line,
		EndLine:   end.Line,
		Signature: "type " + firstLine(source),
		Docstring: doc,
		Source:    source,
	}
}

// valueSymbols extracts package-level var/const declarations as KindVariable
// symbols — one per declared name (skipping the blank identifier `_`). They
// aren't callable, so they get no call edges; they exist so find/source/symbols/
// semantic can locate them (e.g. version.Version, sentinel errors like
// `var ErrX = errors.New(...)`, and const blocks). Only top-level decls reach
// here — function-local var/const live inside FuncDecl bodies, which this never
// walks — so locals stay out of the index.
func valueSymbols(fset *token.FileSet, src []byte, pkg string, gd *ast.GenDecl) []extract.Symbol {
	kw := gd.Tok.String() // "var" or "const"
	var syms []extract.Symbol
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		doc := strings.TrimSpace(vs.Doc.Text())
		if doc == "" && gd.Doc != nil {
			doc = strings.TrimSpace(gd.Doc.Text())
		}
		typeStr := ""
		if vs.Type != nil {
			typeStr = " " + exprString(fset, vs.Type)
		}
		start := fset.Position(vs.Pos())
		end := fset.Position(vs.End())
		source := sliceSrc(src, start.Offset, end.Offset)
		for _, name := range vs.Names {
			if name.Name == "_" { // blank identifier declares nothing findable
				continue
			}
			syms = append(syms, extract.Symbol{
				Name:      name.Name,
				FQN:       pkg + "." + name.Name,
				Kind:      extract.KindVariable,
				Language:  "go",
				StartLine: fset.Position(name.Pos()).Line,
				EndLine:   end.Line,
				Signature: kw + " " + name.Name + typeStr,
				Docstring: doc,
				Source:    source,
			})
		}
	}
	return syms
}

// exprString renders an AST expression (e.g. a var/const's declared type) to its
// source-like string. Returns "" if printing fails.
func exprString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return ""
	}
	return b.String()
}

// signature renders a func declaration without its body or doc comment.
func signature(fset *token.FileSet, d *ast.FuncDecl) string {
	cp := *d
	cp.Body = nil
	cp.Doc = nil
	var b strings.Builder
	if err := printer.Fprint(&b, fset, &cp); err != nil {
		return d.Name.Name
	}
	return strings.TrimSpace(b.String())
}

// recvTypeName extracts the bare receiver type name (stripping pointers and
// generic type parameters).
func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	}
	return ""
}

// callRefs collects unique call targets within a function body.
func callRefs(fset *token.FileSet, from string, d *ast.FuncDecl) []extract.Reference {
	if d.Body == nil {
		return nil
	}
	seen := map[string]bool{}
	var refs []extract.Reference
	ast.Inspect(d.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call.Fun)
		if name == "" || builtins[name] || seen[name] {
			return true
		}
		seen[name] = true
		refs = append(refs, extract.Reference{
			From:      from,
			To:        name,
			Kind:      extract.RefCalls,
			Line:      fset.Position(call.Pos()).Line,
			Qualified: isQualifiedCall(call.Fun),
		})
		return true
	})
	return refs
}

// valueRefs collects function-value references within a function body: a bare
// identifier naming a function used as a *value* (passed, stored, registered)
// rather than called — e.g. `register(handler)`, `[]H{a, b}`. Attributed to the
// enclosing function (from = its FQN).
func valueRefs(fset *token.FileSet, from string, d *ast.FuncDecl) []extract.Reference {
	if d.Body == nil {
		return nil
	}
	var refs []extract.Reference
	// Function bodies: only function-*values*. Calls are already RefCalls edges
	// (callRefs), so don't double-count them as references.
	collectValueRefs(fset, from, d.Body, false, map[string]bool{}, &refs)
	return refs
}

// fileValueRefs collects function-value references in package-level declarations
// (var/const) — e.g. a cobra command table `var initCmd = &cobra.Command{RunE:
// runInit}`. These have no enclosing function, so they're attributed to the file
// (from = relPath; the indexer resolves a file path to its file node). Function
// bodies are skipped here — valueRefs handles those.
func fileValueRefs(fset *token.FileSet, relPath string, f *ast.File) []extract.Reference {
	seen := map[string]bool{}
	var refs []extract.Reference
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok { // *ast.FuncDecl and others — bodies handled by valueRefs
			continue
		}
		// Package-level decls have no enclosing function, so callRefs never walks
		// them — yet a func-literal initializer (e.g. cobra's inline `RunE:
		// func(...){ runStudio(...) }`) really does invoke its callees. Collect
		// those callees too (as references, attributed to the file), so a function
		// reachable only from a package-level closure isn't a false orphan.
		collectValueRefs(fset, relPath, gd, true, seen, &refs)
	}
	return refs
}

// collectValueRefs walks node for a function used as a *value* (passed, stored,
// registered) rather than called, appending each as a RefReferences edge from
// `from`. Without these, framework handlers wired by value (cobra commands, HTTP
// routers like `mux.HandleFunc("/", s.handle)`, callback tables) all look like
// dead code in `orphans`. RefReferences is distinct from RefCalls, so the call
// graph (callers/callees/impact/path) is unaffected. Both bare identifiers
// (`handler`) and selectors (`s.handle`, the method-value pattern) are taken, by
// their function/method name and resolved within the package — references are
// conservative (they only keep a node *out* of the dead-code list), so this never
// produces a false call.
func collectValueRefs(fset *token.FileSet, from string, node ast.Node, collectCalls bool, seen map[string]bool, out *[]extract.Reference) {
	add := func(expr ast.Expr) {
		name := valueRefName(expr)
		if name == "" || builtins[name] || seen[name] {
			return
		}
		seen[name] = true
		*out = append(*out, extract.Reference{
			From:      from,
			To:        name,
			Kind:      extract.RefReferences,
			Line:      fset.Position(expr.Pos()).Line,
			Qualified: false, // resolve within the package (handlers are registered locally)
		})
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.KeyValueExpr:
			add(t.Value) // struct/map field: `RunE: runInit`, `"x": handler`
		case *ast.CallExpr:
			for _, arg := range t.Args { // callbacks passed as args (not the callee)
				add(arg)
			}
			if collectCalls { // package-level: the callee is genuinely invoked (e.g. inside a RunE closure)
				add(t.Fun)
			}
		case *ast.CompositeLit:
			for _, elt := range t.Elts { // slice/array of handlers; KV elts handled above
				if _, isKV := elt.(*ast.KeyValueExpr); !isKV {
					add(elt)
				}
			}
		}
		return true
	})
}

// valueRefName returns the function/method name an expression names when used as
// a value: a bare identifier (`handler`) or a selector's selected name
// (`s.handle`, `pkg.Fn` → "handle"/"Fn"). Anything else isn't a function value.
func valueRefName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr: // instantiated generic used as a value/callee: Map[int]
		return valueRefName(t.X)
	case *ast.IndexListExpr: // multiple type params: Map[K, V]
		return valueRefName(t.X)
	}
	return ""
}

// isQualifiedCall reports whether a call uses a selector (x.Foo(), pkg.Foo()),
// which may cross packages — as opposed to a bare identifier (Foo()), which Go
// resolves within the same package.
func isQualifiedCall(fun ast.Expr) bool {
	switch t := fun.(type) {
	case *ast.SelectorExpr:
		return true
	case *ast.IndexExpr:
		return isQualifiedCall(t.X)
	case *ast.IndexListExpr:
		return isQualifiedCall(t.X)
	default:
		return false
	}
}

func calleeName(fun ast.Expr) string {
	switch t := fun.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return calleeName(t.X)
	case *ast.IndexListExpr:
		return calleeName(t.X)
	}
	return ""
}

func isTestFuncName(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func sliceSrc(src []byte, start, end int) string {
	if start < 0 || end > len(src) || start > end {
		return ""
	}
	return string(src[start:end])
}

// builtins are Go predeclared functions we don't record as call edges.
var builtins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}
