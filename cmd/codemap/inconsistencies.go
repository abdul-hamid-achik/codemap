/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var inconsistenciesCmd = &cobra.Command{
	Use:   "inconsistencies",
	Short: "Report where the compiled knowledge contradicts itself or the working tree",
	Long: `Surface the known contradiction classes in one bounded report:

- dangling annotations whose target no longer matches an indexed symbol
  (repair: annotate --retarget <id> <new-target>, or annotations --rm <id>)
- precise-resolved files that still emit name-based call edges
  (repair: re-run the precise pass)
- call-graph coverage recorded for files that no longer have indexed nodes
  (repair: reindex)

An empty report is evidence of internal coherence, not of correctness; a
stale working tree makes every claim provisional and is reported as such.
CLI-only by contract: peers consume it through versioned CLI JSON.`,
	Args: cobra.NoArgs,
	RunE: runInconsistencies,
}

func runInconsistencies(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	rep, err := app.NewService(sess).Inconsistencies(targetDir(cmd))
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}

	fmt.Printf("inconsistencies v%d for %s%s\n",
		rep.SchemaVersion, rep.Project, staleSuffix(rep.Stale))
	fmt.Printf("dangling annotations: %d\n", len(rep.Dangling))
	for _, d := range rep.Dangling {
		line := fmt.Sprintf("  #%-4d %-5s %-8s %s", d.ID, d.Kind, d.Source, d.Target)
		fmt.Println(line)
		if d.Note != "" {
			fmt.Printf("        note: %s\n", d.Note)
		}
	}
	fmt.Printf("name-based call edges on precise-resolved files: %d\n", len(rep.NameEdges))
	for _, f := range rep.NameEdges {
		fmt.Printf("  %s (%s, resolver %s): %d name edges\n", f.FilePath, f.Language, f.Resolver, f.NameEdge)
	}
	fmt.Printf("coverage without indexed nodes: %d\n", len(rep.OrphanedCoverage))
	for _, f := range rep.OrphanedCoverage {
		fmt.Printf("  %s (resolver %s)\n", f.FilePath, f.Resolver)
	}
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	return nil
}

func staleSuffix(stale bool) string {
	if stale {
		return " (index is stale — see note)"
	}
	return ""
}
