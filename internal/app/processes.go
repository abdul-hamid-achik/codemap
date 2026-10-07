package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	ProcessesSchemaVersion = 1
	DefaultProcessesTop    = 50
	MaxProcessesTop        = 200
	DefaultProcessDepth    = 4
	DefaultProcessSteps    = 40
	MaxProcessSteps        = 200

	// processBuildCeiling bounds the untrimmed tree built per process; flow's
	// own ceiling (5000) is sized for one deliberate drill-down, not for dozens
	// of entries computed on demand.
	processBuildCeiling = 400
	// processEvalCap is the most entrypoints --query will build flows for when
	// it has to inspect every one to decide which match.
	processEvalCap = 300

	// DefaultExploreProcesses is how many processes explore attaches.
	DefaultExploreProcesses = 3
	MaxExploreProcesses     = 10
	exploreProcessSteps     = 8
	// exploreProcessCandidates caps the entrypoints explore builds a flow for:
	// only those whose handler can reach a seed within the process depth, the
	// nearest first.
	exploreProcessCandidates = 12
)

// ProcessesOptions bounds and filters the process list. Zero values use the
// documented defaults.
type ProcessesOptions struct {
	Kinds    []string // feature kinds (empty = all)
	Query    string   // content words matched against the name and every step
	Top      int
	Depth    int
	MaxSteps int // steps kept per process
}

// ProcessStep is one symbol of a process in call order. Depth 0 is the entry
// handler itself.
type ProcessStep struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	Depth     int    `json:"depth"`
}

// Process is one execution flow from a feature entrypoint: the handler and
// the chain of definitions it reaches, in the order the code calls them.
// Ambiguous same-name fan-out is collapsed exactly as flow collapses it;
// unexpanded placeholders and repeats are not listed as steps.
type Process struct {
	ID         string          `json:"id"` // the feature id (kind:name, #n when a name repeats)
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Entry      *SymbolSelector `json:"entry"`
	Steps      []ProcessStep   `json:"steps"`
	StepsTotal int             `json:"steps_total"` // tree size before trimming (a lower bound once the build ceiling hits)
	Files      []string        `json:"files"`
	Truncated  bool            `json:"truncated"` // steps were cut by the node budget or the depth bound
	CallGraph  string          `json:"call_graph"`
}

// ProcessesReport is a bounded, on-demand list of execution flows. Nothing is
// stored: every call re-reads the graph and the registrations.
type ProcessesReport struct {
	SchemaVersion  int       `json:"schema_version"`
	Project        string    `json:"project"`
	Indexed        bool      `json:"indexed"`
	Processes      []Process `json:"processes"`
	ProcessesTotal int       `json:"processes_total"` // entrypoints with a resolved handler (after --query: matches among those evaluated)
	Truncated      bool      `json:"truncated"`
	Depth          int       `json:"depth"`
	MaxSteps       int       `json:"max_steps"`
	CallGraph      string    `json:"call_graph"`
	Resolution     string    `json:"resolution,omitempty"`
	Stale          bool      `json:"stale"`
	Notes          []string  `json:"notes"`
	PartialErrors  []string  `json:"partial_errors"`
}

func normalizeProcessesOptions(opts ProcessesOptions) (ProcessesOptions, error) {
	if opts.Top == 0 {
		opts.Top = DefaultProcessesTop
	}
	if opts.Top < 1 || opts.Top > MaxProcessesTop {
		return opts, coded(CodeInvalidInput, fmt.Sprintf("use top between 1 and %d", MaxProcessesTop), fmt.Errorf("processes top must be between 1 and %d", MaxProcessesTop))
	}
	if opts.Depth == 0 {
		opts.Depth = DefaultProcessDepth
	}
	if opts.Depth < 1 || opts.Depth > MaxFlowDepth {
		return opts, coded(CodeInvalidInput, fmt.Sprintf("use a depth between 1 and %d", MaxFlowDepth), fmt.Errorf("processes depth must be between 1 and %d", MaxFlowDepth))
	}
	if opts.MaxSteps == 0 {
		opts.MaxSteps = DefaultProcessSteps
	}
	if opts.MaxSteps < 1 || opts.MaxSteps > MaxProcessSteps {
		return opts, coded(CodeInvalidInput, fmt.Sprintf("use max steps between 1 and %d", MaxProcessSteps), fmt.Errorf("processes max steps must be between 1 and %d", MaxProcessSteps))
	}
	checked, err := normalizeFeaturesOptions(FeaturesOptions{Kinds: opts.Kinds})
	if err != nil {
		return opts, coded(CodeInvalidInput, "see 'codemap features --help' for the valid kinds", err)
	}
	opts.Kinds = checked.Kinds
	opts.Query = strings.TrimSpace(opts.Query)
	return opts, nil
}

// processEngine holds everything one request needs to build many flows: the
// project's graph in memory (shared by every tree), the detected entrypoints
// with a callable handler, and a reusable flow builder.
type processEngine struct {
	g        *graph.Store
	pid      int64
	project  string
	scan     *featureScan
	entries  []scoredFeature
	builder  *flowBuilder
	resolved map[string]bool
	skipped  int // entrypoints without a resolved callable handler
	notes    []string
	errs     []string
}

// newProcessEngine reads the project's graph and registrations. It returns a
// nil engine (and no error) when the project is not indexed or has no nodes.
func (svc *Service) newProcessEngine(cwd string, kinds []string, depth, maxSteps int) (*processEngine, string, bool, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, name, false, err
	}
	if !found {
		return nil, name, false, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, name, false, err
	}
	proj, err := g.GetProjectByName(name)
	if err != nil {
		return nil, name, false, err
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, name, false, err
	}
	if len(nodes) == 0 {
		return nil, name, false, nil
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		return nil, name, true, err
	}
	// The flow builder copies the nodes; the feature scan then normalizes the
	// slice's paths in place, so build the flow side first.
	opts := FlowOptions{Depth: depth, MaxNodes: maxSteps}
	b := newFlowBuilder(proj.Path, nodes, edges, opts)
	b.ceiling = processBuildCeiling

	scan := newFeatureScan(proj.Path, name, nodes)
	feats := scan.run()
	feats = filterFeatures(feats, FeaturesOptions{Kinds: kinds})
	sortFeatures(feats)
	dedupeFeatureIDs(feats)

	resolved, rErr := g.CallGraphResolvedFiles(pid)
	if rErr != nil {
		resolved = nil
	}
	e := &processEngine{
		g: g, pid: pid, project: name, scan: scan, builder: b, resolved: resolved,
		notes: append([]string{}, scan.notes...), errs: capFeatureErrors(scan.errs),
	}
	for _, f := range feats {
		if h := scan.nodeByID[f.handlerNodeID]; h != nil && (h.Kind == graph.KindFunction || h.Kind == graph.KindMethod) {
			e.entries = append(e.entries, f)
		} else {
			e.skipped++
		}
	}
	return e, name, true, nil
}

// build computes one process from an entrypoint. The returned tree root and
// the builder state stay valid until the next build call.
func (e *processEngine) build(f scoredFeature, depth, maxSteps int) (*Process, *FlowStep) {
	handler := *e.scan.nodeByID[f.handlerNodeID]
	e.builder.reset(FlowOptions{Depth: depth, MaxNodes: maxSteps})
	root, cut := e.builder.buildTree(handler)
	p := &Process{
		ID: f.ID, Kind: f.Kind, Name: f.Label,
		Entry:      selectorForNode(handler),
		Steps:      []ProcessStep{},
		Files:      []string{},
		StepsTotal: e.builder.total,
		Truncated:  cut || e.builder.depthCut,
		CallGraph:  callGraphEnum(e.resolved, callableNodes(e.builder.emittedNodes)),
	}
	p.Entry.File = graph.CanonicalStructuralPath(p.Entry.File)
	seen := map[string]bool{}
	for _, s := range processSteps(root) {
		p.Steps = append(p.Steps, processStepOf(s))
		if !seen[s.File] {
			seen[s.File] = true
			p.Files = append(p.Files, s.File)
		}
	}
	return p, root
}

func processStepOf(s *FlowStep) ProcessStep {
	return ProcessStep{Symbol: s.Symbol, FQN: s.FQN, Kind: s.Kind, File: s.File, StartLine: s.StartLine, Depth: s.Depth}
}

// processSteps lists the real definitions of a flow tree in call order
// (preorder over children that are already in call order). Ambiguous
// placeholders carry no definition and repeats were already listed at their
// first appearance, so neither becomes a step.
func processSteps(root *FlowStep) []*FlowStep {
	var out []*FlowStep
	var walk func(s *FlowStep)
	walk = func(s *FlowStep) {
		if s.File != "" && s.repeatTarget == nil {
			out = append(out, s)
		}
		for _, c := range s.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// Processes returns execution flows from the project's entrypoints: for each
// feature with a resolved handler (CLI command, HTTP route, MCP/RPC tool, page,
// program) the flow call tree, flattened into ordered steps. It is computed
// on demand from the stored graph — nothing is persisted.
func (svc *Service) Processes(cwd string, opts ProcessesOptions) (*ProcessesReport, error) {
	opts, err := normalizeProcessesOptions(opts)
	if err != nil {
		return nil, err
	}
	rep := &ProcessesReport{
		SchemaVersion: ProcessesSchemaVersion, Processes: []Process{},
		Depth: opts.Depth, MaxSteps: opts.MaxSteps, CallGraph: CallGraphNone,
		Notes: []string{}, PartialErrors: []string{},
	}
	e, name, indexed, err := svc.newProcessEngine(cwd, opts.Kinds, opts.Depth, opts.MaxSteps)
	rep.Project, rep.Indexed = name, indexed
	if err != nil {
		return nil, err
	}
	if e == nil {
		rep.Indexed = indexed
		return rep, nil
	}
	rep.Notes = append(rep.Notes, e.notes...)
	rep.PartialErrors = append(rep.PartialErrors, e.errs...)
	if e.skipped > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d entrypoint(s) without a resolved callable handler were skipped", e.skipped))
	}

	cands := e.entries
	rep.ProcessesTotal = len(cands)
	var terms []string
	if opts.Query != "" {
		terms = graph.LexicalTerms(opts.Query)
		if len(terms) == 0 {
			terms = []string{strings.ToLower(opts.Query)}
		}
		if len(cands) > processEvalCap {
			cands = cands[:processEvalCap]
			rep.Notes = append(rep.Notes, fmt.Sprintf("--query evaluated the first %d of %d entrypoints; narrow with --kind to inspect the rest", processEvalCap, len(e.entries)))
		}
	} else if len(cands) > opts.Top {
		cands = cands[:opts.Top]
		rep.Truncated = true
	}

	type ranked struct {
		p     *Process
		match int // distinct query terms found anywhere in the process
		name  int // ... found in its name
	}
	var built []ranked
	var graphNodes []graph.Node
	for _, f := range cands {
		p, _ := e.build(f, opts.Depth, opts.MaxSteps)
		r := ranked{p: p}
		if len(terms) > 0 {
			r.match, r.name = matchProcess(p, terms)
			if r.match == 0 {
				continue
			}
		}
		built = append(built, r)
		graphNodes = append(graphNodes, e.builder.emittedNodes...)
	}
	if len(terms) > 0 {
		sort.SliceStable(built, func(i, j int) bool {
			if built[i].match != built[j].match {
				return built[i].match > built[j].match
			}
			return built[i].name > built[j].name
		})
		rep.ProcessesTotal = len(built)
		if len(built) > opts.Top {
			built = built[:opts.Top]
			rep.Truncated = true
		}
	}
	for _, r := range built {
		rep.Processes = append(rep.Processes, *r.p)
	}

	rep.CallGraph = callGraphEnum(e.resolved, callableNodes(graphNodes))
	rep.Resolution = processesResolution(rep.CallGraph, graphNodes)

	if st, sErr := svc.Staleness(cwd); sErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("staleness: %v", sErr))
	} else if st != nil {
		rep.Stale = st.Any()
	}
	return rep, nil
}

func processesResolution(callGraph string, nodes []graph.Node) string {
	switch callGraph {
	case CallGraphNone:
		if len(nodes) == 0 {
			return ""
		}
		return "no call graph for the handlers' languages; processes hold only their entry steps"
	case CallGraphUnresolved:
		lang := ""
		for _, n := range nodes {
			if noNameBasedCallLang(n.Language) {
				lang = n.Language
				break
			}
		}
		head, _ := callGraphGap(lang)
		return head + " — processes are incomplete; run 'codemap index --precise'"
	case CallGraphName:
		return "name-based call graph: processes follow name-matched calls, with same-name callees collapsed by call-site syntax and proximity; ambiguous ones are not listed"
	}
	return ""
}

// matchProcess counts how many distinct query terms occur in a process (name,
// kind, entry, and every step's symbol/FQN/file) and how many in its name.
func matchProcess(p *Process, terms []string) (match, inName int) {
	name := strings.ToLower(p.ID + "\n" + p.Name)
	var all strings.Builder
	all.WriteString(name)
	for _, s := range p.Steps {
		all.WriteString("\n" + s.Symbol + "\n" + s.FQN + "\n" + s.File)
	}
	hay := strings.ToLower(all.String())
	for _, t := range terms {
		if strings.Contains(hay, t) {
			match++
			if strings.Contains(name, t) {
				inName++
			}
		}
	}
	return match, inName
}

// ---- explore: processes grouped around the seeds ----

// ExploreProcess is a process that passes through one or more explore seeds:
// the route → handler → service chain that explains where the seeds sit.
// Steps are the call-order path from the entry to each matched seed (capped,
// seeds kept), not the process's whole tree — use 'codemap processes' for that.
type ExploreProcess struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	Name         string          `json:"name"`
	Entry        *SymbolSelector `json:"entry"`
	MatchedSeeds []string        `json:"matched_seeds"`
	Steps        []ProcessStep   `json:"steps"`
	CallGraph    string          `json:"call_graph"`
}

func selectorKey(file string, line int, fqn, kind string) string {
	return graph.CanonicalStructuralPath(file) + "\x00" + fmt.Sprint(line) + "\x00" + fqn + "\x00" + kind
}

// exploreProcesses finds the processes whose flow contains a joined seed. The
// call graph is walked backwards from the seeds first (cheap, no file reads) so
// flows are built only for entrypoints that can reach one within the process
// depth. A project without entrypoints costs one registration scan and yields
// nothing.
func (svc *Service) exploreProcesses(ctx context.Context, cwd string, seeds []ExploreSeed, limit int) ([]ExploreProcess, error) {
	var joined []ExploreSeed
	for _, s := range seeds {
		if s.Selector != nil {
			joined = append(joined, s)
		}
	}
	if len(joined) == 0 || limit <= 0 {
		return nil, nil
	}
	e, _, _, err := svc.newProcessEngine(cwd, nil, DefaultProcessDepth, DefaultProcessSteps)
	if err != nil || e == nil || len(e.entries) == 0 {
		return nil, err
	}

	byKey := make(map[string]int64, len(e.scan.nodes))
	for i := range e.scan.nodes {
		n := &e.scan.nodes[i]
		byKey[selectorKey(n.FilePath, n.StartLine, n.FQN, n.Kind)] = n.ID
	}
	seedID := map[int64]int{} // node id -> best (lowest) seed rank
	seedName := map[int64]string{}
	for rank, s := range joined {
		sel := s.Selector
		id, ok := byKey[selectorKey(sel.File, sel.StartLine, sel.FQN, sel.Kind)]
		if !ok {
			continue
		}
		if _, dup := seedID[id]; !dup {
			seedID[id] = rank
			seedName[id] = firstNonEmpty(sel.FQN, s.Symbol)
		}
	}
	if len(seedID) == 0 {
		return nil, nil
	}

	// Reverse BFS from the seeds over calls and value references, bounded by
	// the process depth: dist[n] is how many hops n is from its nearest seed.
	rev := map[int64][]int64{}
	for src, es := range e.builder.out {
		for _, ed := range es {
			rev[ed.TargetID] = append(rev[ed.TargetID], src)
		}
	}
	dist := map[int64]int{}
	frontier := make([]int64, 0, len(seedID))
	for id := range seedID {
		dist[id] = 0
		frontier = append(frontier, id)
	}
	for d := 1; d <= DefaultProcessDepth && len(frontier) > 0; d++ {
		var next []int64
		for _, id := range frontier {
			for _, src := range rev[id] {
				if _, seen := dist[src]; !seen {
					dist[src] = d
					next = append(next, src)
				}
			}
		}
		frontier = next
	}

	type cand struct {
		f    scoredFeature
		dist int
		idx  int
	}
	var cands []cand
	for i, f := range e.entries {
		if d, ok := dist[f.handlerNodeID]; ok {
			cands = append(cands, cand{f: f, dist: d, idx: i})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].idx < cands[j].idx
	})
	if len(cands) > exploreProcessCandidates {
		cands = cands[:exploreProcessCandidates]
	}

	type scored struct {
		ep      ExploreProcess
		matched int
		best    int
	}
	var found []scored
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, root := e.build(c.f, DefaultProcessDepth, DefaultProcessSteps)
		steps := processSteps(root)
		parent := map[*FlowStep]*FlowStep{}
		var link func(s *FlowStep)
		link = func(s *FlowStep) {
			for _, ch := range s.Children {
				parent[ch] = s
				link(ch)
			}
		}
		link(root)

		matchedIDs := map[int64]bool{}
		var seedSteps []*FlowStep
		for _, s := range steps {
			if _, ok := seedID[s.node.ID]; ok {
				seedSteps = append(seedSteps, s)
				matchedIDs[s.node.ID] = true
			}
		}
		if len(seedSteps) == 0 {
			continue
		}
		onPath := map[*FlowStep]bool{}
		for _, s := range seedSteps {
			for cur := s; cur != nil; cur = parent[cur] {
				onPath[cur] = true
			}
		}
		keep := selectExploreSteps(steps, onPath, seedSteps, root, exploreProcessSteps)

		var matchedNames []string
		best := len(joined)
		for id := range matchedIDs {
			if r := seedID[id]; r < best {
				best = r
			}
		}
		for _, s := range seedSteps {
			matchedNames = append(matchedNames, seedName[s.node.ID])
		}
		ep := ExploreProcess{
			ID: p.ID, Kind: p.Kind, Name: p.Name, Entry: p.Entry,
			MatchedSeeds: matchedNames, Steps: []ProcessStep{}, CallGraph: p.CallGraph,
		}
		for _, s := range keep {
			ep.Steps = append(ep.Steps, processStepOf(s))
		}
		found = append(found, scored{ep: ep, matched: len(seedSteps), best: best})
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].matched != found[j].matched {
			return found[i].matched > found[j].matched
		}
		return found[i].best < found[j].best
	})
	if len(found) > limit {
		found = found[:limit]
	}
	out := make([]ExploreProcess, 0, len(found))
	for _, f := range found {
		out = append(out, f.ep)
	}
	return out, nil
}

// selectExploreSteps returns the on-path steps in call order, at most max. When
// the path is longer than max the entry and the seed steps win, then the
// remaining path steps fill in call order.
func selectExploreSteps(steps []*FlowStep, onPath map[*FlowStep]bool, seeds []*FlowStep, root *FlowStep, max int) []*FlowStep {
	var path []*FlowStep
	for _, s := range steps {
		if onPath[s] {
			path = append(path, s)
		}
	}
	if len(path) <= max {
		return path
	}
	chosen := map[*FlowStep]bool{}
	add := func(s *FlowStep) {
		if len(chosen) < max {
			chosen[s] = true
		}
	}
	add(root)
	for _, s := range seeds {
		add(s)
	}
	for _, s := range path {
		add(s)
	}
	out := make([]*FlowStep, 0, max)
	for _, s := range path {
		if chosen[s] {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
