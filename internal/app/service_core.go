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

// NewService wraps a session.
func NewService(s *Session) *Service { return &Service{s: s} }

// hasPreciseEdges reports whether the project has any go/types-resolved call edges
// (i.e. was indexed with --precise). Ambiguity notes use it to recommend the right
// fix: a name-based index can be reindexed --precise; on a precise index the only
// remaining ambiguity is the query name itself matching several definitions.
func (svc *Service) hasPreciseEdges(g *graph.Store, pid int64) bool {
	n, err := g.CountEdgesByProvenance(pid, graph.ProvPrecise)
	return err == nil && n > 0
}

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

// callGraphUnavailable returns the language (and true) when the queried symbol's call
// graph can't be resolved on the current index: its language has no name-based call
// edges AND the project has no precise edges. Callers use this to replace a
// confidently-empty callers/blast/tests result (and a misleading untested:true) with
// an honest "run --precise" note — so `[]` never reads as ground-truth "none".
func (svc *Service) callGraphUnavailable(g *graph.Store, pid int64, nodes []graph.Node) (string, bool) {
	if svc.hasPreciseEdges(g, pid) {
		return "", false // a precise index resolved the call graph — empty really means empty
	}
	for _, n := range nodes {
		if noNameBasedCallLang(n.Language) {
			return n.Language, true
		}
	}
	return "", false
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
