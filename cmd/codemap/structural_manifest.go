/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var structuralManifestCmd = &cobra.Command{
	Use:   "structural-manifest",
	Short: "Print the lightweight identity and freshness manifest for the structural index",
	Long: `Print a single, versioned preflight for consumers of export-symbols.

The manifest streams indexed symbol metadata through the same fingerprint used
by export-symbols without reading source bodies or loading the full export. Its
freshness counters report changed, new, and deleted working-tree files.`,
	Args: cobra.NoArgs,
	RunE: runStructuralManifest,
}

func runStructuralManifest(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	rep, err := app.NewService(sess).StructuralManifest(targetDir(cmd))
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	state := "fresh"
	if !rep.Freshness.Fresh {
		state = fmt.Sprintf("stale (changed=%d new=%d deleted=%d)",
			rep.Freshness.Changed, rep.Freshness.New, rep.Freshness.Deleted)
	}
	fmt.Printf("structural manifest v%d for %s: %d records, %s\n",
		rep.SchemaVersion, rep.Project, rep.TotalRecords, state)
	fmt.Printf("fingerprint: %s\n", rep.IndexFingerprint)
	return nil
}
