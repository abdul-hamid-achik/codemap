/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var structuralExportCmd = newStructuralExportCmd()

func newStructuralExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-symbols",
		Short: "Export a bounded, versioned page of structural symbol records for sibling tools",
		Long: `Export deterministic symbol records without exposing codemap's SQLite store or Go packages.

The JSON contract is paginated and includes durable source selectors, indexed
source hashes, and bounded current source content. Content is omitted explicitly
when a file is stale, missing, unreadable, or resolves outside the project.
Use --offset/--limit to consume every page; complete=true marks the final page.`,
		Args: cobra.NoArgs,
		RunE: runStructuralExport,
	}
	cmd.Flags().Int("offset", 0, "zero-based record offset in deterministic file/line/FQN order")
	cmd.Flags().Int("limit", app.DefaultStructuralExportLimit, fmt.Sprintf("maximum records in this page (max %d)", app.MaxStructuralExportLimit))
	cmd.Flags().Int("max-content-bytes", app.DefaultStructuralExportContentBytes, fmt.Sprintf("maximum source bytes per symbol (max %d; UTF-8 safe)", app.MaxStructuralExportContentBytes))
	return cmd
}

func runStructuralExport(cmd *cobra.Command, _ []string) error {
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
	offset, _ := cmd.Flags().GetInt("offset")
	limit, _ := cmd.Flags().GetInt("limit")
	maxContentBytes, _ := cmd.Flags().GetInt("max-content-bytes")
	rep, err := svc.StructuralExport(cwd, app.StructuralExportOptions{
		Offset:          offset,
		Limit:           limit,
		MaxContentBytes: maxContentBytes,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("structural symbol export v%d for %s: %d/%d records (offset %d)\n",
		rep.SchemaVersion, rep.Project, rep.ReturnedRecords, rep.TotalRecords, rep.Offset)
	if !rep.Complete {
		fmt.Printf("next page: codemap export-symbols --offset %d --limit %d --json\n", rep.NextOffset, rep.Limit)
	}
	return nil
}
