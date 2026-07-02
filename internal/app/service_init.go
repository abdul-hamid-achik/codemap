package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// InitReport is returned by Init.
type InitReport struct {
	Project   string `json:"project"`
	Root      string `json:"root"`
	ProjectID int64  `json:"project_id"`
	DataDir   string `json:"data_dir"`
}

// IndexReport is returned by Index.
type IndexReport struct {
	Project        string   `json:"project"`
	Root           string   `json:"root"`
	Embedded       bool     `json:"embedded"`
	Warning        string   `json:"warning,omitempty"`
	FilesScanned   int      `json:"files_scanned"`
	FilesIndexed   int      `json:"files_indexed"`
	FilesSkipped   int      `json:"files_skipped"`
	FilesUnchanged int      `json:"files_unchanged"` // P2-07 (O108): hash-matched, not a skip
	FilesDeleted   int      `json:"files_deleted,omitempty"`
	Oversized      []string `json:"oversized,omitempty"`
	// Unsupported maps a recognized source language with no available extractor
	// (e.g. its language server isn't installed) to the count of such files. They
	// were scanned but couldn't be indexed — the Warning explains how to enable them.
	Unsupported map[string]int    `json:"unsupported,omitempty"`
	Nodes       int               `json:"nodes"`
	Edges       int               `json:"edges"`
	Errors      []index.FileError `json:"errors,omitempty"`
	// Precise* surface the opt-in go/types pass (index --precise).
	PreciseUpgraded int    `json:"precise_upgraded,omitempty"`
	PreciseSkipped  int    `json:"precise_skipped,omitempty"`
	PreciseNote     string `json:"precise_note,omitempty"`
	// Languages maps each indexed language to its file count (e.g. "go", "typescript").
	Languages map[string]int `json:"languages,omitempty"`
	// Phase timing (wall-clock milliseconds). Extract covers the parse + graph
	// write pass; Embed covers Ollama + vector inserts; Precise covers the opt-in
	// go/types + LSP callHierarchy passes; Total is end-to-end. Zero when N/A.
	ExtractMs int `json:"extract_ms,omitempty"`
	EmbedMs   int `json:"embed_ms,omitempty"`
	PreciseMs int `json:"precise_ms,omitempty"`
	TotalMs   int `json:"total_ms,omitempty"`
}

// StatusReport is returned by Status.
type StatusReport struct {
	Project      string         `json:"project"`
	Root         string         `json:"root"`
	Registered   bool           `json:"registered"`
	Path         string         `json:"path,omitempty"`
	Nodes        int            `json:"nodes"`
	Edges        int            `json:"edges"`
	Files        int            `json:"files"`
	Vectors      int            `json:"vectors"`       // embedded nodes (0 = no semantic index)
	PreciseEdges int            `json:"precise_edges"` // go/types-resolved call edges (0 = name-based index)
	Languages    map[string]int `json:"languages,omitempty"`
	Kinds        map[string]int `json:"kinds,omitempty"`
	// Stale, when set, reports how far the index has drifted from the working tree
	// (changed/new/deleted files). Status does not compute it (keeps studio fast);
	// the CLI `status` and MCP codemap_status populate it via Staleness so an agent
	// knows whether to reindex before trusting query results.
	Stale *index.Staleness `json:"stale,omitempty"`
	// Siblings lists ecosystem tools that also have this project indexed (currently
	// vecgrep), discovered by name from their global registries — a hint that a
	// richer/semantic view exists elsewhere, not an authoritative cross-index.
	Siblings []string `json:"siblings,omitempty"`
	// ProjectKey is the stable, collision-resistant identifier for this project
	// (git.RepoHash). codemap is the AUTHORITY for it: ecosystem tools writing a
	// codemap-scoped agent memory tag it ['codemap', <project_key>] using THIS
	// value (not a re-derived one), so a global memory store can be recalled per
	// project without cross-project leakage. See the G2 memory governance spec.
	ProjectKey string `json:"project_key,omitempty"`
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
	// Embedding ran only if a vector store was wired AND the embed phase wasn't
	// skipped by an embedder failure (EmbedNote) — the structural index still
	// succeeded in that case, so report it as structure-only with the reason.
	rep.Embedded = vec != nil && res.EmbedNote == ""
	if res.EmbedNote != "" {
		if rep.Warning != "" {
			rep.Warning += "; " + res.EmbedNote
		} else {
			rep.Warning = res.EmbedNote
		}
	}
	rep.FilesScanned = res.FilesScanned
	rep.FilesIndexed = res.FilesIndexed
	rep.FilesSkipped = res.FilesSkipped
	rep.FilesUnchanged = res.FilesUnchanged
	rep.FilesDeleted = res.FilesDeleted
	rep.Oversized = res.Oversized
	rep.Unsupported = res.Unsupported
	rep.Nodes = res.Nodes
	rep.Edges = res.Edges
	rep.Errors = res.Errors
	rep.PreciseUpgraded = res.PreciseUpgraded
	rep.PreciseSkipped = res.PreciseSkipped
	rep.PreciseNote = res.PreciseNote
	rep.Languages = res.Languages
	rep.ExtractMs = res.ExtractMs
	rep.EmbedMs = res.EmbedMs
	rep.PreciseMs = res.PreciseMs
	rep.TotalMs = res.TotalMs
	if adv := indexAdvisory(res); adv != "" {
		if rep.Warning != "" {
			rep.Warning += "; " + adv
		} else {
			rep.Warning = adv
		}
	}
	return rep, nil
}

// indexAdvisory builds the index warning: an actionable note for a recognized
// language that's present but whose language server isn't installed (shown
// regardless of what else indexed, so a TS file dropped from a Go+TS repo isn't
// silent), plus a "planned" note for genuinely-unsupported languages when nothing
// at all was indexed.
func indexAdvisory(res *index.Result) string {
	var msgs []string
	for _, lang := range sortedStrKeys(res.MissingServers) {
		msgs = append(msgs, fmt.Sprintf("%d %s file(s) skipped — install %q to index them (or run with --no-lsp)",
			res.Unsupported[lang], lang, res.MissingServers[lang]))
	}
	if res.FilesScanned == 0 {
		planned := map[string]int{}
		for lang, n := range res.Unsupported {
			if _, hasServer := res.MissingServers[lang]; !hasServer {
				planned[lang] = n
			}
		}
		if len(planned) > 0 {
			msgs = append(msgs, "skipped "+summarizeUnsupported(planned)+
				" — codemap indexes Go, TypeScript, JavaScript, and Python; more languages planned (run 'codemap doctor' to see language servers)")
		}
	}
	return strings.Join(msgs, "; ")
}

func sortedStrKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
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
	rep := &StatusReport{Project: name, Root: root, ProjectKey: git.RepoHash(root)}
	// Best-effort: note ecosystem siblings that also index this project (a richer
	// semantic view may live there). Set before the not-registered return so it
	// shows even when codemap itself hasn't indexed the project yet.
	for _, tool := range []string{"vecgrep"} {
		if config.SiblingProjectIndexed(tool, name) {
			rep.Siblings = append(rep.Siblings, tool)
		}
	}

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
	// How many call edges were resolved precisely by go/types (0 ⇒ name-based index).
	if n, cErr := g.CountEdgesByProvenance(p.ID, graph.ProvPrecise); cErr == nil {
		rep.PreciseEdges = n
	}
	return rep, nil
}

// Staleness reports how far the project's index has drifted from the working
// tree (changed/new/deleted files) so a caller can warn that query results may
// be behind the code. Returns nil for an unregistered/unindexed project. It runs
// without language servers (hashes index_state files, recognizes new ones by
// extension), so it's separate from Status and only the agent-facing surfaces
// (CLI status, MCP codemap_status) pay for it — studio startup stays fast.
func (svc *Service) Staleness(cwd string) (*index.Staleness, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, nil // not indexed → nothing to compare
	}
	if err != nil {
		return nil, err
	}
	stats, err := g.Stats(p.ID)
	if err != nil {
		return nil, err
	}
	langs := make(map[string]bool, len(stats.Languages))
	for l := range stats.Languages {
		langs[l] = true
	}
	st, err := index.New(g, nil, nil, svc.s.Config.Index).Staleness(p.ID, p.Path, langs)
	if err != nil {
		return nil, err
	}
	return &st, nil
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

// BranchStatus reports the git state of the repo at cwd — current branch, HEAD
// sha, detached, and the stable repo/branch keys that per-branch index snapshots
// are keyed by. Read-only (no writes, no index changes); the foundation for
// branch-aware index switching (BD.*). A non-git directory reports IsRepo:false.
func (svc *Service) BranchStatus(ctx context.Context, cwd string) (git.Status, error) {
	return git.Inspect(ctx, cwd)
}
