/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Per-file precise call-graph coverage: rollups by language/directory + bounded per-file detail",
	Long: `Report per-file precise call-graph coverage: which files have a recorded
call_graph_coverage row (resolver + when), rolled up by language and by
directory (worst-covered first), plus an optional bounded per-file list.

This complements, not replaces, the per-query call_graph enum: call_graph
classifies the worst file among the definitions one query touched; coverage
answers the standing question "which files/packages are covered right now,
project-wide" before you even ask a symbol question. A file's stale flag is
independent of coverage: it means that file's on-disk content has drifted
since the last index, even if its coverage row hasn't been cleared yet.`,
	Args: cobra.NoArgs,
	RunE: runCoverage,
}

func runCoverage(cmd *cobra.Command, _ []string) error {
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
	prefix, _ := cmd.Flags().GetString("prefix")
	lang, _ := cmd.Flags().GetString("lang")
	uncovered, _ := cmd.Flags().GetBool("uncovered")
	files, _ := cmd.Flags().GetBool("files")
	top, _ := cmd.Flags().GetInt("top")
	rep, err := svc.Coverage(cwd, app.CoverageOptions{
		PathPrefix: prefix, Language: lang, OnlyUncovered: uncovered, Detail: files, Top: top,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}

	fmt.Printf("Coverage in %s\n", rep.Project)
	fmt.Printf("  %d files: %d covered, %d stale\n", rep.TotalFiles, rep.CoveredFiles, rep.StaleFiles)

	if len(rep.ByLanguage) > 0 {
		fmt.Println("  by language:")
		for lang, lr := range rep.ByLanguage {
			fmt.Printf("     %-14s %d files, %d covered, %d stale\n", lang, lr.Files, lr.Covered, lr.Stale)
		}
	}

	if len(rep.ByDirectory) > 0 {
		fmt.Println("  by directory (worst-covered first):")
		for _, dr := range rep.ByDirectory {
			fmt.Printf("     %-40s %d files, %d covered, %d stale\n", dr.Dir, dr.Files, dr.Covered, dr.Stale)
		}
		if rep.ByDirTruncated {
			fmt.Println("     … (more directories omitted — raise --top or use --json)")
		}
	}

	if rep.Files != nil {
		fmt.Printf("  files: %d shown", len(rep.Files))
		if rep.FilesTruncated {
			fmt.Printf(" (%d total, truncated)", rep.FilesTotal)
		}
		fmt.Println()
		for _, f := range rep.Files {
			mark := "  "
			if f.Stale {
				mark = " ⚠"
			}
			status := "uncovered"
			if f.Covered {
				status = f.Resolver
			}
			fmt.Printf("    %s %-50s %-10s %s\n", mark, f.File, status, f.ResolvedAt)
		}
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	return nil
}
