/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Command codemap is local-first code intelligence: a structural code graph
// (LSP + parsers) fused with semantic vector search (veclite), exposed via a
// CLI, an MCP server, and the studio TUI. See AGENTS.md for architecture.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	mcpserver "github.com/abdul-hamid-achik/codemap/internal/mcp"
	"github.com/abdul-hamid-achik/codemap/internal/tui"
	"github.com/abdul-hamid-achik/codemap/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "codemap",
	Short:   "Local-first code intelligence: graph + vectors for agents and people",
	Version: version.Full(),
	// A runtime failure (bad config, not-indexed, …) shouldn't dump the full usage
	// block — that's noise for a human and pollutes stderr for an agent parsing the
	// error. cobra still prints the "Error: …" line; `--help` is there when wanted.
	SilenceUsage: true,
	Long: `codemap combines a structural code graph (LSP + parsers) with semantic
vector search (veclite) and exposes both as a unified query layer.

Three surfaces over one store: a CLI (with --json for agents), an MCP server
(codemap serve), and the interactive studio TUI (codemap studio).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !isInteractiveTerminal() {
			return cmd.Help()
		}
		return runStudio(cmd, args)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("codemap %s\n", version.Version)
		fmt.Printf("  commit: %s\n", version.Commit)
		fmt.Printf("  built:  %s\n", version.Date)
	},
}

// isInteractiveTerminal reports whether both stdin and stdout are TTYs.
func isInteractiveTerminal() bool {
	so, err1 := os.Stdout.Stat()
	si, err2 := os.Stdin.Stat()
	if err1 != nil || err2 != nil {
		return false
	}
	return so.Mode()&os.ModeCharDevice != 0 && si.Mode()&os.ModeCharDevice != 0
}

var (
	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Register the current directory as a codemap project",
		Long: `Register the current directory as a codemap project.
Project data (the graph DB + vectors) is stored centrally under
$XDG_DATA_HOME/codemap/ (falling back to ~/.local/share or ~/.codemap if
present). --local drops a .codemap marker in the project root so a repo-local
codemap.yaml config is picked up from any subdirectory; the index still lives
centrally — set CODEMAP_DATA to a path inside the repo for a repo-local index.`,
		RunE: runInit,
	}
	indexCmd = &cobra.Command{
		Use:   "index [paths...]",
		Short: "Index a project: extract the graph and embed nodes (incremental)",
		RunE:  runIndex,
	}
	statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show index status and statistics (nodes, edges, coverage, languages)",
		RunE:  runStatus,
	}
	doctorCmd = &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment: toolchains, language servers, and embeddings",
		Long: `Check which codemap capabilities are ready in this environment, before
indexing: the go toolchain and gopls (Go precise paths), each language server
(TypeScript/JavaScript via typescript-language-server, Python via pyright), and
Ollama embeddings (semantic search). Nothing here is required for the core graph
— each missing piece just disables one capability, with a hint to enable it.`,
		RunE: runDoctor,
	}
	serveCmd = &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP stdio server for AI agents",
		Long: `Start the Model Context Protocol (MCP) server over stdio for integration
with AI assistants (Claude Code, Codex, OpenCode, ...). Uses newline-delimited
JSON-RPC framing.`,
		RunE: runServe,
	}
	studioCmd = &cobra.Command{
		Use:     "studio [path]",
		Aliases: []string{"browse"},
		Short:   "Open the interactive studio TUI (Graph / Metrics / Impact / Search)",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runStudio,
	}
)

var (
	callersCmd = &cobra.Command{
		Use:   "callers <symbol>",
		Short: "List functions/methods that call a symbol",
		Args:  cobra.ExactArgs(1),
		RunE:  runCallers,
	}
	calleesCmd = &cobra.Command{
		Use:   "callees <symbol>",
		Short: "List functions/methods that a symbol calls",
		Args:  cobra.ExactArgs(1),
		RunE:  runCallees,
	}
	impactCmd = &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Impact analysis: blast radius (transitive callers) + test coverage",
		Args:  cobra.ExactArgs(1),
		RunE:  runImpact,
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
		Args:  cobra.ExactArgs(2),
		RunE:  runPath,
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
	sourceCmd = &cobra.Command{
		Use:   "source <symbol>",
		Short: "Print a symbol's source code (the body behind its signature)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSource,
	}
	contextCmd = &cobra.Command{
		Use:   "context <symbol>",
		Short: "Everything about a symbol in one call: definition, callers, callees, tests, notes",
		Args:  cobra.ExactArgs(1),
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
	annotateCmd = &cobra.Command{
		Use:   "annotate <symbol> | <from> <to>",
		Short: "Attach a note and/or external data (e.g. DB rows) to a symbol or a call path",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runAnnotate,
	}
	annotationsCmd = &cobra.Command{
		Use:   "annotations [symbol] | [from] [to]",
		Short: "List annotations (all, for a symbol, or for a from→to path); --rm <id> to remove",
		Args:  cobra.RangeArgs(0, 2),
		RunE:  runAnnotations,
	}
)

func init() {
	rootCmd.SetVersionTemplate("codemap version {{.Version}}\n")

	// Persistent flags shared by all commands.
	rootCmd.PersistentFlags().StringP("config", "c", "", "path to config file")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().Bool("json", false, "emit machine-readable JSON (for agents)")

	indexCmd.Flags().Bool("reindex", false, "wipe and rebuild the whole project index")
	indexCmd.Flags().Bool("no-embed", false, "skip semantic embeddings (index structure only)")
	indexCmd.Flags().Bool("precise", false, "resolve call edges exactly (Go via go/types, needs the go toolchain; TypeScript/JavaScript/Python via callHierarchy) — eliminates same-named over-matching and gives the LSP languages a call graph")
	indexCmd.Flags().Bool("no-lsp", false, "skip language-server-backed extraction (e.g. TypeScript via typescript-language-server)")
	initCmd.Flags().Bool("local", false, "drop a .codemap marker (so a repo-local codemap.yaml is found; index stays central)")
	callersCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callers (Go)")
	calleesCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callees (Go)")
	impactCmd.Flags().Int("depth", 3, "max hops for the blast radius")
	contextCmd.Flags().Int("depth", 3, "max hops for the blast-radius count")
	semanticCmd.Flags().Int("top", 10, "maximum results")
	hotspotsCmd.Flags().Int("top", 20, "maximum results")
	orphansCmd.Flags().Int("top", 50, "maximum results")
	findCmd.Flags().Int("top", 50, "maximum results")
	annotateCmd.Flags().String("source", "note", "annotation source: note, mongosh, postgres, vidtrace, …")
	annotateCmd.Flags().String("note", "", "free-form note text")
	annotateCmd.Flags().String("data", "", "opaque data payload (e.g. JSON from a DB query)")
	annotationsCmd.Flags().Int64("rm", 0, "remove the annotation with this id")

	rootCmd.AddCommand(versionCmd, initCmd, indexCmd, statusCmd, doctorCmd, serveCmd, studioCmd,
		callersCmd, calleesCmd, impactCmd, semanticCmd, hotspotsCmd, orphansCmd, pathCmd, symbolsCmd, findCmd, sourceCmd, contextCmd, projectsCmd, docsCmd,
		annotateCmd, annotationsCmd)
}

// --- command handlers (thin: resolve flags, call internal/app, render) ---

func runInit(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	rep, err := app.NewService(sess).Init(cwd, local)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("Registered project %q\n  root: %s\n  data: %s\n", rep.Project, rep.Root, rep.DataDir)
	fmt.Println("Run 'codemap index' to build the graph.")
	return nil
}

// indexFilesSummary renders the "files:" line of `codemap index`. Recognized
// files of a language with no available extractor (e.g. its language server isn't
// installed) were genuinely scanned and skipped, so they're folded into the
// scanned+skipped counts here — otherwise the summary would claim "0 skipped"
// while the warning above reports skipped files. (IndexReport keeps FilesScanned
// as extractable-only for the advisory; this reconciles the human view with it.)
func indexFilesSummary(rep *app.IndexReport) string {
	unsupported := 0
	for _, n := range rep.Unsupported {
		unsupported += n
	}
	line := fmt.Sprintf("  files: %d scanned, %d indexed, %d skipped",
		rep.FilesScanned+unsupported, rep.FilesIndexed, rep.FilesSkipped+unsupported)
	if rep.FilesDeleted > 0 {
		line += fmt.Sprintf(", %d removed", rep.FilesDeleted)
	}
	return line
}

func runIndex(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	reindex, _ := cmd.Flags().GetBool("reindex")
	noEmbed, _ := cmd.Flags().GetBool("no-embed")
	precise, _ := cmd.Flags().GetBool("precise")
	noLSP, _ := cmd.Flags().GetBool("no-lsp")
	opts := index.Options{Reindex: reindex, Precise: precise, NoLSP: noLSP}
	svc := app.NewService(sess)
	// Live progress bar only for an interactive `codemap index` (TTY, no --json).
	// Under --json, MCP, studio reindex, or a pipe, opts.OnFile stays nil and
	// indexing runs exactly as before — no bar, no stdout noise.
	var rep *app.IndexReport
	if !jsonOut(cmd) && isInteractiveTTY() {
		rep, err = runIndexWithBar(cmd.Context(), svc, cwd, opts, !noEmbed)
	} else {
		rep, err = svc.Index(cmd.Context(), cwd, opts, !noEmbed)
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if rep.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", rep.Warning)
	}
	fmt.Printf("Indexed %q (%s)\n", rep.Project, rep.Root)
	fmt.Println(indexFilesSummary(rep))
	fmt.Printf("  graph: %d nodes, %d edges (embeddings: %v)\n", rep.Nodes, rep.Edges, rep.Embedded)
	if precise {
		if rep.PreciseNote != "" {
			fmt.Printf("  precise: %s\n", rep.PreciseNote)
		} else {
			fmt.Printf("  precise: %d call edges resolved exactly (%d unresolved)\n", rep.PreciseUpgraded, rep.PreciseSkipped)
		}
	} else {
		// Surface --precise at the moment a user would most benefit: it refines Go's
		// name-based edges, and it's the ONLY source of a call graph for the LSP
		// languages (a TS/JS/Python project has no callers/impact without it).
		goAvailable := false
		if _, lookErr := exec.LookPath("go"); lookErr == nil {
			goAvailable = true
		}
		for _, tip := range preciseTips(rep.Languages, goAvailable) {
			fmt.Println("  tip: " + tip)
		}
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(os.Stderr, "  ! %s: %s\n", e.File, e.Err)
	}
	for _, f := range rep.Oversized {
		fmt.Fprintf(os.Stderr, "  ~ %s: skipped — exceeds index.max_file_bytes (raise it to include this file)\n", f)
	}
	return nil
}

func runStatus(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	svc := app.NewService(sess)
	rep, err := svc.Status(cwd)
	if err != nil {
		return err
	}
	// Drift check (only meaningful once indexed): so you/an agent know whether the
	// graph is behind the code before trusting a query. Best-effort — a failure
	// here never breaks status.
	if rep.Registered {
		if st, sErr := svc.Staleness(cwd); sErr == nil {
			rep.Stale = st
		}
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Registered {
		fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", rep.Project)
		return nil
	}
	edges := fmt.Sprintf("%d", rep.Edges) + preciseEdgeNote(rep.PreciseEdges, rep.Languages)
	fmt.Printf("Project: %s\n  path:  %s\n  nodes: %d\n  edges: %s\n  files: %d\n",
		rep.Project, rep.Path, rep.Nodes, edges, rep.Files)
	if rep.Vectors > 0 {
		fmt.Printf("  vectors: %d (semantic search ready)\n", rep.Vectors)
	} else {
		fmt.Println("  vectors: 0 (structure-only — run 'codemap index' with Ollama for semantic search)")
	}
	if len(rep.Languages) > 0 {
		fmt.Printf("  languages: %s\n", formatCounts(rep.Languages))
	}
	if len(rep.Kinds) > 0 {
		fmt.Printf("  kinds:     %s\n", formatCounts(rep.Kinds))
	}
	if rep.Stale != nil && rep.Stale.Any() {
		fmt.Printf("  ⚠ index is stale: %d changed, %d new, %d deleted since last index — run 'codemap index' to refresh\n",
			rep.Stale.Changed, rep.Stale.New, rep.Stale.Deleted)
	}
	return nil
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	rep := app.NewService(sess).Doctor(context.Background())
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("codemap doctor\n  data: %s\n\n", rep.DataDir)
	for _, c := range rep.Checks {
		mark := "✓"
		if !c.OK {
			mark = "⚠"
		}
		line := fmt.Sprintf("  %s %s", mark, c.Name)
		if c.Detail != "" {
			line += "  " + c.Detail
		}
		fmt.Println(line)
		if !c.OK && c.Hint != "" {
			fmt.Printf("      → %s\n", c.Hint)
		}
	}
	return nil
}

func runStudio(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	if len(args) > 0 {
		cwd = args[0]
	}
	return tui.Run(cmd.Context(), sess, cwd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mcpserver.NewServer(sess).Run(ctx)
}

// requireIndexed reports whether the current project has been indexed, printing a
// clear "run codemap index" message first if not — text, or a structured
// {indexed:false,…} object under --json so agents get the same signal. Query
// commands gate on it so a cold repo doesn't yield misleading empty results.
func requireIndexed(cmd *cobra.Command, svc *app.Service) (bool, error) {
	cwd, _ := os.Getwd()
	indexed, name, err := svc.Indexed(cwd)
	if err != nil {
		return false, err
	}
	if !indexed {
		if jsonOut(cmd) {
			_ = printJSON(map[string]any{
				"project": name,
				"indexed": false,
				"note":    "project not indexed — run 'codemap index' first",
			})
		} else {
			fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", name)
		}
		return false, nil
	}
	return true, nil
}

func runCallers(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	useLSP, _ := cmd.Flags().GetBool("lsp")
	var rep *app.RelationReport
	if useLSP {
		rep, err = svc.PreciseCallers(cmd.Context(), cwd, args[0])
	} else {
		rep, err = svc.Callers(cwd, args[0])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	label := fmt.Sprintf("Callers of %s", rep.Symbol)
	if useLSP && rep.Note == "" { // Note set => precise fell back to name-based; don't mislabel
		label += " (precise, via gopls)"
	}
	renderRefsCapped(label, rep.Results, relationsDisplayCap)
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

func runCallees(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	useLSP, _ := cmd.Flags().GetBool("lsp")
	var rep *app.RelationReport
	if useLSP {
		rep, err = svc.PreciseCallees(cmd.Context(), cwd, args[0])
	} else {
		rep, err = svc.Callees(cwd, args[0])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	label := fmt.Sprintf("Callees of %s", rep.Symbol)
	if useLSP && rep.Note == "" { // Note set => precise fell back to name-based; don't mislabel
		label += " (precise, via gopls)"
	}
	renderRefsCapped(label, rep.Results, relationsDisplayCap)
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

func runImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Impact(cwd, args[0], depth)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		fmt.Printf("symbol %q not found in project %s\n", rep.Symbol, rep.Project)
		return nil
	}
	fmt.Printf("Impact of %s (%s)\n", rep.Symbol, rep.Project)
	for _, l := range rep.Locations {
		fmt.Printf("  defined:        %s:%d\n", l.File, l.StartLine)
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	fmt.Printf("  direct callers: %d\n", len(rep.DirectCallers))
	fmt.Printf("  blast radius:   %d (depth ≤ %d)\n", len(rep.BlastRadius), depth)
	fmt.Printf("  tests covering: %d\n", len(rep.Tests))
	if rep.Untested {
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

func runSemantic(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Semantic(cmd.Context(), cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
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
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	rep, err := app.NewService(sess).Hotspots(cwd, top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hotspots) == 0 {
		fmt.Println("no hotspots (is the project indexed?)")
		return nil
	}
	fmt.Printf("Hotspots in %s:\n", rep.Project)
	for _, h := range rep.Hotspots {
		line := fmt.Sprintf("  %4d  %-36s %s:%d", h.InDegree, disp(h.FQN, h.Symbol), h.File, h.StartLine)
		if h.SharedName > 1 { // count fanned across same-named defs (name-based)
			line += fmt.Sprintf("  ⚠ name shared by %d (inflated)", h.SharedName)
		}
		fmt.Println(line)
	}
	return nil
}

func runOrphans(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
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
	renderRefs(fmt.Sprintf("Orphans in %s (no callers — dead-code candidates)", rep.Project), rep.Orphans)
	return nil
}

func runPath(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Path(cwd, args[0], args[1])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		if rep.Note != "" {
			fmt.Println(rep.Note)
		} else {
			fmt.Printf("no call path from %s to %s\n", rep.From, rep.To)
		}
		renderAnnotations(rep.Annotations) // a pinned path note is worth showing even with no current path
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
	renderAnnotations(rep.Annotations)
	return nil
}

func runFind(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	rep, err := app.NewService(sess).FindSymbols(cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hits) == 0 {
		fmt.Printf("no symbols matching %q (is the project indexed?)\n", rep.Query)
		return nil
	}
	for _, h := range rep.Hits {
		fmt.Printf("%s%-9s %-26s %s\n", annMark(h.Annotations), h.Kind, fmt.Sprintf("%s:%d", h.File, h.StartLine),
			sigOrName(h.Signature, h.FQN, h.Symbol))
	}
	return nil
}

func runSymbols(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	rep, err := app.NewService(sess).Symbols(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Symbols) == 0 {
		fmt.Printf("no symbols in %s (is the project indexed?)\n", rep.File)
		return nil
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
	defer sess.Close()
	cwd, _ := os.Getwd()
	rep, err := app.NewService(sess).Source(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Matches) == 0 {
		fmt.Printf("no symbol named %q (is the project indexed?)\n", rep.Symbol)
		return nil
	}
	for i, mch := range rep.Matches {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("// %s  %s:%d-%d\n", disp(mch.FQN, mch.Symbol), mch.File, mch.StartLine, mch.EndLine)
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
	defer sess.Close()
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

func runAnnotate(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	source, _ := cmd.Flags().GetString("source")
	note, _ := cmd.Flags().GetString("note")
	data, _ := cmd.Flags().GetString("data")
	if note == "" && data == "" {
		return fmt.Errorf("nothing to attach: pass --note and/or --data")
	}
	svc := app.NewService(sess)
	var (
		id     int64
		match  bool
		warn   string
		kind   = "node"
		target string
	)
	if len(args) == 1 {
		target = args[0]
		id, match, err = svc.AnnotateNode(cwd, target, source, note, data)
		if err == nil && !match {
			warn = fmt.Sprintf("no indexed symbol named %q — saved, but it won't surface in queries until one is (typo? not indexed yet?)", target)
		}
	} else {
		kind = "path"
		id, target, match, err = svc.AnnotatePath(cwd, args[0], args[1], source, note, data)
		if err == nil && !match {
			warn = fmt.Sprintf("path endpoints %q and %q aren't both indexed symbols — saved, but it won't surface until they are", args[0], args[1])
		}
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		out := map[string]any{"id": id, "kind": kind, "target": target, "source": source, "matched": match}
		if warn != "" {
			out["note"] = warn
		}
		return printJSON(out)
	}
	label := target
	if kind == "path" {
		label = "path " + target
	}
	fmt.Printf("annotated %s  (#%d, source=%s)\n", label, id, source)
	if warn != "" {
		fmt.Println("⚠ " + warn)
	}
	return nil
}

func runAnnotations(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)

	if rm, _ := cmd.Flags().GetInt64("rm"); rm > 0 {
		ok, err := svc.RemoveAnnotation(cwd, rm)
		if err != nil {
			return err
		}
		if ok {
			fmt.Printf("removed annotation #%d\n", rm)
		} else {
			fmt.Printf("no annotation #%d\n", rm)
		}
		return nil
	}

	var rep *app.AnnotationsReport
	switch len(args) {
	case 0:
		rep, err = svc.AllAnnotations(cwd)
	case 1:
		rep, err = svc.NodeAnnotations(cwd, args[0])
	default:
		rep, err = svc.PathAnnotations(cwd, args[0], args[1])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Annotations) == 0 {
		fmt.Println("no annotations")
		return nil
	}
	dangling := make(map[int64]bool, len(rep.Dangling))
	for _, id := range rep.Dangling {
		dangling[id] = true
	}
	for _, a := range rep.Annotations {
		line := fmt.Sprintf("#%-4d %-5s %-8s %s", a.ID, a.Kind, a.Source, a.Target)
		if dangling[a.ID] {
			line += "  ⚠ no current symbol (renamed/removed — prune with --rm, or re-add)"
		}
		fmt.Println(line)
		if a.Note != "" {
			fmt.Printf("        note: %s\n", a.Note)
		}
		if a.Data != "" {
			fmt.Printf("        data: %s\n", truncStr(a.Data, 100))
		}
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

// truncStr shortens s to at most n runes (for fixed-width columns).
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
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

// disp prefers the fully-qualified name so same-named symbols are distinguishable.
func disp(fqn, symbol string) string {
	if fqn != "" {
		return fqn
	}
	return symbol
}

// Caps for the human-facing `impact` lists. A hub can have hundreds of
// dependents; dumping them all floods the terminal. The blast radius is
// depth-ordered (BFS), so the cap keeps the nearest — most relevant — nodes.
// `--json` always carries the complete set for agents/scripts.
const (
	impactTestsCap = 10
	impactBlastCap = 20
	// relationsDisplayCap bounds the human-facing callers/callees lists. Most
	// symbols have a handful, but a same-named query (e.g. `callers Close`) merges
	// every definition and can run to 100+ rows; the ambiguity ⚠ already explains
	// it. --json stays complete.
	relationsDisplayCap = 40
)

// capList returns the first n items of xs (all, if fewer) and how many were
// elided, so callers can print a "… (N more)" line.
func capList[T any](xs []T, n int) (shown []T, more int) {
	if len(xs) <= n {
		return xs, 0
	}
	return xs[:n], len(xs) - n
}

// sigOrName shows the signature (which includes the name and parameters) when
// available, falling back to the qualified name — so list commands read like a
// file outline rather than a bare list of names.
func sigOrName(signature, fqn, symbol string) string {
	if s := strings.TrimSpace(signature); s != "" {
		return s
	}
	return disp(fqn, symbol)
}

func openSession(cmd *cobra.Command) (*app.Session, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	return app.Open(cfgPath)
}

func jsonOut(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// preciseTips returns the "add --precise" hints shown after a non-precise index,
// tailored to the languages present: Go's name-based edges can be made exact,
// while the LSP languages (TypeScript/JavaScript/Python) have no call graph at
// all without --precise — so a new user isn't left with empty `callers`/`impact`
// and no idea why. A language only appears here when its files were indexed,
// which means its server was present, so the tip is always actionable.
func preciseTips(languages map[string]int, goAvailable bool) []string {
	var tips []string
	if languages["go"] > 0 && goAvailable {
		tips = append(tips, "Go call edges are name-based; add --precise to resolve them exactly (eliminates same-named over-matching)")
	}
	var lsp []string
	for _, l := range []string{"typescript", "javascript", "python"} {
		if languages[l] > 0 {
			lsp = append(lsp, l)
		}
	}
	if len(lsp) > 0 {
		tips = append(tips, "no call graph for "+strings.Join(lsp, "/")+" yet — add --precise for callers/impact/hotspots/path")
	}
	return tips
}

// preciseEdgeNote renders the parenthetical after the edge count in `status`,
// engine-aware so it never lies: precise edges come from go/types for Go but
// callHierarchy for TypeScript, and a TS project without --precise has *no* call
// edges at all (not "name-based" — TS has no name-based call resolution).
func preciseEdgeNote(preciseEdges int, languages map[string]int) string {
	hasGo := languages["go"] > 0
	hasTS := languages["typescript"] > 0
	if preciseEdges > 0 {
		switch {
		case hasGo && hasTS:
			return fmt.Sprintf(" (%d precise: go/types + callHierarchy)", preciseEdges)
		case hasTS:
			return fmt.Sprintf(" (%d precise via callHierarchy)", preciseEdges)
		default:
			return fmt.Sprintf(" (%d precise via go/types)", preciseEdges)
		}
	}
	if hasTS && !hasGo {
		// TypeScript has no name-based call edges — --precise is the only source.
		return " (no call graph yet; run 'codemap index --precise' to resolve TypeScript calls)"
	}
	return " (name-based; run 'codemap index --precise' for exact call edges)"
}

func formatCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}
