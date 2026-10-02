package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	FlowSchemaVersion   = 1
	DefaultFlowDepth    = 4
	MaxFlowDepth        = 8
	DefaultFlowMaxNodes = 120
	MaxFlowMaxNodes     = 1000

	flowBuildCeiling  = 5000    // hard bound on the untrimmed tree (steps_total is a lower bound beyond it)
	flowBodyLineCap   = 400     // lines of a parent body scanned for call order
	flowMaxFileBytes  = 4 << 20 // larger files are not scanned for call order
	flowDocMax        = 160
	flowTopFiles      = 15
	flowCandidateCap  = 6 // ambiguity candidates listed on a placeholder step
	flowPartialErrCap = 10
)

// FlowOptions selects the entry symbol (exactly one of Symbol or Selector) and
// the bounds of the call tree.
type FlowOptions struct {
	Symbol       string
	Selector     *SymbolSelector
	Depth        int
	MaxNodes     int
	IncludeTests bool
}

// FlowStep is one node of the call tree, in the order the parent's body calls it.
//
// Ambiguous name-based fan-out is collapsed: when a parent's name-provenance
// edges reach several definitions of one name, the step is either the single
// definition that best matches the call site (alternatives = how many others
// were rejected, confidence = candidate) or an unexpanded placeholder
// (leaf_reason "ambiguous", no file/fqn, alternatives = the number of plausible
// definitions left, candidates = up to six of them). Precise edges are never
// collapsed.
//
// A symbol that has callees is expanded once (first appearance in print order);
// later appearances are leaf_reason "repeat" with repeat_of naming the expanded
// step. A symbol with no callees simply repeats as a plain leaf.
//
// leaf_reason values: "" (expanded or a true leaf), depth, max_nodes, ambiguous,
// repeat, cycle, reference (a value reference such as a handler passed as a
// callback — never expanded). "external" and "test" are reserved: callees
// outside the index have no node, and test callees are omitted with a note.
type FlowStep struct {
	ID            string               `json:"id"`
	Symbol        string               `json:"symbol"`
	FQN           string               `json:"fqn"`
	Kind          string               `json:"kind"`
	Language      string               `json:"language,omitempty"`
	File          string               `json:"file"`
	StartLine     int                  `json:"start_line"`
	EndLine       int                  `json:"end_line"`
	Subsystem     string               `json:"subsystem"`
	Signature     string               `json:"signature"`
	Doc           string               `json:"doc"`
	Selector      *SymbolSelector      `json:"selector,omitempty"`
	Depth         int                  `json:"depth"`
	Confidence    string               `json:"confidence"`
	Alternatives  int                  `json:"alternatives"`
	CallOrder     int                  `json:"call_order"`
	RepeatOf      string               `json:"repeat_of,omitempty"`
	Cycle         bool                 `json:"cycle"`
	LeafReason    string               `json:"leaf_reason"`
	Candidates    []AmbiguityCandidate `json:"candidates,omitempty"`
	Children      []*FlowStep          `json:"children"`
	ChildrenTotal int                  `json:"children_total"`

	node         graph.Node
	repeatTarget *FlowStep
}

// FlowSubsystem summarizes one subsystem in order of first appearance (BFS).
type FlowSubsystem struct {
	Name       string `json:"name"`
	Steps      int    `json:"steps"`
	FirstDepth int    `json:"first_depth"`
}

// FlowFile summarizes the steps defined in one file.
type FlowFile struct {
	File  string `json:"file"`
	Steps int    `json:"steps"`
}

// FlowReport is the bounded call-tree explanation of one entry symbol.
// steps_total / steps_emitted count tree steps (root, placeholders, repeats and
// reference leaves included); steps_total is the size before max_nodes trimming.
type FlowReport struct {
	SchemaVersion  int                  `json:"schema_version"`
	Project        string               `json:"project"`
	Indexed        bool                 `json:"indexed"`
	Found          bool                 `json:"found"`
	Root           *FlowStep            `json:"root"`
	Candidates     []AmbiguityCandidate `json:"candidates,omitempty"` // set when the symbol name is ambiguous; re-run with candidates[i].selector
	StepsTotal     int                  `json:"steps_total"`
	StepsEmitted   int                  `json:"steps_emitted"`
	MaxDepth       int                  `json:"max_depth"`
	MaxNodes       int                  `json:"max_nodes"`
	Truncated      bool                 `json:"truncated"`
	DepthTruncated bool                 `json:"depth_truncated"`
	Subsystems     []FlowSubsystem      `json:"subsystems"`
	Files          []FlowFile           `json:"files"`
	AmbiguousCalls int                  `json:"ambiguous_calls"`
	CallGraph      string               `json:"call_graph"`
	Resolution     string               `json:"resolution,omitempty"`
	Stale          bool                 `json:"stale"`
	Notes          []string             `json:"notes"`
	PartialErrors  []string             `json:"partial_errors"`
}

func normalizeFlowOptions(opts FlowOptions) (FlowOptions, error) {
	opts.Symbol = strings.TrimSpace(opts.Symbol)
	switch {
	case opts.Symbol == "" && opts.Selector == nil:
		return opts, coded(CodeInvalidInput, "pass a symbol name/FQN or a selector", fmt.Errorf("flow needs a symbol or a selector"))
	case opts.Symbol != "" && opts.Selector != nil:
		return opts, coded(CodeInvalidInput, "pass either a symbol or a selector, not both", fmt.Errorf("flow takes a symbol or a selector, not both"))
	}
	if opts.Depth == 0 {
		opts.Depth = DefaultFlowDepth
	}
	if opts.Depth < 1 || opts.Depth > MaxFlowDepth {
		return opts, coded(CodeInvalidInput, fmt.Sprintf("use a depth between 1 and %d", MaxFlowDepth), fmt.Errorf("flow depth must be between 1 and %d", MaxFlowDepth))
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = DefaultFlowMaxNodes
	}
	if opts.MaxNodes < 1 || opts.MaxNodes > MaxFlowMaxNodes {
		return opts, coded(CodeInvalidInput, fmt.Sprintf("use max nodes between 1 and %d", MaxFlowMaxNodes), fmt.Errorf("flow max nodes must be between 1 and %d", MaxFlowMaxNodes))
	}
	return opts, nil
}

// Flow returns a bounded call tree from one entry symbol: callees in the order
// the code calls them, name-based fan-out collapsed to the most plausible
// definition, each step annotated with its doc, subsystem, and confidence.
func (svc *Service) Flow(cwd string, opts FlowOptions) (*FlowReport, error) {
	opts, err := normalizeFlowOptions(opts)
	if err != nil {
		return nil, err
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &FlowReport{
		SchemaVersion: FlowSchemaVersion, Project: name, Indexed: found,
		MaxDepth: opts.Depth, MaxNodes: opts.MaxNodes,
		Subsystems: []FlowSubsystem{}, Files: []FlowFile{},
		CallGraph: CallGraphNone, Notes: []string{}, PartialErrors: []string{},
	}
	if !found {
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	project, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}

	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		return nil, err
	}
	b := newFlowBuilder(project.Path, nodes, edges, opts)

	var rootNode graph.Node
	if opts.Selector != nil {
		res, err := svc.resolveSourceSelector(cwd, *opts.Selector)
		if err != nil {
			return nil, err
		}
		if !res.found {
			rep.Notes = append(rep.Notes, "the selector does not resolve in the current index")
			return rep, nil
		}
		rootNode = res.node
	} else {
		matches, note := b.resolveSymbol(g, pid, opts.Symbol)
		switch {
		case len(matches) == 0:
			rep.Notes = append(rep.Notes, note)
			return rep, nil
		case len(matches) > 1:
			rep.Candidates = candidatesFromNodes(matches)
			rep.Notes = append(rep.Notes, fmt.Sprintf("%q matches %d definitions — re-run with candidates[i].selector (or --at file:line) to pick one", opts.Symbol, len(matches)))
			return rep, nil
		}
		rootNode = matches[0]
	}

	rep.Found = true
	root := b.rootStep(rootNode)
	b.build(root)
	rep.StepsTotal = b.total
	rep.Truncated = b.trim(root)
	rep.DepthTruncated = b.depthCut
	b.finalize(root)
	rep.Root = root
	rep.StepsEmitted = b.emitted

	b.summarize(rep, root)
	rep.PartialErrors = append(rep.PartialErrors, b.partial...)
	if len(b.partial) > flowPartialErrCap {
		rep.PartialErrors = rep.PartialErrors[:flowPartialErrCap]
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("… %d more unreadable files", len(b.partial)-flowPartialErrCap))
	}

	resolvedFiles, _ := g.CallGraphResolvedFiles(pid)
	callable := callableNodes(b.emittedNodes)
	rep.CallGraph = callGraphEnum(resolvedFiles, callable)
	if declarativeLanguage(rootNode.Language) {
		rep.Resolution = declarativeGuidance
	} else if lang, unavailable := callGraphUnavailableResolved(resolvedFiles, callable); unavailable {
		rep.Resolution = fmt.Sprintf("call relations are unresolved for %s without precise indexing — this tree is incomplete, not proof of absence", lang) + svc.coverageHintResolved(g, pid, resolvedFiles)
	}
	rep.Notes = append(rep.Notes, b.notes(rep)...)

	if st, sErr := svc.Staleness(cwd); sErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("staleness: %v", sErr))
	} else if st != nil {
		rep.Stale = st.Any()
	}
	return rep, nil
}

// ---- builder ----

type flowBuilder struct {
	root          string
	nodes         map[int64]graph.Node
	out           map[int64][]graph.Edge
	opts          FlowOptions
	maxDepth      int
	files         map[string]*flowFile
	visited       map[int64]*FlowStep
	total         int
	depthCut      bool
	ceilingHit    bool
	omittedTests  map[int64]bool
	partial       []string
	partialSeen   map[string]bool
	emitted       int
	emittedNodes  []graph.Node
	trimmed       bool
	ambiguousRefs int
	rejectedCalls int
	imports       map[string]flowImportInfo
}

type flowFile struct {
	lines []string
	ok    bool
}

func newFlowBuilder(root string, nodes []graph.Node, edges []graph.Edge, opts FlowOptions) *flowBuilder {
	b := &flowBuilder{
		root: root, nodes: make(map[int64]graph.Node, len(nodes)),
		out: map[int64][]graph.Edge{}, opts: opts, maxDepth: opts.Depth,
		files: map[string]*flowFile{}, visited: map[int64]*FlowStep{},
		omittedTests: map[int64]bool{}, partialSeen: map[string]bool{}, imports: map[string]flowImportInfo{},
	}
	for _, n := range nodes {
		b.nodes[n.ID] = n
	}
	for _, e := range edges {
		if e.EdgeType == graph.EdgeCalls || e.EdgeType == graph.EdgeReferences {
			b.out[e.SourceID] = append(b.out[e.SourceID], e)
		}
	}
	return b
}

// resolveSymbol maps a name or FQN to definitions, preferring callable kinds.
func (b *flowBuilder) resolveSymbol(g *graph.Store, pid int64, input string) ([]graph.Node, string) {
	var matches []graph.Node
	if byFQN, err := g.FindNodesByFQN(pid, input); err == nil && len(byFQN) > 0 {
		matches = byFQN
	} else {
		bare := canonicalSymbol(g, pid, input)
		matches, _ = g.FindNodesBySymbol(pid, bare)
		if len(matches) == 0 && bare != input {
			matches, _ = g.FindNodesBySymbol(pid, input)
		}
	}
	defs := make([]graph.Node, 0, len(matches))
	for _, n := range matches {
		if n.Kind != graph.KindFile {
			defs = append(defs, n)
		}
	}
	if callable := callableNodes(defs); len(callable) > 0 {
		defs = callable
	}
	if len(defs) == 0 {
		return nil, fmt.Sprintf("%q is not a symbol in this project", input)
	}
	return defs, ""
}

func (b *flowBuilder) rootStep(n graph.Node) *FlowStep {
	s := b.stepFor(n, 0)
	s.Confidence = "confirmed"
	b.total = 1
	return s
}

func (b *flowBuilder) stepFor(n graph.Node, depth int) *FlowStep {
	file := graph.CanonicalStructuralPath(n.FilePath)
	sel := selectorForNode(n)
	sel.File = file
	return &FlowStep{
		Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, Language: n.Language, File: file,
		StartLine: n.StartLine, EndLine: n.EndLine,
		Subsystem: graph.SubsystemOf(file), Signature: n.Signature,
		Doc: flowDoc(n.Docstring, flowDocMax), Selector: sel, Depth: depth,
		Confidence: "candidate", Children: []*FlowStep{}, node: n,
	}
}

func (b *flowBuilder) excluded(t graph.Node) bool {
	return !b.opts.IncludeTests && graph.IsTestNode(t)
}

type flowGroup struct {
	name      string
	targets   []graph.Node
	precise   bool
	reference bool
}

func flowSortNodes(ns []graph.Node) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].FilePath != ns[j].FilePath {
			return ns[i].FilePath < ns[j].FilePath
		}
		if ns[i].StartLine != ns[j].StartLine {
			return ns[i].StartLine < ns[j].StartLine
		}
		if ns[i].FQN != ns[j].FQN {
			return ns[i].FQN < ns[j].FQN
		}
		return ns[i].ID < ns[j].ID
	})
}

// groups returns the distinct callee groups of n: one per precise target, one
// per bare name among name-based call targets, one per bare name among value
// references (callable kinds only).
func (b *flowBuilder) groups(n graph.Node) []*flowGroup {
	preciseSeen := map[int64]bool{}
	nameTargets := map[int64]graph.Node{}
	refTargets := map[int64]graph.Node{}
	var preciseNodes []graph.Node
	for _, e := range b.out[n.ID] {
		t, ok := b.nodes[e.TargetID]
		if !ok || t.Kind == graph.KindFile {
			continue
		}
		if b.excluded(t) {
			b.omittedTests[t.ID] = true
			continue
		}
		switch e.EdgeType {
		case graph.EdgeCalls:
			if e.Provenance == graph.ProvPrecise {
				if !preciseSeen[t.ID] {
					preciseSeen[t.ID] = true
					preciseNodes = append(preciseNodes, t)
				}
			} else {
				nameTargets[t.ID] = t
			}
		case graph.EdgeReferences:
			if t.Kind == graph.KindFunction || t.Kind == graph.KindMethod {
				refTargets[t.ID] = t
			}
		}
	}
	var groups []*flowGroup
	flowSortNodes(preciseNodes)
	for _, t := range preciseNodes {
		groups = append(groups, &flowGroup{name: t.Symbol, targets: []graph.Node{t}, precise: true})
		delete(nameTargets, t.ID)
	}
	groups = append(groups, flowBucket(nameTargets, false, func(id int64) bool { return false })...)
	groups = append(groups, flowBucket(refTargets, true, func(id int64) bool {
		_, isCall := nameTargets[id]
		return isCall || preciseSeen[id] || id == n.ID
	})...)
	return groups
}

func flowBucket(targets map[int64]graph.Node, reference bool, skip func(int64) bool) []*flowGroup {
	byName := map[string][]graph.Node{}
	for id, t := range targets {
		if skip(id) {
			continue
		}
		byName[t.Symbol] = append(byName[t.Symbol], t)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*flowGroup, 0, len(names))
	for _, name := range names {
		ts := byName[name]
		flowSortNodes(ts)
		out = append(out, &flowGroup{name: name, targets: ts, reference: reference})
	}
	return out
}

// ---- file bodies ----

func (b *flowBuilder) fileLines(file string) []string {
	if f, ok := b.files[file]; ok {
		return f.lines
	}
	f := &flowFile{}
	b.files[file] = f
	full := filepath.Join(b.root, filepath.FromSlash(graph.CanonicalStructuralPath(file)))
	fail := func(why string) {
		if !b.partialSeen[file] {
			b.partialSeen[file] = true
			b.partial = append(b.partial, fmt.Sprintf("%s: %s — call order for its symbols falls back to name order", graph.CanonicalStructuralPath(file), why))
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			fail("missing on disk")
		} else {
			fail("unreadable")
		}
		return nil
	}
	if info.Size() > flowMaxFileBytes {
		fail("too large to scan")
		return nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		fail("unreadable")
		return nil
	}
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if strings.IndexByte(string(probe), 0) >= 0 {
		fail("binary file")
		return nil
	}
	f.lines = strings.Split(string(data), "\n")
	f.ok = true
	return f.lines
}

// body returns the masked text of n's definition (bounded) and the offset where
// its body proper starts. ok is false when the file cannot be read.
func (b *flowBuilder) body(n graph.Node) (string, int, bool) {
	lines := b.fileLines(n.FilePath)
	if lines == nil || n.StartLine < 1 || n.StartLine > len(lines) {
		return "", 0, false
	}
	end := n.EndLine
	if end < n.StartLine {
		end = n.StartLine
	}
	if end > n.StartLine+flowBodyLineCap-1 {
		end = n.StartLine + flowBodyLineCap - 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	src := strings.Join(lines[n.StartLine-1:end], "\n")
	masked := flowMask(src, flowSyntaxFor(n.Language))
	return masked, flowBodyStart(masked, n.Language), true
}

// ---- choosing among same-named definitions ----

type flowChoice struct {
	group        *flowGroup
	node         *graph.Node // nil for an ambiguous placeholder
	alternatives int
	offset       int // -1 when no call site was found
	remaining    []graph.Node
}

func (b *flowBuilder) resolve(parent graph.Node, groups []*flowGroup) []flowChoice {
	masked, start, ok := b.body(parent)
	jsx := false
	switch strings.ToLower(parent.Language) {
	case "javascript", "typescript", "tsx", "jsx":
		jsx = true
	}
	recvName, recvType := flowReceiver(parent)
	choices := make([]flowChoice, 0, len(groups))
	for _, g := range groups {
		var sites []flowSite
		if ok {
			if g.reference {
				sites = flowWordSites(masked, start, g.name)
			} else {
				sites = flowCallSites(masked, start, g.name, jsx)
				if len(sites) == 0 {
					sites = flowWordSites(masked, start, g.name)
				}
			}
		}
		if g.reference {
			ref := b.resolveReference(parent, g, sites, recvName, recvType)
			if ref == nil {
				continue
			}
			choices = append(choices, *ref)
			continue
		}
		if strings.EqualFold(parent.Language, "go") && callShaped(sites) {
			choices = append(choices, b.resolveGoCall(parent, g, sites, recvName, recvType)...)
			continue
		}
		choices = append(choices, b.resolveByProximity(parent, g, sites))
	}
	sort.SliceStable(choices, func(i, j int) bool {
		a, c := choices[i], choices[j]
		if (a.offset >= 0) != (c.offset >= 0) {
			return a.offset >= 0
		}
		if a.offset != c.offset {
			return a.offset < c.offset
		}
		if a.group.name != c.group.name {
			return a.group.name < c.group.name
		}
		return flowChoiceKey(a) < flowChoiceKey(c)
	})
	return choices
}

func flowChoiceKey(c flowChoice) string {
	if c.node == nil {
		return "~"
	}
	return fmt.Sprintf("%s:%08d:%s", c.node.FilePath, c.node.StartLine, c.node.FQN)
}

// callShaped reports whether sites came from call-shaped matches (Name(…) /
// <Name) rather than the bare word-boundary fallback.
func callShaped(sites []flowSite) bool {
	return len(sites) > 0 && sites[0].call
}

// resolveByProximity is the language-agnostic fallback: one child per group,
// the single definition nearest the parent (same directory, then same
// subsystem), or an unexpanded placeholder when proximity cannot decide.
func (b *flowBuilder) resolveByProximity(parent graph.Node, g *flowGroup, sites []flowSite) flowChoice {
	ch := flowChoice{group: g, offset: -1}
	if len(sites) > 0 {
		ch.offset = sites[0].off
	}
	if len(g.targets) == 1 {
		t := g.targets[0]
		ch.node = &t
		return ch
	}
	cands := flowProximity(parent, g.targets)
	if len(cands) == 1 {
		t := cands[0]
		ch.node = &t
		ch.alternatives = len(g.targets) - 1
	} else {
		ch.alternatives = len(cands)
		ch.remaining = cands
	}
	return ch
}

func flowProximity(parent graph.Node, cands []graph.Node) []graph.Node {
	cands = flowNarrow(cands, func(t graph.Node) bool { return filepath.Dir(t.FilePath) == filepath.Dir(parent.FilePath) })
	return flowNarrow(cands, func(t graph.Node) bool {
		return graph.SubsystemOf(t.FilePath) == graph.SubsystemOf(parent.FilePath)
	})
}

// resolveGoCall resolves a name-based Go call group site by site. Each call
// site's syntax says what it can reach: a bare Name() is a function of the
// parent's own package; pkg.Name() is that package's function; recv.Name() on
// the parent's receiver is a method of its type; x.Name() on any other
// expression is a method. Sites resolving to one definition become their own
// steps (so repeated pkg.New() calls each appear); sites that stay ambiguous
// collapse into one placeholder. A group whose every site is syntactically
// unable to reach any target (a local closure named like a function elsewhere)
// is a spurious edge and is dropped — counted, never silent.
func (b *flowBuilder) resolveGoCall(parent graph.Node, g *flowGroup, sites []flowSite, recvName, recvType string) []flowChoice {
	const maxSites = 50
	if len(sites) > maxSites {
		sites = sites[:maxSites]
	}
	parentDir := filepath.Dir(parent.FilePath)
	ii := b.goImports(parent.FilePath)
	picked := map[int64]int{} // node id → first call-site offset
	var order []graph.Node
	unresolved := map[int64]graph.Node{}
	firstUnresolved := -1
	weakSeen := false
	anyAllowed := false
	for _, s := range sites {
		allowed := flowGoSiteCands(g.targets, s, recvName, recvType, parentDir, ii)
		if len(allowed) == 0 {
			continue
		}
		anyAllowed = true
		cands, weak := allowed, false
		if len(g.targets) > 1 {
			cands, weak = flowGoNarrowSite(parent, allowed, s, recvName, len(g.targets))
		}
		if len(cands) == 1 && !weak {
			if _, seen := picked[cands[0].ID]; !seen {
				picked[cands[0].ID] = s.off
				order = append(order, cands[0])
			}
			continue
		}
		weakSeen = weakSeen || weak
		for _, c := range cands {
			unresolved[c.ID] = c
		}
		if firstUnresolved < 0 {
			firstUnresolved = s.off
		}
	}
	if !anyAllowed {
		b.rejectedCalls++
		return nil
	}
	var out []flowChoice
	for _, n := range order {
		node := n
		alt := 0
		if len(g.targets) > 1 {
			alt = len(g.targets) - 1
		}
		out = append(out, flowChoice{group: g, node: &node, alternatives: alt, offset: picked[n.ID]})
	}
	var rest []graph.Node
	for id, n := range unresolved {
		if _, isPick := picked[id]; !isPick {
			rest = append(rest, n)
		}
	}
	if len(rest) > 0 {
		flowSortNodes(rest)
		alt := len(rest)
		if weakSeen {
			alt = len(g.targets) // proximity was too weak to trust: report every definition of the name
		}
		out = append(out, flowChoice{group: g, alternatives: alt, remaining: rest, offset: firstUnresolved})
	}
	return out
}

// flowGoNarrowSite narrows one site's candidates: methods called on a variable
// prefer types whose name relates to the variable (sess → Session); a very
// common method name (4+ definitions) with no such relation is weak evidence
// and stays unresolved rather than being guessed by proximity.
func flowGoNarrowSite(parent graph.Node, cands []graph.Node, s flowSite, recvName string, total int) ([]graph.Node, bool) {
	allMethods := true
	for _, c := range cands {
		if c.Kind != graph.KindMethod {
			allMethods = false
		}
	}
	if allMethods && s.qualified && s.qual != "" && s.qual != recvName && len(cands) > 1 {
		var related []graph.Node
		for _, c := range cands {
			if flowTypeRelated(s.qual, c) {
				related = append(related, c)
			}
		}
		if len(related) > 0 {
			cands = related
		} else if total >= 4 {
			return cands, true
		}
	}
	return flowProximity(parent, cands), false
}

func flowTypeRelated(variable string, method graph.Node) bool {
	parts := strings.Split(method.FQN, ".")
	if len(parts) < 3 {
		return false
	}
	v, t := strings.ToLower(variable), strings.ToLower(parts[len(parts)-2])
	return len(v) >= 3 && (strings.Contains(t, v) || strings.Contains(v, t))
}

// resolveReference decides whether a value-reference group is a genuine
// callback/handler use worth showing, and which definition it is. Name-based
// reference edges fan out to every same-named symbol, so most are field reads
// (cfg.Path) or locals; a site only counts when its syntax can denote one of the
// targets: a bare identifier for a package-level function, pkg.Func for that
// package, or recv.method on the parent's own receiver. Anything still
// ambiguous after narrowing is omitted (and counted), never guessed.
func (b *flowBuilder) resolveReference(parent graph.Node, g *flowGroup, sites []flowSite, recvName, recvType string) *flowChoice {
	isGo := strings.EqualFold(parent.Language, "go")
	for _, s := range sites {
		var allowed []graph.Node
		for _, t := range g.targets {
			switch {
			case !s.qualified:
				// In Go a bare identifier can only name a function of the same
				// package; a same-named function elsewhere is a local or a field.
				if t.Kind != graph.KindMethod && (!isGo || filepath.Dir(t.FilePath) == filepath.Dir(parent.FilePath)) {
					allowed = append(allowed, t)
				}
			case !isGo:
				if s.qual == "this" || s.qual == "self" {
					allowed = append(allowed, t)
				}
			case s.qual != "" && s.qual == recvName && recvType != "":
				if t.Kind == graph.KindMethod && strings.HasPrefix(t.FQN, recvType+".") {
					allowed = append(allowed, t)
				}
			case s.qual != "":
				if t.Kind != graph.KindMethod && b.goImports(parent.FilePath).reaches(s.qual, t) {
					allowed = append(allowed, t)
				}
			}
		}
		if len(allowed) == 0 {
			continue
		}
		allowed = flowNarrow(allowed, func(t graph.Node) bool { return filepath.Dir(t.FilePath) == filepath.Dir(parent.FilePath) })
		allowed = flowNarrow(allowed, func(t graph.Node) bool {
			return graph.SubsystemOf(t.FilePath) == graph.SubsystemOf(parent.FilePath)
		})
		if len(allowed) != 1 {
			b.ambiguousRefs++
			return nil
		}
		t := allowed[0]
		return &flowChoice{group: g, node: &t, offset: s.off, alternatives: len(g.targets) - 1}
	}
	return nil
}

func flowNarrow(cands []graph.Node, pred func(graph.Node) bool) []graph.Node {
	if len(cands) <= 1 {
		return cands
	}
	var sub []graph.Node
	for _, c := range cands {
		if pred(c) {
			sub = append(sub, c)
		}
	}
	if len(sub) == 0 {
		return cands
	}
	return sub
}

// flowReceiver extracts the receiver variable and the receiver type's FQN from
// a Go method, e.g. "func (s *Session) Close()" with FQN app.Session.Close →
// ("s", "app.Session").
func flowReceiver(n graph.Node) (name, typeFQN string) {
	if n.Kind != graph.KindMethod || !strings.EqualFold(n.Language, "go") {
		return "", ""
	}
	sig := strings.TrimSpace(n.Signature)
	if !strings.HasPrefix(sig, "func (") {
		return "", ""
	}
	rest := sig[len("func ("):]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return "", ""
	}
	fields := strings.Fields(rest[:end])
	if len(fields) < 2 {
		return "", ""
	}
	dot := strings.LastIndexByte(n.FQN, '.')
	if dot < 0 {
		return "", ""
	}
	return fields[0], n.FQN[:dot]
}

func flowPackageOf(fqn string) string {
	if dot := strings.IndexByte(fqn, '.'); dot > 0 {
		return fqn[:dot]
	}
	return ""
}

// ---- tree construction ----

func (b *flowBuilder) build(root *FlowStep) {
	b.expand(root, map[int64]bool{})
}

func (b *flowBuilder) expand(step *FlowStep, ancestors map[int64]bool) {
	n := step.node
	groups := b.groups(n)
	if len(groups) == 0 {
		return
	}
	choices := b.resolve(n, groups)
	step.ChildrenTotal = len(choices)
	if len(choices) == 0 {
		return
	}
	if step.Depth >= b.maxDepth {
		step.LeafReason = "depth"
		b.depthCut = true
		return
	}
	b.visited[n.ID] = step
	ancestors[n.ID] = true
	defer delete(ancestors, n.ID)

	order := 0
	for _, ch := range choices {
		if ch.offset >= 0 {
			order++
		}
		if b.total >= flowBuildCeiling {
			b.ceilingHit = true
			step.LeafReason = "max_nodes"
			break
		}
		child := b.childStep(ch, step.Depth+1)
		if ch.offset >= 0 {
			child.CallOrder = order
		}
		b.total++
		step.Children = append(step.Children, child)
		switch {
		case ch.node == nil:
			child.LeafReason = "ambiguous"
		case ch.group.reference:
			child.LeafReason = "reference"
		case ancestors[child.node.ID]:
			child.Cycle = true
			child.LeafReason = "cycle"
		case b.visited[child.node.ID] != nil:
			child.repeatTarget = b.visited[child.node.ID]
			child.LeafReason = "repeat"
		default:
			b.expand(child, ancestors)
		}
	}
}

func (b *flowBuilder) childStep(ch flowChoice, depth int) *FlowStep {
	if ch.node == nil {
		s := &FlowStep{
			Symbol: ch.group.name, Depth: depth, Confidence: "candidate",
			Alternatives: ch.alternatives, Children: []*FlowStep{},
		}
		cands := ch.remaining
		if len(cands) > flowCandidateCap {
			cands = cands[:flowCandidateCap]
		}
		s.Candidates = candidatesFromNodes(cands)
		for i := range s.Candidates {
			s.Candidates[i].File = graph.CanonicalStructuralPath(s.Candidates[i].File)
			s.Candidates[i].Selector.File = s.Candidates[i].File
		}
		return s
	}
	s := b.stepFor(*ch.node, depth)
	s.Alternatives = ch.alternatives
	if ch.group.precise {
		s.Confidence = "confirmed"
	}
	return s
}

// trim applies the max_nodes budget: a node's children are all admitted before
// any child is expanded, so siblings stay visible even when a deep first
// subtree would otherwise eat the budget. It reports whether anything was cut.
func (b *flowBuilder) trim(root *FlowStep) bool {
	budget := b.opts.MaxNodes - 1
	cut := b.ceilingHit
	var walk func(s *FlowStep)
	walk = func(s *FlowStep) {
		if len(s.Children) == 0 {
			return
		}
		if budget <= 0 {
			s.Children = []*FlowStep{}
			s.LeafReason = "max_nodes"
			cut = true
			return
		}
		keep := len(s.Children)
		if keep > budget {
			keep = budget
			s.LeafReason = "max_nodes"
			cut = true
		}
		budget -= keep
		s.Children = s.Children[:keep]
		for _, c := range s.Children {
			walk(c)
		}
	}
	walk(root)
	b.trimmed = cut
	return cut
}

// finalize assigns ids in print order, resolves repeat_of, and downgrades
// repeats whose original was trimmed away.
func (b *flowBuilder) finalize(root *FlowStep) {
	kept := map[*FlowStep]bool{}
	var collect func(s *FlowStep)
	collect = func(s *FlowStep) {
		kept[s] = true
		for _, c := range s.Children {
			collect(c)
		}
	}
	collect(root)
	n := 0
	var walk func(s *FlowStep)
	walk = func(s *FlowStep) {
		s.ID = fmt.Sprintf("s%d", n)
		n++
		if s.repeatTarget != nil {
			if kept[s.repeatTarget] {
				s.RepeatOf = s.repeatTarget.ID
			} else {
				// the expansion this repeats was trimmed away: report an honest cut instead
				s.LeafReason = "max_nodes"
				s.ChildrenTotal = len(b.resolve(s.node, b.groups(s.node)))
			}
		}
		b.emittedNodes = append(b.emittedNodes, s.node)
		for _, c := range s.Children {
			walk(c)
		}
	}
	walk(root)
	b.emitted = n
	// placeholders carry no node: drop their zero values from the confidence set
	real := b.emittedNodes[:0]
	for _, nd := range b.emittedNodes {
		if nd.ID != 0 {
			real = append(real, nd)
		}
	}
	b.emittedNodes = real
}

// summarize fills subsystems (BFS first-appearance order), files, and the
// ambiguous-call count from the emitted tree.
func (b *flowBuilder) summarize(rep *FlowReport, root *FlowStep) {
	subIdx := map[string]int{}
	fileCount := map[string]int{}
	queue := []*FlowStep{root}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s.Alternatives > 0 || s.LeafReason == "ambiguous" {
			rep.AmbiguousCalls++
		}
		if s.File != "" {
			fileCount[s.File]++
			i, ok := subIdx[s.Subsystem]
			if !ok {
				i = len(rep.Subsystems)
				subIdx[s.Subsystem] = i
				rep.Subsystems = append(rep.Subsystems, FlowSubsystem{Name: s.Subsystem, FirstDepth: s.Depth})
			}
			rep.Subsystems[i].Steps++
		}
		queue = append(queue, s.Children...)
	}
	for file, steps := range fileCount {
		rep.Files = append(rep.Files, FlowFile{File: file, Steps: steps})
	}
	sort.Slice(rep.Files, func(i, j int) bool {
		if rep.Files[i].Steps != rep.Files[j].Steps {
			return rep.Files[i].Steps > rep.Files[j].Steps
		}
		return rep.Files[i].File < rep.Files[j].File
	})
	if len(rep.Files) > flowTopFiles {
		rep.Files = rep.Files[:flowTopFiles]
	}
}

func (b *flowBuilder) notes(rep *FlowReport) []string {
	var notes []string
	if rep.CallGraph == CallGraphName {
		notes = append(notes, "calls are name-based candidates: edges to same-named definitions were collapsed using call-site syntax and package/directory proximity; run 'codemap index --precise' for exact edges")
	}
	if rep.AmbiguousCalls > 0 {
		notes = append(notes, fmt.Sprintf("%d step(s) involve same-named definitions (alternatives > 0); placeholders with leaf_reason \"ambiguous\" are not expanded", rep.AmbiguousCalls))
	}
	if b.rejectedCalls > 0 {
		notes = append(notes, fmt.Sprintf("%d name-based call edge(s) rejected: the call-site syntax cannot reach the target (e.g. a local closure named like a function elsewhere)", b.rejectedCalls))
	}
	if b.ambiguousRefs > 0 {
		notes = append(notes, fmt.Sprintf("%d ambiguous value reference(s) omitted (same-named definitions with no call-site evidence)", b.ambiguousRefs))
	}
	if len(b.omittedTests) > 0 {
		notes = append(notes, fmt.Sprintf("%d test callee(s) omitted; pass include_tests to show them", len(b.omittedTests)))
	}
	if rep.Truncated {
		notes = append(notes, fmt.Sprintf("tree trimmed to max_nodes=%d (%d steps before trimming); raise max_nodes or lower depth", rep.MaxNodes, rep.StepsTotal))
	}
	if b.ceilingHit {
		notes = append(notes, fmt.Sprintf("tree construction stopped at %d steps; steps_total is a lower bound", flowBuildCeiling))
	}
	if rep.DepthTruncated {
		notes = append(notes, fmt.Sprintf("steps at depth %d have callees that are not shown (leaf_reason \"depth\"); raise depth to see them", rep.MaxDepth))
	}
	return notes
}
