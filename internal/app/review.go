package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// Review tuning. These bound work on a pathological changeset and decide when a
// changed symbol is "load-bearing" enough to flag as a hotspot.
const (
	reviewMaxSymbols        = 200 // cap Impact calls on a huge diff (then note the elision)
	reviewMaxPartialErrors  = 20  // keep degraded reports useful without letting repeated failures dominate the payload
	reviewHotspotMinCallers = 8   // direct callers at/above which a changed symbol is a hotspot
	reviewGitTimeout        = 10 * time.Second
)

// ReviewOpts selects which diff to analyze. Mode is "working" (everything since
// HEAD — staged + unstaged + untracked), "staged" (the index only), or "since"
// (between Since and the working tree). Depth bounds each symbol's blast radius.
type ReviewOpts struct {
	Mode  string
	Since string
	Depth int
}

// ReviewFile is one changed file with how many of its changed lines mapped to
// indexed symbols — so an agent sees coverage of the diff itself, not just a list.
type ReviewFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Symbols int    `json:"symbols"`
}

// ReviewDeletionAnalysis explains whether deleted files could be analyzed from
// the last indexed graph. Deleted files have no post-image hunks, so review must
// intentionally use the retained index snapshot; after a reindex those nodes are
// pruned and the historical impact is no longer available.
type ReviewDeletionAnalysis struct {
	Files    int    `json:"files"`
	Analyzed int    `json:"analyzed"`
	Missing  int    `json:"missing"`
	Source   string `json:"source"` // last_index
	Complete bool   `json:"complete"`
}

// ReviewPartialError is one bounded, non-fatal failure encountered while
// building a review. Code is the stable machine signal; Message preserves the
// actionable underlying error for humans. File identifies mapping failures;
// Symbol identifies per-definition impact failures. Both are absent for
// project-wide stages such as staleness.
type ReviewPartialError struct {
	Stage   string     `json:"stage"` // staleness | mapping | impact
	Code    string     `json:"code"`  // stable machine-readable failure category
	Message string     `json:"message"`
	File    string     `json:"file,omitempty"`
	Symbol  *SymbolRef `json:"symbol,omitempty"`
}

// ReviewReport is the diff-scoped intelligence bundle: the symbols a changeset
// touches, the union of their blast radius, the tests that cover them (regression
// test selection), the changed symbols that are untested or load-bearing, and the
// same freshness/resolution honesty signals every other codemap report carries.
// It is the keystone an agent harness queries after editing — "what did I just
// affect, and what should I run?" — in one call.
type ReviewReport struct {
	SchemaVersion  int          `json:"schema_version"`
	Project        string       `json:"project"`
	Mode           string       `json:"mode"`
	Since          string       `json:"since,omitempty"`
	Depth          int          `json:"depth"` // effective blast-radius depth (after the <=0 → 3 default)
	IsRepo         bool         `json:"is_repo"`
	Indexed        bool         `json:"indexed"`
	ChangedFiles   []ReviewFile `json:"changed_files"`
	ChangedSymbols []SymbolRef  `json:"changed_symbols"`
	// AnalysisComplete is true only when the index is fresh, every mapped changed
	// symbol was analyzed, and all supporting stages succeeded. The counts make
	// an omitted tail or failed symbol visible without changing ChangedSymbols'
	// historical capped shape.
	AnalysisComplete       bool                 `json:"analysis_complete"`
	TotalSymbols           int                  `json:"total_symbols"`
	AnalyzedSymbols        int                  `json:"analyzed_symbols"`
	TruncatedSymbols       int                  `json:"truncated_symbols"`
	PartialErrors          []ReviewPartialError `json:"partial_errors,omitempty"`
	PartialErrorsTruncated int                  `json:"partial_errors_truncated,omitempty"`
	BlastRadius            []ImpactNode         `json:"blast_radius"`
	CoveringTests          []ImpactNode         `json:"covering_tests"`
	UntestedSymbols        []SymbolRef          `json:"untested_symbols"`   // changed symbols with no covering test (P1-19: bare "untested" name was the list here vs a bool elsewhere; renamed for consistency)
	Hotspots               []SymbolRef          `json:"hotspots,omitempty"` // changed symbols with many direct callers
	Stale                  bool                 `json:"stale"`
	Staleness              *index.Staleness     `json:"staleness,omitempty"`
	Resolution             string               `json:"resolution,omitempty"` // human sentence set when some changed symbols' call graph is unresolved (TS/JS/Py without --precise)
	CallGraph              string               `json:"call_graph,omitempty"` // stable machine enum: resolved|name|unresolved|none — the worst (least-confident) across changed symbols
	// Risk is the aggregate change-risk band for the whole diff, folded from the
	// per-symbol risk signals over every changed symbol so a harness can gate
	// verification on ONE band (instead of fanning `risk` out per symbol). It is
	// absent for complete zero-symbol and early graceful reports; finalized
	// incomplete reports carry level unknown even when no symbol maps safely.
	Risk *ReviewRisk `json:"risk,omitempty"`
	// DeletionAnalysis is present only when the diff deletes files. It lets a
	// harness distinguish a deletion whose prior definitions were analyzed from
	// one whose nodes were already pruned from the index.
	DeletionAnalysis *ReviewDeletionAnalysis `json:"deletion_analysis,omitempty"`
	Note             string                  `json:"note,omitempty"`
	TestCommands     []string                `json:"test_commands,omitempty"`
	Next             []NextAction            `json:"next,omitempty"`
	// Gate is the computed CI-gate signal so a harness reproduces the CLI
	// --fail-on-risk/--fail-on-untested exit-6 logic from the report alone (D9).
	Gate *ReviewGate `json:"gate,omitempty"`
}

// Review computes diff-scoped impact + test selection for the working tree. It
// reuses the existing primitives end to end: git for the diff, Symbols for
// hunk→symbol resolution, and Impact for each changed symbol's blast radius and
// covering tests. Never errors on a non-repo or unindexed project — it degrades to
// a plain changed-file list with a Note so an agent always gets an actionable answer.
func (svc *Service) Review(cwd string, opts ReviewOpts) (rep *ReviewReport, err error) {
	// Attach the computed CI-gate signal on every return path so MCP/CLI
	// consumers can reproduce the --fail-on-risk/--fail-on-untested logic from
	// the report alone (D9).
	defer func() {
		if rep != nil {
			rep.Gate = rep.ComputeGate()
		}
	}()
	if opts.Depth <= 0 {
		opts.Depth = 3
	}
	mode := opts.Mode
	if mode == "" {
		mode = "working"
	}
	switch mode {
	case "working", "staged":
	case "since":
		if opts.Since == "" {
			return nil, fmt.Errorf("review mode %q requires a non-empty since ref", mode)
		}
	default:
		return nil, fmt.Errorf("unsupported review mode %q: must be working, staged, or since", mode)
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep = &ReviewReport{
		SchemaVersion: 1, Project: name, Mode: mode, Since: opts.Since, Depth: opts.Depth,
		ChangedFiles: []ReviewFile{}, ChangedSymbols: []SymbolRef{},
		BlastRadius: []ImpactNode{}, CoveringTests: []ImpactNode{}, UntestedSymbols: []SymbolRef{},
	}
	indexed, _, perr := svc.Indexed(cwd)
	if perr != nil {
		return nil, perr
	}
	rep.Indexed = indexed

	// Defensive validation of agent-supplied `--since` ref BEFORE any exec call —
	// option-injection guard (P0-03). A leading-dash ref is parsed as a git
	// option even past `--`; refuse here so we never write an arbitrary file via
	// --output or similar. Return a graceful Note instead of a hard error so the
	// harness always gets an actionable answer.
	if mode == "since" && !git.ValidRef(opts.Since) {
		rep.Note = fmt.Sprintf("invalid --since ref %q: must be non-empty and must not start with '-'; pass a commit, branch, or tag name", opts.Since)
		return rep, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), reviewGitTimeout)
	defer cancel()

	root, gerr := git.RepoRoot(ctx, cwd)
	if gerr != nil {
		rep.Note = "not a git repository — codemap review needs git to compute the diff"
		return rep, nil
	}
	rep.IsRepo = true

	changed, cerr := git.ChangedFiles(ctx, root, mode, opts.Since)
	if cerr != nil {
		return nil, fmt.Errorf("git diff failed: %w", cerr)
	}

	// Always report the changed files, even unindexed — the agent still sees what
	// moved. Without an index there is no graph to walk for impact/tests.
	if !rep.Indexed {
		for _, cf := range changed {
			rep.ChangedFiles = append(rep.ChangedFiles, ReviewFile{Path: cf.Path, Status: cf.Status})
		}
		rep.Note = joinNote(rep.Note, "project not indexed — run 'codemap index' for blast radius and test selection")
		return rep, nil
	}

	if st, serr := svc.Staleness(cwd); serr != nil {
		rep.addPartialError(ReviewPartialError{
			Stage: "staleness", Code: "staleness_failed",
			Message: fmt.Sprintf("could not determine index staleness: %v", serr),
		})
	} else if st != nil {
		rep.Staleness = st
		rep.Stale = st.Any()
	}

	// 1) Resolve each changed file's hunks to the enclosing indexed symbols.
	mapReviewChangedFiles(rep, changed, func(path string) ([]SymbolRef, error) {
		return svc.symbolsForChangedFile(cwd, root, path)
	})
	if rep.DeletionAnalysis != nil {
		rep.DeletionAnalysis.Complete = rep.DeletionAnalysis.Missing == 0
		if rep.DeletionAnalysis.Analyzed > 0 {
			rep.Note = joinNote(rep.Note, "deleted-file impact uses definitions retained in the last indexed snapshot — run selected tests before reindexing, then refresh the index")
		}
		if rep.DeletionAnalysis.Missing > 0 {
			rep.Note = joinNote(rep.Note, fmt.Sprintf("%d deleted file(s) no longer have indexed definitions; their prior impact is unavailable", rep.DeletionAnalysis.Missing))
		}
	}

	rep.TotalSymbols = len(rep.ChangedSymbols)
	if len(rep.ChangedSymbols) > reviewMaxSymbols {
		rep.TruncatedSymbols = len(rep.ChangedSymbols) - reviewMaxSymbols
		rep.ChangedSymbols = rep.ChangedSymbols[:reviewMaxSymbols]
	}

	// 2) Union the blast radius + covering tests across changed symbols; flag the
	//    untested and load-bearing ones. Analyze them sharing one project-wide
	//    load (resolved-coverage map, heuristic test-file scan, coverage hint)
	//    across the whole diff instead of re-doing it per symbol — review is the
	//    hot path an agent harness hits after every edit, and a 200-symbol diff
	//    would otherwise re-scan call_graph_coverage and re-read every test file
	//    up to 200×. Falls back to the per-symbol path if the shared context
	//    can't be built.
	analyze := svc.reviewImpactAnalyzer(cwd, rep.ChangedSymbols, opts.Depth)
	if analyze == nil {
		analyze = func(s SymbolRef) (*ImpactReport, error) {
			// ChangedSymbols already carry a durable source identity. Keep the
			// per-symbol review exact instead of sending the FQN through the legacy
			// name resolver, which canonicalizes to a short name and unions unrelated
			// same-named definitions even on a precise graph.
			return svc.ImpactBySelector(cwd, SymbolSelector{
				File: s.File, StartLine: s.StartLine, FQN: s.FQN, Kind: s.Kind,
			}, opts.Depth)
		}
	}
	imps := analyzeReviewImpacts(rep, rep.ChangedSymbols, analyze)
	// Fold the per-symbol resolution + risk signals into one diff-scoped band.
	// call_graph is the worst (least-confident) across changed symbols — a
	// review is only as trustworthy as its least-resolved change. Risk reuses
	// the `risk` command's factor model, aggregated (max severity per factor)
	// and combined with probabilistic OR.
	finalizeReviewAnalysis(rep, imps)

	// Shallowest-first blast radius (the closest callers matter most), stable by file.
	sort.SliceStable(rep.BlastRadius, func(i, j int) bool {
		if rep.BlastRadius[i].Depth != rep.BlastRadius[j].Depth {
			return rep.BlastRadius[i].Depth < rep.BlastRadius[j].Depth
		}
		return rep.BlastRadius[i].File < rep.BlastRadius[j].File
	})

	if rep.TruncatedSymbols > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("large changeset — analyzed the first %d changed symbols (%d more elided; use a narrower --since)", reviewMaxSymbols, rep.TruncatedSymbols))
	}
	if len(rep.ChangedSymbols) == 0 && len(changed) > 0 {
		rep.Note = joinNote(rep.Note, "changed lines don't map to indexed symbols (comments, imports, or unindexed/untracked files) — nothing to analyze")
	}
	if partials := len(rep.PartialErrors) + rep.PartialErrorsTruncated; partials > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("review analysis is incomplete — %d non-fatal error(s); inspect partial_errors in --json output", partials))
	}
	rep.TestCommands = testCommands(rep.CoveringTests)
	// For deletions, run the selected regressions while the last-index snapshot
	// still carries the removed definitions. Reindexing first would prune the
	// exact evidence review just used.
	if rep.DeletionAnalysis != nil && rep.DeletionAnalysis.Analyzed > 0 && len(rep.TestCommands) > 0 {
		rep.Next = append(rep.Next, nextAction("terminal",
			"run the selected regression tests before reindexing removes deleted-file graph evidence",
			map[string]any{"command": rep.TestCommands[0]}))
	}
	if rep.Stale {
		why := "the index is stale; refresh it before trusting diff-scoped impact"
		if rep.DeletionAnalysis != nil && rep.DeletionAnalysis.Analyzed > 0 {
			why = "after reviewing deleted-file impact, refresh the index to remove deleted definitions"
		}
		rep.Next = append(rep.Next, nextAction("codemap_index",
			why,
			map[string]any{"path": cwd}))
	}
	if len(rep.TestCommands) > 0 && len(rep.Next) < 2 {
		rep.Next = append(rep.Next, nextAction("terminal",
			"run the selected regression tests that cover this changeset",
			map[string]any{"command": rep.TestCommands[0]}))
	} else if len(rep.UntestedSymbols) > 0 && len(rep.Next) < 2 {
		rep.Next = append(rep.Next, nextAction("codemap_risk",
			"some changed symbols have no covering tests; inspect their risk before declaring the change verified",
			map[string]any{"path": cwd, "symbol": rep.UntestedSymbols[0].Symbol, "depth": opts.Depth}))
	}
	return rep, nil
}

// addPartialError appends a degraded-analysis diagnostic up to the public
// payload cap, then counts the omitted tail. AnalysisComplete consults both
// fields, so a capped error list can never make a partial result look complete.
func (rep *ReviewReport) addPartialError(partial ReviewPartialError) {
	if len(rep.PartialErrors) < reviewMaxPartialErrors {
		rep.PartialErrors = append(rep.PartialErrors, partial)
		return
	}
	rep.PartialErrorsTruncated++
}

// analyzeReviewImpacts applies one impact analyzer to each mapped symbol and
// unions the successful results into rep. Keeping the callback at this seam
// makes partial-failure behavior deterministic and directly testable without
// weakening Service's concrete package boundaries.
func analyzeReviewImpacts(rep *ReviewReport, symbols []SymbolRef, analyze func(SymbolRef) (*ImpactReport, error)) []*ImpactReport {
	seenBlast, seenTest := map[string]bool{}, map[string]bool{}
	imps := make([]*ImpactReport, 0, len(symbols))
	for _, s := range symbols {
		imp, err := analyze(s)
		if err != nil {
			sym := s
			rep.addPartialError(ReviewPartialError{
				Stage: "impact", Code: "impact_failed",
				Message: fmt.Sprintf("impact analysis failed: %v", err), Symbol: &sym,
			})
			continue
		}
		if imp == nil {
			sym := s
			rep.addPartialError(ReviewPartialError{
				Stage: "impact", Code: "impact_unavailable",
				Message: "impact analysis returned no report", Symbol: &sym,
			})
			continue
		}
		if !imp.Found {
			sym := s
			rep.addPartialError(ReviewPartialError{
				Stage: "impact", Code: "symbol_not_found",
				Message: "changed symbol was not found during impact analysis", Symbol: &sym,
			})
			continue
		}

		rep.AnalyzedSymbols++
		imps = append(imps, imp)
		if imp.Resolution != "" && rep.Resolution == "" {
			rep.Resolution = imp.Resolution
		}
		for _, b := range imp.BlastRadius {
			if key := symKey(b.FQN, b.File, b.StartLine); !seenBlast[key] {
				seenBlast[key] = true
				rep.BlastRadius = append(rep.BlastRadius, b)
			}
		}
		for _, tn := range imp.Tests {
			if key := symKey(tn.FQN, tn.File, tn.StartLine); !seenTest[key] {
				seenTest[key] = true
				rep.CoveringTests = append(rep.CoveringTests, tn)
			}
		}
		if imp.Untested && imp.Resolution == "" {
			rep.UntestedSymbols = append(rep.UntestedSymbols, s)
		}
		if len(imp.DirectCallers) >= reviewHotspotMinCallers {
			rep.Hotspots = append(rep.Hotspots, s)
		}
	}
	return imps
}

func finalizeReviewAnalysis(rep *ReviewReport, imps []*ImpactReport) {
	if len(imps) > 0 {
		rep.CallGraph = worstCallGraph(imps)
		rep.Risk = aggregateReviewRisk(imps)
	}
	rep.AnalysisComplete = !rep.Stale && rep.TruncatedSymbols == 0 && len(rep.PartialErrors) == 0 && rep.PartialErrorsTruncated == 0
	if rep.DeletionAnalysis != nil && !rep.DeletionAnalysis.Complete {
		rep.AnalysisComplete = false
	}
	// Risk from a successful subset must never read as authoritative for the
	// entire diff. Keep any observed score/factors, but make the aggregate band
	// explicitly unknown. When no symbol can be mapped safely, still emit an
	// unknown band instead of silently omitting risk as though nothing changed.
	if !rep.AnalysisComplete {
		if rep.Risk == nil {
			rep.Risk = &ReviewRisk{Level: "unknown", Factors: []RiskFactor{}}
		} else {
			rep.Risk.Level = "unknown"
		}
	}
}

// mapReviewChangedFiles maps post-image line ranges to indexed symbols and
// records conservative mapping diagnostics. Old definitions or old-image lines
// without post-image counterparts cannot be associated safely after a fresh
// reindex, so deletions inside surviving files are partial rather than an
// authoritative subset. Whole-file deletes use the retained index separately.
func mapReviewChangedFiles(rep *ReviewReport, changed []git.ChangedFile, lookup func(string) ([]SymbolRef, error)) {
	seenSym := map[string]bool{}
	for _, cf := range changed {
		rf := ReviewFile{Path: cf.Path, Status: cf.Status}
		// Documentation, assets, and configuration files are deliberately outside
		// the structural graph. Do not turn their ordinary edits, deletions, or
		// renames into false mapping failures (and policy-gate failures). For a
		// rename, consider both sides so moving source to or from a non-source
		// extension still receives conservative structural treatment.
		structural := reviewStructuralPath(cf.Path) || (cf.Renamed && reviewStructuralPath(cf.OldPath))
		var syms []SymbolRef
		var err error
		if structural {
			syms, err = lookup(cf.Path)
			if err != nil {
				rep.addPartialError(ReviewPartialError{
					Stage: "mapping", Code: "symbol_mapping_failed", File: cf.Path,
					Message: fmt.Sprintf("could not map changed file to indexed symbols: %v", err),
				})
				syms = nil
			}
		}
		if structural && cf.Status != "D" && cf.DeletedLines > 0 {
			code := "deletion_hunk_unmapped"
			if len(cf.Hunks) == 0 {
				code = "deletion_only_hunk"
			}
			rep.addPartialError(ReviewPartialError{
				Stage: "mapping", Code: code, File: cf.Path,
				Message: fmt.Sprintf("%d deleted line(s) have no post-image range and cannot be mapped to an enclosing symbol", cf.DeletedLines),
			})
		}
		if structural && cf.Status != "D" && cf.DeletedLines == 0 && len(cf.RemovedDefinitions) > 0 {
			rep.addPartialError(ReviewPartialError{
				Stage: "mapping", Code: "removed_definition_unavailable", File: cf.Path,
				Message: fmt.Sprintf("%d old definition(s) are absent from the post-image hunk and cannot be analyzed from the current index", len(cf.RemovedDefinitions)),
			})
		}
		if structural && cf.Renamed && len(syms) == 0 && err == nil {
			rep.addPartialError(ReviewPartialError{
				Stage: "mapping", Code: "rename_unmapped", File: cf.Path,
				Message: fmt.Sprintf("renamed file from %q has no indexed symbols at its new path", cf.OldPath),
			})
		}
		if structural && cf.Status == "D" {
			if rep.DeletionAnalysis == nil {
				rep.DeletionAnalysis = &ReviewDeletionAnalysis{Source: "last_index"}
			}
			rep.DeletionAnalysis.Files++
			if len(syms) == 0 {
				rep.DeletionAnalysis.Missing++
			} else {
				rep.DeletionAnalysis.Analyzed++
			}
		}
		for _, s := range syms {
			// A deleted file has no post-image line ranges. Treat every retained
			// definition as changed; modified/added files still use exact hunks.
			wholeFile := cf.Status == "D" || cf.Status == "?" || cf.Renamed
			if !wholeFile && !symbolTouched(s, cf.Hunks) {
				continue
			}
			key := symKey(s.FQN, s.File, s.StartLine)
			if seenSym[key] {
				continue
			}
			seenSym[key] = true
			rep.ChangedSymbols = append(rep.ChangedSymbols, s)
			rf.Symbols++
		}
		rep.ChangedFiles = append(rep.ChangedFiles, rf)
	}
}

// reviewStructuralPath reports languages for which this build has a shipped
// structural backend (always-on Go or the LSP-backed TS/JS/Python/Vue paths).
// extract.LanguageForPath also recognizes roadmap/config/markup languages, so
// recognition alone is intentionally not enough here.
func reviewStructuralPath(path string) bool {
	switch extract.LanguageForPath(path) {
	case "go", "typescript", "javascript", "python", "vue":
		return true
	default:
		return false
	}
}

// symbolsForChangedFile resolves a diff path (relative to the git root) to its
// indexed symbols, robust to a symlinked checkout. git's root comes back
// symlink-resolved (e.g. /private/var/…) while the project may have been indexed
// via the un-resolved cwd (/var/…), so a path built from the git root won't match
// the indexed paths. Try the project-relative path first (Symbols joins it to cwd,
// matching the index's path form), then fall back to the git-root-absolute path
// (covering a project nested below the repo root). An error is returned only
// when neither lookup provides a trustworthy result.
func (svc *Service) symbolsForChangedFile(cwd, gitRoot, relPath string) ([]SymbolRef, error) {
	return lookupChangedFileSymbols(relPath, filepath.Join(gitRoot, relPath), func(file string) (*SymbolsReport, error) {
		return svc.Symbols(cwd, file)
	})
}

func lookupChangedFileSymbols(relPath, absPath string, lookup func(string) (*SymbolsReport, error)) ([]SymbolRef, error) {
	primary, primaryErr := lookup(relPath)
	if primaryErr == nil && primary != nil && len(primary.Symbols) > 0 {
		return primary.Symbols, nil
	}

	fallback, fallbackErr := lookup(absPath)
	if fallbackErr == nil {
		if fallback == nil {
			return nil, nil
		}
		return fallback.Symbols, nil
	}
	// A successful primary lookup is authoritative even when it found no
	// symbols; the absolute fallback is only needed to recover path-form skew.
	if primaryErr == nil && primary != nil {
		return primary.Symbols, nil
	}
	if primaryErr != nil {
		return nil, fmt.Errorf("relative lookup failed: %v; absolute fallback failed: %w", primaryErr, fallbackErr)
	}
	return nil, fmt.Errorf("relative lookup returned no report; absolute fallback failed: %w", fallbackErr)
}

// symbolTouched reports whether any changed hunk overlaps the symbol's line span.
func symbolTouched(s SymbolRef, hunks []git.LineRange) bool {
	if s.StartLine == 0 {
		return false
	}
	end := s.EndLine
	if end < s.StartLine {
		end = s.StartLine
	}
	for _, h := range hunks {
		if h.Overlaps(s.StartLine, end) {
			return true
		}
	}
	return false
}

// symKey is the de-dup identity for a symbol/node across the union passes.
func symKey(fqn, file string, line int) string {
	return fqn + "\x00" + file + "\x00" + fmt.Sprint(line)
}
