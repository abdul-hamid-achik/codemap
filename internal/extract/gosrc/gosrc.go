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
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						res.Symbols = append(res.Symbols, typeSymbol(fset, src, pkg, d, ts))
					}
				}
			}
		}
	}
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
			From: from,
			To:   name,
			Kind: extract.RefCalls,
			Line: fset.Position(call.Pos()).Line,
		})
		return true
	})
	return refs
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
