/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/spf13/cobra"
)

var (
	callersCmd = &cobra.Command{
		Use:   "callers [<symbol>]",
		Short: "List functions/methods that call a symbol",
		Args:  symbolOrAtArgs,
		RunE:  runCallers,
	}
	referencesCmd = &cobra.Command{
		Use:   "references [<symbol>]",
		Short: "List enclosing scopes that use a function/method as a value",
		Long: `List indexed callback and registration wiring for a function or method.

This follows value-reference edges, not calls. Each result is the enclosing
function, method, or file scope; it is not an exact expression line. Coverage
and confidence remain explicit because an empty result does not prove there is
no dynamic or otherwise-unindexed wiring.`,
		Args: symbolOrAtArgs,
		RunE: runReferences,
	}
	calleesCmd = &cobra.Command{
		Use:   "callees [<symbol>]",
		Short: "List functions/methods that a symbol calls",
		Args:  symbolOrAtArgs,
		RunE:  runCallees,
	}
	impactCmd = &cobra.Command{
		Use:   "impact [<symbol>]",
		Short: "Impact analysis: blast radius (transitive callers) + test coverage",
		Args:  symbolOrAtArgs,
		RunE:  runImpact,
	}
	reviewCmd = &cobra.Command{
		Use:   "review",
		Short: "Diff-scoped impact + test selection: what your changes affect, and which tests to run",
		Args:  cobra.NoArgs,
		RunE:  runReview,
	}
	readOrderCmd = &cobra.Command{
		Use:   "read-order [query]",
		Short: "Where to start reading: rank entrypoints + load-bearing hubs (optionally filtered by a name/path query)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runReadOrder,
	}
	relatedFilesCmd = &cobra.Command{
		Use:   "related-files <file>",
		Short: "Files related to a file via the call/test graph (callers, callees, covering tests)",
		Args:  cobra.ExactArgs(1),
		RunE:  runRelatedFiles,
	}
	fileImpactCmd = &cobra.Command{
		Use:   "file-impact <file>",
		Short: "File-level impact: who depends on this file, its blast radius, and whether it's safe to change/delete",
		Args:  cobra.ExactArgs(1),
		RunE:  runFileImpact,
	}
	riskCmd = &cobra.Command{
		Use:   "risk [<symbol>]",
		Short: "Change-risk score: untested + fan-in + cross-package spread + ambiguity, combined into one number",
		Args:  symbolOrAtArgs,
		RunE:  runRisk,
	}
	symbolAtCmd = &cobra.Command{
		Use:   "symbol-at <file>:<line> [<file>:<line>...]",
		Short: "Resolve one or more file:line positions to their enclosing symbol (FQN, kind, range)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runSymbolAt,
	}
	secretImpactCmd = &cobra.Command{
		Use:   "secret-impact [<KEY>...]",
		Short: "Code blast radius of rotating secret keys: which symbols read each key, + covering tests (value-free — only key NAMES)",
		Long: fmt.Sprintf("Code blast radius of rotating secret key names, including readers and covering tests. Inputs are value-free and bounded to %d unique names of at most %d bytes each, including names loaded with --via-vault.",
			app.MaxSecretKeyNames, app.MaxSecretKeyNameBytes),
		Args: cobra.ArbitraryArgs, // 0 args is valid with --via-vault
		RunE: runSecretImpact,
	}
	requiredKeysCmd = &cobra.Command{
		Use:   "required-keys <entrypoint>",
		Short: "Least-privilege key set: which candidate secret keys an entrypoint's call tree actually reads (for tvault seal/export)",
		Long: fmt.Sprintf("Return the least-privilege candidate key names read by an entrypoint's call tree. Inputs are value-free and bounded to %d unique names of at most %d bytes each, including names loaded with --via-vault.",
			app.MaxSecretKeyNames, app.MaxSecretKeyNameBytes),
		Args: cobra.ExactArgs(1),
		RunE: runRequiredKeys,
	}
	semanticCmd = &cobra.Command{
		Use:     "semantic <query>",
		Aliases: []string{"search"}, // matches the studio "Search" tab and the common mental model
		Short:   "Semantic search across the code graph by meaning",
		Args:    cobra.MinimumNArgs(1),
		RunE:    runSemantic,
	}
	hotspotsCmd = &cobra.Command{
		Use:   "hotspots",
		Short: "List the most-referenced symbols (hubs)",
		RunE:  runHotspots,
	}
	orphansCmd = &cobra.Command{
		Use:   "orphans",
		Short: "List functions/methods with no callers (dead-code candidates)",
		RunE:  runOrphans,
	}
	pathCmd = &cobra.Command{
		Use:   "path <from> <to>",
		Short: "Shortest call path between two symbols",
		Long: `Find the shortest indexed call path between two symbols.

Bare names may match several definitions. Use unique fully qualified names to
select exact endpoints. Human-readable results always state call-graph
confidence. Run 'codemap index --precise' when a name-based or unresolved
result is not strong enough for the decision you are making.`,
		Example: `  codemap path app.Main app.Helper
  codemap path app.Controller.Run app.Store.Save`,
		Args: cobra.ExactArgs(2),
		RunE: runPath,
	}
	symbolsCmd = &cobra.Command{
		Use:   "symbols <file>",
		Short: "List the symbols defined in a file (functions, types, methods, tests)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSymbols,
	}
	findCmd = &cobra.Command{
		Use:   "find <query>",
		Short: "Find symbols by name (fast, offline — no embeddings needed)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runFind,
	}
	grepCmd = &cobra.Command{
		Use:   "grep <pattern>",
		Short: "Search indexed file content for a literal or regex pattern, joined onto its enclosing symbol",
		Long: `Search the content of every indexed file for pattern and resolve each hit to its
enclosing symbol (FQN, kind, source range) via the same machinery as 'codemap symbol-at' — so a
text hit is immediately chainable into codemap_context/callers/source without a manual re-join.

Only searches the indexed file set (files codemap extracted structure from, same excludes as
'codemap index'), not the whole repo — files with no registered extractor (e.g. plain YAML/
Markdown with no codemap language support) are invisible to it, same as 'codemap find'. Reads
are live from disk at query time; a file added since the last index is not yet in that set (see
the "stale" field). Literal substring match by default; --regex for RE2 syntax.

Distinct from 'codemap semantic' (meaning/similarity search over embeddings) and 'codemap find'
(name/FQN search) — this is exact text content search.`,
		Example: `  codemap grep "TODO(security)"
  codemap grep --regex 'STRIPE_[A-Z_]+'
  codemap grep --ignore-case "not implemented"`,
		Args: cobra.ExactArgs(1),
		RunE: runGrep,
	}
	sourceCmd = &cobra.Command{
		Use:   "source [<symbol>]",
		Short: "Print a symbol's source code (the body behind its signature)",
		Args:  symbolOrAtArgs,
		RunE:  runSource,
	}
	contextCmd = &cobra.Command{
		Use:   "context [<symbol>...]",
		Short: "Everything about a symbol in one call: definition, callers, callees, tests, notes (pass several for a batch + shared callers)",
		Args:  contextOrAtArgs,
		RunE:  runContext,
	}
	projectsCmd = &cobra.Command{
		Use:   "projects",
		Short: "List all projects registered with codemap and their index sizes",
		Args:  cobra.NoArgs,
		RunE:  runProjects,
	}
	docsCmd = &cobra.Command{
		Use:   "docs [topic]",
		Short: "Print the agent guide to codemap (topics: overview, workflow, commands, annotations, accuracy, ecosystem)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runDocs,
	}
)

func symbolOrAtArgs(cmd *cobra.Command, args []string) error {
	at, _ := cmd.Flags().GetString("at")
	if at != "" {
		if len(args) > 0 {
			return fmt.Errorf("--at and positional symbol arguments are mutually exclusive")
		}
		return nil
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func contextOrAtArgs(cmd *cobra.Command, args []string) error {
	at, _ := cmd.Flags().GetString("at")
	if at != "" {
		if len(args) > 0 {
			return fmt.Errorf("--at and positional symbol arguments are mutually exclusive")
		}
		return nil
	}
	return cobra.MinimumNArgs(1)(cmd, args)
}

func runCallers(cmd *cobra.Command, args []string) error {
	return runRelation(cmd, args, true)
}

func runCallees(cmd *cobra.Command, args []string) error {
	return runRelation(cmd, args, false)
}

func runReferences(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	symbol := ""
	if len(args) > 0 {
		symbol = args[0]
	}
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	var rep *app.ReferencesReport
	if selector != nil {
		rep, err = svc.ReferencesBySelector(cwd, *selector)
	} else {
		rep, err = svc.References(cwd, symbol)
	}
	if err != nil {
		return err
	}
	if !rep.Found {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", symbol))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}

	fmt.Printf("Value references to %s (%s; not callers)\n", rep.Symbol, rep.Project)
	if rep.Selector != nil {
		fmt.Printf("  selected:      %s — %s:%d\n", disp(rep.Selector.FQN, rep.Symbol), rep.Selector.File, rep.Selector.StartLine)
	} else if rep.DefinitionsTotal > 1 {
		fmt.Printf("  definitions:   %d merged (use --at <file>:<line> for one)\n", rep.DefinitionsTotal)
	}
	fmt.Printf("  scopes:        %d total", rep.ReferencesTotal)
	if rep.ReferencesTruncated > 0 {
		fmt.Printf(" (%d shown, %d omitted; text and JSON are bounded)", len(rep.References), rep.ReferencesTruncated)
	}
	fmt.Println()
	fmt.Printf("  confidence:    %s\n", rep.Confidence)
	fmt.Printf("  coverage:      %s\n", rep.Coverage)
	fmt.Printf("  call graph:    %s (independent of value-reference confidence)\n", rep.CallGraph)
	if rep.Stale {
		fmt.Println("  ⚠ stale index — stored scopes are candidates and newer wiring may be missing")
	}
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	}
	if len(rep.References) == 0 {
		fmt.Println("  no indexed value-reference scopes found — absence is not proof of no wiring")
	} else {
		fmt.Println("  enclosing scopes:")
		for _, site := range rep.References {
			reason := strings.ReplaceAll(site.ConfidenceReason, "_", " ")
			confidence := site.Confidence
			if reason != "" {
				confidence += " · " + reason
			}
			if site.Source.Kind == graph.KindFile {
				fmt.Printf("     file scope — %-30s [%s]\n", site.Source.File, confidence)
				continue
			}
			fmt.Printf("     %-36s %s:%d  [%s]\n",
				disp(site.Source.FQN, site.Source.Symbol), site.Source.File, site.Source.StartLine, confidence)
		}
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

// runRelation implements callers/callees. The only differences are the service
// method and the human label; --json, --precise, and the not-found/annotation flow
// are identical.
func runRelation(cmd *cobra.Command, args []string, callers bool) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	usePrecise := relationPreciseRequested(cmd)
	symbol := ""
	if len(args) > 0 {
		symbol = args[0]
	}
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	if selector == nil && symbol == "" {
		return fmt.Errorf("%s needs a <symbol> argument or --at <file>:<line>", map[bool]string{true: "callers", false: "callees"}[callers])
	}
	var rep *app.RelationReport
	if callers {
		if selector != nil && usePrecise {
			rep, err = svc.PreciseCallersBySelector(cmd.Context(), cwd, *selector)
		} else if selector != nil {
			rep, err = svc.CallersBySelector(cwd, *selector)
		} else if usePrecise {
			rep, err = svc.PreciseCallers(cmd.Context(), cwd, symbol)
		} else {
			rep, err = svc.Callers(cwd, symbol)
		}
	} else {
		if selector != nil && usePrecise {
			rep, err = svc.PreciseCalleesBySelector(cmd.Context(), cwd, *selector)
		} else if selector != nil {
			rep, err = svc.CalleesBySelector(cwd, *selector)
		} else if usePrecise {
			rep, err = svc.PreciseCallees(cmd.Context(), cwd, symbol)
		} else {
			rep, err = svc.Callees(cwd, symbol)
		}
	}
	if err != nil {
		return err
	}
	if !rep.Found {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", symbol))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	label := fmt.Sprintf("%s of %s", map[bool]string{true: "Callers", false: "Callees"}[callers], rep.Symbol)
	if usePrecise && rep.Note == "" { // Note set => precise fell back to name-based; don't mislabel
		label += " (precise, via language server)"
	}
	renderRefsCapped(label, rep.Results, relationsDisplayCap)
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	renderCandidates(rep.Candidates)
	if rep.Resolution != "" {
		fmt.Println("⚠ " + rep.Resolution)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

func selectorFromAtFlag(svc *app.Service, cwd string, cmd *cobra.Command) (*app.SymbolSelector, error) {
	at, _ := cmd.Flags().GetString("at")
	if at == "" {
		return nil, nil
	}
	file, line, err := parseFileLine(at)
	if err != nil {
		return nil, err
	}
	rep, err := svc.SymbolAt(cwd, file, line)
	if err != nil {
		return nil, err
	}
	if rep.Resolution == "none" || rep.Selector == nil {
		return nil, notFoundError("no symbol found at "+at, "check the file path and line number")
	}
	return rep.Selector, nil
}

func relationPreciseRequested(cmd *cobra.Command) bool {
	precise, _ := cmd.Flags().GetBool("precise")
	legacy, _ := cmd.Flags().GetBool("lsp")
	return precise || legacy
}

func runImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	// Resolve the target: a positional name unions matching definitions; --at
	// carries the exact source selector through the impact traversal.
	symbol := ""
	if len(args) > 0 {
		symbol = args[0]
	}
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	if selector == nil && symbol == "" {
		return fmt.Errorf("impact needs a <symbol> argument or --at <file>:<line>")
	}
	var rep *app.ImpactReport
	if selector != nil {
		rep, err = svc.ImpactBySelector(cwd, *selector, depth)
	} else {
		rep, err = svc.Impact(cwd, symbol, depth)
	}
	if err != nil {
		return err
	}
	if !rep.Found {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", symbol))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("Impact of %s (%s)\n", rep.Symbol, rep.Project)
	for _, l := range rep.Locations {
		fmt.Printf("  defined:        %s:%d\n", l.File, l.StartLine)
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	renderCandidates(rep.Candidates)
	fmt.Printf("  direct callers: %d\n", len(rep.DirectCallers))
	fmt.Printf("  blast radius:   %d (depth ≤ %d)\n", len(rep.BlastRadius), depth)
	fmt.Printf("  tests covering: %d\n", len(rep.Tests))
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	} else if rep.Untested {
		fmt.Println("  ⚠ no tests reach this symbol")
	}
	renderAnnotations(rep.Annotations)
	// List the covering tests explicitly so you know what to run — they're a
	// subset of the blast radius, but spelling them out beats hunting for ✓.
	if len(rep.Tests) > 0 {
		fmt.Println("  covering tests (run these):")
		tests, more := capList(rep.Tests, impactTestsCap)
		for _, t := range tests {
			fmt.Printf("     %-36s %s:%d\n", disp(t.FQN, t.Symbol), t.File, t.StartLine)
		}
		if more > 0 {
			fmt.Printf("     … (%d more — use --json for all)\n", more)
		}
	}
	if len(rep.BlastRadius) > 0 {
		fmt.Println("  affected (blast radius):")
		nodes, more := capList(rep.BlastRadius, impactBlastCap)
		for _, n := range nodes {
			marker := " "
			if n.Kind == "test" {
				marker = "✓"
			}
			fmt.Printf("   %s [%d] %-36s %s:%d\n", marker, n.Depth, disp(n.FQN, n.Symbol), n.File, n.StartLine)
		}
		if more > 0 {
			fmt.Printf("   … (%d more — use --json for all, or lower --depth)\n", more)
		}
	}
	return nil
}

// runReview renders diff-scoped intelligence: the symbols the working diff (or a
// --since range, or --staged index) touches, their union blast radius, the tests
// that cover them, and the changed symbols that are untested or load-bearing. The
// human view is a scannable summary; --json carries the full bundle for an agent.
func runReview(cmd *cobra.Command, args []string) error {
	// Validate --fail-on-risk before any query work: a bad value is an
	// operational error (exit 1), fast-failed like any other bad flag.
	failOnRiskThreshold, hasFailOnRisk, err := parseFailOnRiskFlag(cmd)
	if err != nil {
		return err
	}
	failOnUntested, _ := cmd.Flags().GetBool("fail-on-untested")
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	since, _ := cmd.Flags().GetString("since")
	staged, _ := cmd.Flags().GetBool("staged")
	mode := "working"
	if staged {
		mode = "staged"
	} else if since != "" {
		mode = "since"
	}
	rep, err := app.NewService(sess).Review(cwd, app.ReviewOpts{Mode: mode, Since: since, Depth: depth})
	if err != nil {
		return err
	}
	// reviewGate is called AFTER every point below that prints the normal
	// (unchanged) output, human or --json, so a tripped --fail-on-risk/
	// --fail-on-untested threshold only changes the exit code.
	reviewGate := func() error { return reviewGateResult(rep, hasFailOnRisk, failOnRiskThreshold, failOnUntested) }
	if jsonOut(cmd) {
		if err := printJSON(rep); err != nil {
			return err
		}
		return reviewGate()
	}
	if !rep.IsRepo {
		fmt.Println(rep.Note)
		return reviewGate()
	}
	scope := rep.Mode
	if rep.Mode == "since" {
		scope = "since " + rep.Since
	}
	fmt.Printf("Review (%s · %s)\n", rep.Project, scope)
	if !rep.Indexed {
		for _, f := range rep.ChangedFiles {
			fmt.Printf("  %s %s\n", f.Status, f.Path)
		}
		if rep.Note != "" {
			fmt.Println("  ⚠ " + rep.Note)
		}
		return reviewGate()
	}
	if line := reviewStalenessLine(rep); line != "" {
		fmt.Println(line)
	}
	fmt.Printf("  changed files:   %d\n", len(rep.ChangedFiles))
	if line := reviewDeletionAnalysisLine(rep.DeletionAnalysis); line != "" {
		fmt.Println(line)
	}
	fmt.Printf("  changed symbols: %d\n", len(rep.ChangedSymbols))
	fmt.Printf("  blast radius:    %d (depth ≤ %d)\n", len(rep.BlastRadius), rep.Depth)
	fmt.Printf("  covering tests:  %d\n", len(rep.CoveringTests))
	if rep.Risk != nil {
		icon, label := riskBadge(rep.Risk.Level)
		fmt.Printf("  risk:            %s %s (%.2f)\n", icon, label, rep.Risk.Score)
	}
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	}
	if rep.Note != "" {
		fmt.Println("  • " + rep.Note)
	}
	if len(rep.ChangedSymbols) > 0 {
		fmt.Println("  changed symbols:")
		syms, more := capList(rep.ChangedSymbols, 15)
		for _, s := range syms {
			fmt.Printf("     %-36s %s:%d\n", disp(s.FQN, s.Symbol), s.File, s.StartLine)
		}
		if more > 0 {
			fmt.Printf("     … (%d more — use --json)\n", more)
		}
	}
	if len(rep.UntestedSymbols) > 0 {
		fmt.Printf("  ⚠ untested changes (%d): %s\n", len(rep.UntestedSymbols), joinRefNames(rep.UntestedSymbols, 8))
	}
	if len(rep.Hotspots) > 0 {
		fmt.Printf("  ⚑ hotspots changed (%d): %s\n", len(rep.Hotspots), joinRefNames(rep.Hotspots, 8))
	}
	if len(rep.CoveringTests) > 0 {
		fmt.Println("  tests to run:")
		tests, more := capList(rep.CoveringTests, impactTestsCap)
		for _, t := range tests {
			h := ""
			if t.Heuristic {
				h = " (heuristic)"
			}
			fmt.Printf("     %-36s %s:%d%s\n", disp(t.FQN, t.Symbol), t.File, t.StartLine, h)
		}
		if more > 0 {
			fmt.Printf("     … (%d more — use --json)\n", more)
		}
	} else if len(rep.ChangedSymbols) > 0 && rep.Resolution == "" {
		// Only assert "no tests" when the call graph was resolved; otherwise the
		// absence is unverified (the Resolution line above already explains it).
		fmt.Println("  ⚠ no tests cover these changes")
	}
	return reviewGate()
}

// reviewGateResult evaluates --fail-on-risk/--fail-on-untested against an
// already-printed ReviewReport. It never changes what was printed — it only
// turns a tripped threshold into the dedicated gate exit code (errGate; see
// gate.go). "unknown" risk never trips --fail-on-risk (the honesty rule), and
// a diff with no changed symbols (rep.Risk == nil) never trips it either —
// there is nothing to gate on.
func reviewGateResult(rep *app.ReviewReport, hasFailOnRisk bool, threshold int, failOnUntested bool) error {
	if hasFailOnRisk && rep.Risk != nil && riskGateTrips(rep.Risk.Level, threshold) {
		return errGate
	}
	if failOnUntested && len(rep.UntestedSymbols) > 0 {
		return errGate
	}
	return nil
}

func reviewStalenessLine(rep *app.ReviewReport) string {
	if rep == nil || !rep.Stale || rep.Staleness == nil {
		return ""
	}
	if rep.DeletionAnalysis != nil && rep.DeletionAnalysis.Analyzed > 0 {
		return fmt.Sprintf("  ⚠ index is stale: %d changed, %d new, %d deleted — run selected tests before reindexing; deleted-file impact uses the last index",
			rep.Staleness.Changed, rep.Staleness.New, rep.Staleness.Deleted)
	}
	return fmt.Sprintf("  ⚠ index is stale: %d changed, %d new, %d deleted — reindex for accurate impact",
		rep.Staleness.Changed, rep.Staleness.New, rep.Staleness.Deleted)
}

func reviewDeletionAnalysisLine(analysis *app.ReviewDeletionAnalysis) string {
	if analysis == nil {
		return ""
	}
	source := strings.ReplaceAll(strings.TrimSpace(analysis.Source), "_", " ")
	if source == "" {
		source = "last index"
	}
	return fmt.Sprintf("  deleted analysis: %d/%d analyzed from %s · %d missing",
		analysis.Analyzed, analysis.Files, source, analysis.Missing)
}

// runRisk renders a symbol's change-risk: one score + level, the caller/test
// counts behind it, and the factors that drove it. --json carries the full report.
func runRisk(cmd *cobra.Command, args []string) error {
	// Validate --fail-on-risk before any query work: a bad value is an
	// operational error (exit 1), fast-failed like any other bad flag.
	failOnRiskThreshold, hasFailOnRisk, err := parseFailOnRiskFlag(cmd)
	if err != nil {
		return err
	}
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	var rep *app.RiskReport
	if selector != nil {
		rep, err = svc.RiskBySelector(cwd, *selector, depth)
	} else {
		rep, err = svc.Risk(cwd, args[0], depth)
	}
	if err != nil {
		return err
	}
	if !rep.Found {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", args[0]))
	}
	// riskGate is called AFTER every point below that prints the normal
	// (unchanged) output, human or --json, so a tripped --fail-on-risk
	// threshold only changes the exit code.
	riskGate := func() error {
		if hasFailOnRisk && riskGateTrips(rep.Level, failOnRiskThreshold) {
			return errGate
		}
		return nil
	}
	if jsonOut(cmd) {
		if err := printJSON(rep); err != nil {
			return err
		}
		return riskGate()
	}
	icon, label := riskBadge(rep.Level)
	fmt.Printf("Risk of %s (%s): %s %s (%.2f)\n", rep.Symbol, rep.Project, icon, label, rep.Score)
	fmt.Printf("  %d direct callers · %d covering tests\n", rep.Callers, rep.Tests)
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	renderCandidates(rep.Candidates)
	if len(rep.Factors) == 0 {
		fmt.Println("  no risk factors — a low-impact, covered change")
		return riskGate()
	}
	fmt.Println("  factors:")
	for _, f := range rep.Factors {
		fmt.Printf("     %-14s %s\n", f.Factor, f.Detail)
	}
	return riskGate()
}

func riskBadge(level string) (icon, label string) {
	label = strings.ToUpper(strings.TrimSpace(level))
	switch level {
	case "low":
		return "✓", label
	case "medium":
		return "•", label
	case "high":
		return "⚠", label
	case "unknown":
		return "?", "UNKNOWN"
	default:
		if label == "" {
			label = "UNKNOWN"
		}
		return "?", label
	}
}

// runReadOrder renders the suggested reading order for orienting on a codebase:
// entrypoints and load-bearing hubs first, each with the reason it ranked. --json
// carries the full ranked list for an agent harness.
func runReadOrder(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	top, _ := cmd.Flags().GetInt("top")
	query := ""
	if len(args) > 0 {
		query = args[0]
	}
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.ReadOrder(cwd, app.ReadOrderOpts{Top: top, Query: query})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	header := fmt.Sprintf("Read order (%s) — start here", rep.Project)
	if rep.Query != "" {
		header += fmt.Sprintf(" · matching %q", rep.Query)
	}
	fmt.Println(header)
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	}
	if len(rep.Entries) == 0 {
		fmt.Println("  (nothing to rank — " + strings.TrimSpace(rep.Note) + ")")
		return nil
	}
	for _, e := range rep.Entries {
		mark := " "
		if e.Entrypoint {
			mark = "▶"
		}
		fmt.Printf("  %2d %s %s\n        %s — %s:%d\n", e.Rank, mark, disp(e.FQN, e.Symbol), e.Reason, e.File, e.StartLine)
	}
	return nil
}

// joinRefNames renders up to n symbol names as "a, b, c … (+k)".
func joinRefNames(refs []app.SymbolRef, n int) string {
	shown, more := capList(refs, n)
	names := make([]string, 0, len(shown))
	for _, s := range shown {
		names = append(names, disp(s.FQN, s.Symbol))
	}
	line := strings.Join(names, ", ")
	if more > 0 {
		line += fmt.Sprintf(" … (+%d)", more)
	}
	return line
}

// parseFileLine splits a "path/to/file.go:42" position. The file part may itself
// contain no colon issues since we split on the LAST colon (Windows-style drive
// letters aren't a concern for project-relative paths).
func parseFileLine(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("expected <file>:<line>, got %q", s)
	}
	line, err := strconv.Atoi(s[i+1:])
	if err != nil || line < 1 {
		return "", 0, fmt.Errorf("invalid line number in %q", s)
	}
	return s[:i], line, nil
}

// runFileImpact renders file-level impact: dependent files, blast radius, covering
// tests, and conservative deletion/change evidence for a whole file.
func runFileImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.FileImpact(cwd, args[0], depth)
	if err != nil {
		return err
	}
	if !rep.Found {
		return notFoundError(
			fmt.Sprintf("no indexed symbols in file %q", rep.File),
			"check the path relative to the project root")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("File impact: %s (%s)\n", rep.File, rep.Project)
	if rep.Stale {
		fmt.Println("  ⚠ index is stale — reindex (ctrl+r / codemap index) for accurate impact")
	}
	fmt.Printf("  symbols defined:  %d\n", rep.Symbols)
	fmt.Printf("  dependent files:  %d\n", len(rep.DependentFiles))
	fmt.Printf("  blast radius:     %d (depth ≤ %d)\n", rep.BlastRadius, rep.Depth)
	fmt.Printf("  covering tests:   %d\n", len(rep.CoveringTests))
	switch rep.DeleteVerdict {
	case app.DeleteVerdictUnsafe:
		fmt.Println("  ⚠ delete verdict: unsafe — confirmed indexed dependency evidence proves external dependencies")
	default:
		fmt.Println("  ? delete verdict: unknown — dependency evidence is not complete enough to prove safety")
	}
	if rep.Resolution != "" {
		fmt.Println("  ⚠ call graph: " + rep.Resolution)
	}
	if rep.BreakingChange {
		fmt.Println("  ⚠ breaking change: externally-called symbols here are untested")
	}
	if len(rep.DependentFiles) > 0 {
		fmt.Println("  depended on by:")
		files, more := capList(rep.DependentFiles, 12)
		for _, f := range files {
			fmt.Printf("     %s\n", f)
		}
		if more > 0 {
			fmt.Printf("     … (%d more — use --json)\n", more)
		}
	}
	if len(rep.UntestedSymbols) > 0 {
		fmt.Printf("  ⚠ externally-called but untested: %s\n", joinRefNames(rep.UntestedSymbols, 8))
	}
	return nil
}

func runRelatedFiles(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.RelatedFiles(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Related) == 0 {
		fmt.Printf("No files related to %s in the graph.\n", rep.File)
		return nil
	}
	fmt.Printf("Files related to %s (%s)\n", rep.File, rep.Project)
	for _, r := range rep.Related {
		fmt.Printf("  %-7s %s\n", r.Reason, r.RelativePath)
	}
	return nil
}

func runSymbolAt(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	cwd := targetDir(cmd)
	if len(args) > 1 {
		return runSymbolAtBatch(cmd, svc, cwd, args)
	}
	file, line, err := parseFileLine(args[0])
	if err != nil {
		return err
	}
	rep, err := svc.SymbolAt(cwd, file, line)
	if err != nil {
		return err
	}
	if rep.Resolution == "none" {
		return notFoundError(
			fmt.Sprintf("no symbol at %s:%d", file, line),
			"check the file path and line number")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("%s  %s:%d-%d  (%s, %s)\n", disp(rep.FQN, rep.Symbol), rep.File, rep.StartLine, rep.EndLine, rep.Kind, rep.Resolution)
	return nil
}

// runSymbolAtBatch resolves several <file>:<line> positions in one call — a
// pasted multi-frame stack trace or a diff's changed-line list, without one
// round-trip per frame. --json prints the whole SymbolAtBatchReport; the human
// form prints one line per result, reusing the single-position line format.
func runSymbolAtBatch(cmd *cobra.Command, svc *app.Service, cwd string, args []string) error {
	positions := make([]app.FilePosition, 0, len(args))
	for _, arg := range args {
		file, line, err := parseFileLine(arg)
		if err != nil {
			return err
		}
		positions = append(positions, app.FilePosition{File: file, Line: line})
	}
	rep, err := svc.SymbolAtBatch(cwd, positions)
	if err != nil {
		return err
	}
	misses := 0
	for _, r := range rep.Results {
		if r.Resolution == "none" {
			misses++
		}
	}
	if len(rep.Results) > 0 && misses == len(rep.Results) {
		return notFoundError("no symbol found at any of the requested positions", "check the file paths and line numbers")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	for _, r := range rep.Results {
		if r.Resolution == "none" {
			fmt.Printf("%s:%d  — not found\n", r.File, r.Line)
			continue
		}
		fmt.Printf("%s  %s:%d-%d  (%s, %s)\n", disp(r.FQN, r.Symbol), r.File, r.StartLine, r.EndLine, r.Kind, r.Resolution)
	}
	return nil
}

func runSecretImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	keys := append([]string{}, args...)
	vault, _ := cmd.Flags().GetString("via-vault")
	prefix, _ := cmd.Flags().GetString("prefix")
	if len(keys) == 0 && vault == "" {
		return fmt.Errorf("supply one or more secret key names, or --via-vault <project> to fetch them")
	}
	svc := app.NewService(sess)
	if ok, ierr := requireIndexed(cmd, svc); ierr != nil || !ok {
		return ierr
	}
	rep, err := svc.SecretImpactWithInventory(cmd.Context(), cwd, keys, depth, vault, prefix)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	for _, k := range rep.Keys {
		warn := ""
		if k.Untested {
			warn = "  ⚠ untested"
		}
		fmt.Printf("%s — %d reader(s), blast radius %d, %d covering test(s)%s\n",
			k.Key, len(k.UsedBy), k.BlastRadius, k.CoveringTests, warn)
		for _, u := range k.UsedBy {
			fmt.Printf("    %s  %s:%d\n", disp(u.FQN, u.Symbol), u.File, u.Line)
		}
	}
	if len(rep.OrphanKeys) > 0 {
		fmt.Printf("no code usages found (verify before treating as dead): %s\n", strings.Join(rep.OrphanKeys, ", "))
	}
	if !rep.Precise {
		fmt.Println("⚠ " + rep.Note)
	}
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before trusting a rotation")
	}
	return nil
}

func runRequiredKeys(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	keys, _ := cmd.Flags().GetStringSlice("keys")
	vault, _ := cmd.Flags().GetString("via-vault")
	prefix, _ := cmd.Flags().GetString("prefix")
	if len(keys) == 0 && vault == "" {
		return fmt.Errorf("supply candidate keys with --keys K1,K2 or --via-vault <project>")
	}
	svc := app.NewService(sess)
	if ok, ierr := requireIndexed(cmd, svc); ierr != nil || !ok {
		return ierr
	}
	rep, err := svc.RequiredKeysWithInventory(cmd.Context(), cwd, args[0], keys, depth, vault, prefix)
	if err != nil {
		return err
	}
	if !rep.Found {
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Entrypoint, rep.Project),
			fmt.Sprintf("run: codemap find %q", rep.Entrypoint))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	// One key per line — pipe-friendly: `… | xargs -I{} tvault seal --key {}`.
	for _, k := range rep.RequiredKeys {
		fmt.Println(k)
	}
	return nil
}

func runSemantic(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Semantic(cmd.Context(), cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if len(rep.Hits) == 0 && rep.Mode != "none" {
		return notFoundError(
			fmt.Sprintf("no semantic matches for %q", rep.Query),
			fmt.Sprintf("run: codemap find %q", rep.Query))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hits) == 0 {
		if rep.Note != "" {
			fmt.Println(rep.Note)
		} else {
			fmt.Println("no matches")
		}
		return nil
	}
	if rep.Fusion != "" {
		fmt.Printf("fusion: %s\n", rep.Fusion)
	}
	for _, h := range rep.Hits {
		// Mirror `find`'s outline (file:line + signature) but lead with the relevance
		// score — so a meaning-based hit shows WHAT it is, not just a bare name.
		fmt.Printf("%s%.3f  %-26s %s\n", annMark(h.Annotations), h.Score,
			fmt.Sprintf("%s:%d", h.File, h.StartLine), sigOrName(h.Signature, h.FQN, h.Symbol))
	}
	return nil
}

func runHotspots(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Hotspots(cwd, top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hotspots) == 0 {
		fmt.Printf("Hotspots in %s: none\n", rep.Project)
		renderCallGraphReliability(rep.CallGraph, rep.Resolution, rep.Note)
		return nil
	}
	fmt.Printf("Hotspots in %s:\n", rep.Project)
	anyInflated := false
	for _, h := range rep.Hotspots {
		line := fmt.Sprintf("  %4d  %-36s %s:%d", h.InDegree, disp(h.FQN, h.Symbol), h.File, h.StartLine)
		if h.SharedName > 1 { // count fanned across same-named defs (name-based)
			line += fmt.Sprintf("  ⚠ name shared by %d (inflated)", h.SharedName)
			anyInflated = true
		}
		fmt.Println(line)
	}
	// The ⚠ rows say WHAT is wrong; point at the fix. Inflation only happens on a
	// name-based index (precise edges are exact), so the hint is always actionable here.
	if anyInflated {
		fmt.Println("  → ⚠ counts are inflated by same-named symbols; 'codemap index --precise' records exact edges for files it resolves")
	}
	renderCallGraphReliability(rep.CallGraph, rep.Resolution, rep.Note)
	return nil
}

func runOrphans(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Orphans(cwd, top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	label := fmt.Sprintf("Orphans in %s (no callers — dead-code candidates)", rep.Project)
	if rep.CallGraph == app.CallGraphUnresolved {
		label = fmt.Sprintf("Orphan candidates in %s (call graph unresolved)", rep.Project)
	}
	renderRefs(label, rep.Orphans)
	renderCallGraphReliability(rep.CallGraph, rep.Resolution, rep.Note)
	return nil
}

func renderCallGraphReliability(callGraph, resolution, note string) {
	if callGraph != "" {
		fmt.Printf("  call graph: %s\n", callGraph)
	}
	if resolution != "" {
		if callGraph == app.CallGraphUnresolved && !strings.ContainsAny(strings.TrimSpace(resolution), " \t\n") {
			fmt.Printf("  ⚠ call graph unavailable for %s — run 'codemap index --precise'\n", resolution)
		} else {
			fmt.Println("  ⚠ " + resolution)
		}
	}
	if note != "" && note != resolution {
		fmt.Println("  ⚠ " + note)
	}
}

func runPath(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Path(cwd, args[0], args[1])
	if err != nil {
		return err
	}
	if !pathReportAnswered(rep) {
		return pathMissError(rep)
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		if rep.CallGraph == app.CallGraphUnresolved {
			fmt.Printf("Call path from %s to %s is unresolved\n", rep.From, rep.To)
		} else {
			fmt.Printf("No call path from %s to %s\n", rep.From, rep.To)
		}
		renderPathReliability(rep)
		renderAnnotations(rep.Annotations)
		return nil
	}
	names := make([]string, 0, len(rep.Path))
	for _, p := range rep.Path {
		names = append(names, p.Symbol)
	}
	fmt.Println(strings.Join(names, " → "))
	for _, p := range rep.Path {
		fmt.Printf("  %-30s %s:%d\n", p.Symbol, p.File, p.StartLine)
	}
	renderPathReliability(rep)
	renderAnnotations(rep.Annotations)
	return nil
}

func renderPathReliability(rep *app.PathReport) {
	if rep == nil {
		return
	}
	renderCallGraphReliability(rep.CallGraph, rep.Resolution, rep.Note)
	if rep.Stale {
		fmt.Println("  ⚠ index is stale — reindex before trusting this path result")
	}
}

func pathReportAnswered(rep *app.PathReport) bool {
	return rep != nil && (rep.Found || rep.CallGraph != app.CallGraphNone)
}

func pathMissError(rep *app.PathReport) error {
	msg := rep.Note
	if msg == "" {
		msg = fmt.Sprintf("no call path from %s to %s", rep.From, rep.To)
	}
	return notFoundError(msg, "check both symbols with codemap find")
}

func runFind(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.FindSymbols(cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if len(rep.Hits) == 0 {
		return notFoundError(fmt.Sprintf("no symbols matching %q", rep.Query), "try a broader name or substring")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	for _, h := range rep.Hits {
		fmt.Printf("%s%-9s %-26s %s\n", annMark(h.Annotations), h.Kind, fmt.Sprintf("%s:%d", h.File, h.StartLine),
			sigOrName(h.Signature, h.FQN, h.Symbol))
	}
	return nil
}

func runGrep(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	regex, _ := cmd.Flags().GetBool("regex")
	ignoreCase, _ := cmd.Flags().GetBool("ignore-case")
	top, _ := cmd.Flags().GetInt("top")
	rep, err := svc.Grep(cwd, args[0], app.GrepOptions{Regex: regex, IgnoreCase: ignoreCase, Top: top})
	if err != nil {
		return err
	}
	if rep.Stale {
		fmt.Println("⚠ index is stale — files added since the last index are not in the searched set (run codemap index)")
	}
	if len(rep.Hits) == 0 {
		return notFoundError(fmt.Sprintf("no matches for %q", rep.Pattern), "try --regex, --ignore-case, or a broader pattern")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	for _, h := range rep.Hits {
		sym := h.Symbol
		if sym == "" {
			sym = "(none)"
		}
		fmt.Printf("%-9s %-26s %-30s %s\n", h.Kind, fmt.Sprintf("%s:%d", h.File, h.Line), sym, h.MatchedLine)
	}
	if rep.Truncated {
		fmt.Printf("… %d more matches (top %d shown; raise --top)\n", rep.Total-len(rep.Hits), len(rep.Hits))
	}
	return nil
}

func runSymbols(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Symbols(cwd, args[0])
	if err != nil {
		return err
	}
	if len(rep.Symbols) == 0 {
		return notFoundError(
			fmt.Sprintf("no indexed symbols in file %q", rep.File),
			"check the path relative to the project root")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("%s (%d symbols):\n", rep.File, len(rep.Symbols))
	for _, s := range rep.Symbols {
		fmt.Printf("  %-9s %5d  %s\n", s.Kind, s.StartLine, sigOrName(s.Signature, s.FQN, s.Symbol))
	}
	return nil
}

func runSource(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	cwd := targetDir(cmd)
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	if selector == nil && len(args) == 0 {
		return fmt.Errorf("source needs a <symbol> argument or --at <file>:<line>")
	}
	brief, _ := cmd.Flags().GetBool("brief")
	var rep *app.SourceReport
	if selector != nil {
		rep, err = svc.SourceBySelector(cwd, *selector, brief)
	} else {
		rep, err = svc.Source(cwd, args[0], brief)
	}
	if err != nil {
		return err
	}
	if len(rep.Matches) == 0 {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", rep.Symbol))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	for i, mch := range rep.Matches {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("// %s  %s:%d-%d\n", disp(mch.FQN, mch.Symbol), mch.File, mch.StartLine, mch.EndLine)
		if mch.SourceOmitted {
			fmt.Println("// [source omitted --brief; re-run without --brief for the body]")
			continue
		}
		fmt.Println(mch.Source)
	}
	if len(rep.Annotations) > 0 {
		fmt.Println()
		renderAnnotations(rep.Annotations)
	}
	return nil
}

func runProjects(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	rep, err := app.NewService(sess).Projects()
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Projects) == 0 {
		fmt.Println("no projects registered yet — run 'codemap init' in a project")
		return nil
	}
	fmt.Printf("%-20s %8s %8s %7s  %s\n", "PROJECT", "NODES", "EDGES", "FILES", "PATH")
	for _, p := range rep.Projects {
		fmt.Printf("%-20s %8d %8d %7d  %s\n", truncStr(p.Name, 20), p.Nodes, p.Edges, p.Files, p.Path)
	}
	return nil
}

func runDocs(_ *cobra.Command, args []string) error {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}
	fmt.Print(app.Docs(topic))
	return nil
}

// requireIndexed reports whether the target project has been indexed. A cold
// repo returns the exit-2 errNotIndexed sentinel; jsonHandler turns it into the
// same structured failure envelope as every other CLI error.
func requireIndexed(cmd *cobra.Command, svc *app.Service) (bool, error) {
	cwd := targetDir(cmd)
	indexed, name, err := svc.Indexed(cwd)
	if err != nil {
		return false, err
	}
	if !indexed {
		return false, notIndexedError(name)
	}
	return true, nil
}

// renderRefs renders a relation list under a label (e.g. "Callers of X").
func renderRefs(label string, refs []app.SymbolRef) {
	if len(refs) == 0 {
		fmt.Printf("%s: none\n", label)
		return
	}
	fmt.Printf("%s:\n", label)
	for _, r := range refs {
		fmt.Printf("  %-36s %s:%d\n", disp(r.FQN, r.Symbol), r.File, r.StartLine)
	}
}

// renderRefsCapped renders a relation list bounded to cap rows — the complete
// set is always in --json — printing a "… (N more)" line when truncated. Used by
// callers/callees, which (unlike the ranked top-N commands) return the full set.
func renderRefsCapped(label string, refs []app.SymbolRef, limit int) {
	shown, more := capList(refs, limit)
	renderRefs(label, shown)
	if more > 0 {
		fmt.Printf("  … (%d more — use --json for all)\n", more)
	}
}

// annMark is a row prefix flagging that a result carries annotations.
func annMark(anns []graph.Annotation) string {
	if len(anns) > 0 {
		return "⟐ "
	}
	return "  "
}

// renderAnnotations prints pinned notes/data under a query result, if any.
func renderAnnotations(anns []graph.Annotation) {
	if len(anns) == 0 {
		return
	}
	fmt.Println("  annotations:")
	for _, a := range anns {
		line := a.Source
		if a.Note != "" {
			line += ": " + a.Note
		}
		if a.Data != "" {
			line += "  " + truncStr(a.Data, 80)
		}
		fmt.Printf("     #%d %s\n", a.ID, line)
	}
}

// renderCandidates prints the exact merged-definition set behind an ambiguous
// Note ("X matches N definitions") as a short numbered "did you mean" list —
// the same selectors an agent would read from --json's candidates field,
// so a human doesn't have to re-run find/symbols to locate them by hand.
func renderCandidates(candidates []app.AmbiguityCandidate) {
	if len(candidates) == 0 {
		return
	}
	fmt.Println("  did you mean:")
	shown, more := capList(candidates, 8)
	for i, c := range shown {
		name := c.Signature
		if name == "" && c.Selector != nil {
			name = c.Selector.FQN
		}
		fmt.Printf("     %d. %-36s %s:%d\n", i+1, name, c.File, c.StartLine)
	}
	if more > 0 {
		fmt.Printf("     … (%d more — use --json for the full selector list)\n", more)
	}
}
