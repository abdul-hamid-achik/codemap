package app

import "github.com/abdul-hamid-achik/codemap/internal/graph"

// HierarchyReport is a definition's declared inheritance neighborhood:
// what it extends and implements, what extends/implements it, and — for a
// method — the base methods it overrides and the methods overriding it. The
// edges come from declared heritage (TS/JS/Python via tree-sitter) and are
// name-provenance candidates; overrides are derived from same-named methods of
// direct bases only.
type HierarchyReport struct {
	Extends      []SymbolRef `json:"extends,omitempty"`
	Implements   []SymbolRef `json:"implements,omitempty"`
	Subtypes     []SymbolRef `json:"subtypes,omitempty"`
	Overrides    []SymbolRef `json:"overrides,omitempty"`
	OverriddenBy []SymbolRef `json:"overridden_by,omitempty"`
}

func (h *HierarchyReport) empty() bool {
	return len(h.Extends)+len(h.Implements)+len(h.Subtypes)+len(h.Overrides)+len(h.OverriddenBy) == 0
}

// attachHierarchy fills rep.Hierarchy from the definitions' inheritance
// edges. Best-effort: a query failure leaves the field absent.
func (svc *Service) attachHierarchy(cwd string, rep *ContextReport) {
	pid, _, found, err := svc.project(cwd)
	if err != nil || !found {
		return
	}
	g, err := svc.s.Graph()
	if err != nil {
		return
	}
	h := &HierarchyReport{}
	for _, d := range rep.Definitions {
		edges, err := g.HierarchyOf(pid, d.File, d.FQN)
		if err != nil {
			return
		}
		for _, e := range edges {
			ref := nodeToRef(e.Node)
			switch {
			case e.EdgeType == graph.EdgeExtends && e.Outgoing:
				h.Extends = append(h.Extends, ref)
			case e.EdgeType == graph.EdgeImplements && e.Outgoing:
				h.Implements = append(h.Implements, ref)
			case e.EdgeType == graph.EdgeOverrides && e.Outgoing:
				h.Overrides = append(h.Overrides, ref)
			case e.EdgeType == graph.EdgeOverrides:
				h.OverriddenBy = append(h.OverriddenBy, ref)
			default: // incoming extends/implements
				h.Subtypes = append(h.Subtypes, ref)
			}
		}
	}
	if !h.empty() {
		h.Subtypes = capSlice(h.Subtypes, contextListCap)
		h.OverriddenBy = capSlice(h.OverriddenBy, contextListCap)
		rep.Hierarchy = h
	}
}
