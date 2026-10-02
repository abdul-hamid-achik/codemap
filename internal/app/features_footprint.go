package app

import (
	"path"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

type footprintEdge struct {
	target     int64
	precise    bool
	targetName string
}

type testCall struct {
	source int64
	edge   footprintEdge
}

// footprintGraph is the in-memory call graph a footprint walk runs over. It is
// built once per request from the project's nodes and edges.
type footprintGraph struct {
	byID      map[int64]graph.Node
	out       map[int64][]footprintEdge
	testCalls map[int64][]testCall // target -> test callers
	prodIn    map[int64]int        // target -> distinct production (non-test) callers
	cands     map[string][]int64   // callable symbol name -> definition ids (non-test)
	chosen    map[string]int64     // (dir, subsystem, name) -> picked definition id (0 = none)
	dupDirs   map[string][]string  // FQN shared by several definitions -> their directories
}

func newFootprintGraph(nodes []graph.Node, edges []graph.Edge) *footprintGraph {
	fg := &footprintGraph{
		byID: make(map[int64]graph.Node, len(nodes)), out: map[int64][]footprintEdge{},
		testCalls: map[int64][]testCall{}, prodIn: map[int64]int{}, cands: map[string][]int64{}, chosen: map[string]int64{},
		dupDirs: map[string][]string{},
	}
	for i := range nodes {
		n := nodes[i]
		n.FilePath = path.Clean(n.FilePath)
		fg.byID[n.ID] = n
		if isFootprintTest(n) {
			continue
		}
		switch n.Kind {
		case graph.KindFunction, graph.KindMethod, graph.KindClass:
			fg.cands[n.Symbol] = append(fg.cands[n.Symbol], n.ID)
			if n.FQN != "" {
				fg.dupDirs[n.FQN] = append(fg.dupDirs[n.FQN], path.Dir(n.FilePath))
			}
		}
	}
	for fqn, dirs := range fg.dupDirs {
		if len(dirs) < 2 {
			delete(fg.dupDirs, fqn)
		}
	}
	seen := map[[2]int64]bool{}
	for _, e := range edges {
		if e.EdgeType != graph.EdgeCalls {
			continue
		}
		src, ok1 := fg.byID[e.SourceID]
		tgt, ok2 := fg.byID[e.TargetID]
		if !ok1 || !ok2 || tgt.Kind == graph.KindFile {
			continue
		}
		fe := footprintEdge{target: e.TargetID, precise: e.Provenance == graph.ProvPrecise, targetName: tgt.Symbol}
		if isFootprintTest(src) {
			fg.testCalls[e.TargetID] = append(fg.testCalls[e.TargetID], testCall{source: e.SourceID, edge: fe})
			continue
		}
		fg.out[e.SourceID] = append(fg.out[e.SourceID], fe)
		if seen[[2]int64{e.SourceID, e.TargetID}] {
			continue
		}
		seen[[2]int64{e.SourceID, e.TargetID}] = true
		fg.prodIn[e.TargetID]++
	}
	return fg
}

func isFootprintTest(n graph.Node) bool {
	return isFeatureTestNode(&n)
}

// choose picks the single plausible definition a name-based call from caller
// refers to when the callee name has several definitions: the only candidate in
// the caller's directory, else the only one in the caller's subsystem, else
// none (0).
func (fg *footprintGraph) choose(caller graph.Node, name string) int64 {
	dir, sub := path.Dir(caller.FilePath), graph.SubsystemOf(caller.FilePath)
	key := dir + "\x00" + sub + "\x00" + name
	if id, ok := fg.chosen[key]; ok {
		return id
	}
	var inDir, inSub []int64
	for _, id := range fg.cands[name] {
		c := fg.byID[id]
		if path.Dir(c.FilePath) == dir {
			inDir = append(inDir, id)
		}
		if graph.SubsystemOf(c.FilePath) == sub {
			inSub = append(inSub, id)
		}
	}
	var pick int64
	switch {
	case len(inDir) == 1:
		pick = inDir[0]
	case len(inDir) == 0 && len(inSub) == 1:
		pick = inSub[0]
	}
	fg.chosen[key] = pick
	return pick
}

// foreignTerritory guards against definitions that share one FQN (every
// `main.main` of a multi-binary repo, repeated `init`s): edges are attributed
// by FQN, so a duplicate caller inherits its twins' calls. An edge whose target
// lives under another twin's directory (and not under the caller's own, deepest
// twin directory first) is not trusted.
func (fg *footprintGraph) foreignTerritory(caller graph.Node, target int64) bool {
	twins := fg.dupDirs[caller.FQN]
	if len(twins) == 0 {
		return false
	}
	tdir := path.Dir(fg.byID[target].FilePath)
	deepest := ""
	for _, d := range twins {
		if (tdir == d || (d != "." && strings.HasPrefix(tdir, d+"/"))) && len(d) > len(deepest) {
			deepest = d
		}
	}
	return deepest != "" && deepest != path.Dir(caller.FilePath)
}

// followable decides whether a call edge from caller may be followed.
// Precise edges always are; name edges to an ambiguous callee name only when
// the disambiguation rule selects exactly that target.
func (fg *footprintGraph) followable(caller graph.Node, e footprintEdge) (follow, ambiguous bool) {
	if fg.foreignTerritory(caller, e.target) {
		return false, true
	}
	if e.precise || len(fg.cands[e.targetName]) <= 1 {
		return true, false
	}
	if fg.choose(caller, e.targetName) == e.target {
		return true, false
	}
	return false, true
}

// walk returns the bounded footprint reached from the seeds plus the visited
// node ids (for confidence classification).
func (fg *footprintGraph) walk(seeds []int64, depth int) (*FeatureFootprint, []int64) {
	fp := &FeatureFootprint{Depth: depth, Subsystems: []FeatureSubsystem{}}
	visited := map[int64]bool{}
	depthOf := map[int64]int{}
	var order []int64
	type item struct {
		id    int64
		depth int
	}
	var queue []item
	for _, id := range seeds {
		if _, ok := fg.byID[id]; !ok || visited[id] {
			continue
		}
		visited[id] = true
		order = append(order, id)
		queue = append(queue, item{id, 0})
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= depth {
			continue
		}
		caller := fg.byID[cur.id]
		for _, e := range fg.out[cur.id] {
			if visited[e.target] {
				continue
			}
			tgt := fg.byID[e.target]
			if isFootprintTest(tgt) {
				continue
			}
			follow, ambiguous := fg.followable(caller, e)
			if ambiguous {
				fp.AmbiguousEdges++
				continue
			}
			if !follow {
				continue
			}
			if len(order) >= featureFootprintNodeCap {
				fp.Truncated = true
				continue
			}
			visited[e.target] = true
			depthOf[e.target] = cur.depth + 1
			order = append(order, e.target)
			queue = append(queue, item{e.target, cur.depth + 1})
		}
	}
	files := map[string]bool{}
	subs := map[string]int{}
	for _, id := range order {
		n := fg.byID[id]
		files[n.FilePath] = true
		subs[graph.SubsystemOf(n.FilePath)]++
	}
	fp.Symbols, fp.Files = len(order), len(files)
	for name, c := range subs {
		fp.Subsystems = append(fp.Subsystems, FeatureSubsystem{Name: name, Symbols: c})
	}
	sort.Slice(fp.Subsystems, func(i, j int) bool {
		if fp.Subsystems[i].Symbols != fp.Subsystems[j].Symbols {
			return fp.Subsystems[i].Symbols > fp.Subsystems[j].Symbols
		}
		return fp.Subsystems[i].Name < fp.Subsystems[j].Name
	})
	if len(fp.Subsystems) > featureFootprintSubsystem {
		fp.Subsystems = fp.Subsystems[:featureFootprintSubsystem]
	}
	// Tests count only when they exercise the feature's own code: a seed, or a
	// direct callee of one with at most featureSpecificCallers production callers.
	// Shared infrastructure (a session opener every command calls) is reached by
	// hundreds of tests that say nothing about this feature.
	seeded := map[int64]bool{}
	for _, id := range seeds {
		seeded[id] = true
	}
	tests := map[int64]bool{}
	for _, id := range order {
		if !seeded[id] && (depthOf[id] > 1 || fg.prodIn[id] > featureSpecificCallers) {
			continue
		}
		for _, tc := range fg.testCalls[id] {
			if tests[tc.source] {
				continue
			}
			if follow, _ := fg.followable(fg.byID[tc.source], tc.edge); follow {
				tests[tc.source] = true
			}
		}
	}
	fp.Tests = len(tests)
	return fp, order
}
