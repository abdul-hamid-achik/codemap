package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	Vectors    int            `json:"vectors"` // embedded nodes (0 = no semantic index)
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
	// No supported (Go) files but other recognized source present → explain the
	// empty result rather than leaving the user puzzled (v0.1 indexes Go).
	if res.FilesScanned == 0 && len(res.Unsupported) > 0 {
		rep.Warning = "no Go files to index (codemap v0.1 indexes Go); skipped " +
			summarizeUnsupported(res.Unsupported) + " — support for more languages is planned"
	}
	return rep, nil
}

// summarizeUnsupported renders a stable "12 typescript, 3 python" summary.
func summarizeUnsupported(m map[string]int) string {
	langs := make([]string, 0, len(m))
	for l := range m {
		langs = append(langs, l)
	}
	sort.Slice(langs, func(i, j int) bool {
		if m[langs[i]] != m[langs[j]] {
			return m[langs[i]] > m[langs[j]]
		}
		return langs[i] < langs[j]
	})
	parts := make([]string, len(langs))
	for i, l := range langs {
		parts[i] = fmt.Sprintf("%d %s", m[l], l)
	}
	return strings.Join(parts, ", ")
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
	// Best-effort: how many of this project's nodes are embedded (so callers know
	// whether semantic search is available).
	if n, ok := svc.embeddedCount(name); ok {
		rep.Vectors = n
	}
	return rep, nil
}

// embeddedCount reports how many vectors exist for the named project and whether
// that count is known. It never creates the veclite store: if the file is absent
// the project is structure-only, which is a known 0 — so a structure-only project
// is never charged a store-open or an empty file just to be counted.
func (svc *Service) embeddedCount(name string) (n int, ok bool) {
	if _, err := os.Stat(config.VeclitePath()); err != nil {
		return 0, true // no veclite file → definitely no embeddings
	}
	v, err := svc.s.Vectors()
	if err != nil {
		return 0, false
	}
	c, err := v.CountByProject(name)
	if err != nil {
		return 0, false
	}
	return c, true
}

// ProjectInfo is one registered project with its index size.
type ProjectInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	Nodes    int    `json:"nodes"`
	Edges    int    `json:"edges"`
	Files    int    `json:"files"`
}

// ProjectsReport lists every project registered with codemap.
type ProjectsReport struct {
	Projects []ProjectInfo `json:"projects"`
}

// Projects lists all registered projects with their index sizes — the registry
// is shared across repos, so this shows everything codemap has indexed. (Queries
// still target one project at a time, resolved from cwd or an explicit path.)
func (svc *Service) Projects() (*ProjectsReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	projs, err := g.ListProjects()
	if err != nil {
		return nil, err
	}
	rep := &ProjectsReport{Projects: []ProjectInfo{}}
	for _, p := range projs {
		st, err := g.Stats(p.ID)
		if err != nil {
			return nil, err
		}
		rep.Projects = append(rep.Projects, ProjectInfo{
			Name: p.Name, Path: p.Path, Language: p.Language,
			Nodes: st.Nodes, Edges: st.Edges, Files: st.Files,
		})
	}
	return rep, nil
}

// AnnotationsReport is returned by the annotation read methods.
type AnnotationsReport struct {
	Project     string             `json:"project"`
	Annotations []graph.Annotation `json:"annotations"`
}

// pathTarget is the canonical key for a path annotation.
func pathTarget(from, to string) string { return from + " -> " + to }

// annotateProject resolves cwd to a project, auto-registering it (so you can
// annotate before indexing), and returns its id + the graph store.
func (svc *Service) annotateProject(cwd string) (int64, *graph.Store, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return 0, nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return 0, nil, err
	}
	pid, err := g.UpsertProject(name, root, detectLanguage(root))
	if err != nil {
		return 0, nil, err
	}
	return pid, g, nil
}

// AnnotateNode pins a note / external data to a symbol (by FQN or name).
func (svc *Service) AnnotateNode(cwd, symbol, source, note, data string) (int64, error) {
	pid, g, err := svc.annotateProject(cwd)
	if err != nil {
		return 0, err
	}
	return g.AddAnnotation(pid, graph.Annotation{
		Kind: graph.AnnotationNode, Target: symbol, Source: source, Note: note, Data: data,
	})
}

// AnnotatePath pins a note / external data to a call path from→to. Returns the
// new id and the canonical path target.
func (svc *Service) AnnotatePath(cwd, from, to, source, note, data string) (int64, string, error) {
	pid, g, err := svc.annotateProject(cwd)
	if err != nil {
		return 0, "", err
	}
	target := pathTarget(from, to)
	id, err := g.AddAnnotation(pid, graph.Annotation{
		Kind: graph.AnnotationPath, Target: target, Source: source, Note: note, Data: data,
	})
	return id, target, err
}

// AllAnnotations lists every annotation in the project.
func (svc *Service) AllAnnotations(cwd string) (*AnnotationsReport, error) {
	return svc.annotations(cwd, "", "")
}

// NodeAnnotations lists annotations attached to a symbol.
func (svc *Service) NodeAnnotations(cwd, symbol string) (*AnnotationsReport, error) {
	return svc.annotations(cwd, graph.AnnotationNode, symbol)
}

// PathAnnotations lists annotations attached to a call path from→to.
func (svc *Service) PathAnnotations(cwd, from, to string) (*AnnotationsReport, error) {
	return svc.annotations(cwd, graph.AnnotationPath, pathTarget(from, to))
}

// annotations lists annotations: all for the project (kind==""), or those on a
// specific node/path target.
func (svc *Service) annotations(cwd, kind, target string) (*AnnotationsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &AnnotationsReport{Project: name, Annotations: []graph.Annotation{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	var anns []graph.Annotation
	if kind == "" {
		anns, err = g.AllAnnotations(pid)
	} else {
		anns, err = g.AnnotationsByTarget(pid, kind, target)
	}
	if err != nil {
		return nil, err
	}
	if anns != nil {
		rep.Annotations = anns
	}
	return rep, nil
}

// RemoveAnnotation deletes one annotation by id; reports whether it existed.
func (svc *Service) RemoveAnnotation(cwd string, id int64) (bool, error) {
	pid, _, found, err := svc.project(cwd)
	if err != nil || !found {
		return false, err
	}
	g, _ := svc.s.Graph()
	return g.DeleteAnnotation(pid, id)
}

// SymbolRef is a lightweight reference to a graph node (for query results).
// Signature and Doc let callers understand each result without a file read.
type SymbolRef struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
}

func nodeToRef(n graph.Node) SymbolRef {
	return SymbolRef{Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
		StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Doc: n.Docstring}
}

// RelationReport is returned by Callers/Callees.
type RelationReport struct {
	Symbol      string             `json:"symbol"`
	Project     string             `json:"project"`
	Results     []SymbolRef        `json:"results"`
	Note        string             `json:"note,omitempty"`        // set when precise resolution fell back to name-based
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to the queried symbol
}

// SemanticHit is one semantic-search result.
type SemanticHit struct {
	Symbol      string             `json:"symbol"`
	FQN         string             `json:"fqn,omitempty"`
	Kind        string             `json:"kind"`
	File        string             `json:"file"`
	StartLine   int                `json:"start_line"`
	EndLine     int                `json:"end_line"`
	Score       float32            `json:"score"`
	Signature   string             `json:"signature,omitempty"`
	Doc         string             `json:"doc,omitempty"`
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to this symbol
}

// enrichHitAnnotations attaches each hit's node-annotations in one bulk query,
// matching by the hit's FQN or symbol name.
func enrichHitAnnotations(g *graph.Store, projectID int64, hits []SemanticHit) {
	if len(hits) == 0 {
		return
	}
	all, err := g.AllAnnotations(projectID)
	if err != nil || len(all) == 0 {
		return
	}
	byTarget := map[string][]graph.Annotation{}
	for _, a := range all {
		if a.Kind == graph.AnnotationNode {
			byTarget[a.Target] = append(byTarget[a.Target], a)
		}
	}
	for i := range hits {
		seen := map[int64]bool{}
		var out []graph.Annotation
		for _, t := range []string{hits[i].FQN, hits[i].Symbol} {
			for _, a := range byTarget[t] {
				if !seen[a.ID] {
					seen[a.ID] = true
					out = append(out, a)
				}
			}
		}
		hits[i].Annotations = out
	}
}

// SemanticReport is returned by Semantic / FindSymbols / Search.
type SemanticReport struct {
	Query   string        `json:"query"`
	Project string        `json:"project"`
	Mode    string        `json:"mode"`           // "semantic", "name", or "none" (no embeddings)
	Note    string        `json:"note,omitempty"` // why there are no results, when applicable
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
	// Name-based resolution conflates same-named definitions, so these results are
	// the union across all of them. Flag it (mirrors impact) and point at the
	// precise path that disambiguates — far better than a silent over-count.
	if defs, derr := g.FindNodesBySymbol(p.ID, symbol); derr == nil && len(defs) > 1 {
		rep.Note = fmt.Sprintf("%q matches %d definitions (name-based) — these results merge all of them; add --lsp (gopls) for one exact method", symbol, len(defs))
	}
	rep.Annotations = symbolAnnotations(g, p.ID, symbol)
	return rep, nil
}

// symbolAnnotations returns the annotations pinned to a symbol — matched by the
// query name or any of its resolved definition FQNs/symbols.
func symbolAnnotations(g *graph.Store, projectID int64, symbol string) []graph.Annotation {
	candidates := []string{symbol}
	if locs, err := g.FindNodesBySymbol(projectID, symbol); err == nil {
		for _, n := range locs {
			candidates = append(candidates, n.FQN, n.Symbol)
		}
	}
	return nodeAnnotationsFor(g, projectID, candidates...)
}

// symbolAnnotationsByName resolves a symbol's annotations given the project name
// (used by the precise/gopls path, which carries the name rather than the pid).
func (svc *Service) symbolAnnotationsByName(name, symbol string) []graph.Annotation {
	g, err := svc.s.Graph()
	if err != nil {
		return nil
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil
	}
	return symbolAnnotations(g, p.ID, symbol)
}

// PreciseCallers computes exact callers of a Go symbol using gopls callHierarchy
// (no by-name inflation). Go-only for now; errors if gopls is unavailable.
func (svc *Service) PreciseCallers(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	c, _, project, err := svc.preciseRelations(ctx, cwd, symbol, "", 0)
	if err != nil {
		return svc.preciseFallback(cwd, symbol, err, svc.Callers)
	}
	return &RelationReport{Symbol: symbol, Project: project, Results: nonNil(c),
		Annotations: svc.symbolAnnotationsByName(project, symbol)}, nil
}

// preciseFallback degrades to name-based results when the language server can't
// resolve precisely (e.g. gopls can't form a workspace view in a restricted
// environment, or the project isn't a buildable module), attaching a note so the
// caller knows the results are name-based. Far better than failing a query with a
// raw "jsonrpc error: no views". If name-based resolution itself errors, that's
// surfaced instead.
func (svc *Service) preciseFallback(cwd, symbol string, cause error, nameBased func(cwd, symbol string) (*RelationReport, error)) (*RelationReport, error) {
	rep, err := nameBased(cwd, symbol)
	if err != nil {
		return nil, err
	}
	rep.Note = fmt.Sprintf("precise (gopls) resolution unavailable (%v) — showing name-based results", cause)
	return rep, nil
}

// PreciseCallees computes exact callees of a Go symbol using gopls callHierarchy.
func (svc *Service) PreciseCallees(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	_, ce, project, err := svc.preciseRelations(ctx, cwd, symbol, "", 0)
	if err != nil {
		return svc.preciseFallback(cwd, symbol, err, svc.Callees)
	}
	return &RelationReport{Symbol: symbol, Project: project, Results: nonNil(ce),
		Annotations: svc.symbolAnnotationsByName(project, symbol)}, nil
}

// PreciseRelationsAt returns both exact callers and callees of the symbol whose
// declaration is at file:line (to disambiguate same-named symbols), in one gopls
// session. Used by the studio precise toggle.
func (svc *Service) PreciseRelationsAt(ctx context.Context, cwd, symbol, file string, line int) (callers, callees []SymbolRef, err error) {
	c, ce, _, err := svc.preciseRelations(ctx, cwd, symbol, file, line)
	return c, ce, err
}

// preciseRelations resolves the symbol's node via the graph (preferring the one
// at hintFile:hintLine), then drives gopls (documentSymbol → prepareCallHierarchy
// → incoming + outgoing) in a single session.
func (svc *Service) preciseRelations(ctx context.Context, cwd, symbol, hintFile string, hintLine int) (callers, callees []SymbolRef, project string, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, nil, "", err
	}
	if _, project, err = svc.resolveProject(cwd); err != nil {
		return nil, nil, project, err
	}
	p, err := g.GetProjectByName(project)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, nil, project, nil
	}
	if err != nil {
		return nil, nil, project, err
	}

	nodes, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, nil, project, err
	}
	var node *graph.Node
	for i := range nodes {
		if nodes[i].Language != "go" {
			continue
		}
		if hintFile != "" && nodes[i].FilePath == hintFile && (hintLine == 0 || nodes[i].StartLine == hintLine) {
			node = &nodes[i] // exact match for the requested declaration
			break
		}
		if node == nil {
			node = &nodes[i] // first Go node as fallback
		}
	}
	if node == nil {
		return nil, nil, project, fmt.Errorf("precise queries currently support Go only (no Go symbol named %q)", symbol)
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		return nil, nil, project, fmt.Errorf("gopls not found on PATH (required for --lsp)")
	}

	root := p.Path
	absFile := filepath.Join(root, node.FilePath)
	src, err := os.ReadFile(absFile)
	if err != nil {
		return nil, nil, project, err
	}

	cl, err := lsp.Spawn(ctx, "gopls")
	if err != nil {
		return nil, nil, project, err
	}
	defer cl.Close()
	if err := cl.Initialize(ctx, root); err != nil {
		return nil, nil, project, err
	}
	uri := lsp.URI(absFile)
	if err := cl.DidOpen(uri, "go", string(src)); err != nil {
		return nil, nil, project, err
	}
	// callHierarchy needs the whole workspace analyzed; wait for gopls to load.
	cl.WaitReady(ctx, 20*time.Second)

	syms, err := cl.DocumentSymbols(ctx, uri)
	if err != nil {
		return nil, nil, project, err
	}
	pos, ok := findSymbolPos(syms, symbol, node.StartLine)
	if !ok {
		return nil, nil, project, nil
	}
	items, err := cl.PrepareCallHierarchy(ctx, uri, pos)
	if err != nil || len(items) == 0 {
		return nil, nil, project, err
	}

	in, err := cl.IncomingCalls(ctx, items[0])
	if err != nil {
		return nil, nil, project, err
	}
	out, err := cl.OutgoingCalls(ctx, items[0])
	if err != nil {
		return nil, nil, project, err
	}
	for _, c := range in {
		callers = append(callers, itemToRef(c.From, root))
	}
	for _, c := range out {
		callees = append(callees, itemToRef(c.To, root))
	}
	return callers, callees, project, nil
}

func nonNil(s []SymbolRef) []SymbolRef {
	if s == nil {
		return []SymbolRef{}
	}
	return s
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
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SemanticReport{Query: query, Project: name, Mode: "semantic", Hits: []SemanticHit{}}
	if !found {
		return rep, nil
	}

	// Structure-only projects have no vectors. Detect that up front so the answer is
	// an accurate "no embeddings" instead of an empty "no matches" — and so we skip
	// both a pointless embedder call (which would error if Ollama is down) and the
	// creation of an empty veclite file.
	if n, ok := svc.embeddedCount(name); ok && n == 0 {
		rep.Mode = "none"
		rep.Note = "no embeddings for this project — run 'codemap index' with Ollama running to enable semantic search, or use 'codemap find' for name search"
		return rep, nil
	}

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
	// Vector payloads don't store signatures or docstrings; resolve them from the
	// graph (one query) so semantic results are as self-contained as name search.
	var info map[string]graph.SymInfo
	if len(hits) > 0 {
		if g, gerr := svc.s.Graph(); gerr == nil {
			info, _ = g.SymbolInfoIndex(pid)
		}
	}
	for _, h := range hits {
		meta := info[h.Meta.FQN]
		rep.Hits = append(rep.Hits, SemanticHit{
			Symbol: h.Meta.Symbol, FQN: h.Meta.FQN, Kind: h.Meta.Kind, File: h.Meta.File,
			StartLine: h.Meta.StartLine, EndLine: h.Meta.EndLine, Score: h.Score,
			Signature: meta.Signature, Doc: meta.Doc,
		})
	}
	if g, gerr := svc.s.Graph(); gerr == nil {
		enrichHitAnnotations(g, pid, rep.Hits)
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
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
}

// ImpactReport is the flagship impact analysis: who is affected by changing a
// symbol, and which tests cover those paths.
type ImpactReport struct {
	Symbol        string             `json:"symbol"`
	Project       string             `json:"project"`
	Found         bool               `json:"found"`
	Locations     []SymbolRef        `json:"locations,omitempty"`
	DirectCallers []SymbolRef        `json:"direct_callers"`
	BlastRadius   []ImpactNode       `json:"blast_radius"`
	Tests         []ImpactNode       `json:"tests"`
	Untested      bool               `json:"untested"`
	Note          string             `json:"note,omitempty"`        // set when the name is ambiguous (merges same-named defs)
	Annotations   []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to this symbol
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
	if len(locs) > 1 {
		// Name-based lookup conflates every definition with this name, so the
		// callers/blast-radius/tests below are the union across all of them. Say
		// so — a "71 callers" number is misleading when it merges six unrelated
		// Close() methods.
		rep.Note = fmt.Sprintf("%q matches %d definitions (name-based) — direct callers, blast radius, and covering tests below merge all of them; for one exact method use callers/callees --lsp", symbol, len(locs))
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
			Signature: nd.Node.Signature, Doc: nd.Node.Docstring,
		}
		rep.BlastRadius = append(rep.BlastRadius, in)
		if nd.Node.Kind == graph.KindTest {
			rep.Tests = append(rep.Tests, in)
		}
	}
	rep.Untested = len(rep.Tests) == 0

	// Surface any annotations pinned to this symbol — by the query name or by a
	// resolved FQN/symbol of its definition sites — so analysis shows pinned
	// knowledge inline.
	targets := []string{symbol}
	for _, l := range rep.Locations {
		targets = append(targets, l.FQN, l.Symbol)
	}
	rep.Annotations = nodeAnnotationsFor(g, p.ID, targets...)
	return rep, nil
}

// nodeAnnotationsFor returns the deduped node-annotations whose target matches
// any of the candidates (a query name and/or resolved FQNs).
func nodeAnnotationsFor(g *graph.Store, projectID int64, candidates ...string) []graph.Annotation {
	seen := map[int64]bool{}
	var out []graph.Annotation
	for _, t := range candidates {
		if t == "" {
			continue
		}
		anns, err := g.AnnotationsByTarget(projectID, graph.AnnotationNode, t)
		if err != nil {
			continue
		}
		for _, a := range anns {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	return out
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

// Indexed reports whether the project for cwd has been indexed (registered in the
// graph) along with the resolved project name. It's a cheap registration check so
// query commands can give a clear "run codemap index" message instead of
// misleading empty results (e.g. "Callers of X: none") on a cold repo.
func (svc *Service) Indexed(cwd string) (indexed bool, name string, err error) {
	_, name, found, err := svc.project(cwd)
	return found, name, err
}

// SymbolsReport is returned by Symbols.
type SymbolsReport struct {
	Project string      `json:"project"`
	File    string      `json:"file"`
	Symbols []SymbolRef `json:"symbols"`
}

// Symbols lists the symbols defined in a file (functions, types, methods, tests)
// straight from the index — no file read needed. file may be relative to cwd or
// absolute; it is resolved to a project-relative path.
func (svc *Service) Symbols(cwd, file string) (*SymbolsReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SymbolsReport{Project: name, File: file, Symbols: []SymbolRef{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}
	rel := projectRel(p.Path, cwd, file)
	rep.File = rel
	nodes, err := g.NodesInFile(pid, rel)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			continue
		}
		rep.Symbols = append(rep.Symbols, nodeToRef(n))
	}
	return rep, nil
}

func projectRel(root, cwd, file string) string {
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, file)
	}
	if rel, err := filepath.Rel(root, abs); err == nil {
		return rel
	}
	return file
}

// SourceMatch is a symbol's definition with its source text read back from disk.
type SourceMatch struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Source    string `json:"source"`
}

// SourceReport is returned by Source.
type SourceReport struct {
	Symbol      string             `json:"symbol"`
	Project     string             `json:"project"`
	Matches     []SourceMatch      `json:"matches"`
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to this symbol
}

// Source returns the source code of every symbol matching name, read from the
// indexed file at its recorded line range — the implementation behind the
// signature/docstring, without the caller having to open the file. The graph
// only stores line ranges (not source), so this reads from disk; reindex if a
// file changed since indexing.
func (svc *Service) Source(cwd, name string) (*SourceReport, error) {
	pid, projName, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SourceReport{Symbol: name, Project: projName, Matches: []SourceMatch{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	p, err := g.GetProjectByName(projName)
	if err != nil {
		return nil, err
	}
	nodes, err := g.FindNodesBySymbol(pid, name)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Kind == graph.KindFile {
			continue
		}
		src, _ := readLineRange(filepath.Join(p.Path, n.FilePath), n.StartLine, n.EndLine)
		rep.Matches = append(rep.Matches, SourceMatch{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
			StartLine: n.StartLine, EndLine: n.EndLine,
			Signature: n.Signature, Doc: n.Docstring, Source: src,
		})
	}
	rep.Annotations = symbolAnnotations(g, pid, name)
	return rep, nil
}

// readLineRange returns lines [start, end] (1-based, inclusive) of a file.
func readLineRange(path string, start, end int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if start < 1 {
		start = 1
	}
	lines := strings.Split(string(data), "\n")
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", nil
	}
	return strings.Join(lines[start-1:end], "\n"), nil
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

// FindSymbols does a fast, offline name search over the indexed symbols (no
// embeddings needed).
func (svc *Service) FindSymbols(cwd, query string, limit int) (*SemanticReport, error) {
	if limit <= 0 {
		limit = 50
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SemanticReport{Query: query, Project: name, Mode: "name", Hits: []SemanticHit{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	nodes, err := g.SearchSymbols(pid, query, limit)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Hits = append(rep.Hits, SemanticHit{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
			StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Doc: n.Docstring,
		})
	}
	enrichHitAnnotations(g, pid, rep.Hits)
	return rep, nil
}

// Search runs semantic search, falling back to a name search when embeddings
// are unavailable (e.g. Ollama not running, or a structure-only index) so the
// query always returns something useful.
func (svc *Service) Search(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	rep, err := svc.Semantic(ctx, cwd, query, topK)
	if err == nil && rep != nil && len(rep.Hits) > 0 {
		return rep, nil
	}
	return svc.FindSymbols(cwd, query, topK)
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
