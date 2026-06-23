package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// Service implements codemap's operations over a Session.
type Service struct {
	s *Session
}

// NewService wraps a session.
func NewService(s *Session) *Service { return &Service{s: s} }

// InitReport is returned by Init.
type InitReport struct {
	Project   string `json:"project"`
	Root      string `json:"root"`
	ProjectID int64  `json:"project_id"`
	DataDir   string `json:"data_dir"`
}

// IndexReport is returned by Index.
type IndexReport struct {
	Project      string            `json:"project"`
	Root         string            `json:"root"`
	Embedded     bool              `json:"embedded"`
	Warning      string            `json:"warning,omitempty"`
	FilesScanned int               `json:"files_scanned"`
	FilesIndexed int               `json:"files_indexed"`
	FilesSkipped int               `json:"files_skipped"`
	Nodes        int               `json:"nodes"`
	Edges        int               `json:"edges"`
	Errors       []index.FileError `json:"errors,omitempty"`
}

// StatusReport is returned by Status.
type StatusReport struct {
	Project    string         `json:"project"`
	Root       string         `json:"root"`
	Registered bool           `json:"registered"`
	Path       string         `json:"path,omitempty"`
	Nodes      int            `json:"nodes"`
	Edges      int            `json:"edges"`
	Files      int            `json:"files"`
	Languages  map[string]int `json:"languages,omitempty"`
	Kinds      map[string]int `json:"kinds,omitempty"`
}

// Init registers cwd as a codemap project in the global registry.
func (svc *Service) Init(cwd string, local bool) (*InitReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root := clean(cwd)
	name := config.DeriveProjectName(root)
	pid, err := g.UpsertProject(name, root, detectLanguage(root))
	if err != nil {
		return nil, err
	}
	if local {
		if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
			return nil, err
		}
	}
	return &InitReport{Project: name, Root: root, ProjectID: pid, DataDir: config.DataDir()}, nil
}

// Index indexes the project containing cwd. When embed is true it attempts
// semantic embeddings, falling back to structure-only (with a warning) if the
// embedding provider is unreachable.
func (svc *Service) Index(ctx context.Context, cwd string, opts index.Options, withEmbed bool) (*IndexReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	pid, err := g.UpsertProject(name, root, detectLanguage(root))
	if err != nil {
		return nil, err
	}

	rep := &IndexReport{Project: name, Root: root}
	var vec *vector.Store
	emb := svc.s.Embedder()

	if !withEmbed {
		emb = nil
	} else {
		// Availability is an optional capability; if the provider reports it's
		// unreachable, fall back to structure-only with a warning.
		if c, ok := emb.(interface {
			Available(context.Context) error
		}); ok {
			if availErr := c.Available(ctx); availErr != nil {
				rep.Warning = "embeddings disabled: " + availErr.Error() + " (indexed structure only)"
				emb = nil
			}
		}
		if emb != nil {
			if vec, err = svc.s.Vectors(); err != nil {
				return nil, err
			}
		}
	}

	res, err := index.New(g, vec, emb, svc.s.Config.Index).IndexProject(ctx, pid, name, root, opts)
	if err != nil {
		return rep, err
	}
	rep.Embedded = vec != nil
	rep.FilesScanned = res.FilesScanned
	rep.FilesIndexed = res.FilesIndexed
	rep.FilesSkipped = res.FilesSkipped
	rep.Nodes = res.Nodes
	rep.Edges = res.Edges
	rep.Errors = res.Errors
	return rep, nil
}

// Status reports index statistics for the project containing cwd.
func (svc *Service) Status(cwd string) (*StatusReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &StatusReport{Project: name, Root: root}

	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil // not registered yet
	}
	if err != nil {
		return nil, err
	}
	st, err := g.Stats(p.ID)
	if err != nil {
		return nil, err
	}
	rep.Registered = true
	rep.Path = p.Path
	rep.Nodes = st.Nodes
	rep.Edges = st.Edges
	rep.Files = st.Files
	rep.Languages = st.Languages
	rep.Kinds = st.Kinds
	return rep, nil
}

// SymbolRef is a lightweight reference to a graph node (for query results).
type SymbolRef struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func nodeToRef(n graph.Node) SymbolRef {
	return SymbolRef{Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath, StartLine: n.StartLine, EndLine: n.EndLine}
}

// RelationReport is returned by Callers/Callees.
type RelationReport struct {
	Symbol  string      `json:"symbol"`
	Project string      `json:"project"`
	Results []SymbolRef `json:"results"`
}

// SemanticHit is one semantic-search result.
type SemanticHit struct {
	Symbol    string  `json:"symbol"`
	FQN       string  `json:"fqn,omitempty"`
	Kind      string  `json:"kind"`
	File      string  `json:"file"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
}

// SemanticReport is returned by Semantic.
type SemanticReport struct {
	Query   string        `json:"query"`
	Project string        `json:"project"`
	Hits    []SemanticHit `json:"hits"`
}

// Callers returns the functions/methods that call symbol.
func (svc *Service) Callers(cwd, symbol string) (*RelationReport, error) {
	return svc.relation(cwd, symbol, (*graph.Store).Callers)
}

// Callees returns the functions/methods that symbol calls.
func (svc *Service) Callees(cwd, symbol string) (*RelationReport, error) {
	return svc.relation(cwd, symbol, (*graph.Store).Callees)
}

func (svc *Service) relation(cwd, symbol string, query func(*graph.Store, int64, string) ([]graph.Node, error)) (*RelationReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &RelationReport{Symbol: symbol, Project: name, Results: []SymbolRef{}}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	nodes, err := query(g, p.ID, symbol)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Results = append(rep.Results, nodeToRef(n))
	}
	return rep, nil
}

// PreciseCallers computes exact callers of a Go symbol using gopls callHierarchy
// (no by-name inflation). Go-only for now; errors if gopls is unavailable.
func (svc *Service) PreciseCallers(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	return svc.preciseCallHierarchy(ctx, cwd, symbol, true)
}

// PreciseCallees computes exact callees of a Go symbol using gopls callHierarchy.
func (svc *Service) PreciseCallees(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	return svc.preciseCallHierarchy(ctx, cwd, symbol, false)
}

// preciseCallHierarchy resolves the symbol's location via the graph, then drives
// gopls (documentSymbol → prepareCallHierarchy → in/out calls). incoming=true
// returns callers; false returns callees.
func (svc *Service) preciseCallHierarchy(ctx context.Context, cwd, symbol string, incoming bool) (*RelationReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &RelationReport{Symbol: symbol, Project: name, Results: []SymbolRef{}}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}

	nodes, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, err
	}
	var node *graph.Node
	for i := range nodes {
		if nodes[i].Language == "go" {
			node = &nodes[i]
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("precise queries currently support Go only (no Go symbol named %q)", symbol)
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		return nil, fmt.Errorf("gopls not found on PATH (required for --lsp)")
	}

	root := p.Path
	absFile := filepath.Join(root, node.FilePath)
	src, err := os.ReadFile(absFile)
	if err != nil {
		return nil, err
	}

	cl, err := lsp.Spawn(ctx, "gopls")
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	if err := cl.Initialize(ctx, root); err != nil {
		return nil, err
	}
	uri := lsp.URI(absFile)
	if err := cl.DidOpen(uri, "go", string(src)); err != nil {
		return nil, err
	}
	// callHierarchy needs the whole workspace analyzed; wait for gopls to finish
	// its initial load (or time out and try anyway).
	cl.WaitReady(ctx, 20*time.Second)

	syms, err := cl.DocumentSymbols(ctx, uri)
	if err != nil {
		return nil, err
	}
	pos, ok := findSymbolPos(syms, symbol, node.StartLine)
	if !ok {
		return rep, nil
	}
	items, err := cl.PrepareCallHierarchy(ctx, uri, pos)
	if err != nil || len(items) == 0 {
		return rep, err
	}

	if incoming {
		calls, err := cl.IncomingCalls(ctx, items[0])
		if err != nil {
			return nil, err
		}
		for _, c := range calls {
			rep.Results = append(rep.Results, itemToRef(c.From, root))
		}
	} else {
		calls, err := cl.OutgoingCalls(ctx, items[0])
		if err != nil {
			return nil, err
		}
		for _, c := range calls {
			rep.Results = append(rep.Results, itemToRef(c.To, root))
		}
	}
	return rep, nil
}

func itemToRef(item lsp.CallHierarchyItem, root string) SymbolRef {
	return SymbolRef{
		Symbol:    symbolBase(item.Name),
		File:      uriToRel(item.URI, root),
		StartLine: item.Range.Start.Line + 1,
	}
}

// findSymbolPos returns the selection-range start of a symbol by name, preferring
// the declaration whose range starts at wantLine (1-based).
func findSymbolPos(syms []lsp.DocumentSymbol, name string, wantLine int) (lsp.Position, bool) {
	var best lsp.Position
	found := false
	var walk func([]lsp.DocumentSymbol)
	walk = func(ss []lsp.DocumentSymbol) {
		for _, s := range ss {
			// gopls names methods like "(*Store).AddNode"; match the base name.
			if symbolBase(s.Name) == name {
				if s.Range.Start.Line+1 == wantLine {
					best = s.SelectionRange.Start
					found = true
					return
				}
				if !found {
					best = s.SelectionRange.Start
					found = true
				}
			}
			walk(s.Children)
		}
	}
	walk(syms)
	return best, found
}

// symbolBase strips a method's receiver prefix: "(*Store).AddNode" -> "AddNode".
func symbolBase(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func uriToRel(uri, root string) string {
	p := strings.TrimPrefix(uri, "file://")
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}

// Semantic runs a meaning-based search over the project's embedded nodes.
func (svc *Service) Semantic(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	if topK <= 0 {
		topK = 10
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SemanticReport{Query: query, Project: name, Hits: []SemanticHit{}}

	vecs, err := svc.s.Embedder().Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return rep, nil
	}
	vstore, err := svc.s.Vectors()
	if err != nil {
		return nil, err
	}
	hits, err := vstore.Search(vecs[0], topK, name)
	if err != nil {
		return nil, err
	}
	for _, h := range hits {
		rep.Hits = append(rep.Hits, SemanticHit{
			Symbol: h.Meta.Symbol, FQN: h.Meta.FQN, Kind: h.Meta.Kind, File: h.Meta.File,
			StartLine: h.Meta.StartLine, EndLine: h.Meta.EndLine, Score: h.Score,
		})
	}
	return rep, nil
}

// ImpactNode is a node in the blast radius with its hop distance.
type ImpactNode struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	Depth     int    `json:"depth"`
}

// ImpactReport is the flagship impact analysis: who is affected by changing a
// symbol, and which tests cover those paths.
type ImpactReport struct {
	Symbol        string       `json:"symbol"`
	Project       string       `json:"project"`
	Found         bool         `json:"found"`
	Locations     []SymbolRef  `json:"locations,omitempty"`
	DirectCallers []SymbolRef  `json:"direct_callers"`
	BlastRadius   []ImpactNode `json:"blast_radius"`
	Tests         []ImpactNode `json:"tests"`
	Untested      bool         `json:"untested"`
}

// Impact computes impact analysis for a symbol: its definition site(s), direct
// callers, the transitive blast radius (up to depth hops), and which of those
// are tests (coverage). depth <= 0 defaults to 3.
func (svc *Service) Impact(cwd, symbol string, depth int) (*ImpactReport, error) {
	if depth <= 0 {
		depth = 3
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ImpactReport{
		Symbol: symbol, Project: name,
		DirectCallers: []SymbolRef{}, BlastRadius: []ImpactNode{}, Tests: []ImpactNode{},
	}

	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}

	locs, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, err
	}
	if len(locs) == 0 {
		return rep, nil // symbol not in the graph
	}
	rep.Found = true
	for _, n := range locs {
		rep.Locations = append(rep.Locations, nodeToRef(n))
	}

	callers, err := g.Callers(p.ID, symbol)
	if err != nil {
		return nil, err
	}
	for _, n := range callers {
		rep.DirectCallers = append(rep.DirectCallers, nodeToRef(n))
	}

	radius, err := g.BlastRadius(p.ID, symbol, depth)
	if err != nil {
		return nil, err
	}
	for _, nd := range radius {
		in := ImpactNode{
			Symbol: nd.Node.Symbol, FQN: nd.Node.FQN, Kind: nd.Node.Kind,
			File: nd.Node.FilePath, StartLine: nd.Node.StartLine, Depth: nd.Depth,
		}
		rep.BlastRadius = append(rep.BlastRadius, in)
		if nd.Node.Kind == graph.KindTest {
			rep.Tests = append(rep.Tests, in)
		}
	}
	rep.Untested = len(rep.Tests) == 0
	return rep, nil
}

// HotspotRef is a hub node with its incoming-usage count.
type HotspotRef struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	InDegree  int    `json:"in_degree"`
}

// HotspotsReport is returned by Hotspots.
type HotspotsReport struct {
	Project  string       `json:"project"`
	Hotspots []HotspotRef `json:"hotspots"`
}

// OrphansReport is returned by Orphans.
type OrphansReport struct {
	Project string      `json:"project"`
	Orphans []SymbolRef `json:"orphans"`
}

// PathReport is returned by Path.
type PathReport struct {
	From    string      `json:"from"`
	To      string      `json:"to"`
	Project string      `json:"project"`
	Found   bool        `json:"found"`
	Path    []SymbolRef `json:"path"`
}

// project resolves cwd to a registered project id. found is false (no error)
// when the project isn't indexed yet.
func (svc *Service) project(cwd string) (id int64, name string, found bool, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return 0, "", false, err
	}
	if _, name, err = svc.resolveProject(cwd); err != nil {
		return 0, name, false, err
	}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return 0, name, false, nil
	}
	if err != nil {
		return 0, name, false, err
	}
	return p.ID, name, true, nil
}

// Hotspots returns the most-referenced nodes (hubs).
func (svc *Service) Hotspots(cwd string, limit int) (*HotspotsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &HotspotsReport{Project: name, Hotspots: []HotspotRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	hs, err := g.Hotspots(pid, limit)
	if err != nil {
		return nil, err
	}
	for _, h := range hs {
		rep.Hotspots = append(rep.Hotspots, HotspotRef{
			Symbol: h.Node.Symbol, FQN: h.Node.FQN, Kind: h.Node.Kind,
			File: h.Node.FilePath, StartLine: h.Node.StartLine, InDegree: h.InDegree,
		})
	}
	return rep, nil
}

// Orphans returns function/method nodes with no callers (dead-code candidates).
func (svc *Service) Orphans(cwd string, limit int) (*OrphansReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &OrphansReport{Project: name, Orphans: []SymbolRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	nodes, err := g.Orphans(pid, limit)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Orphans = append(rep.Orphans, nodeToRef(n))
	}
	return rep, nil
}

// Path returns the shortest call path between two symbols.
func (svc *Service) Path(cwd, from, to string) (*PathReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &PathReport{From: from, To: to, Project: name, Path: []SymbolRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	nodes, err := g.Path(pid, from, to, 0)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Path = append(rep.Path, nodeToRef(n))
	}
	rep.Found = len(nodes) > 0
	return rep, nil
}

// resolveProject finds the registered project whose path is cwd or an ancestor
// of cwd (closest wins). If none is registered it defaults to (cwd, basename).
func (svc *Service) resolveProject(cwd string) (root, name string, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return "", "", err
	}
	projs, err := g.ListProjects()
	if err != nil {
		return "", "", err
	}
	byPath := make(map[string]string, len(projs))
	for _, p := range projs {
		byPath[clean(p.Path)] = p.Name
	}
	dir := clean(cwd)
	for {
		if n, ok := byPath[dir]; ok {
			return dir, n, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	root = clean(cwd)
	return root, config.DeriveProjectName(root), nil
}

func clean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// detectLanguage guesses a project's primary language from marker files.
func detectLanguage(root string) string {
	markers := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"package.json", "typescript"},
		{"pyproject.toml", "python"},
		{"requirements.txt", "python"},
		{"Gemfile", "ruby"},
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(root, m.file)); err == nil {
			return m.lang
		}
	}
	return ""
}
