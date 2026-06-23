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
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/abdul-hamid-achik/codemap/internal/app"
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
By default, project data is stored centrally under $XDG_DATA_HOME/codemap/
(falling back to ~/.local/share or ~/.codemap if present). Use --local to keep
state inside the project.`,
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
		Use:   "semantic <query>",
		Short: "Semantic search across the code graph by meaning",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runSemantic,
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
	projectsCmd = &cobra.Command{
		Use:   "projects",
		Short: "List all projects registered with codemap and their index sizes",
		Args:  cobra.NoArgs,
		RunE:  runProjects,
	}
	docsCmd = &cobra.Command{
		Use:   "docs [topic]",
		Short: "Print the agent guide to codemap (topics: overview, workflow, commands, accuracy, ecosystem)",
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
	initCmd.Flags().Bool("local", false, "create a .codemap directory inside the project")
	callersCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callers (Go)")
	calleesCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callees (Go)")
	impactCmd.Flags().Int("depth", 3, "max hops for the blast radius")
	semanticCmd.Flags().Int("top", 10, "maximum results")
	hotspotsCmd.Flags().Int("top", 20, "maximum results")
	orphansCmd.Flags().Int("top", 50, "maximum results")
	findCmd.Flags().Int("top", 50, "maximum results")
	annotateCmd.Flags().String("source", "note", "annotation source: note, mongosh, postgres, vidtrace, …")
	annotateCmd.Flags().String("note", "", "free-form note text")
	annotateCmd.Flags().String("data", "", "opaque data payload (e.g. JSON from a DB query)")
	annotationsCmd.Flags().Int64("rm", 0, "remove the annotation with this id")

	rootCmd.AddCommand(versionCmd, initCmd, indexCmd, statusCmd, serveCmd, studioCmd,
		callersCmd, calleesCmd, impactCmd, semanticCmd, hotspotsCmd, orphansCmd, pathCmd, symbolsCmd, findCmd, sourceCmd, projectsCmd, docsCmd,
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
	rep, err := app.NewService(sess).Index(cmd.Context(), cwd, index.Options{Reindex: reindex}, !noEmbed)
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
	fmt.Printf("  files: %d scanned, %d indexed, %d skipped\n", rep.FilesScanned, rep.FilesIndexed, rep.FilesSkipped)
	fmt.Printf("  graph: %d nodes, %d edges (embeddings: %v)\n", rep.Nodes, rep.Edges, rep.Embedded)
	for _, e := range rep.Errors {
		fmt.Fprintf(os.Stderr, "  ! %s: %s\n", e.File, e.Err)
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
	rep, err := app.NewService(sess).Status(cwd)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Registered {
		fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", rep.Project)
		return nil
	}
	fmt.Printf("Project: %s\n  path:  %s\n  nodes: %d\n  edges: %d\n  files: %d\n",
		rep.Project, rep.Path, rep.Nodes, rep.Edges, rep.Files)
	if len(rep.Languages) > 0 {
		fmt.Printf("  languages: %s\n", formatCounts(rep.Languages))
	}
	if len(rep.Kinds) > 0 {
		fmt.Printf("  kinds:     %s\n", formatCounts(rep.Kinds))
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

func runCallers(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
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
	if useLSP {
		label += " (precise, via gopls)"
	}
	renderRefs(label, rep.Results)
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
	if useLSP {
		label += " (precise, via gopls)"
	}
	renderRefs(label, rep.Results)
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
	rep, err := app.NewService(sess).Impact(cwd, args[0], depth)
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
	fmt.Printf("  direct callers: %d\n", len(rep.DirectCallers))
	fmt.Printf("  blast radius:   %d (depth ≤ %d)\n", len(rep.BlastRadius), depth)
	fmt.Printf("  tests covering: %d\n", len(rep.Tests))
	if rep.Untested {
		fmt.Println("  ⚠ no tests reach this symbol")
	}
	// List the covering tests explicitly so you know what to run — they're a
	// subset of the blast radius, but spelling them out beats hunting for ✓.
	if len(rep.Tests) > 0 {
		fmt.Println("  covering tests (run these):")
		for _, t := range rep.Tests {
			fmt.Printf("     %-36s %s:%d\n", disp(t.FQN, t.Symbol), t.File, t.StartLine)
		}
	}
	if len(rep.BlastRadius) > 0 {
		fmt.Println("  affected (blast radius):")
		for _, n := range rep.BlastRadius {
			marker := " "
			if n.Kind == "test" {
				marker = "✓"
			}
			fmt.Printf("   %s [%d] %-36s %s:%d\n", marker, n.Depth, disp(n.FQN, n.Symbol), n.File, n.StartLine)
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
	rep, err := app.NewService(sess).Semantic(cmd.Context(), cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hits) == 0 {
		fmt.Println("no matches")
		return nil
	}
	for _, h := range rep.Hits {
		fmt.Printf("  %.3f  %-36s %s:%d\n", h.Score, disp(h.FQN, h.Symbol), h.File, h.StartLine)
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
		fmt.Printf("  %4d  %-36s %s:%d\n", h.InDegree, disp(h.FQN, h.Symbol), h.File, h.StartLine)
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
	rep, err := app.NewService(sess).Orphans(cwd, top)
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
	rep, err := app.NewService(sess).Path(cwd, args[0], args[1])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		fmt.Printf("no call path from %s to %s\n", rep.From, rep.To)
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
		fmt.Printf("  %-9s %-26s %s\n", h.Kind, fmt.Sprintf("%s:%d", h.File, h.StartLine),
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
	if len(args) == 1 {
		id, err := svc.AnnotateNode(cwd, args[0], source, note, data)
		if err != nil {
			return err
		}
		fmt.Printf("annotated %s  (#%d, source=%s)\n", args[0], id, source)
		return nil
	}
	id, target, err := svc.AnnotatePath(cwd, args[0], args[1], source, note, data)
	if err != nil {
		return err
	}
	fmt.Printf("annotated path %s  (#%d, source=%s)\n", target, id, source)
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
	for _, a := range rep.Annotations {
		fmt.Printf("#%-4d %-5s %-8s %s\n", a.ID, a.Kind, a.Source, a.Target)
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

// disp prefers the fully-qualified name so same-named symbols are distinguishable.
func disp(fqn, symbol string) string {
	if fqn != "" {
		return fqn
	}
	return symbol
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
