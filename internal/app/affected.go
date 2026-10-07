package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Affected tuning. Everything is bounded so a pathological changeset or a hub
// file imported by thousands of modules cannot make a pre-commit hook hang.
const (
	affectedDefaultDepth = 3   // call-graph hops and import hops when AffectedOpts.Depth <= 0
	affectedMaxDepth     = 10  // hard ceiling on a caller-supplied depth
	affectedMaxSymbols   = 300 // cap impact analyses across the whole changeset (then note the elision)
	affectedMaxImporters = 500 // cap files visited by the import walk per changed file
	affectedMaxReasons   = 10  // cap reasons listed per test (reasons_truncated counts the rest)
)

// Sources an AffectedReport can be computed from.
const (
	AffectedSourceFiles   = "files"   // only the explicitly supplied paths
	AffectedSourceWorking = "working" // git: everything since HEAD (+ untracked)
	AffectedSourceStaged  = "staged"  // git: the index only
	AffectedSourceSince   = "since"   // git: everything since a ref
)

// AffectedOpts selects the changed files. Files are project-relative (absolute
// paths under the project root are accepted). Source picks where the git diff
// comes from: "" infers it (Since set → since, Staged → staged, no Files →
// working, otherwise files only); AffectedSourceFiles forces explicit paths only
// (so an empty --stdin list means "nothing changed", not "the working tree").
// A git source is unioned with any explicit Files. Filter is a glob that
// restricts the reported test files; Depth bounds both the call-graph and the
// import walk (default 3).
type AffectedOpts struct {
	Files  []string
	Source string
	Since  string
	Staged bool
	Filter string
	Depth  int
}

// AffectedTest is one test file to run and why it was selected. Reasons are
// `changed` (the file itself changed), `covers:<symbol>` (the file holds a test
// that reaches a changed symbol through the call graph) and `imports:<file>`
// (the file imports a changed file, directly or transitively).
type AffectedTest struct {
	File             string   `json:"file"`
	Reasons          []string `json:"reasons"`
	ReasonsTruncated int      `json:"reasons_truncated,omitempty"`
}

// AffectedReport answers "which test files should I run for these changed
// files?" in a shape CI and pre-commit hooks can consume without parsing a
// review report. Tests is the sorted, de-duplicated answer; Unmapped lists the
// changed files the index could say nothing about; CallGraph is the weakest
// confidence among the symbols that contributed (same enum as every report).
type AffectedReport struct {
	SchemaVersion    int            `json:"schema_version"`
	Project          string         `json:"project"`
	Source           string         `json:"source"`
	Since            string         `json:"since,omitempty"`
	Depth            int            `json:"depth"`
	Indexed          bool           `json:"indexed"`
	Files            []string       `json:"files"`
	Tests            []AffectedTest `json:"tests"`
	Unmapped         []string       `json:"unmapped"`
	Filter           string         `json:"filter,omitempty"`
	FilteredOut      int            `json:"filtered_out,omitempty"` // tests dropped by Filter
	CallGraph        string         `json:"call_graph"`
	AnalysisComplete bool           `json:"analysis_complete"`
	Stale            bool           `json:"stale"`
	Note             string         `json:"note,omitempty"`
}

// TestFiles returns the selected test file paths in report order.
func (r *AffectedReport) TestFiles() []string {
	out := make([]string, 0, len(r.Tests))
	for _, t := range r.Tests {
		out = append(out, t.File)
	}
	return out
}

// Affected maps changed files to the test files that should run. It reuses the
// primitives review and impact already use: git.ChangedFiles for the diff, the
// indexed symbols of each file, and ImpactBySelector's covering tests (call
// graph plus its heuristic text scan), then adds test files that import a
// changed file (file→file import edges, up to Depth hops) and changed files that
// are tests themselves. Resolution honesty is inherited: call_graph is the
// weakest confidence among contributing symbols and a note says when coverage is
// name-based or unresolved.
func (svc *Service) Affected(cwd string, opts AffectedOpts) (*AffectedReport, error) {
	depth := opts.Depth
	if depth <= 0 {
		depth = affectedDefaultDepth
	}
	if depth > affectedMaxDepth {
		depth = affectedMaxDepth
	}
	source := opts.Source
	switch source {
	case "":
		switch {
		case opts.Since != "":
			source = AffectedSourceSince
		case opts.Staged:
			source = AffectedSourceStaged
		case len(opts.Files) == 0:
			source = AffectedSourceWorking
		default:
			source = AffectedSourceFiles
		}
	case AffectedSourceFiles, AffectedSourceWorking, AffectedSourceStaged:
	case AffectedSourceSince:
		if opts.Since == "" {
			return nil, fmt.Errorf("affected source %q requires a non-empty since ref", source)
		}
	default:
		return nil, fmt.Errorf("unsupported affected source %q: must be files, working, staged, or since", source)
	}
	if source == AffectedSourceSince && !git.ValidRef(opts.Since) {
		return nil, fmt.Errorf("invalid --since ref %q: must be non-empty and must not start with '-'; pass a commit, branch, or tag name", opts.Since)
	}
	filter, err := compileAffectedFilter(opts.Filter)
	if err != nil {
		return nil, err
	}

	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &AffectedReport{
		SchemaVersion: 1, Project: name, Source: source, Depth: depth, Indexed: found,
		Files: []string{}, Tests: []AffectedTest{}, Unmapped: []string{},
		Filter: opts.Filter, CallGraph: CallGraphNone,
	}
	if source == AffectedSourceSince {
		rep.Since = opts.Since
	}
	if !found {
		rep.Note = "project not indexed — run 'codemap index' first"
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	proj, err := g.GetProjectByID(pid)
	if err != nil {
		return nil, err
	}

	// 1) Collect the changed paths: git diff (when asked) plus explicit files.
	type input struct{ raw, gitRoot string }
	var inputs []input
	if source != AffectedSourceFiles {
		ctx, cancel := context.WithTimeout(context.Background(), reviewGitTimeout)
		defer cancel()
		root, gerr := git.RepoRoot(ctx, cwd)
		if gerr != nil {
			return nil, fmt.Errorf("not a git repository — codemap affected needs git to compute the diff (pass files or --stdin instead): %w", gerr)
		}
		changed, cerr := git.ChangedFiles(ctx, root, source, opts.Since)
		if cerr != nil {
			return nil, fmt.Errorf("git diff failed: %w", cerr)
		}
		for _, cf := range changed {
			inputs = append(inputs, input{raw: cf.Path, gitRoot: root})
		}
	}
	for _, f := range opts.Files {
		inputs = append(inputs, input{raw: f})
	}

	seenFile := map[string]bool{}
	var files []string
	var outside []string
	for _, in := range inputs {
		rel, ok := affectedRel(proj.Path, cwd, in.gitRoot, in.raw)
		if rel == "" {
			continue
		}
		if seenFile[rel] {
			continue
		}
		seenFile[rel] = true
		if !ok {
			outside = append(outside, rel)
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	rep.Files = append(rep.Files, files...)
	sort.Strings(outside)

	if st, serr := svc.Staleness(cwd); serr == nil && st != nil {
		rep.Stale = st.Any()
	}

	// 2) Per file: its indexed symbols, own-test status, and import walk.
	type testAcc struct{ reasons map[string]bool }
	tests := map[string]*testAcc{}
	addTest := func(file, reason string) {
		acc := tests[file]
		if acc == nil {
			acc = &testAcc{reasons: map[string]bool{}}
			tests[file] = acc
		}
		acc.reasons[reason] = true
	}

	var symbols []SymbolRef
	incomplete := false
	var notes []string
	outsideSet := map[string]bool{}
	for _, o := range outside {
		outsideSet[o] = true
	}
	for _, rel := range files {
		isTest := graph.IsTestFilePath(rel)
		if isTest {
			addTest(rel, "changed")
		}
		if outsideSet[rel] {
			rep.Unmapped = append(rep.Unmapped, rel)
			if !isTest {
				incomplete = true
			}
			continue
		}
		nodes, nerr := g.NodesInFile(pid, rel)
		if nerr != nil {
			return nil, nerr
		}
		if len(nodes) == 0 {
			if !isTest {
				rep.Unmapped = append(rep.Unmapped, rel)
				if reviewStructuralPath(rel) {
					incomplete = true
				}
			}
			continue
		}
		refs := make([]SymbolRef, 0, len(nodes))
		for _, n := range nodes {
			if n.Kind == graph.KindFile {
				continue
			}
			refs = append(refs, nodeToRef(n))
		}
		symbols = append(symbols, definableSymbols(refs)...)

		importers, truncated, ierr := svc.importingTests(g, pid, rel, depth)
		if ierr != nil {
			return nil, ierr
		}
		for _, t := range importers {
			addTest(t, "imports:"+rel)
		}
		if truncated {
			incomplete = true
			notes = append(notes, fmt.Sprintf("import walk from %s stopped after %d files", rel, affectedMaxImporters))
		}
	}

	// 3) Covering tests via the call graph, one shared project-wide load.
	if len(symbols) > affectedMaxSymbols {
		notes = append(notes, fmt.Sprintf("large changeset — analyzed the first %d of %d changed symbols", affectedMaxSymbols, len(symbols)))
		symbols = symbols[:affectedMaxSymbols]
		incomplete = true
	}
	analyze := svc.reviewImpactAnalyzer(cwd, symbols, depth)
	if analyze == nil {
		analyze = func(s SymbolRef) (*ImpactReport, error) {
			return svc.ImpactBySelector(cwd, SymbolSelector{
				File: s.File, StartLine: s.StartLine, FQN: s.FQN, Kind: s.Kind,
			}, depth)
		}
	}
	var imps []*ImpactReport
	failed := 0
	for _, s := range symbols {
		imp, ierr := analyze(s)
		if ierr != nil || imp == nil || !imp.Found {
			failed++
			continue
		}
		imps = append(imps, imp)
		label := s.FQN
		if label == "" {
			label = s.Symbol
		}
		for _, tn := range imp.Tests {
			if tn.File != "" {
				addTest(tn.File, "covers:"+label)
			}
		}
	}
	if failed > 0 {
		incomplete = true
		notes = append(notes, fmt.Sprintf("%d changed symbol(s) could not be analyzed", failed))
	}
	if len(imps) > 0 {
		rep.CallGraph = worstCallGraph(imps)
	}
	switch rep.CallGraph {
	case CallGraphUnresolved:
		notes = append(notes, "call graph is unresolved for a changed file (TS/JS/Python without --precise) — tests come from import edges and text references only; run 'codemap index --precise' for exact coverage")
	case CallGraphName:
		notes = append(notes, "covering tests come from name-based call edges and may over- or under-select; 'codemap index --precise' makes them exact")
	}
	if rep.Stale {
		notes = append(notes, "the index is stale — run 'codemap index' before trusting the selection")
	}
	if len(rep.Unmapped) > 0 {
		notes = append(notes, fmt.Sprintf("%d changed file(s) have no indexed symbols (docs, config, unindexed or outside the project) — nothing could be selected for them", len(rep.Unmapped)))
	}
	rep.AnalysisComplete = !incomplete && !rep.Stale

	// 4) Filter, sort, and cap reasons.
	paths := make([]string, 0, len(tests))
	for p := range tests {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if filter != nil && !filter(p) {
			rep.FilteredOut++
			continue
		}
		reasons := make([]string, 0, len(tests[p].reasons))
		for r := range tests[p].reasons {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		at := AffectedTest{File: p, Reasons: reasons}
		if len(reasons) > affectedMaxReasons {
			at.ReasonsTruncated = len(reasons) - affectedMaxReasons
			at.Reasons = reasons[:affectedMaxReasons]
		}
		rep.Tests = append(rep.Tests, at)
	}
	sort.Strings(rep.Unmapped)
	for _, n := range notes {
		rep.Note = joinNote(rep.Note, n)
	}
	return rep, nil
}

// importingTests walks inbound file→file import edges from file, up to depth
// hops, and returns the test files found (the walk does not continue through a
// test file). Go imports are package-scoped and cannot prove that this exact
// file is imported, so they are skipped — Go coverage comes from call edges.
// truncated reports that the visited-file cap was hit.
func (svc *Service) importingTests(g *graph.Store, pid int64, file string, depth int) (tests []string, truncated bool, err error) {
	visited := map[string]bool{file: true}
	frontier := []string{file}
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		var next []string
		for _, f := range frontier {
			edges, derr := g.InboundFileDependencies(pid, f)
			if derr != nil {
				return nil, false, derr
			}
			for _, e := range edges {
				if e.EdgeType != graph.EdgeImports || e.Source.Language == "go" {
					continue
				}
				src := e.Source.File
				if visited[src] {
					continue
				}
				if len(visited) > affectedMaxImporters {
					return tests, true, nil
				}
				visited[src] = true
				if graph.IsTestFilePath(src) {
					tests = append(tests, src)
					continue
				}
				next = append(next, src)
			}
		}
		frontier = next
	}
	return tests, false, nil
}

// affectedRel normalizes one changed path to the project-relative, slash form
// the index stores. gitRoot is non-empty for paths that came from git (relative
// to the repository root, not the project). ok is false when the path resolves
// outside the project; rel then carries the cleaned input. An empty rel means
// the input was blank.
func affectedRel(projRoot, cwd, gitRoot, raw string) (rel string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	var abs string
	switch {
	case filepath.IsAbs(clean):
		abs = clean
	case gitRoot != "":
		abs = filepath.Join(gitRoot, clean)
	default:
		abs = filepath.Join(projRoot, clean)
		if cwd != "" && cwd != projRoot {
			if _, err := os.Stat(abs); err != nil {
				if alt := filepath.Join(cwd, clean); fileExists(alt) {
					abs = alt
				}
			}
		}
	}
	if r, ok := relUnder(projRoot, abs); ok {
		return filepath.ToSlash(r), true
	}
	// A symlinked checkout (macOS /var → /private/var) makes git's root and the
	// indexed project path disagree; compare their resolved forms.
	if rp, err := filepath.EvalSymlinks(projRoot); err == nil {
		dir, base := filepath.Split(abs)
		if rd, derr := filepath.EvalSymlinks(filepath.Clean(dir)); derr == nil {
			if r, ok := relUnder(rp, filepath.Join(rd, base)); ok {
				return filepath.ToSlash(r), true
			}
		}
	}
	return filepath.ToSlash(clean), false
}

func relUnder(root, abs string) (string, bool) {
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", false
	}
	return r, true
}

// compileAffectedFilter turns a glob into a path predicate. `*` and `?` stay
// within a path segment, `**` crosses segments, and a pattern without a slash
// also matches the file's base name (so `*.test.ts` selects tests anywhere).
// An empty pattern means no filter.
func compileAffectedFilter(glob string) (func(string) bool, error) {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return nil, nil
	}
	re, err := regexp.Compile("^" + globToRegexp(filepath.ToSlash(glob)) + "$")
	if err != nil {
		return nil, fmt.Errorf("invalid --filter glob %q: %w", glob, err)
	}
	baseOnly := !strings.Contains(glob, "/")
	return func(p string) bool {
		p = filepath.ToSlash(p)
		if re.MatchString(p) {
			return true
		}
		return baseOnly && re.MatchString(filepath.Base(p))
	}, nil
}

func globToRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
