package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Service implements codemap's operations over a Session.
type Service struct {
	s *Session
}

// NextAction is one bounded, executable follow-up recommendation attached to
// an agent-facing report. Reports expose at most two: enough to remove tool
// choice ambiguity without replacing it with a wall of suggestions.
type NextAction struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
	Why  string         `json:"why"`
}

func nextAction(tool, why string, args map[string]any) NextAction {
	return NextAction{Tool: tool, Args: args, Why: why}
}

// NewService wraps a session.
func NewService(s *Session) *Service { return &Service{s: s} }

// noNameBasedCallLang reports whether a language has NO name-based call edges — its
// call graph exists ONLY under `index --precise` (callHierarchy). For these, empty
// callers/callees/blast/tests on a name-based index means "unresolved", not "none".
func noNameBasedCallLang(lang string) bool {
	switch lang {
	case "typescript", "javascript", "python", "vue":
		return true
	}
	return false
}

// callGraphUnavailable returns the language (and true) when at least one queried
// definition has no usable call graph: its language has no name-based edges and
// that definition's file lacks successful precise-resolution coverage. Callers
// use this to replace a
// confidently-empty callers/blast/tests result (and a misleading untested:true) with
// an honest "run --precise" note — so `[]` never reads as ground-truth "none".
func (svc *Service) callGraphUnavailable(g *graph.Store, pid int64, nodes []graph.Node) (string, bool) {
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		resolved = nil // conservative on legacy/corrupt coverage state
	}
	for _, n := range nodes {
		if !resolved[n.FilePath] && noNameBasedCallLang(n.Language) {
			return n.Language, true
		}
	}
	return "", false
}

// CallGraphStatus is the stable, machine-readable enum a consumer reads to
// down-weight confidence when the call graph is unresolved. It accompanies the
// free-form human sentence (Resolution) on impact/callers/callees/review/context
// so an adapter can switch on the enum instead of parsing prose:
//
//   - "resolved"   — the queried symbol's call graph is exact (precise edges:
//     go/types for Go, language-server callHierarchy for TS/JS/Python/Vue).
//   - "name"       — a name-based call graph (Go default). Intra-package calls
//     resolve precisely, but a cross-package method call (x.Foo()) may match
//     every same-named method — medium confidence.
//   - "unresolved" — the symbol's language has NO name-based call edges and the
//     index isn't precise (TS/JS/Python/Vue without --precise). callers/blast/
//     tests are unavailable, NOT absent — low confidence; reindex --precise.
//   - "none"       — no symbol matched, or no nodes to classify.
const (
	CallGraphResolved   = "resolved"
	CallGraphName       = "name"
	CallGraphUnresolved = "unresolved"
	CallGraphNone       = "none"
)

// callGraphEnum classifies matching definition nodes from persisted per-file
// coverage. All definitions must be covered for "resolved". Otherwise an
// uncovered no-name-based language wins as "unresolved"; remaining parser/Go
// definitions degrade to "name". Empty coverage is intentionally conservative
// for legacy indexes.
func callGraphEnum(resolvedFiles map[string]bool, nodes []graph.Node) string {
	if len(nodes) == 0 {
		return CallGraphNone
	}
	allResolved := true
	for _, n := range nodes {
		if resolvedFiles[n.FilePath] {
			continue
		}
		allResolved = false
		if noNameBasedCallLang(n.Language) {
			return CallGraphUnresolved
		}
	}
	if allResolved {
		return CallGraphResolved
	}
	return CallGraphName
}

// callGraphStatus loads persisted coverage and classifies the queried
// definitions. A read failure degrades conservatively to legacy/no coverage.
func (svc *Service) callGraphStatus(g *graph.Store, pid int64, nodes []graph.Node) string {
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		resolved = nil
	}
	return callGraphEnum(resolved, nodes)
}

// callableNodes filters structural/file/type nodes out of a project node set so
// project-wide confidence reports classify only definitions that can own calls.
func callableNodes(nodes []graph.Node) []graph.Node {
	out := make([]graph.Node, 0, len(nodes))
	for _, n := range nodes {
		switch n.Kind {
		case graph.KindFunction, graph.KindMethod, graph.KindTest:
			out = append(out, n)
		}
	}
	return out
}

// callGraphRank orders the enum worst→best for aggregation (a review's band is
// only as confident as its least-resolved changed symbol). Higher is better.
func callGraphRank(s string) int {
	switch s {
	case CallGraphResolved:
		return 3
	case CallGraphName:
		return 2
	case CallGraphUnresolved:
		return 1
	}
	return 0 // CallGraphNone
}

// worstCallGraph returns the least-confident call_graph enum across a set of
// per-symbol impacts (a review is only as trustworthy as its least-resolved
// change). An empty set is "none".
func worstCallGraph(imps []*ImpactReport) string {
	if len(imps) == 0 {
		return CallGraphNone
	}
	worst := CallGraphResolved
	for _, imp := range imps {
		if callGraphRank(imp.CallGraph) < callGraphRank(worst) {
			worst = imp.CallGraph
		}
	}
	return worst
}

// embeddedCount reports how many vectors exist for the named project and whether
// that count is known. It never creates the veclite store: if the file is absent
// the project is structure-only, which is a known 0 — so a structure-only project
// is never charged a store-open or an empty file just to be counted.
func (svc *Service) embeddedCount(name string) (n int, ok bool) {
	if _, err := os.Stat(config.VeclitePath()); err != nil {
		return 0, true // no veclite file → definitely no embeddings
	}
	v, err := svc.s.VectorsReadOnly()
	if err != nil {
		return 0, false
	}
	c, err := v.CountByProject(name)
	if err != nil {
		return 0, false
	}
	return c, true
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
// Indexed reports whether the project has indexable content to query — i.e. it's
// registered AND has at least one node. A registered-but-never-indexed project
// (init without index) returns false so query commands print "run codemap index"
// instead of a misleading empty result (e.g. "callers: none" reads as "no callers"
// when nothing is indexed at all).
func (svc *Service) Indexed(cwd string) (indexed bool, name string, err error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil || !found {
		return false, name, err
	}
	g, err := svc.s.Graph()
	if err != nil {
		return false, name, err
	}
	st, err := g.Stats(pid)
	if err != nil {
		return false, name, err
	}
	return st.Nodes > 0, name, nil
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
