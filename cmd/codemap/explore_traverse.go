/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/spf13/cobra"
)

const defaultExploreDepth = 2

var (
	exploreCmd  = newExploreCmd()
	traverseCmd = newTraverseCmd()
)

func newExploreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explore <query>",
		Short: "Turn an intent query into exact, bounded structural neighborhoods",
		Long: `Search by intent, promote each usable hit to a durable source selector,
and return a compact callers/callees/references/tests neighborhood for every
exact definition. Source bodies stay omitted; follow a returned selector with
'codemap context' or 'codemap source' when one definition is worth opening.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runExplore,
	}
	cmd.Flags().Int("seeds", app.DefaultExploreSeeds,
		fmt.Sprintf("maximum search seeds (1-%d)", app.MaxExploreSeeds))
	cmd.Flags().Int("edges", app.DefaultExploreEdges,
		fmt.Sprintf("maximum callers/callees/references/tests per seed (1-%d)", app.MaxExploreEdges))
	cmd.Flags().Int("depth", defaultExploreDepth, "maximum blast-radius depth per seed (1-10)")
	return cmd
}

func newTraverseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traverse",
		Short: "Walk typed graph relations from one exact source definition",
		Long: `Perform a bounded, cycle-safe graph walk from one exact definition.

The start is always selected with --at <file>:<line>; codemap resolves that
position to the durable {file,start_line,fqn,kind} selector carried in JSON.
Bare symbol names are deliberately not accepted because heterogeneous traversal
must never silently merge same-named definitions.`,
		Args: cobra.NoArgs,
		RunE: runTraverse,
	}
	cmd.Flags().String("at", "", "select the exact starting definition: <file>:<line> (required)")
	cmd.Flags().String("direction", graph.TraversalBoth, "walk outgoing, incoming, or both relations")
	cmd.Flags().StringSlice("edge-types", nil, "relation types to follow (comma-separated; defaults to calls,references,imports,implements,overrides,depends_on,tests)")
	cmd.Flags().Int("depth", app.DefaultTraverseDepth,
		fmt.Sprintf("maximum traversal depth (1-%d)", app.MaxTraverseDepth))
	cmd.Flags().Int("limit", app.DefaultTraverseLimit,
		fmt.Sprintf("maximum reached nodes (1-%d)", app.MaxTraverseLimit))
	return cmd
}

func runExplore(cmd *cobra.Command, args []string) error {
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
	seeds, _ := cmd.Flags().GetInt("seeds")
	edges, _ := cmd.Flags().GetInt("edges")
	depth, _ := cmd.Flags().GetInt("depth")
	rep, err := svc.Explore(cmd.Context(), cwd, strings.Join(args, " "), app.ExploreOptions{
		Seeds: seeds, Edges: edges, Depth: depth,
	})
	if err != nil {
		return err
	}
	if len(rep.Seeds) == 0 {
		return notFoundError(fmt.Sprintf("no matches for %q", rep.Query), "try a broader intent or symbol name")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderExplore(rep)
	return nil
}

func runTraverse(cmd *cobra.Command, _ []string) error {
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
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	if selector == nil {
		return fmt.Errorf("traverse requires --at <file>:<line> to select one exact definition")
	}
	direction, _ := cmd.Flags().GetString("direction")
	edgeTypes, _ := cmd.Flags().GetStringSlice("edge-types")
	depth, _ := cmd.Flags().GetInt("depth")
	limit, _ := cmd.Flags().GetInt("limit")
	rep, err := svc.TraverseBySelector(cwd, *selector, app.TraverseOptions{
		Direction: direction, EdgeTypes: edgeTypes, Depth: depth, Limit: limit,
	})
	if err != nil {
		return err
	}
	if !rep.Found {
		return notFoundError("the selected definition is no longer in the index", "run: codemap index")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderTraverse(rep)
	return nil
}

func renderExplore(rep *app.ExploreReport) {
	mode := rep.SearchMode
	if mode == "" {
		mode = "none"
	}
	search := mode
	if rep.Fusion != "" {
		search += "/" + rep.Fusion
	}
	fmt.Printf("Explore %q in %s (search: %s)\n", rep.Query, rep.Project, search)
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	for _, partial := range rep.PartialErrors {
		label := partial.Component
		if partial.Symbol != "" {
			label += "/" + partial.Symbol
		}
		fmt.Printf("  ⚠ partial %s: %s\n", label, partial.Error)
	}

	fmt.Printf("\nSeeds (%d; %d joined):\n", len(rep.Seeds), len(rep.Seeds)-rep.NotJoined)
	for i, seed := range rep.Seeds {
		joined := "joined"
		if seed.Selector == nil {
			joined = "not joined"
		}
		fmt.Printf("  %2d. %.3f  %-36s %s:%d  [%s]\n", i+1, seed.Score,
			truncStr(disp(seed.FQN, seed.Symbol), 36), seed.File, seed.StartLine, joined)
	}

	fmt.Printf("\nContexts (%d):\n", len(rep.Contexts))
	if len(rep.Contexts) == 0 {
		fmt.Println("  none — follow an unjoined hit with codemap find or symbol-at")
		return
	}
	for _, context := range rep.Contexts {
		loc := ""
		if context.Selector != nil {
			loc = fmt.Sprintf("%s:%d", context.Selector.File, context.Selector.StartLine)
		}
		fmt.Printf("  %-36s %-24s callers:%d callees:%d refs:%d tests:%d blast:%d\n",
			truncStr(disp("", context.Symbol), 36), loc,
			len(context.Callers), len(context.Callees), len(context.References), len(context.Tests), context.BlastRadius)
	}
}

func renderTraverse(rep *app.TraverseReport) {
	start := "(unknown)"
	location := ""
	if rep.Start != nil {
		start = rep.Start.FQN
		if start == "" {
			start = rep.Start.File
		}
		location = fmt.Sprintf("%s:%d", rep.Start.File, rep.Start.StartLine)
	}
	fmt.Printf("Traverse from %s — %s (%s)\n", start, location, rep.Project)
	fmt.Printf("  direction: %s · edge types: %s · depth ≤ %d · node limit: %d\n",
		rep.Direction, strings.Join(rep.EdgeTypes, ","), rep.DepthLimit, rep.NodeLimit)
	renderCallGraphReliability(rep.CallGraph, rep.Resolution, "")

	fmt.Printf("\nHops (%d):\n", len(rep.Hops))
	if len(rep.Hops) == 0 {
		fmt.Println("  none")
	} else {
		for _, hop := range rep.Hops {
			arrow := "→"
			if hop.Direction == graph.TraversalIncoming {
				arrow = "←"
			}
			fmt.Printf("  d%-2d %s %-12s %-36s %s:%d  [%s]\n",
				hop.Depth, arrow, hop.EdgeType, truncStr(disp(hop.Symbol.FQN, hop.Symbol.Symbol), 36),
				hop.Symbol.File, hop.Symbol.StartLine, hop.Confidence)
		}
	}

	fmt.Printf("\nDomains (%d):\n", len(rep.Domains))
	if len(rep.Domains) == 0 {
		fmt.Println("  none")
	} else {
		for _, domain := range rep.Domains {
			fmt.Printf("  %-12s confirmed:%d candidate:%d\n", domain.EdgeType, domain.Confirmed, domain.Candidate)
		}
	}
	if rep.Truncated {
		fmt.Println("\n… traversal truncated; raise --limit or narrow --edge-types/--direction")
	}
}
