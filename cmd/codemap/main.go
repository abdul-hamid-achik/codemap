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
		return cmd.Help()
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

// notImplemented is a placeholder for command handlers that land in later
// build-loop iterations (see BACKLOG.md). It keeps the CLI surface visible and
// the binary buildable while the internals are filled in.
func notImplemented(feature string) error {
	return fmt.Errorf("%s is not implemented yet (see BACKLOG.md)", feature)
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
		RunE:    func(cmd *cobra.Command, args []string) error { return notImplemented("studio") },
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

	rootCmd.AddCommand(versionCmd, initCmd, indexCmd, statusCmd, serveCmd, studioCmd)
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
