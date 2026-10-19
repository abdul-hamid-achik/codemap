/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var vectorsGCCmd = &cobra.Command{
	Use:   "vectors-gc",
	Short: "Prune local vector records no graph node claims (orphan embeddings)",
	Long: `Scan the shared local vector store and delete orphan records: embeddings
whose graph node was deleted or replaced, or whose project left the graph
(e.g. migrated to the vecgrep semantic backend). Use --dry-run to only report.

Opening the store loads every project's records into memory, so this is an
explicit maintenance command — nothing runs it implicitly. CLI-only.`,
	Args: cobra.NoArgs,
	RunE: runVectorsGC,
}

func runVectorsGC(cmd *cobra.Command, _ []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	rep, err := app.NewService(sess).PruneOrphanVectors(dryRun)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.StorePresent {
		fmt.Println("no local vector store found; nothing to prune")
		return nil
	}
	verb := "deleted"
	if rep.DryRun {
		verb = "would delete"
	}
	fmt.Printf("scanned %d records, %d orphans, %s %d\n", rep.Scanned, rep.Orphans, verb, rep.Deleted)
	for _, p := range rep.Projects {
		if p.Orphans > 0 {
			fmt.Printf("  %s: %d/%d orphaned\n", p.Project, p.Orphans, p.Scanned)
		}
	}
	return nil
}
