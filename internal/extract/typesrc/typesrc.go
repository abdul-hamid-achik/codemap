// Package typesrc resolves Go call edges precisely using in-process pure-Go
// go/types (via golang.org/x/tools/go/packages), so a cross-package method call
// like x.Close() links to the ONE method it actually invokes instead of every
// method named Close. It is the opt-in counterpart to the fast name-based gosrc
// extractor: gosrc tolerates any source and over-matches; typesrc type-checks the
// module and resolves exactly, but needs the `go` toolchain and a buildable
// module. The indexer uses it to supersede name-based call edges (see the
// edges.provenance column) only for cleanly type-checked packages, degrading to
// the name baseline everywhere else.
package typesrc

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

// LoadMode is the package-loading mode. It deliberately EXCLUDES packages.NeedDeps:
// NeedTypesInfo already loads dependency type information from compiled export data
// (enough to resolve cross-package callees), while NeedDeps would type-check every
// transitive dependency from source — ~40x the heap and time for no benefit here.
// (TestLoadModeExcludesNeedDeps guards this.)
const LoadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo

// PreciseEdge is one resolved call: from a caller declaration to the exact callee.
// CalleeFile/CalleeLine locate the callee's declaration (root-relative, matching
// gosrc's node positions) for a position-keyed join; CalleeFQN is the same-scheme
// FQN as a fallback. External callees (stdlib/deps) have no codemap node.
type PreciseEdge struct {
	CallerFQN  string
	CalleeFQN  string
	CalleeFile string
	CalleeLine int
	External   bool // callee declared outside the loaded module (no graph node)
	Interface  bool // callee is an interface method (static dispatch target)
}

// Result is what Resolve returns. Available is false when the toolchain/module
// could not be loaded at all (the caller then keeps its name-based edges).
type Result struct {
	Available  bool
	Edges      []PreciseEdge
	CleanFiles map[string]bool // root-relative caller files whose package type-checked cleanly
}

// Resolve type-checks the module rooted at root and returns precise call edges for
// every caller declaration in a cleanly type-checked package. A package with any
// type error is skipped entirely (its files are absent from CleanFiles), so the
// caller keeps that package's tolerant name-based edges. Errors loading the module
// (no `go`, no go.mod) yield Available=false and a nil-safe Result, never a panic.
func Resolve(ctx context.Context, root string) (*Result, error) {
	res := &Result{CleanFiles: map[string]bool{}}
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode:    LoadMode,
		Dir:     root,
		Context: ctx,
		Fset:    fset,
		Tests:   false, // match the default index scope (no _test.go callers) for slice 1
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return res, err // toolchain/module load failed — caller degrades to name edges
	}
	if len(pkgs) == 0 {
		return res, nil
	}
	res.Available = true

	// Packages whose *types.Package belongs to the loaded module — a callee in one
	// of these has a graph node; anything else (stdlib/deps via export data) is
	// External.
	inModule := map[*types.Package]bool{}
	for _, p := range pkgs {
		if p.Types != nil {
			inModule[p.Types] = true
		}
	}

	for _, p := range pkgs {
		if len(p.Errors) > 0 || p.TypesInfo == nil {
			continue // not cleanly type-checked — keep its name edges
		}
		pkgName := p.Name
		for _, file := range p.Syntax {
			rel := relOf(root, fset.Position(file.Pos()).Filename)
			res.CleanFiles[rel] = true
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				callerFQN := declFQN(pkgName, fd)
				res.Edges = append(res.Edges, callEdges(fset, root, p.TypesInfo, inModule, callerFQN, fd)...)
			}
		}
	}
	return res, nil
}

// callEdges resolves every call in a function body to its exact callee.
func callEdges(fset *token.FileSet, root string, info *types.Info, inModule map[*types.Package]bool, callerFQN string, fd *ast.FuncDecl) []PreciseEdge {
	var out []PreciseEdge
	seen := map[string]bool{} // dedup identical (callee) edges within one caller, like gosrc
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, iface := resolveCallee(info, call.Fun)
		if fn == nil || fn.Pkg() == nil {
			return true // builtin, conversion, or unresolved (func value / dynamic)
		}
		external := !inModule[fn.Pkg()]
		pos := fset.Position(fn.Origin().Pos())
		e := PreciseEdge{
			CallerFQN:  callerFQN,
			CalleeFQN:  funcFQN(fn),
			CalleeFile: relOf(root, pos.Filename),
			CalleeLine: pos.Line,
			External:   external,
			Interface:  iface,
		}
		key := e.CalleeFQN + "\x00" + e.CalleeFile
		if seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, e)
		return true
	})
	return out
}

// resolveCallee maps a call's function expression to the exact *types.Func it
// invokes (generics collapsed via Origin), and whether it's an interface method.
func resolveCallee(info *types.Info, fun ast.Expr) (*types.Func, bool) {
	switch e := fun.(type) {
	case *ast.Ident: // Foo()
		if fn, ok := info.Uses[e].(*types.Func); ok {
			return fn.Origin(), false
		}
	case *ast.SelectorExpr: // x.Method() or pkg.Func()
		if sel, ok := info.Selections[e]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				iface := false
				if recv := sel.Recv(); recv != nil {
					if _, isIface := recv.Underlying().(*types.Interface); isIface {
						iface = true
					}
				}
				return fn.Origin(), iface
			}
		}
		// package-qualified function (not a method selection)
		if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
			return fn.Origin(), false
		}
	case *ast.IndexExpr: // generic instantiation Foo[T]()
		return resolveCallee(info, e.X)
	case *ast.IndexListExpr:
		return resolveCallee(info, e.X)
	}
	return nil, false
}

// declFQN builds a function/method FQN with the SAME scheme gosrc uses
// (pkgClauseName.Func or pkgClauseName.RecvType.Method, pointer/generics stripped)
// so precise edges join codemap's existing nodes.
func declFQN(pkgName string, d *ast.FuncDecl) string {
	name := d.Name.Name
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recv := recvTypeName(d.Recv.List[0].Type); recv != "" {
			return pkgName + "." + recv + "." + name
		}
	}
	return pkgName + "." + name
}

// funcFQN builds the gosrc-scheme FQN for a resolved callee *types.Func.
func funcFQN(fn *types.Func) string {
	pkgName := fn.Pkg().Name()
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		if recv := recvTypeNameFromType(sig.Recv().Type()); recv != "" {
			return pkgName + "." + recv + "." + fn.Name()
		}
	}
	return pkgName + "." + fn.Name()
}

// recvTypeName mirrors gosrc.recvTypeName for AST receiver expressions.
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

// recvTypeNameFromType is the types.Type analogue: strip pointer, take the named
// type's bare name (generics collapse to the origin named type).
func recvTypeNameFromType(t types.Type) string {
	switch tt := t.(type) {
	case *types.Pointer:
		return recvTypeNameFromType(tt.Elem())
	case *types.Named:
		return tt.Obj().Name()
	}
	return ""
}

// relOf returns p relative to root, matching how gosrc/nodes store file paths.
func relOf(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}
