package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

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
	// Heuristic marks a covering test found by scanning test files for a reference
	// to the symbol's name (not via the call graph) — see heuristicTestCoverage.
	Heuristic bool `json:"heuristic,omitempty"`
}

// ImpactReport is the flagship impact analysis: who is affected by changing a
// symbol, and which tests cover those paths.
type ImpactReport struct {
	Symbol        string               `json:"symbol"`
	Selector      *SymbolSelector      `json:"selector,omitempty"` // exact selected definition; absent on a name-union query
	Project       string               `json:"project"`
	Found         bool                 `json:"found"`
	Locations     []SymbolRef          `json:"locations,omitempty"`
	DirectCallers []SymbolRef          `json:"direct_callers"`
	BlastRadius   []ImpactNode         `json:"blast_radius"`
	Tests         []ImpactNode         `json:"tests"`
	Untested      bool                 `json:"untested"`
	Note          string               `json:"note,omitempty"`        // set when the name is ambiguous (merges same-named defs)
	Candidates    []AmbiguityCandidate `json:"candidates,omitempty"`  // the merged definition set behind Note; re-query with candidates[i].selector
	Resolution    string               `json:"resolution,omitempty"`  // human sentence set when the call graph is unresolved (e.g. TS/JS/Python without --precise) — direct_callers/blast_radius/tests are unavailable, not empty
	CallGraph     string               `json:"call_graph"`            // stable machine enum: resolved|name|unresolved|none (keep Resolution for the human sentence)
	Annotations   []graph.Annotation   `json:"annotations,omitempty"` // notes/data pinned to this symbol
	// TestCommands is Tests turned into copy/paste-ready runner invocations (see
	// testCommands) — the same derivation codemap_review applies to its
	// covering_tests, so a pre-edit orientation call is just as actionable as the
	// post-edit one.
	TestCommands []string     `json:"test_commands,omitempty"`
	Next         []NextAction `json:"next,omitempty"`
}

// Impact computes impact analysis for a symbol: its definition site(s), direct
// callers, the transitive blast radius (up to depth hops), and which of those
// are tests (coverage). depth <= 0 defaults to 3.
func (svc *Service) Impact(cwd, symbol string, depth int) (*ImpactReport, error) {
	// P1-04: reject blank symbols up front. A blank input would match
	// every file node (stored with Symbol="") and report Found:true
	// with every file as a location — a confidently-wrong answer.
	if !validSymbol(symbol) {
		return &ImpactReport{Found: false, Note: "supply a non-empty symbol name (a blank symbol would match every file node)"}, nil
	}
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
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return emptyImpactReport(name, symbol, depth, nil), nil
	}
	if err != nil {
		return nil, err
	}
	symbol = canonicalSymbol(g, p.ID, symbol) // accept the qualified form hotspots/orphans print

	locs, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, err
	}
	if len(locs) == 0 {
		return emptyImpactReport(name, symbol, depth, nil), nil // symbol not in the graph
	}
	return svc.impactFromLocations(cwd, g, p, symbol, locs, depth, nil)
}

// ImpactBySelector analyzes one exact definition. The selector resolves to the
// current node after each reindex; traversal then uses that ephemeral node id so
// another definition with the same short name is never merged into the result.
func (svc *Service) ImpactBySelector(cwd string, selector SymbolSelector, depth int) (*ImpactReport, error) {
	if depth <= 0 {
		depth = 3
	}
	res, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	project := res.projectName
	if res.project != nil {
		project = res.project.Name
	}
	if !res.found {
		return emptyImpactReport(project, "", depth, &selector), nil
	}
	n := res.node
	return svc.impactFromLocations(cwd, res.graph, res.project, n.Symbol, []graph.Node{n}, depth, &n)
}

func emptyImpactReport(project, symbol string, _ int, selector *SymbolSelector) *ImpactReport {
	return &ImpactReport{
		Symbol: symbol, Selector: selector, Project: project, CallGraph: CallGraphNone,
		DirectCallers: []SymbolRef{}, BlastRadius: []ImpactNode{}, Tests: []ImpactNode{},
	}
}

func (svc *Service) impactFromLocations(cwd string, g *graph.Store, p *graph.Project, symbol string, locs []graph.Node, depth int, exact *graph.Node) (*ImpactReport, error) {
	rep := emptyImpactReport(p.Name, symbol, depth, nil)
	if exact != nil {
		rep.Selector = selectorForNode(*exact)
	}
	rep.Found = true
	for _, n := range locs {
		rep.Locations = append(rep.Locations, nodeToRef(n))
	}
	// call_graph: the stable machine enum a consumer switches on (vs the
	// human Resolution sentence). precise → resolved; a no-name-based-call
	// language on a name-based index → unresolved; else name-based.
	rep.CallGraph = svc.callGraphStatus(g, p.ID, locs)
	if len(locs) > 1 {
		// Lookup is by name, so the callers/blast-radius/tests below are the union
		// across every definition with this name. Say so — a "71 callers" number is
		// misleading when it merges six unrelated Close() methods. The fix depends
		// on the index: name-based can be reindexed --precise; a precise index has
		// exact per-method edges, so only the query name itself is ambiguous.
		rep.Candidates = candidatesFromNodes(locs)
		if rep.CallGraph == CallGraphResolved {
			rep.Note = fmt.Sprintf("%q matches %d definitions — each resolved precisely, but the direct callers, blast radius, and covering tests below still merge all of them; query a more specific name to separate them", symbol, len(locs))
		} else {
			rep.Note = fmt.Sprintf("%q matches %d definitions (name-based) — direct callers, blast radius, and covering tests below merge all of them; reindex with 'codemap index --precise' for exact edges, or use callers/callees --precise for one method", symbol, len(locs))
		}
	}

	var callers []graph.Node
	var err error
	if exact != nil {
		callers, err = g.CallersOfNode(p.ID, exact.ID)
	} else {
		callers, err = g.Callers(p.ID, symbol)
	}
	if err != nil {
		return nil, err
	}
	for _, n := range callers {
		rep.DirectCallers = append(rep.DirectCallers, nodeToRef(n))
	}

	var radius []graph.NodeDepth
	if exact != nil {
		radius, err = g.BlastRadiusFromNode(p.ID, exact.ID, depth)
	} else {
		radius, err = g.BlastRadius(p.ID, symbol, depth)
	}
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
	// Heuristic coverage: when the call graph found NO tests — genuinely untested, OR
	// the test's call edge was lost (a TS test whose call lives in an anonymous
	// it(() => …) callback filtered at index time, #196), OR there's no call graph at
	// all (TS/JS/Python without --precise) — scan test files for a reference to the
	// symbol so a covered symbol isn't reported untested. Conservative + bounded.
	allowHeuristic := true
	if exact != nil {
		// A text scan for the bare name cannot distinguish two same-named
		// definitions. Keep an exact-selector result exact rather than attaching
		// another definition's likely test as if it covered this one.
		if sameName, findErr := g.FindNodesBySymbol(p.ID, symbol); findErr == nil && len(sameName) > 1 {
			allowHeuristic = false
		}
	}
	if len(rep.Tests) == 0 && allowHeuristic {
		if ht := heuristicTestCoverage(g, p.ID, p.Path, symbol); len(ht) > 0 {
			rep.Tests = append(rep.Tests, ht...)
			rep.Untested = false
			rep.Note = joinNote(rep.Note, fmt.Sprintf("%d covering test(s) found heuristically (test files referencing %q) — not confirmed via the call graph; run 'codemap index --precise' to confirm", len(ht), symbol))
		}
	}
	// If the symbol's language has no name-based call graph and the index isn't
	// precise, the empty callers/blast/tests are UNRESOLVED, not absent — say so and
	// don't claim "untested" (that fired for a function with 106 real tests).
	if lang, yes := svc.callGraphUnavailable(g, p.ID, locs); yes {
		rep.Untested = false
		rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — direct callers, blast radius, and covering tests are unresolved (not absent); run 'codemap index --precise' to resolve them", lang)
	}
	// Same derivation codemap_review applies to covering_tests, so impact — the
	// more common pre-edit path — is just as runnable as the post-edit review.
	rep.TestCommands = testCommands(rep.Tests)

	// Surface any annotations pinned to this symbol — by the query name or by a
	// resolved FQN/symbol of its definition sites — so analysis shows pinned
	// knowledge inline.
	targets := []string{symbol}
	for _, l := range rep.Locations {
		targets = append(targets, l.FQN, l.Symbol)
	}
	rep.Annotations = nodeAnnotationsFor(g, p.ID, targets...)
	if rep.CallGraph == CallGraphUnresolved {
		rep.Next = append(rep.Next, nextAction("codemap_index",
			"impact is unresolved without a precise call graph",
			map[string]any{"path": cwd, "precise": true}))
	} else if rep.Untested {
		args := map[string]any{"path": cwd, "symbol": symbol, "depth": depth}
		if rep.Selector != nil {
			args = map[string]any{"path": cwd, "selector": rep.Selector, "depth": depth}
		}
		rep.Next = append(rep.Next, nextAction("codemap_risk",
			"no covering test reaches this symbol; quantify risk before editing",
			args))
	}
	if len(rep.BlastRadius) >= 20 && len(rep.Next) < 2 {
		rep.Next = append(rep.Next, nextAction("codemap_review",
			"the blast radius is large; run diff-scoped review after editing to select regressions",
			map[string]any{"path": cwd, "depth": depth}))
	}
	return rep, nil
}

// RelatedFile is one file structurally related to a target file, with why and how
// strongly. reason ∈ caller|callee|test (codemap's call graph supersedes the
// import-text heuristics a semantic tool would otherwise use). confidence is a
// ranking hint (0..1), not a probability.
type RelatedFile struct {
	RelativePath string  `json:"relative_path"`
	Reason       string  `json:"reason"`
	Confidence   float64 `json:"confidence"`
}

// RelatedFilesReport is the committed cross-tool contract (codemap⇄vecgrep C1):
// the files related to one file, via the resolved call/test graph. indexed
// distinguishes "project not indexed" (false) from "indexed, nothing related"
// (true + empty Related) — a non-error, non-nil answer either way.
type RelatedFilesReport struct {
	Project string        `json:"project"`
	File    string        `json:"file"`
	Indexed bool          `json:"indexed"`
	Related []RelatedFile `json:"related"`
}

// RelatedFiles returns the files related to file through the structural graph:
// the files of its callers, its callees, and the tests covering its symbols —
// aggregated and de-duplicated per (path, reason). It is the one-call replacement
// for a sibling fanning out symbols→impact per symbol. Graceful: an unindexed
// project returns Indexed=false with an empty list, never an error.
func (svc *Service) RelatedFiles(cwd, file string) (*RelatedFilesReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &RelatedFilesReport{Project: name, File: file, Related: []RelatedFile{}}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil // indexed:false
	}
	if err != nil {
		return nil, err
	}
	rep.Indexed = true

	nodes, err := g.NodesInFile(p.ID, file)
	if err != nil {
		return nil, err
	}
	// Keep the strongest confidence seen per (reason, path); skip self-references.
	best := map[string]RelatedFile{}
	add := func(path, reason string, conf float64) {
		if path == "" || path == file {
			return
		}
		key := reason + "\x00" + path
		if ex, ok := best[key]; !ok || conf > ex.Confidence {
			best[key] = RelatedFile{RelativePath: path, Reason: reason, Confidence: conf}
		}
	}
	for _, n := range nodes {
		if n.Symbol == "" { // the file node itself
			continue
		}
		callers, err := g.Callers(p.ID, n.Symbol)
		if err != nil {
			return nil, err
		}
		for _, c := range callers {
			add(c.FilePath, "caller", 0.9)
		}
		callees, err := g.Callees(p.ID, n.Symbol)
		if err != nil {
			return nil, err
		}
		for _, c := range callees {
			add(c.FilePath, "callee", 0.7)
		}
		for _, t := range heuristicTestCoverage(g, p.ID, p.Path, n.Symbol) {
			add(t.File, "test", 1.0)
		}
	}
	for _, rf := range best {
		rep.Related = append(rep.Related, rf)
	}
	// Deterministic order: strongest first, then path, then reason.
	sort.Slice(rep.Related, func(i, j int) bool {
		a, b := rep.Related[i], rep.Related[j]
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if a.RelativePath != b.RelativePath {
			return a.RelativePath < b.RelativePath
		}
		return a.Reason < b.Reason
	})
	return rep, nil
}

// joinNote appends an additional note to an existing one (which may be empty).
func joinNote(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "; " + add
}

// isTestFilePath reports whether a project-relative path looks like a test file by
// the common conventions across codemap's languages (Go _test.go; JS/TS
// .test/.spec; Python test_*.py / *_test.py).
func isTestFilePath(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	for _, ext := range []string{"ts", "tsx", "js", "jsx", "mjs", "cjs"} {
		if strings.HasSuffix(base, ".test."+ext) || strings.HasSuffix(base, ".spec."+ext) {
			return true
		}
	}
	return false
}

// heuristicTestCoverage finds test files that REFERENCE the symbol's bare name (a
// word-boundary match), as a conservative fallback for coverage the call graph
// can't see — a TS test whose call lives in a filtered anonymous it(() => …)
// callback (#196), or any LSP-language symbol on a non-precise index. Returns one
// heuristic ImpactNode per matching test file (bounded). Marked Heuristic so it's
// distinguishable from call-graph-confirmed coverage.
func heuristicTestCoverage(g *graph.Store, projectID int64, root, symbol string) []ImpactNode {
	name := symbol
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:] // bare name; a method is referenced as obj.method, not Type.method
	}
	if name == "" {
		return nil
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return nil
	}
	files, err := g.IndexedFiles(projectID)
	if err != nil {
		return nil
	}
	var out []ImpactNode
	for _, rel := range files {
		if !isTestFilePath(rel) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		if !re.Match(content) {
			continue
		}
		out = append(out, ImpactNode{
			Symbol: filepath.Base(rel), FQN: rel, Kind: graph.KindTest,
			File: rel, StartLine: 1, Heuristic: true,
		})
		if len(out) >= 50 {
			break // bound output; the point is "is it tested", not an exhaustive list
		}
	}
	return out
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

// testCommands turns covering-test nodes into copy/paste-ready commands. It
// deliberately emits a small bounded set and groups Go tests by package so a
// weaker model doesn't have to infer tool syntax from file names. Shared by
// every surface that carries a Tests/covering_tests list — codemap_review's
// covering_tests, codemap_impact/context's tests, and context_batch's
// per-symbol bundles — so the same symbol always yields the same commands
// regardless of which report produced the Tests list (a Heuristic:true node
// is treated the same as a call-graph-confirmed one; it still names a real
// test file worth running).
func testCommands(tests []ImpactNode) []string {
	goTests := map[string][]string{}
	other := map[string]bool{}
	for _, t := range tests {
		ext := strings.ToLower(filepath.Ext(t.File))
		switch ext {
		case ".go":
			dir := filepath.Dir(t.File)
			if dir == "." {
				dir = ""
			}
			if t.Symbol != "" {
				goTests[dir] = append(goTests[dir], regexp.QuoteMeta(t.Symbol))
			}
		case ".ts", ".tsx", ".js", ".jsx":
			other["bun test "+t.File] = true
		case ".py":
			cmd := "pytest " + t.File
			if t.Symbol != "" {
				cmd += "::" + t.Symbol
			}
			other[cmd] = true
		}
	}
	cmds := make([]string, 0, len(goTests)+len(other))
	dirs := make([]string, 0, len(goTests))
	for dir := range goTests {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		names := dedupStrings(goTests[dir])
		sort.Strings(names)
		pkg := "./"
		if dir != "" {
			pkg += filepath.ToSlash(dir)
		}
		// A giant -run regex is worse than the inference work it saves: it floods
		// context, hits shell limits, and is fragile when a changed test file maps
		// to many subtests. Above the small focused threshold, run the package.
		if len(names) > 12 {
			cmds = append(cmds, "go test "+pkg)
		} else {
			cmds = append(cmds, fmt.Sprintf("go test %s -run '^(%s)$'", pkg, strings.Join(names, "|")))
		}
	}
	for cmd := range other {
		cmds = append(cmds, cmd)
	}
	sort.Strings(cmds)
	if len(cmds) > 10 {
		cmds = cmds[:10]
	}
	return cmds
}
