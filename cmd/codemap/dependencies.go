/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var dependenciesCmd = &cobra.Command{
	Use:   "dependencies <file>",
	Short: "Inbound call, reference, and import evidence for a file, with coverage",
	Long: `Report indexed inbound call, reference, and import evidence for a file.

Confirmed relationships are usable exact-file evidence; candidates require
verification because they come from name fan-out, package scope, or a stale
snapshot. Missing evidence never proves that a file is independent or safe to
delete while any coverage domain remains incomplete.`,
	Example: `  codemap dependencies internal/app/review.go
  codemap dependencies internal/app/review.go --json`,
	Args: cobra.ExactArgs(1),
	RunE: runDependencies,
}

func runDependencies(cmd *cobra.Command, args []string) error {
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
	rep, err := svc.Dependencies(cwd, args[0])
	if err != nil {
		return err
	}
	if !rep.Found {
		return notFoundError(
			fmt.Sprintf("file %q has no indexed nodes", rep.File),
			"check the path relative to the project root")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}

	fmt.Printf("Dependencies entering %s (%s)\n", rep.File, rep.Project)
	fmt.Printf("  evidence:      %d total\n", rep.EvidenceTotal)
	fmt.Printf("  confidence:    %d confirmed · %d candidate\n", rep.ConfirmedTotal, rep.CandidateTotal)
	fmt.Printf("  scope:         %d file-scoped (%d confirmed · %d candidate) · %d package-scoped\n",
		rep.FileScopedEvidenceTotal, rep.ConfirmedFileScopedTotal, rep.CandidateFileScopedTotal,
		rep.PackageScopedEvidenceTotal)
	fmt.Printf("  dependents:    %d", rep.DependentsTotal)
	if rep.DependentsTruncated > 0 {
		fmt.Printf(" (%d shown, %d omitted)", len(rep.Dependents), rep.DependentsTruncated)
	}
	fmt.Println()
	fmt.Printf("  samples:       %d shown", rep.SamplesTotal)
	if rep.SamplesTruncated > 0 {
		fmt.Printf(" (%d omitted)", rep.SamplesTruncated)
	}
	fmt.Println()
	fmt.Printf("  call graph:    %s\n", rep.CallGraph)
	if rep.Stale {
		fmt.Println("  ⚠ stale index — run codemap index before relying on missing evidence")
	}

	if len(rep.Dependents) == 0 {
		fmt.Println("  no inbound evidence found")
	} else {
		fmt.Println("  dependent files:")
		for _, dependent := range rep.Dependents {
			kinds := make([]string, 0, len(dependent.Kinds))
			for _, evidence := range dependent.Kinds {
				label := fmt.Sprintf("%s:%d", evidence.Kind, evidence.Total)
				confidence := make([]string, 0, 2)
				if evidence.ConfirmedTotal > 0 {
					confidence = append(confidence, fmt.Sprintf("%d confirmed", evidence.ConfirmedTotal))
				}
				if evidence.CandidateTotal > 0 {
					confidence = append(confidence, fmt.Sprintf("%d candidate", evidence.CandidateTotal))
				}
				if len(confidence) > 0 {
					label += " (" + strings.Join(confidence, ", ") + ")"
				}
				if evidence.PackageScopedTotal > 0 {
					label += fmt.Sprintf(" (package:%d)", evidence.PackageScopedTotal)
				}
				kinds = append(kinds, label)
			}
			fmt.Printf("     %s  %s  [%d confirmed · %d candidate]\n",
				dependent.File, strings.Join(kinds, " · "), dependent.ConfirmedTotal, dependent.CandidateTotal)
			for _, evidence := range dependent.Kinds {
				if len(evidence.Samples) == 0 {
					continue
				}
				sample := evidence.Samples[0]
				confidence := sample.Confidence
				if confidence == "" {
					confidence = "unclassified"
				}
				reason := strings.ReplaceAll(sample.ConfidenceReason, "_", " ")
				if reason != "" {
					confidence += " (" + reason + ")"
				}
				fmt.Printf("       ↳ %s:%d → %s:%d  [%s, %s; confidence: %s]\n",
					sample.Source.File, sample.Source.StartLine,
					sample.Target.File, sample.Target.StartLine,
					evidence.Kind, sample.TargetScope, confidence)
			}
		}
	}

	coverage := "incomplete"
	if rep.Coverage.Complete {
		coverage = "complete"
	}
	fmt.Println("  coverage:      " + coverage)
	for _, domain := range rep.Coverage.Domains {
		fmt.Printf("     %-18s %-11s %s\n", domain.Domain, domain.Status, domain.Note)
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	return nil
}
