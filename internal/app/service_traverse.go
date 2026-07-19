package app

import (
	"fmt"
	"sort"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	TraverseSchemaVersion = 1
	DefaultTraverseDepth  = 2
	DefaultTraverseLimit  = 100
	MaxTraverseDepth      = 10
	MaxTraverseLimit      = 500
)

var supportedTraverseEdges = map[string]bool{
	graph.EdgeCalls: true, graph.EdgeReferences: true, graph.EdgeImports: true,
	graph.EdgeImplements: true, graph.EdgeOverrides: true, graph.EdgeDependsOn: true,
	graph.EdgeTests: true, graph.EdgeDefines: true,
}

type TraverseOptions struct {
	Direction string
	EdgeTypes []string
	Depth     int
	Limit     int
}

// TraverseHop is one graph entity reached through one stored relation. Most
// hops are exact definitions; import traversal can also reach file nodes.
// Confidence applies to this edge only; it does not silently upgrade the other
// domains in the walk.
type TraverseHop struct {
	Symbol           SymbolRef       `json:"symbol"`
	Selector         *SymbolSelector `json:"selector"`
	ParentSelector   *SymbolSelector `json:"parent_selector"`
	Depth            int             `json:"depth"`
	Direction        string          `json:"direction"`
	EdgeType         string          `json:"edge_type"`
	Weight           float64         `json:"weight"`
	Provenance       string          `json:"provenance"`
	Confidence       string          `json:"confidence"`
	ConfidenceReason string          `json:"confidence_reason"`
}

type TraverseReport struct {
	SchemaVersion int              `json:"schema_version"`
	Project       string           `json:"project"`
	Indexed       bool             `json:"indexed"`
	Found         bool             `json:"found"`
	Start         *SymbolSelector  `json:"start,omitempty"`
	Direction     string           `json:"direction"`
	EdgeTypes     []string         `json:"edge_types"`
	DepthLimit    int              `json:"depth_limit"`
	NodeLimit     int              `json:"node_limit"`
	Hops          []TraverseHop    `json:"hops"`
	Truncated     bool             `json:"truncated,omitempty"`
	CallGraph     string           `json:"call_graph"`
	Resolution    string           `json:"resolution,omitempty"`
	Domains       []TraverseDomain `json:"domains"`
}

// TraverseDomain summarizes the confidence observed for one relationship
// domain, so consumers can make domain-specific decisions without parsing all
// hops or treating one precise call as proof about imports/references.
type TraverseDomain struct {
	EdgeType  string `json:"edge_type"`
	Confirmed int    `json:"confirmed"`
	Candidate int    `json:"candidate"`
}

// TraverseBySelector performs a bounded heterogeneous walk from one durable
// source selector. It never accepts a name union: exploration across relation
// types must start from an exact current definition.
func (svc *Service) TraverseBySelector(cwd string, selector SymbolSelector, opts TraverseOptions) (*TraverseReport, error) {
	opts, err := normalizeTraverseOptions(opts)
	if err != nil {
		return nil, err
	}
	resolved, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	rep := &TraverseReport{
		SchemaVersion: TraverseSchemaVersion,
		Project:       resolved.projectName,
		Indexed:       resolved.project != nil,
		Direction:     opts.Direction,
		EdgeTypes:     append([]string(nil), opts.EdgeTypes...),
		DepthLimit:    opts.Depth,
		NodeLimit:     opts.Limit,
		Hops:          []TraverseHop{},
		CallGraph:     CallGraphNone,
		Domains:       []TraverseDomain{},
	}
	if !resolved.found {
		return rep, nil
	}
	rep.Found = true
	rep.Start = selectorForNode(resolved.node)

	walk, err := resolved.graph.TraverseFromNode(resolved.project.ID, resolved.node.ID, graph.TraversalOptions{
		Direction: opts.Direction,
		EdgeTypes: opts.EdgeTypes,
		MaxDepth:  opts.Depth,
		MaxNodes:  opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	rep.Truncated = walk.Truncated
	nodes := map[int64]graph.Node{resolved.node.ID: resolved.node}
	for _, step := range walk.Steps {
		nodes[step.Node.ID] = step.Node
	}
	domains := map[string]*TraverseDomain{}
	for _, step := range walk.Steps {
		parent, ok := nodes[step.ParentID]
		if !ok {
			return nil, fmt.Errorf("traversal parent missing from result")
		}
		confidence, reason := traversalEdgeConfidence(step.Edge.EdgeType, step.Edge.Provenance)
		domain := domains[step.Edge.EdgeType]
		if domain == nil {
			domain = &TraverseDomain{EdgeType: step.Edge.EdgeType}
			domains[step.Edge.EdgeType] = domain
		}
		if confidence == "confirmed" {
			domain.Confirmed++
		} else {
			domain.Candidate++
		}
		rep.Hops = append(rep.Hops, TraverseHop{
			Symbol: nodeToRef(step.Node), Selector: selectorForNode(step.Node), ParentSelector: selectorForNode(parent),
			Depth: step.Depth, Direction: step.Direction, EdgeType: step.Edge.EdgeType,
			Weight: step.Edge.Weight, Provenance: step.Edge.Provenance,
			Confidence: confidence, ConfidenceReason: reason,
		})
	}
	for _, domain := range domains {
		rep.Domains = append(rep.Domains, *domain)
	}
	sort.Slice(rep.Domains, func(i, j int) bool { return rep.Domains[i].EdgeType < rep.Domains[j].EdgeType })

	if containsString(opts.EdgeTypes, graph.EdgeCalls) {
		resolvedFiles, _ := resolved.graph.CallGraphResolvedFiles(resolved.project.ID)
		rep.CallGraph = callGraphEnum(resolvedFiles, []graph.Node{resolved.node})
		if lang, unavailable := callGraphUnavailableResolved(resolvedFiles, []graph.Node{resolved.node}); unavailable {
			rep.Resolution = fmt.Sprintf("call relations are unresolved for %s without precise indexing; non-call domains remain independently available", lang) + svc.coverageHintResolved(resolved.graph, resolved.project.ID, resolvedFiles)
		}
	}
	return rep, nil
}

func normalizeTraverseOptions(opts TraverseOptions) (TraverseOptions, error) {
	if opts.Direction == "" {
		opts.Direction = graph.TraversalBoth
	}
	if opts.Direction != graph.TraversalOutgoing && opts.Direction != graph.TraversalIncoming && opts.Direction != graph.TraversalBoth {
		return opts, fmt.Errorf("traverse direction must be outgoing, incoming, or both")
	}
	if len(opts.EdgeTypes) == 0 {
		opts.EdgeTypes = []string{
			graph.EdgeCalls, graph.EdgeReferences, graph.EdgeImports,
			graph.EdgeImplements, graph.EdgeOverrides, graph.EdgeDependsOn, graph.EdgeTests,
		}
	}
	seen := map[string]bool{}
	edges := make([]string, 0, len(opts.EdgeTypes))
	for _, edgeType := range opts.EdgeTypes {
		if !supportedTraverseEdges[edgeType] {
			return opts, fmt.Errorf("unsupported traverse edge type %q", edgeType)
		}
		if !seen[edgeType] {
			seen[edgeType] = true
			edges = append(edges, edgeType)
		}
	}
	sort.Strings(edges)
	opts.EdgeTypes = edges
	if opts.Depth == 0 {
		opts.Depth = DefaultTraverseDepth
	}
	if opts.Depth < 1 || opts.Depth > MaxTraverseDepth {
		return opts, fmt.Errorf("traverse depth must be between 1 and %d", MaxTraverseDepth)
	}
	if opts.Limit == 0 {
		opts.Limit = DefaultTraverseLimit
	}
	if opts.Limit < 1 || opts.Limit > MaxTraverseLimit {
		return opts, fmt.Errorf("traverse limit must be between 1 and %d", MaxTraverseLimit)
	}
	return opts, nil
}

func traversalEdgeConfidence(edgeType, provenance string) (string, string) {
	if edgeType == graph.EdgeDefines {
		return "confirmed", "the extractor observed direct file membership"
	}
	if edgeType == graph.EdgeImports {
		return "candidate", "imports are package-scoped dependency evidence, not proof of a symbol use"
	}
	if provenance == graph.ProvPrecise {
		return "confirmed", "the relation was resolved by an exact backend"
	}
	return "candidate", "the relation is name-based or heuristic and may fan out"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
