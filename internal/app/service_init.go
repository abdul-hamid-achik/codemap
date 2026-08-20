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
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
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
	Project  string `json:"project"`
	Root     string `json:"root"`
	Embedded bool   `json:"embedded"`
	Warning  string `json:"warning,omitempty"`
	// Degraded is true when present project languages could not be indexed
	// because a required language server was missing or failed under the
	// project runtime (asdf shim, spawn/init error, …). Agents must not treat
	// the graph as complete for those languages when Degraded is set — see
	// Tooling.Issues for codes, stderr, and agent_fix steps.
	Degraded bool `json:"degraded,omitempty"`
	// DegradedReason is a stable machine token when Degraded is true
	// (currently "lsp_unavailable").
	DegradedReason string `json:"degraded_reason,omitempty"`
	// Tooling holds structured external-binary failures. Preferred over parsing Warning.
	Tooling        *ToolingReport `json:"tooling,omitempty"`
	FilesScanned   int            `json:"files_scanned"`
	FilesIndexed   int            `json:"files_indexed"`
	FilesSkipped   int            `json:"files_skipped"`
	FilesUnchanged int            `json:"files_unchanged"` // P2-07 (O108): hash-matched, not a skip
	FilesDeleted   int            `json:"files_deleted,omitempty"`
	Oversized      []string       `json:"oversized,omitempty"`
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

// ToolingReport is the agent-facing view of external binaries needed for this
// index (language servers). Empty/omitted when every required server was healthy.
type ToolingReport struct {
	Issues []tooling.Issue `json:"issues,omitempty"`
}

// StatusReport is returned by Status.
type StatusReport struct {
	Project         string `json:"project"`
	Root            string `json:"root"`
	Registered      bool   `json:"registered"`
	Path            string `json:"path,omitempty"`
	Nodes           int    `json:"nodes"`
	Edges           int    `json:"edges"`
	Files           int    `json:"files"`
	Vectors         int    `json:"vectors"`          // locally embedded nodes (0 is expected when semantic_backend=vecgrep)
	VectorsKnown    bool   `json:"vectors_known"`    // false when lightweight status skips the vector store
	SemanticBackend string `json:"semantic_backend"` // configured retrieval owner: fallback|local|vecgrep
	PreciseEdges    int    `json:"precise_edges"`    // exact call edges from go/types/LSP; diagnostic only (leaf files can be precise with 0)
	// Precise reports whether every indexed file in each call-graph language
	// completed precise resolution at the last index. Staleness is independent;
	// consumers combine this project-level preflight with stale and the queried
	// report's call_graph enum.
	Precise   map[string]bool `json:"precise,omitempty"`
	Languages map[string]int  `json:"languages,omitempty"`
	Kinds     map[string]int  `json:"kinds,omitempty"`
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

// StatusOptions controls optional status work. Vector counts require opening
// the semantic store, which may materialize a large in-memory index. Keep that
// cost explicit for diagnostics that truly need it.
type StatusOptions struct {
	IncludeVectors bool
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
	project, err := g.GetProjectByID(pid)
	if err != nil {
		return nil, err
	}
	name = project.Name // UpsertProject may disambiguate a colliding basename.
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
	project, err := g.GetProjectByID(pid)
	if err != nil {
		return nil, err
	}
	name = project.Name // canonical vector/search scope after basename collision

	rep := &IndexReport{Project: name, Root: root}
	var vec *vector.Store
	emb := svc.s.Embedder()
	if strings.EqualFold(strings.TrimSpace(svc.s.Config.Semantic.Backend), "vecgrep") && withEmbed {
		// An explicit semantic owner is also an indexing decision: writing a
		// second local vector space that no query will read wastes embed time and
		// can leave two stores disagreeing about freshness. Structure remains in
		// codemap; vecgrep owns retrieval. The fallback/local modes preserve the
		// existing local-vector behavior during migration.
		withEmbed = false
		rep.Warning = "local embeddings disabled: semantic.backend=vecgrep delegates retrieval to vecgrep"
	}

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
	// Structure-only indexing must not leave semantic records pointing at old
	// source or deleted graph nodes. Open an existing collection for cleanup even
	// when embeddings were explicitly disabled or the provider is unavailable.
	// This path never creates a vector store and never inserts embeddings.
	if emb == nil {
		if vec, err = svc.s.VectorsForMaintenance(); err != nil {
			return nil, err
		}
	}

	res, indexErr := index.New(g, vec, emb, svc.s.Config.Index).IndexProject(ctx, pid, name, root, opts)
	// The vector writer belongs to this index operation, not to the long-lived
	// service. Release its exclusive flock on every return path so this process
	// can reopen read-only and unrelated processes can acquire the writer lock.
	var releaseErr error
	if emb == nil {
		releaseErr = svc.s.ReleaseMaintenanceVectors()
	} else {
		releaseErr = svc.s.ReleaseVectors()
	}
	if indexErr != nil || releaseErr != nil {
		return rep, errors.Join(indexErr, releaseErr)
	}
	// Embedding ran only if a vector store was wired AND the embed phase wasn't
	// skipped by an embedder failure (EmbedNote) — the structural index still
	// succeeded in that case, so report it as structure-only with the reason.
	rep.Embedded = emb != nil && vec != nil && res.EmbedNote == ""
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
	attachTooling(rep, res)
	if adv := indexAdvisory(res); adv != "" {
		if rep.Warning != "" {
			rep.Warning += "; " + adv
		} else {
			rep.Warning = adv
		}
	}
	return rep, nil
}

// attachTooling copies structured server issues onto the report, fills
// files_affected from Unsupported counts, and sets Degraded when LSP-backed
// languages were present but unusable.
func attachTooling(rep *IndexReport, res *index.Result) {
	if len(res.ServerIssues) == 0 {
		return
	}
	issues := make([]tooling.Issue, len(res.ServerIssues))
	copy(issues, res.ServerIssues)
	for i := range issues {
		n := 0
		for _, lang := range issues[i].Languages {
			n += res.Unsupported[lang]
		}
		issues[i].FilesAffected = n
	}
	rep.Tooling = &ToolingReport{Issues: issues}
	rep.Degraded = true
	rep.DegradedReason = "lsp_unavailable"
}

// indexAdvisory builds the index warning: structured tooling issues first
// (preferred agent signal lives in Tooling.Issues; Warning is the prose
// projection), then a "planned" note for genuinely-unsupported languages.
func indexAdvisory(res *index.Result) string {
	var msgs []string
	if len(res.ServerIssues) > 0 {
		// Project issues onto prose with accurate file counts.
		for _, iss := range res.ServerIssues {
			n := 0
			for _, lang := range iss.Languages {
				n += res.Unsupported[lang]
			}
			iss.FilesAffected = n
			msgs = append(msgs, tooling.WarningLine(iss))
		}
	} else {
		// Back-compat path when only MissingServers is populated (tests/old callers).
		for _, lang := range sortedStrKeys(res.MissingServers) {
			msgs = append(msgs, fmt.Sprintf("%d %s file(s) skipped — install %q to index them (or run with --no-lsp); see tooling.issues when present",
				res.Unsupported[lang], lang, res.MissingServers[lang]))
		}
	}
	planned := map[string]int{}
	for lang, n := range res.Unsupported {
		if _, hasServer := res.MissingServers[lang]; !hasServer {
			planned[lang] = n
		}
	}
	if len(planned) > 0 {
		msgs = append(msgs, "skipped "+summarizeUnsupported(planned)+
			" — recognized at T0 only (structural backend planned, not shipped); codemap currently indexes Go, TypeScript, JavaScript, Python, Ruby, Lua, Vue script blocks, CSS/SCSS/Sass/Less, and HTML")
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
	return svc.StatusWithOptions(cwd, StatusOptions{IncludeVectors: true})
}

// LightweightStatus reports graph statistics and project identity without
// opening the semantic vector store. It is safe for health checks and agents.
func (svc *Service) LightweightStatus(cwd string) (*StatusReport, error) {
	return svc.StatusWithOptions(cwd, StatusOptions{})
}

// StatusWithOptions reports index statistics with explicitly selected costs.
func (svc *Service) StatusWithOptions(cwd string, options StatusOptions) (*StatusReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	backend := strings.ToLower(strings.TrimSpace(svc.s.Config.Semantic.Backend))
	if backend == "" {
		backend = "fallback"
	}
	rep := &StatusReport{Project: name, Root: root, ProjectKey: git.RepoHash(root), SemanticBackend: backend}
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
	if options.IncludeVectors {
		if n, ok := svc.embeddedCount(name); ok {
			rep.Vectors = n
			rep.VectorsKnown = true
		}
	}
	// How many call edges were resolved precisely by go/types or LSP
	// callHierarchy. This is a diagnostic count only: leaf files can complete
	// precise resolution without producing an edge, so readiness comes from the
	// per-file coverage table below.
	if n, cErr := g.CountEdgesByProvenance(p.ID, graph.ProvPrecise); cErr == nil {
		rep.PreciseEdges = n
	}
	// A language is precise only when every indexed file for that language has a
	// successful call_graph_coverage row. Never infer this from precise edges:
	// one Go edge must not upgrade an uncovered TypeScript file, and a precisely
	// resolved leaf file with zero calls must still count as covered. Languages
	// with a shipped call-graph backend are included explicitly as false when
	// uncovered so machine consumers never have to interpret an omitted key.
	if rows, covErr := g.ProjectFileCoverage(p.ID); covErr == nil {
		rep.Precise = preciseStatusByLanguage(st.Languages, rows)
	}
	return rep, nil
}

// preciseStatusByLanguage derives the conservative status.precise contract from
// per-file coverage. Ruby/Lua are included even though their current graph is
// name-based only; explicit false is more useful than omission and will become
// true naturally if a precise backend is added later. Markup/recognized-only
// languages are omitted because they do not own a call graph.
func preciseStatusByLanguage(languages map[string]int, rows []graph.FileCoverage) map[string]bool {
	precise := make(map[string]bool)
	for lang := range languages {
		if callGraphLanguage(lang) {
			precise[lang] = false
		}
	}
	if len(precise) == 0 {
		return nil
	}
	total := make(map[string]int, len(precise))
	covered := make(map[string]int, len(precise))
	for _, row := range rows {
		if _, ok := precise[row.Language]; !ok {
			continue
		}
		total[row.Language]++
		if row.Resolver != "" {
			covered[row.Language]++
		}
	}
	for lang := range precise {
		precise[lang] = total[lang] > 0 && covered[lang] == total[lang]
	}
	return precise
}

func callGraphLanguage(lang string) bool {
	switch lang {
	case "go", "typescript", "javascript", "python", "vue", "ruby", "lua":
		return true
	default:
		return false
	}
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
