/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var featuresCmd = newFeaturesCmd()

func init() { rootCmd.AddCommand(featuresCmd) }

func newFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "Capability inventory: CLI commands, HTTP routes, MCP/RPC tools, pages, and programs with their handlers",
		Long: `List what the software can DO and where each capability lives: user-facing entry
surfaces (cobra/urfave commands, HTTP routes, MCP tools, gRPC services, Next.js
pages, programs), each tied to the handler symbol that implements it, its
description from the framework registration itself, and a bounded call
footprint (symbols, files, subsystems, covering tests).

Go registrations are read with the Go AST; TS/JS and Python use pattern
detectors and are labelled candidate. Pair with 'codemap context' on a handler
selector to drill in.`,
		Args: cobra.NoArgs,
		RunE: runFeatures,
	}
	cmd.Flags().StringSlice("kind", nil, "only these kinds (comma-separated): program, cli_command, rpc_tool, http_route, api_route, page")
	cmd.Flags().String("query", "", "case-insensitive substring over label, description, handler and file")
	cmd.Flags().Int("top", app.DefaultFeaturesTop, fmt.Sprintf("maximum features to return (max %d)", app.MaxFeaturesTop))
	cmd.Flags().Int("depth", app.DefaultFootprintDepth, fmt.Sprintf("footprint call-walk depth (max %d)", app.MaxFootprintDepth))
	cmd.Flags().Bool("no-footprint", false, "skip footprints (faster)")
	return cmd
}

func runFeatures(cmd *cobra.Command, _ []string) error {
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
	noFootprint, _ := cmd.Flags().GetBool("no-footprint")
	rep, err := svc.Features(targetDir(cmd), app.FeaturesOptions{
		Kinds: kinds, Query: query, Top: top, Depth: depth, NoFootprint: noFootprint,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderFeatures(rep)
	return nil
}

func renderFeatures(rep *app.FeaturesReport) {
	callGraph := rep.CallGraph
	if callGraph == "" {
		callGraph = "unknown"
	}
	fmt.Printf("Features of %s (%d total, call graph: %s)\n", rep.Project, rep.FeaturesTotal, callGraph)
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before treating this inventory as current")
	}
	if rep.Resolution != "" {
		fmt.Printf("⚠ %s\n", rep.Resolution)
	}
	for _, partial := range rep.PartialErrors {
		fmt.Printf("⚠ partial: %s\n", partial)
	}

	var order []string
	groups := map[string][]app.Feature{}
	for _, f := range rep.Features {
		if _, ok := groups[f.Surface]; !ok {
			order = append(order, f.Surface)
		}
		groups[f.Surface] = append(groups[f.Surface], f)
	}
	if len(order) == 0 {
		fmt.Println("\nno features detected")
	}
	for _, surface := range order {
		fs := groups[surface]
		fmt.Printf("\n%s (%d):\n", surface, len(fs))
		for _, f := range fs {
			desc := truncStr(f.Description, 60)
			handler := "-"
			if f.Handler != nil {
				handler = fmt.Sprintf("%s:%d", f.Handler.File, f.Handler.StartLine)
			}
			mark := ""
			if f.Confidence == "candidate" {
				mark = " ?"
			}
			if f.Hidden {
				mark += " (hidden)"
			}
			fmt.Printf("  %-34s %-60s %s%s\n", truncStr(f.Label, 34), desc, handler, mark)
			if fp := f.Footprint; fp != nil {
				more := ""
				if fp.Truncated {
					more = "+"
				}
				fmt.Printf("  %-34s   %d sym%s · %d files · %d subsystems · %d tests\n", "", fp.Symbols, more, fp.Files, len(fp.Subsystems), fp.Tests)
			}
		}
	}
	if len(rep.ByKind) > 0 {
		var parts []string
		for _, k := range []string{"program", "cli_command", "rpc_tool", "http_route", "api_route", "page"} {
			if n := rep.ByKind[k]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", k, n))
			}
		}
		fmt.Printf("\n%s\n", strings.Join(parts, " · "))
	}
	for _, note := range rep.Notes {
		fmt.Printf("note: %s\n", note)
	}
	if rep.Truncated {
		fmt.Println("… inventory truncated; raise --top for more")
	}
	fmt.Println("(? = candidate: pattern-detected or handler unresolved)")
}
