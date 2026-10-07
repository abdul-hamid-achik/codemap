/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var processesCmd = newProcessesCmd()

func init() { rootCmd.AddCommand(processesCmd) }

func newProcessesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "processes",
		Short: "Execution flows from every entrypoint: route/command -> handler -> service chain",
		Long: `List how each entrypoint works in one call: for every CLI command, HTTP route,
MCP/RPC tool, page or program with a resolved handler, the chain of definitions
it reaches in call order (the same builder as 'codemap flow', same-name fan-out
collapsed), flattened into steps with file:line.

Computed on demand from the stored graph; nothing is persisted. Bounded by
--top, --depth and --max-steps. --query keeps the processes whose name or steps
match the query's content words (same tokenization as the keyword search floor),
best match first, e.g. 'codemap processes --query "how does signup work"'.`,
		Args: cobra.NoArgs,
		RunE: runProcesses,
	}
	cmd.Flags().StringSlice("kind", nil, "only these entrypoint kinds (comma-separated): program, cli_command, rpc_tool, http_route, api_route, page")
	cmd.Flags().String("query", "", "keep processes whose name or steps match these content words")
	cmd.Flags().Int("top", app.DefaultProcessesTop, fmt.Sprintf("maximum processes to return (max %d)", app.MaxProcessesTop))
	cmd.Flags().Int("depth", app.DefaultProcessDepth, fmt.Sprintf("maximum call depth per process (max %d)", app.MaxFlowDepth))
	cmd.Flags().Int("max-steps", app.DefaultProcessSteps, fmt.Sprintf("maximum steps per process (max %d)", app.MaxProcessSteps))
	return cmd
}

func runProcesses(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	kinds, _ := cmd.Flags().GetStringSlice("kind")
	query, _ := cmd.Flags().GetString("query")
	top, _ := cmd.Flags().GetInt("top")
	depth, _ := cmd.Flags().GetInt("depth")
	maxSteps, _ := cmd.Flags().GetInt("max-steps")
	rep, err := svc.Processes(targetDir(cmd), app.ProcessesOptions{
		Kinds: kinds, Query: query, Top: top, Depth: depth, MaxSteps: maxSteps,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderProcesses(rep)
	return nil
}

func renderProcesses(rep *app.ProcessesReport) {
	callGraph := rep.CallGraph
	if callGraph == "" {
		callGraph = "unknown"
	}
	fmt.Printf("Processes of %s (%d shown of %d, call graph: %s)\n", rep.Project, len(rep.Processes), rep.ProcessesTotal, callGraph)
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before treating these flows as current")
	}
	if rep.Resolution != "" {
		fmt.Printf("⚠ %s\n", rep.Resolution)
	}
	for _, partial := range rep.PartialErrors {
		fmt.Printf("⚠ partial: %s\n", partial)
	}
	if len(rep.Processes) == 0 {
		fmt.Println("\nno processes — no entrypoint with a resolved handler matched (see 'codemap features')")
	}
	for _, p := range rep.Processes {
		more := ""
		if p.Truncated {
			more = " (truncated)"
		}
		fmt.Printf("\n%s  [%s · %d steps · %d files%s]\n", p.ID, p.Kind, len(p.Steps), len(p.Files), more)
		for _, s := range p.Steps {
			fmt.Printf("  %s%-40s %s:%d\n", strings.Repeat("  ", s.Depth), truncStr(disp(s.FQN, s.Symbol), 40), s.File, s.StartLine)
		}
	}
	for _, note := range rep.Notes {
		fmt.Printf("note: %s\n", note)
	}
	if rep.Truncated {
		fmt.Println("… more processes available; raise --top or narrow with --kind/--query")
	}
}
