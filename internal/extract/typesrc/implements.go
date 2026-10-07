package typesrc

import (
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Position locates a declaration the way gosrc stores node start lines (the
// declared name's line), root-relative.
type Position struct {
	File string
	Line int
}

// ImplementsEdge records that a named type of the module satisfies an
// interface of the module (directly or through its pointer).
type ImplementsEdge struct {
	Type, Interface Position
}

// OverrideEdge links a concrete method to the interface method it satisfies.
type OverrideEdge struct {
	Method, InterfaceMethod Position
}

// maxImplementsChecks bounds the types × interfaces satisfiability matrix so a
// huge module cannot stall the precise pass; past it the pass is skipped
// (Result.ImplementsSkipped) rather than partially written.
const maxImplementsChecks = 5_000_000

// implementsEdges computes which module types satisfy which module
// interfaces. Interfaces with no methods (any) are skipped: everything
// satisfies them, which says nothing.
func implementsEdges(fset *token.FileSet, root string, pkgs []*packages.Package) (impls []ImplementsEdge, overrides []OverrideEdge, skipped bool) {
	type named struct {
		obj *types.TypeName
		pos Position
	}
	var concrete, ifaces []named
	seen := map[Position]bool{}
	pos := func(p token.Pos) (Position, bool) {
		if !p.IsValid() {
			return Position{}, false
		}
		at := fset.Position(p)
		rel := relOf(root, at.Filename)
		if strings.HasPrefix(rel, "..") || strings.HasSuffix(rel, ".test") {
			return Position{}, false
		}
		return Position{File: rel, Line: at.Line}, true
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 || p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			at, ok := pos(tn.Pos())
			if !ok || seen[at] {
				continue // test variants load the same declarations again
			}
			seen[at] = true
			if it, isIface := tn.Type().Underlying().(*types.Interface); isIface {
				if it.NumMethods() > 0 {
					ifaces = append(ifaces, named{tn, at})
				}
				continue
			}
			concrete = append(concrete, named{tn, at})
		}
	}
	if len(concrete)*len(ifaces) > maxImplementsChecks {
		return nil, nil, true
	}
	for _, c := range concrete {
		t := c.obj.Type()
		ptr := types.NewPointer(t)
		for _, i := range ifaces {
			it := i.obj.Type().Underlying().(*types.Interface)
			var via types.Type
			switch {
			case types.Implements(t, it):
				via = t
			case types.Implements(ptr, it):
				via = ptr
			default:
				continue
			}
			impls = append(impls, ImplementsEdge{Type: c.pos, Interface: i.pos})
			mset := types.NewMethodSet(via)
			for k := 0; k < it.NumMethods(); k++ {
				im := it.Method(k)
				sel := mset.Lookup(im.Pkg(), im.Name())
				if sel == nil {
					continue
				}
				mp, ok1 := pos(sel.Obj().Pos())
				ip, ok2 := pos(im.Pos())
				if ok1 && ok2 && mp != ip {
					overrides = append(overrides, OverrideEdge{Method: mp, InterfaceMethod: ip})
				}
			}
		}
	}
	return impls, overrides, false
}
