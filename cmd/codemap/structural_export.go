/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

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
Use --offset/--limit to consume every page; complete=true marks the final page.

--files/--files-from restrict the export to the given files and switch the
response to codemap.structural-export.v2: ordinals and total_records are
scoped to the filtered deterministic order, files_filter plus its
files_filter_fingerprint echo the requested slice, and index_fingerprint keeps
identifying the full underlying index.`,
		Args: cobra.NoArgs,
		RunE: runStructuralExport,
	}
	cmd.Flags().Int("offset", 0, "zero-based record offset in deterministic file/line/FQN order")
	cmd.Flags().Int("limit", app.DefaultStructuralExportLimit, fmt.Sprintf("maximum records in this page (max %d)", app.MaxStructuralExportLimit))
	cmd.Flags().Int("max-content-bytes", app.DefaultStructuralExportContentBytes, fmt.Sprintf("maximum source bytes per symbol (max %d; UTF-8 safe)", app.MaxStructuralExportContentBytes))
	cmd.Flags().StringSlice("files", nil, "restrict the export to these project-relative files (v2 filtered export)")
	cmd.Flags().String("files-from", "", "read one project-relative file path per line from this file (v2 filtered export; - for stdin)")
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
	files, _ := cmd.Flags().GetStringSlice("files")
	if from, _ := cmd.Flags().GetString("files-from"); from != "" {
		lines, err := readFilesFromList(from)
		if err != nil {
			return err
		}
		files = append(files, lines...)
	}
	rep, err := svc.StructuralExport(cwd, app.StructuralExportOptions{
		Offset:          offset,
		Limit:           limit,
		MaxContentBytes: maxContentBytes,
		FilesFilter:     files,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	fmt.Printf("structural symbol export v%d for %s: %d/%d records (offset %d)\n",
		rep.SchemaVersion, rep.Project, rep.ReturnedRecords, rep.TotalRecords, rep.Offset)
	if rep.FilesFilterFingerprint != "" {
		fmt.Printf("filter: %d files, fingerprint %s\n", len(rep.FilesFilter), rep.FilesFilterFingerprint)
	}
	if !rep.Complete {
		fmt.Printf("next page: codemap export-symbols --offset %d --limit %d --json\n", rep.NextOffset, rep.Limit)
	}
	return nil
}

// readFilesFromList reads one path per line (trimmed, blanks dropped). "-"
// reads stdin, so a large delta can be piped without argv limits.
func readFilesFromList(path string) ([]string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read file list: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}
