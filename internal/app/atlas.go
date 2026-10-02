package app

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// atlasCodeNotFound is the stable machine code for an unknown --prefix; the CLI
// maps "not_found" to exit code 2.
const atlasCodeNotFound = "not_found"

const (
	DefaultAtlasDepth      = 2
	MaxAtlasDepth          = 8
	DefaultAtlasMaxNodes   = 1500
	MaxAtlasMaxNodes       = 20000
	DefaultAtlasKeySymbols = 5
	MaxAtlasKeySymbols     = 20
	atlasMaxNeighbors      = 3
)

// AtlasOptions bounds the atlas tree. Zero values use the defaults above.
type AtlasOptions struct {
	Prefix     string // project-relative directory to zoom into ("" = project root)
	Depth      int    // directory levels below the prefix to expand
	Files      bool   // include file leaves
	MaxNodes   int    // total tree nodes emitted
	KeySymbols int    // key symbols per directory/file
}

// AtlasTotals aggregates the subtree rooted at the requested prefix. Languages
// count every indexed node (files included, like codemap map); Kinds count
// symbols only (the file kind is the Files total).
type AtlasTotals struct {
	Dirs      int            `json:"dirs"`
	Files     int            `json:"files"`
	Symbols   int            `json:"symbols"`
	Lines     int            `json:"lines"`
	Tests     int            `json:"tests"`
	Languages map[string]int `json:"languages"`
	Kinds     map[string]int `json:"kinds"`
}

// AtlasKeySymbol is one load-bearing symbol of a directory or file.
type AtlasKeySymbol struct {
	Symbol     string          `json:"symbol"`
	FQN        string          `json:"fqn,omitempty"`
	Kind       string          `json:"kind"`
	File       string          `json:"file"`
	StartLine  int             `json:"start_line"`
	InDegree   int             `json:"in_degree"`
	SharedName int             `json:"shared_name,omitempty"` // >1: name-based callers are split across this many same-named definitions
	Doc        string          `json:"doc,omitempty"`
	Selector   *SymbolSelector `json:"selector"`
}

// AtlasNeighbor is an aggregated relationship to another part of the project.
type AtlasNeighbor struct {
	Path  string `json:"path"`
	Edges int    `json:"edges"`
}

// AtlasNeighbors lists the strongest inbound/outbound counterparts of a directory.
type AtlasNeighbors struct {
	In  []AtlasNeighbor `json:"in"`
	Out []AtlasNeighbor `json:"out"`
}

// AtlasNode is one directory or file of the atlas tree.
type AtlasNode struct {
	Path          string           `json:"path"`
	Name          string           `json:"name"`
	Type          string           `json:"type"` // dir | file
	Language      string           `json:"language,omitempty"`
	Files         int              `json:"files"`
	Symbols       int              `json:"symbols"`
	Lines         int              `json:"lines"`
	Tests         int              `json:"tests"`
	TestFiles     int              `json:"test_files"`
	Languages     map[string]int   `json:"languages"`
	Kinds         map[string]int   `json:"kinds"`
	Roles         []string         `json:"roles"`
	Summary       string           `json:"summary"`
	SummarySource string           `json:"summary_source"`
	Inbound       int              `json:"inbound"`
	Outbound      int              `json:"outbound"`
	Internal      int              `json:"internal"`
	Entrypoints   int              `json:"entrypoints"`
	KeySymbols    []AtlasKeySymbol `json:"key_symbols"`
	TopNeighbors  *AtlasNeighbors  `json:"top_neighbors,omitempty"`
	Children      []*AtlasNode     `json:"children"`
	ChildrenTotal int              `json:"children_total"`
	ChildrenTrunc bool             `json:"children_truncated"`
	CollapsedDirs int              `json:"collapsed_dirs,omitempty"` // sub-directories not emitted because of --depth
}

// AtlasReport is the repo as a navigable, described tree: directories and
// files with metrics, roles, plain-language summaries and key symbols.
type AtlasReport struct {
	SchemaVersion int         `json:"schema_version"`
	Project       string      `json:"project"`
	Root          string      `json:"root"`
	Indexed       bool        `json:"indexed"`
	Prefix        string      `json:"prefix"`
	Depth         int         `json:"depth"`
	FilesIncluded bool        `json:"files_included"`
	Summary       string      `json:"summary"`
	SummarySource string      `json:"summary_source"`
	Totals        AtlasTotals `json:"totals"`
	Tree          *AtlasNode  `json:"tree"`
	NodesEmitted  int         `json:"nodes_emitted"`
	Truncated     bool        `json:"truncated"`
	CallGraph     string      `json:"call_graph"`
	Resolution    string      `json:"resolution,omitempty"`
	Stale         bool        `json:"stale"`
	PartialErrors []string    `json:"partial_errors"`
}

// atlasAgg is one directory or file of the in-memory project tree with its
// subtree aggregates. The whole graph is folded into these in one pass.
type atlasAgg struct {
	path     string
	name     string
	isDir    bool
	parent   *atlasAgg
	language string // files only

	dirs   []*atlasAgg
	files  []*atlasAgg
	byName map[string]*atlasAgg

	nFiles, symbols, lines, tests, testFiles, entrypoints int
	codeFiles                                             int // non-test files in a code language
	languages, kinds, fileLangs                           map[string]int

	inbound, outbound, internal int
	nbIn, nbOut                 map[string]int

	chain   []*atlasAgg // root..self, files only (edge pass)
	cands   []atlasCand // file: ranked key-symbol candidates (top KeySymbols)
	hasMain bool
}

type atlasCand struct {
	node     graph.Node
	score    float64
	raw      int
	shared   int
	hasDoc   bool
	exported bool
}

func atlasNewAgg(p, name string, isDir bool, parent *atlasAgg) *atlasAgg {
	a := &atlasAgg{path: p, name: name, isDir: isDir, parent: parent,
		languages: map[string]int{}, kinds: map[string]int{}, fileLangs: map[string]int{}}
	if isDir {
		a.byName = map[string]*atlasAgg{}
	}
	return a
}

// Atlas returns the project as a bounded, deterministic, described directory
// tree with per-node metrics, roles, summaries and key symbols.
func (svc *Service) Atlas(cwd string, opts AtlasOptions) (*AtlasReport, error) {
	opts, err := normalizeAtlasOptions(opts)
	if err != nil {
		return nil, err
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &AtlasReport{
		SchemaVersion: 1, Project: name, Indexed: found, Prefix: opts.Prefix, Depth: opts.Depth,
		FilesIncluded: opts.Files, CallGraph: CallGraphNone, PartialErrors: []string{},
		Totals: AtlasTotals{Languages: map[string]int{}, Kinds: map[string]int{}},
	}
	if !found {
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	proj, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}
	rep.Root = proj.Path

	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		return nil, err
	}

	root, nodeFile := atlasBuildTree(name, nodes)
	atlasAggregateEdges(root, nodeFile, nodes, edges)
	atlasRankSymbols(nodes, edges, nodeFile, opts.KeySymbols)

	target := atlasFind(root, opts.Prefix)
	if target == nil {
		return nil, coded(atlasCodeNotFound, "run: codemap atlas (no --prefix) to list the top-level directories",
			fmt.Errorf("atlas prefix %q is not a directory in the index", opts.Prefix))
	}

	sum := newSummarizer(proj.Path)
	em := &atlasEmitter{opts: opts, sum: sum, root: root, projectName: name}
	rep.Tree = em.emit(target)
	rep.NodesEmitted = em.count
	rep.Truncated = em.truncated
	atlasFillTotals(&rep.Totals, target)

	// Project summary: root README, else root-dir rules (always about the project root).
	rs := atlasNodeSummary(sum, root)
	rep.Summary, rep.SummarySource = rs.text, rs.source

	callables, cErr := g.CallableFileLangs(pid)
	if cErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, "call_graph: "+cErr.Error())
	} else {
		resolvedFiles, _ := g.CallGraphResolvedFiles(pid)
		rep.CallGraph = callGraphEnum(resolvedFiles, callables)
		if lang, unavailable := callGraphUnavailableResolved(resolvedFiles, callables); unavailable {
			head, _ := callGraphGap(lang)
			rep.Resolution = head + " — key symbols lean on exported/documented symbols; run 'codemap index --precise'" + svc.coverageHintResolved(g, pid, resolvedFiles)
		}
	}
	if stale, sErr := svc.Staleness(cwd); sErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("staleness: %v", sErr))
	} else if stale != nil {
		rep.Stale = stale.Any()
	}
	rep.PartialErrors = append(rep.PartialErrors, sum.errors...)
	if len(rep.PartialErrors) > 20 {
		rep.PartialErrors = append(rep.PartialErrors[:20], fmt.Sprintf("… %d more", len(rep.PartialErrors)-20))
	}
	return rep, nil
}

func normalizeAtlasOptions(opts AtlasOptions) (AtlasOptions, error) {
	bad := func(msg string) error {
		return coded(CodeInvalidInput, "adjust the flag and retry", fmt.Errorf("%s", msg))
	}
	limits := []struct {
		name         string
		value        *int
		defaultValue int
		maxValue     int
	}{
		{"depth", &opts.Depth, DefaultAtlasDepth, MaxAtlasDepth},
		{"max nodes", &opts.MaxNodes, DefaultAtlasMaxNodes, MaxAtlasMaxNodes},
		{"key symbols", &opts.KeySymbols, DefaultAtlasKeySymbols, MaxAtlasKeySymbols},
	}
	for _, l := range limits {
		if *l.value == 0 {
			*l.value = l.defaultValue
		}
		if *l.value < 1 || *l.value > l.maxValue {
			return opts, bad(fmt.Sprintf("atlas %s must be between 1 and %d", l.name, l.maxValue))
		}
	}
	p := strings.ReplaceAll(strings.TrimSpace(opts.Prefix), `\`, "/")
	p = strings.Trim(path.Clean("/"+p), "/")
	if strings.Contains(opts.Prefix, "..") {
		for _, seg := range strings.Split(strings.ReplaceAll(opts.Prefix, `\`, "/"), "/") {
			if seg == ".." {
				return opts, bad("atlas prefix must stay inside the project")
			}
		}
	}
	opts.Prefix = p
	return opts, nil
}

// atlasBuildTree folds the node list into the directory/file tree and the
// per-file counters, then propagates them to every ancestor.
func atlasBuildTree(projectName string, nodes []graph.Node) (*atlasAgg, map[int64]*atlasAgg) {
	root := atlasNewAgg("", projectName, true, nil)
	nodeFile := make(map[int64]*atlasAgg, len(nodes))
	fileByPath := map[string]*atlasAgg{}

	fileOf := func(p string) *atlasAgg {
		if f, ok := fileByPath[p]; ok {
			return f
		}
		cur := root
		parts := strings.Split(p, "/")
		for i, seg := range parts[:len(parts)-1] {
			child, ok := cur.byName[seg]
			if !ok {
				child = atlasNewAgg(strings.Join(parts[:i+1], "/"), seg, true, cur)
				cur.byName[seg] = child
				cur.dirs = append(cur.dirs, child)
			}
			cur = child
		}
		base := parts[len(parts)-1]
		f := atlasNewAgg(p, base, false, cur)
		cur.byName[base] = f
		cur.files = append(cur.files, f)
		fileByPath[p] = f
		return f
	}

	for _, n := range nodes {
		p := graph.CanonicalStructuralPath(n.FilePath)
		p = strings.TrimPrefix(path.Clean(p), "./")
		if p == "" || p == "." {
			continue
		}
		f := fileOf(p)
		nodeFile[n.ID] = f
		if n.Language != "" {
			f.languages[n.Language]++
			if f.language == "" || n.Kind == graph.KindFile {
				f.language = n.Language
			}
		}
		if n.Kind == graph.KindFile {
			if n.EndLine > f.lines {
				f.lines = n.EndLine
			}
			continue
		}
		f.symbols++
		f.kinds[n.Kind]++
		if n.Kind == graph.KindTest {
			f.tests++
		}
		if n.Kind == graph.KindFunction && n.Symbol == "main" && !atlasIsTestFile(p) {
			f.entrypoints++
			f.hasMain = true
		}
	}

	// Propagate file counters to all ancestor directories.
	for _, f := range fileByPath {
		f.nFiles = 1
		isTest := atlasIsTestFile(f.path)
		if isTest {
			f.testFiles = 1
		}
		code := atlasIsCodeLang(f.language) && !isTest
		if code {
			f.codeFiles = 1
		}
		f.fileLangs[f.language]++
		for d := f.parent; d != nil; d = d.parent {
			d.nFiles++
			d.symbols += f.symbols
			d.lines += f.lines
			d.tests += f.tests
			d.testFiles += f.testFiles
			d.codeFiles += f.codeFiles
			d.entrypoints += f.entrypoints
			if f.hasMain {
				d.hasMain = true
			}
			for k, v := range f.languages {
				d.languages[k] += v
			}
			for k, v := range f.kinds {
				d.kinds[k] += v
			}
			d.fileLangs[f.language]++
		}
	}
	var sortKids func(a *atlasAgg)
	sortKids = func(a *atlasAgg) {
		atlasSortAggs(a.dirs)
		atlasSortAggs(a.files)
		for _, d := range a.dirs {
			sortKids(d)
		}
	}
	sortKids(root)
	return root, nodeFile
}

func atlasSortAggs(s []*atlasAgg) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].symbols != s[j].symbols {
			return s[i].symbols > s[j].symbols
		}
		return s[i].name < s[j].name
	})
}

func atlasFind(root *atlasAgg, prefix string) *atlasAgg {
	if prefix == "" {
		return root
	}
	cur := root
	for _, seg := range strings.Split(prefix, "/") {
		next, ok := cur.byName[seg]
		if !ok || !next.isDir {
			return nil
		}
		cur = next
	}
	return cur
}

// atlasAggregateEdges classifies every non-defines edge as internal/inbound/
// outbound for every ancestor of its endpoints (including the file nodes) using
// the common-ancestor rule, and accumulates per-directory neighbour counts.
func atlasAggregateEdges(root *atlasAgg, nodeFile map[int64]*atlasAgg, nodes []graph.Node, edges []graph.Edge) {
	chainOf := func(f *atlasAgg) []*atlasAgg {
		if f.chain != nil {
			return f.chain
		}
		var rev []*atlasAgg
		for a := f; a != nil; a = a.parent {
			rev = append(rev, a)
		}
		c := make([]*atlasAgg, len(rev))
		for i := range rev {
			c[len(rev)-1-i] = rev[i]
		}
		f.chain = c
		return c
	}
	subOf := map[*atlasAgg]string{}
	label := func(f *atlasAgg) string {
		if s, ok := subOf[f]; ok {
			return s
		}
		s := graph.SubsystemOf(f.path)
		subOf[f] = s
		return s
	}
	for _, e := range edges {
		if e.EdgeType == graph.EdgeDefines {
			continue
		}
		sf, ok1 := nodeFile[e.SourceID]
		tf, ok2 := nodeFile[e.TargetID]
		if !ok1 || !ok2 {
			continue
		}
		sc, tc := chainOf(sf), chainOf(tf)
		common := 0
		for common < len(sc) && common < len(tc) && sc[common] == tc[common] {
			common++
		}
		for i := 0; i < common; i++ {
			sc[i].internal++
		}
		for i := common; i < len(sc); i++ {
			sc[i].outbound++
			if sc[i].isDir {
				atlasBump(&sc[i].nbOut, atlasNeighborLabel(sc[i], tf, tc, common, label))
			}
		}
		for i := common; i < len(tc); i++ {
			tc[i].inbound++
			if tc[i].isDir {
				atlasBump(&tc[i].nbIn, atlasNeighborLabel(tc[i], sf, sc, common, label))
			}
		}
	}
	_ = root
	_ = nodes
}

func atlasBump(m *map[string]int, key string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}

// atlasNeighborLabel names the far end of an edge relative to dir x. It groups by
// the far file's codemap subsystem, except when that subsystem contains x itself
// (a sibling inside the same subsystem), where it names the branch below the
// common ancestor instead.
func atlasNeighborLabel(x, far *atlasAgg, farChain []*atlasAgg, common int, label func(*atlasAgg) string) string {
	l := label(far)
	if l == x.path || strings.HasPrefix(x.path, l+"/") || l == "(root)" && x.parent == nil {
		if common < len(farChain) {
			return farChain[common].path
		}
	}
	return l
}

// atlasRankSymbols scores every key-symbol candidate and stores each file's top
// candidates on the file aggregate.
func atlasRankSymbols(nodes []graph.Node, edges []graph.Edge, nodeFile map[int64]*atlasAgg, limit int) {
	byID := make(map[int64]*graph.Node, len(nodes))
	defCount := map[string]int{}
	for i := range nodes {
		n := &nodes[i]
		byID[n.ID] = n
		if n.Kind != graph.KindFile {
			defCount[n.Symbol]++
		}
	}
	type acc struct {
		score  float64
		raw    int
		shared int
	}
	accs := map[int64]*acc{}
	for _, e := range edges {
		if e.EdgeType != graph.EdgeCalls && e.EdgeType != graph.EdgeReferences {
			continue
		}
		if e.SourceID == e.TargetID {
			continue
		}
		src, tgt := byID[e.SourceID], byID[e.TargetID]
		if src == nil || tgt == nil || !atlasCandidateKind(tgt.Kind) {
			continue
		}
		if src.Kind == graph.KindTest || atlasIsTestFile(nodeFile[src.ID].path) {
			continue
		}
		w := 1.0
		shared := 0
		if e.Provenance == graph.ProvName || e.Provenance == "" {
			if d := defCount[tgt.Symbol]; d > 1 {
				w = 1 / float64(d)
				shared = d
			}
		}
		if e.EdgeType == graph.EdgeReferences {
			w *= 0.5
		}
		a := accs[tgt.ID]
		if a == nil {
			a = &acc{}
			accs[tgt.ID] = a
		}
		a.score += w
		if e.EdgeType == graph.EdgeCalls {
			a.raw++
		}
		if shared > a.shared {
			a.shared = shared
		}
	}

	// Members' scores credit their owning type (Go receivers / class methods) so
	// central types are not outranked by their own busiest method alone.
	member := map[string]float64{}
	memberRaw := map[string]int{}
	for i := range nodes {
		n := &nodes[i]
		if n.Kind != graph.KindMethod {
			continue
		}
		a := accs[n.ID]
		if a == nil {
			continue
		}
		if dot := strings.LastIndex(n.FQN, "."); dot > 0 {
			key := path.Dir(nodeFile[n.ID].path) + "\x00" + n.FQN[:dot]
			member[key] += a.score
			memberRaw[key] += a.raw
		}
	}

	perFile := map[*atlasAgg][]atlasCand{}
	for i := range nodes {
		n := nodes[i]
		if !atlasCandidateKind(n.Kind) {
			continue
		}
		f := nodeFile[n.ID]
		if f == nil || atlasIsTestFile(f.path) {
			continue
		}
		c := atlasCand{node: n, hasDoc: strings.TrimSpace(n.Docstring) != "", exported: isExportedName(n.Symbol)}
		if a := accs[n.ID]; a != nil {
			c.score, c.raw, c.shared = a.score, a.raw, a.shared
		}
		if n.Kind == graph.KindType || n.Kind == graph.KindClass {
			key := path.Dir(f.path) + "\x00" + n.FQN
			c.score += 0.3 * member[key]
			c.raw = memberRaw[key] // a type is "called" through its methods
		}
		// Unexported helpers (result, cwdOf, clamp, now…) are called everywhere but
		// rarely explain a package; damp them unless they carry a doc comment.
		if !c.exported {
			if c.hasDoc {
				c.score *= 0.8
			} else {
				c.score *= 0.4
			}
		}
		if n.Kind == graph.KindFunction && n.Symbol == "main" {
			c.score += 0.5
		}
		if c.score <= 0 && !c.exported && !c.hasDoc {
			continue
		}
		perFile[f] = append(perFile[f], c)
	}
	for f, cs := range perFile {
		sort.Slice(cs, func(i, j int) bool { return atlasCandLess(cs[i], cs[j]) })
		if len(cs) > limit {
			cs = cs[:limit]
		}
		f.cands = cs
	}
}

func atlasCandidateKind(k string) bool {
	switch k {
	case graph.KindFunction, graph.KindMethod, graph.KindType, graph.KindClass:
		return true
	}
	return false
}

func atlasCandLess(a, b atlasCand) bool {
	if math.Abs(a.score-b.score) > 1e-9 {
		return a.score > b.score
	}
	if a.hasDoc != b.hasDoc {
		return a.hasDoc
	}
	if a.exported != b.exported {
		return a.exported
	}
	if a.node.FQN != b.node.FQN {
		return a.node.FQN < b.node.FQN
	}
	if a.node.FilePath != b.node.FilePath {
		return a.node.FilePath < b.node.FilePath
	}
	return a.node.StartLine < b.node.StartLine
}

// atlasEmitter walks the aggregate tree breadth-first, converting aggregates to
// AtlasNodes under the --depth and --max-nodes budgets.
type atlasEmitter struct {
	opts        AtlasOptions
	sum         *summarizer
	root        *atlasAgg
	projectName string
	count       int
	truncated   bool
}

type atlasQueued struct {
	agg   *atlasAgg
	node  *AtlasNode
	level int
}

func (em *atlasEmitter) emit(target *atlasAgg) *AtlasNode {
	rootNode := em.newNode(target)
	em.count = 1
	queue := []atlasQueued{{target, rootNode, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		var kids []*atlasAgg
		if cur.level < em.opts.Depth {
			kids = append(kids, cur.agg.dirs...)
		} else {
			cur.node.CollapsedDirs = len(cur.agg.dirs)
		}
		if em.opts.Files {
			kids = append(kids, cur.agg.files...)
		}
		cur.node.ChildrenTotal = len(kids)
		if cur.level >= em.opts.Depth {
			cur.node.ChildrenTotal = len(kids) // files only; dirs are reported via collapsed_dirs
		}
		budget := em.opts.MaxNodes - em.count
		if budget < 0 {
			budget = 0
		}
		chosen := kids
		if len(kids) > budget {
			chosen = atlasLargest(kids, budget)
			cur.node.ChildrenTrunc = true
			cur.node.ChildrenTotal = len(kids)
			em.truncated = true
		}
		// Standard order: dirs first, then files, each by symbols desc then name.
		sort.SliceStable(chosen, func(i, j int) bool {
			if chosen[i].isDir != chosen[j].isDir {
				return chosen[i].isDir
			}
			if chosen[i].symbols != chosen[j].symbols {
				return chosen[i].symbols > chosen[j].symbols
			}
			return chosen[i].name < chosen[j].name
		})
		for _, k := range chosen {
			child := em.newNode(k)
			cur.node.Children = append(cur.node.Children, child)
			em.count++
			if k.isDir {
				queue = append(queue, atlasQueued{k, child, cur.level + 1})
			}
		}
	}
	return rootNode
}

// atlasLargest keeps the n largest aggregates (symbols desc, then name).
func atlasLargest(kids []*atlasAgg, n int) []*atlasAgg {
	c := append([]*atlasAgg(nil), kids...)
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].symbols != c[j].symbols {
			return c[i].symbols > c[j].symbols
		}
		if c[i].name != c[j].name {
			return c[i].name < c[j].name
		}
		return c[i].isDir && !c[j].isDir
	})
	return c[:n]
}

func (em *atlasEmitter) newNode(a *atlasAgg) *AtlasNode {
	n := &AtlasNode{
		Path: a.path, Name: a.name, Type: "file", Language: a.language,
		Files: a.nFiles, Symbols: a.symbols, Lines: a.lines, Tests: a.tests, TestFiles: a.testFiles,
		Languages: atlasCopy(a.languages), Kinds: atlasCopy(a.kinds),
		Inbound: a.inbound, Outbound: a.outbound, Internal: a.internal, Entrypoints: a.entrypoints,
		Roles: atlasRoles(a), Children: []*AtlasNode{}, KeySymbols: []AtlasKeySymbol{},
	}
	if a.isDir {
		n.Type = "dir"
		n.Language = ""
		if a.parent == nil {
			n.Name = em.projectName
		}
		n.TopNeighbors = &AtlasNeighbors{In: atlasTopNeighbors(a.nbIn), Out: atlasTopNeighbors(a.nbOut)}
	} else {
		n.Files = 1
	}
	n.KeySymbols = atlasKeySymbols(a, em.opts.KeySymbols)
	r := atlasNodeSummary(em.sum, a)
	n.Summary, n.SummarySource = r.text, r.source
	if n.Summary == "" && !a.isDir && !atlasIsTestFile(a.path) {
		// A file with no header comment borrows the doc sentence of its leading
		// exported, documented symbol, labelled "symbol doc" so a UI never presents
		// it as a file comment (the sentence names the symbol it describes).
		for _, ks := range n.KeySymbols {
			if ks.Doc != "" && isExportedName(ks.Symbol) {
				n.Summary, n.SummarySource = ks.Doc, "symbol doc"
				break
			}
		}
	}
	return n
}

func atlasNodeSummary(s *summarizer, a *atlasAgg) summaryResult {
	if !a.isDir {
		return s.fileSummary(a.path, a.language)
	}
	var goFiles []string
	for _, f := range a.files {
		if strings.HasSuffix(f.path, ".go") {
			goFiles = append(goFiles, f.path)
		}
	}
	if r := s.dirSummary(a.path, goFiles); r.text != "" {
		return r
	}
	// A pure wrapper directory (cmd/ holding only cmd/codemap/) says what its
	// single sub-directory says; summary_source names where the text lives.
	if len(a.files) == 0 && len(a.dirs) == 1 {
		if r := atlasNodeSummary(s, a.dirs[0]); r.text != "" {
			return summaryResult{text: r.text, source: summarySubdir + " " + a.dirs[0].name + "/"}
		}
	}
	return summaryResult{}
}

func atlasCopy(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func atlasTopNeighbors(m map[string]int) []AtlasNeighbor {
	out := make([]AtlasNeighbor, 0, len(m))
	for p, c := range m {
		out = append(out, AtlasNeighbor{Path: p, Edges: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Edges != out[j].Edges {
			return out[i].Edges > out[j].Edges
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > atlasMaxNeighbors {
		out = out[:atlasMaxNeighbors]
	}
	return out
}

// atlasKeySymbols merges the per-file candidates under a node into its top N.
func atlasKeySymbols(a *atlasAgg, limit int) []AtlasKeySymbol {
	var cands []atlasCand
	var walk func(x *atlasAgg)
	walk = func(x *atlasAgg) {
		cands = append(cands, x.cands...)
		for _, d := range x.dirs {
			walk(d)
		}
		for _, f := range x.files {
			cands = append(cands, f.cands...)
		}
	}
	if a.isDir {
		walk(a)
	} else {
		cands = a.cands
	}
	sort.Slice(cands, func(i, j int) bool { return atlasCandLess(cands[i], cands[j]) })
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]AtlasKeySymbol, 0, len(cands))
	for _, c := range cands {
		n := c.node
		out = append(out, AtlasKeySymbol{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: graph.CanonicalStructuralPath(n.FilePath),
			StartLine: n.StartLine, InDegree: c.raw, SharedName: c.shared,
			Doc: firstSentence(n.Docstring, atlasDocSentence), Selector: selectorForNode(n),
		})
	}
	return out
}

func atlasFillTotals(t *AtlasTotals, a *atlasAgg) {
	var countDirs func(x *atlasAgg) int
	countDirs = func(x *atlasAgg) int {
		n := len(x.dirs)
		for _, d := range x.dirs {
			n += countDirs(d)
		}
		return n
	}
	t.Dirs = countDirs(a)
	t.Files = a.nFiles
	t.Symbols = a.symbols
	t.Lines = a.lines
	t.Tests = a.tests
	t.Languages = atlasCopy(a.languages)
	t.Kinds = atlasCopy(a.kinds)
}

// atlasIsCodeLang reports whether a language is code (as opposed to docs/config/markup).
func atlasIsCodeLang(l string) bool {
	switch l {
	case "", "markdown", "yaml", "json", "toml", "html", "css", "sql":
		return false
	}
	return true
}

// atlasExtraTestDirs are directories the atlas tags as test code for its role
// colouring on top of graph.IsTestPath's test-only directories: spec suites,
// fixtures and end-to-end specs.
var atlasExtraTestDirs = map[string]bool{"spec": true, "specs": true, "fixtures": true, "e2e": true}

// atlasIsTestFile extends the shared test-path heuristic (graph.IsTestPath)
// with the extra test directories above.
func atlasIsTestFile(p string) bool {
	if graph.IsTestPath(p) {
		return true
	}
	for _, seg := range strings.Split(strings.ToLower(path.Dir(p)), "/") {
		if atlasExtraTestDirs[seg] {
			return true
		}
	}
	return false
}

// atlasRoles derives the deterministic role tags of a node.
//
//	tests       ≥60% of files are test files, or a path segment is test|tests|__tests__|spec|specs|testdata|fixtures|e2e
//	docs        markdown/mdx is the majority language, or a segment is docs|doc|documentation
//	config      yaml/json/toml is the majority language, or a segment is .github|.config|deploy|infra
//	entrypoint  contains a main(), or is bin / cmd with code
//	examples    a segment is example(s)|sample(s)|demo
//	bench       a segment is bench|benchmark(s)
//	generated   a segment is gen|generated|dist|build, or ends _pb (conservative)
//	vendor      a segment is vendor|third_party|node_modules
//	source      has at least one non-test code file and none of tests/examples/bench/generated/vendor
//
// A file gets the applicable subset (a _test.go file is "tests").
func atlasRoles(a *atlasAgg) []string {
	segs := strings.Split(strings.ToLower(a.path), "/")
	if a.path == "" {
		segs = nil
	}
	has := func(names ...string) bool {
		for _, s := range segs {
			for _, n := range names {
				if s == n {
					return true
				}
			}
		}
		return false
	}
	majority := func(langs ...string) bool {
		if a.nFiles == 0 && a.isDir {
			return false
		}
		total := a.nFiles
		if !a.isDir {
			total = 1
		}
		sum := 0
		for _, l := range langs {
			sum += a.fileLangs[l]
		}
		return total > 0 && sum*2 > total
	}
	var roles []string
	segTests := has("test", "tests", "__tests__", "spec", "specs", "testdata", "fixtures", "e2e") || (!a.isDir && atlasIsTestFile(a.path))
	tests := segTests || (a.nFiles > 0 && a.testFiles*100 >= a.nFiles*60)
	examples := has("example", "examples", "sample", "samples", "demo")
	bench := has("bench", "benchmark", "benchmarks")
	generated := has("gen", "generated", "dist", "build")
	if !a.isDir {
		base := strings.TrimSuffix(strings.ToLower(a.name), path.Ext(a.name))
		if strings.HasSuffix(base, "_pb") || strings.Contains(strings.ToLower(a.name), ".pb.") || strings.HasSuffix(base, ".gen") || strings.HasSuffix(base, "_gen") {
			generated = true
		}
	}
	vendor := has("vendor", "third_party", "node_modules")
	// A directory that is merely test-heavy (ratio, not a test-named path) still
	// holds real source, so "source" is kept alongside "tests" there.
	if a.codeFiles > 0 && !segTests && !examples && !bench && !generated && !vendor {
		roles = append(roles, "source")
	}
	if tests {
		roles = append(roles, "tests")
	}
	if majority("markdown", "mdx") || has("docs", "doc", "documentation") {
		roles = append(roles, "docs")
	}
	if majority("yaml", "json", "toml") || has(".github", ".config", "deploy", "infra") {
		roles = append(roles, "config")
	}
	if !tests && (a.entrypoints > 0 || (a.codeFiles > 0 && len(segs) > 0 && (segs[0] == "bin" || segs[0] == "cmd"))) {
		roles = append(roles, "entrypoint")
	}
	if examples {
		roles = append(roles, "examples")
	}
	if bench {
		roles = append(roles, "bench")
	}
	if generated {
		roles = append(roles, "generated")
	}
	if vendor {
		roles = append(roles, "vendor")
	}
	if roles == nil {
		roles = []string{}
	}
	return roles
}
