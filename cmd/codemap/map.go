/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var mapCmd = newMapCmd()

func newMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Architecture overview: subsystems, cross-subsystem bridges, hubs, and entrypoints",
		Long: `Build a bounded, deterministic architecture overview from the indexed graph.

Subsystems are source-path shaped rather than inferred compiler facts. Bridges
preserve edge direction/type/provenance, while hubs and entrypoints carry the
same call-graph and freshness honesty signals as the underlying queries.`,
		Args: cobra.NoArgs,
		RunE: runMap,
	}
	cmd.Flags().Int("top-subsystems", app.DefaultMapSubsystems, fmt.Sprintf("maximum subsystems to include (max %d)", app.MaxMapSubsystems))
	cmd.Flags().Int("top-bridges", app.DefaultMapBridges, fmt.Sprintf("maximum cross-subsystem bridges to include (max %d)", app.MaxMapBridges))
	cmd.Flags().Int("top-hubs", app.DefaultMapHubs, fmt.Sprintf("maximum call-graph hubs to include (max %d)", app.MaxMapHubs))
	cmd.Flags().Int("top-entrypoints", app.DefaultMapEntrypoints, fmt.Sprintf("maximum likely entrypoints to include (max %d)", app.MaxMapEntrypoints))
	return cmd
}

func runMap(cmd *cobra.Command, _ []string) error {
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
	topSubsystems, _ := cmd.Flags().GetInt("top-subsystems")
	topBridges, _ := cmd.Flags().GetInt("top-bridges")
	topHubs, _ := cmd.Flags().GetInt("top-hubs")
	topEntrypoints, _ := cmd.Flags().GetInt("top-entrypoints")
	rep, err := svc.ArchitectureMap(cwd, app.ArchitectureMapOptions{
		TopSubsystems: topSubsystems, TopBridges: topBridges,
		TopHubs: topHubs, TopEntrypoints: topEntrypoints,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderArchitectureMap(rep)
	return nil
}

func renderArchitectureMap(rep *app.ArchitectureMapReport) {
	callGraph := rep.CallGraph
	if callGraph == "" {
		callGraph = "unknown"
	}
	fmt.Printf("Architecture map for %s (strategy: %s, call graph: %s)\n", rep.Project, rep.Strategy, callGraph)
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before treating this map as current")
	}
	if rep.Resolution != "" {
		fmt.Printf("⚠ %s\n", rep.Resolution)
	}
	for _, partial := range rep.PartialErrors {
		fmt.Printf("⚠ partial: %s\n", partial)
	}

	fmt.Printf("\nSubsystems (%d/%d):\n", len(rep.Subsystems), rep.SubsystemsTotal)
	if len(rep.Subsystems) == 0 {
		fmt.Println("  none")
	} else {
		fmt.Printf("  %-28s %6s %7s %8s %7s %8s  %s\n", "NAME", "FILES", "SYMBOLS", "INTERNAL", "IN", "OUT", "LANGUAGES")
		for _, subsystem := range rep.Subsystems {
			fmt.Printf("  %-28s %6d %7d %8d %7d %8d  %s\n",
				truncStr(subsystem.Name, 28), subsystem.Files, subsystem.Symbols, subsystem.InternalEdges,
				subsystem.InboundEdges, subsystem.OutboundEdges, formatCounts(subsystem.Languages))
		}
	}

	fmt.Printf("\nBridges (%d/%d):\n", len(rep.Bridges), rep.BridgesTotal)
	if len(rep.Bridges) == 0 {
		fmt.Println("  none")
	} else {
		for _, bridge := range rep.Bridges {
			provenance := formatCounts(bridge.Provenance)
			if provenance != "" {
				provenance = " · " + provenance
			}
			fmt.Printf("  %s → %s  %-10s ×%d%s\n", bridge.From, bridge.To, bridge.EdgeType, bridge.Count, provenance)
		}
	}

	fmt.Printf("\nEntrypoints (%d):\n", len(rep.Entrypoints))
	if len(rep.Entrypoints) == 0 {
		fmt.Println("  none")
	} else {
		for _, entry := range rep.Entrypoints {
			fmt.Printf("  %2d. %-36s %s:%d  %s\n", entry.Rank, truncStr(disp(entry.FQN, entry.Symbol), 36), entry.File, entry.StartLine, entry.Reason)
		}
	}

	fmt.Printf("\nHubs (%d):\n", len(rep.Hubs))
	if len(rep.Hubs) == 0 {
		fmt.Println("  none")
	} else {
		for _, hub := range rep.Hubs {
			fmt.Printf("  %5d  %-36s %s:%d\n", hub.InDegree, truncStr(disp(hub.FQN, hub.Symbol), 36), hub.File, hub.StartLine)
		}
	}
	if rep.Truncated {
		fmt.Println("\n… map truncated; raise the matching --top-* limit for more")
	}
}
