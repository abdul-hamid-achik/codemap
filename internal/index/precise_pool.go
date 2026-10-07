package index

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/sittersrc"
)

// A language server answers one callHierarchy request at a time, so the
// --precise pass over thousands of files is bounded by one process. With
// tree-sitter doing extraction the servers are disposable: the pass forks extra
// processes of the same server and splits the files between them by their
// nearest project config, so each process loads only its own projects (memory
// stays close to one process that loads them all) and the wall time divides.

// preciseWorker is one server process in the precise pass. Workers of one
// server kind pull whole projects from a shared queue (largest first), so a
// process that drew cheap projects takes the next one instead of idling while
// another grinds through an expensive project.
type preciseWorker struct {
	resolvers map[string]extract.CallResolver // language → resolver on this process
	queue     chan []int                      // projects (indexes into the pass's file list); shared by the server's workers
	files     int                             // files of this worker's server kind
}

// next returns the next project's files, or false when the queue is drained.
func (w *preciseWorker) next() ([]int, bool) {
	g, ok := <-w.queue
	return g, ok
}

func queueOf(groups [][]int) chan []int {
	ch := make(chan []int, len(groups))
	for _, g := range groups {
		ch <- g
	}
	close(ch)
	return ch
}

// preciseServerCount is how many processes serve n files of one server kind.
// The default is one. Extra processes are opt-in (index.precise_servers):
// they pay off on a big single project (~25-30% faster with 2-3 processes on
// a 1,100-file repo), but each process loads its projects and their shared
// dependencies again, which on a many-project monorepo cost as much as it
// saved — and every process can claim tsserver's 8 GB heap ceiling.
func preciseServerCount(configured, n int) int {
	if configured <= 1 || n <= 1 {
		return 1
	}
	return min(configured, n)
}

// planPreciseWorkers groups the pass's files by the server process that
// resolves their language, then splits each server's files across forked
// processes of it (see preciseServerCount). Worker resolvers for the first
// share are the live ones; the closers are the forked processes, which the
// caller closes when the pass ends. A fork that fails folds its share back
// into the live process — the pass degrades to fewer processes, never fails.
func (ix *Indexer) planPreciseWorkers(ctx context.Context, root string, langs []string, fileOf func(int) string, resolvers map[string]extract.CallResolver) ([]*preciseWorker, []io.Closer) {
	type server struct {
		langs []string
		files []int
	}
	byServer := map[string]*server{}
	var order []string
	for i, lang := range langs {
		id := "lang:" + lang
		if s, ok := resolvers[lang].(interface{ ServerID() string }); ok {
			id = s.ServerID()
		}
		srv := byServer[id]
		if srv == nil {
			srv = &server{}
			byServer[id] = srv
			order = append(order, id)
		}
		if !containsString(srv.langs, lang) {
			srv.langs = append(srv.langs, lang)
		}
		srv.files = append(srv.files, i)
	}

	var workers []*preciseWorker
	var closers []io.Closer
	for _, id := range order {
		srv := byServer[id]
		sort.Strings(srv.langs)
		live := map[string]extract.CallResolver{}
		hybrids := map[string]*sittersrc.Hybrid{}
		for _, lang := range srv.langs {
			live[lang] = resolvers[lang]
			if h, ok := resolvers[lang].(*sittersrc.Hybrid); ok {
				hybrids[lang] = h
			}
		}
		k := preciseServerCount(ix.cfg.PreciseServers, len(srv.files))
		if k <= 1 || len(hybrids) != len(srv.langs) {
			workers = append(workers, &preciseWorker{resolvers: live, queue: queueOf([][]int{srv.files}), files: len(srv.files)})
			continue
		}
		groups := splitOversized(projectGroups(root, fileOf, srv.files), k)
		k = min(k, len(groups))
		queue := queueOf(groups)
		workers = append(workers, &preciseWorker{resolvers: live, queue: queue, files: len(srv.files)})
		for n := 1; n < k; n++ {
			forked, closer, ok := forkHybrids(ctx, srv.langs, hybrids)
			if !ok {
				break // fewer processes drain the same queue
			}
			closers = append(closers, closer)
			workers = append(workers, &preciseWorker{resolvers: forked, queue: queue, files: len(srv.files)})
		}
	}
	return workers, closers
}

// forkHybrids spawns one new server process for langs (the first language
// owns it, the rest bind to it, as at registration).
func forkHybrids(ctx context.Context, langs []string, hybrids map[string]*sittersrc.Hybrid) (map[string]extract.CallResolver, io.Closer, bool) {
	owner, closer, err := hybrids[langs[0]].ForkServer(ctx)
	if err != nil {
		return nil, nil, false
	}
	out := map[string]extract.CallResolver{langs[0]: owner}
	for _, lang := range langs[1:] {
		bound, ok := hybrids[lang].Share(owner)
		if !ok {
			_ = closer.Close()
			return nil, nil, false
		}
		out[lang] = bound
	}
	return out, closer, true
}

// projectGroups groups files by project (nearest tsconfig.json /
// jsconfig.json / package.json / pyproject.toml / pyrightconfig.json),
// largest first; files keep their input order within a group.
func projectGroups(root string, fileOf func(int) string, files []int) [][]int {
	cache := map[string]string{}
	groups := map[string][]int{}
	var keys []string
	for _, i := range files {
		key := projectKey(root, fileOf(i), cache)
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], i)
	}
	sort.SliceStable(keys, func(a, b int) bool {
		if len(groups[keys[a]]) != len(groups[keys[b]]) {
			return len(groups[keys[a]]) > len(groups[keys[b]])
		}
		return keys[a] < keys[b]
	})
	out := make([][]int, 0, len(keys))
	for _, key := range keys {
		out = append(out, groups[key])
	}
	return out
}

// splitOversized cuts any project larger than a fair share (files/k) into
// fair-share chunks, so one big project (a single-tsconfig repo) still spreads
// across k processes — each loads that project, which the automatic count's
// memory bound already budgets. Order stays largest-first.
func splitOversized(groups [][]int, k int) [][]int {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if k <= 1 || total == 0 {
		return groups
	}
	fair := (total + k - 1) / k
	var out [][]int
	for _, g := range groups {
		for len(g) > fair {
			out = append(out, g[:fair])
			g = g[fair:]
		}
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return len(out[a]) > len(out[b]) })
	return out
}

var projectMarkers = []string{"tsconfig.json", "jsconfig.json", "package.json", "pyproject.toml", "pyrightconfig.json"}

// projectKey is the nearest ancestor directory of rel (root-relative, slash
// separated) holding a project marker, or "." when none does below root.
func projectKey(root, rel string, cache map[string]string) string {
	dir := path.Dir(rel)
	var visited []string
	key := "."
	for dir != "." && dir != "/" && dir != "" {
		if k, ok := cache[dir]; ok {
			key = k
			break
		}
		visited = append(visited, dir)
		if hasProjectMarker(filepath.Join(root, filepath.FromSlash(dir))) {
			key = dir
			break
		}
		dir = path.Dir(dir)
	}
	for _, d := range visited {
		cache[d] = key
	}
	return key
}

func hasProjectMarker(dir string) bool {
	for _, m := range projectMarkers {
		if st, err := os.Stat(filepath.Join(dir, m)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
