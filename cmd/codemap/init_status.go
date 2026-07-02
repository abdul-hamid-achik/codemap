/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	mcpserver "github.com/abdul-hamid-achik/codemap/internal/mcp"
	"github.com/abdul-hamid-achik/codemap/internal/tui"
	"github.com/spf13/cobra"
)

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

func runInit(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
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

func runStatus(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
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
	// Attach live background-daemon state (nil if none is running) so a human or
	// agent can tell whether the index is being kept fresh automatically.
	out := daemon.AttachStatus(rep)
	if jsonOut(cmd) {
		return printJSON(out)
	}
	if !rep.Registered {
		fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", rep.Project)
		printDaemonLine(out.Daemon)
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
	if len(rep.Siblings) > 0 {
		fmt.Printf("  also indexed in: %s\n", strings.Join(rep.Siblings, ", "))
	}
	printDaemonLine(out.Daemon)
	return nil
}

// printDaemonLine reports whether a background daemon is keeping the index fresh.
// A nil info means no daemon answered the control socket.
func printDaemonLine(info *daemon.Info) {
	if info == nil {
		fmt.Println("  daemon: not running")
		return
	}
	line := fmt.Sprintf("  daemon: running — %s (pid %d", info.ProjectName, info.PID)
	if info.Watching {
		line += ", watching"
	}
	line += ")"
	if info.LastReindexAt != "" {
		line += "  last reindex " + info.LastReindexAt
	}
	fmt.Println(line)
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
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
	defer func() { _ = sess.Close() }()
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
	defer func() { _ = sess.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mcpserver.NewServer(sess).Run(ctx)
}
