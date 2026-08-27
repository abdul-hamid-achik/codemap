/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

var taskContextCmd = newTaskContextCmd()

func newTaskContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		// `brief` is a convenience alias only; docs and playbooks teach
		// task-context so the token can't be confused with --brief (dropping
		// source bodies) on source/context/context_batch.
		Use:     "task-context <task>",
		Aliases: []string{"brief"},
		Short:   "One-call, mode-scoped orientation for a task",
		Long: `Assemble a bounded context package for a task in one call.

The task text is used verbatim as the retrieval query — codemap never interprets
intent. --mode picks the deterministic composition: understand (freshness +
explore neighborhoods), change (contexts + impact drill-downs + related files),
debug (explore + caller/callee-emphasized contexts). 'review' is not a mode:
diff-scoped post-edit analysis is 'codemap review'.

Every section keeps its own caps and honesty signals (call_graph weakest-of,
partial_errors, *_total); staleness is reported, never acted on.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runTaskContext,
	}
	cmd.Flags().String("mode", app.TaskModeUnderstand,
		"composition to assemble: understand|change|debug (review is codemap review)")
	cmd.Flags().StringArray("at", nil,
		"restrict to an exact definition: <file>:<line> (repeatable, up to 25; requires --mode change or debug)")
	return cmd
}

func runTaskContext(cmd *cobra.Command, args []string) error {
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
	mode, _ := cmd.Flags().GetString("mode")
	ats, _ := cmd.Flags().GetStringArray("at")
	// Validate the mode/--at combination before resolving positions, so a bad
	// combination is exit-1 invalid_input rather than an exit-2 miss on a
	// position the service was going to reject anyway.
	probe := app.TaskContextOptions{Mode: mode}
	if len(ats) > 0 {
		probe.Selectors = []app.SymbolSelector{{File: "probe"}} // count is all validation needs
	}
	if err := app.ValidateTaskContext(strings.Join(args, " "), probe); err != nil {
		return err
	}
	selectors, err := selectorsFromAtFlags(svc, cwd, cmd)
	if err != nil {
		return err
	}
	rep, err := svc.TaskContext(cmd.Context(), cwd, strings.Join(args, " "),
		app.TaskContextOptions{Mode: mode, Selectors: selectors})
	if err != nil {
		return err
	}
	if !taskContextResolvedAnything(rep) {
		// A backend failure in the only content section is operational (exit 1),
		// not a query dead end — the partial error names the real cause.
		if len(rep.PartialErrors) > 0 {
			pe := rep.PartialErrors[0]
			return fmt.Errorf("task-context could not assemble any section: %s: %s", pe.Component, pe.Error)
		}
		return notFoundError(
			fmt.Sprintf("no definitions resolved for task %q in project %s", rep.Task, rep.Project),
			"try 'codemap find' or 'codemap semantic' first, or check the --at positions")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderTaskContext(rep)
	return nil
}

// taskContextResolvedAnything reports whether any section produced content, so
// an all-miss bundle reads as the exit-2 dead end it is instead of a
// confidently-empty orientation.
func taskContextResolvedAnything(rep *app.TaskContextReport) bool {
	if rep.Explore != nil && (len(rep.Explore.Seeds) > 0 || len(rep.Explore.Contexts) > 0) {
		return true
	}
	if rep.Contexts != nil {
		for _, c := range rep.Contexts.Results {
			if c.Found {
				return true
			}
		}
	}
	for _, t := range rep.Targets {
		if t.Found {
			return true
		}
	}
	return len(rep.Impacts) > 0 || len(rep.RelatedFiles) > 0
}

func renderTaskContext(rep *app.TaskContextReport) {
	fresh := "fresh"
	if !rep.Freshness.Checked {
		fresh = "freshness unknown"
	} else if rep.Freshness.Stale {
		fresh = fmt.Sprintf("STALE (changed %d / new %d / deleted %d)",
			rep.Freshness.Staleness.Changed, rep.Freshness.Staleness.New, rep.Freshness.Staleness.Deleted)
	}
	fmt.Printf("Task %q — %s mode (%s, %s)\n", rep.Task, rep.Mode, rep.Project, fresh)
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	fmt.Printf("  call graph: %s\n", rep.CallGraph)
	for _, pe := range rep.PartialErrors {
		fmt.Printf("  ⚠ partial %s: %s\n", pe.Component, pe.Error)
	}
	if rep.PartialErrorsTruncated > 0 {
		fmt.Printf("  ⛔ %d more partial errors omitted\n", rep.PartialErrorsTruncated)
	}
	if rep.Explore != nil {
		fmt.Printf("  explore: %d seeds (%s)%s\n", len(rep.Explore.Seeds), rep.Explore.SearchMode,
			noteSuffix(rep.Explore.Note))
	}
	for _, t := range rep.Targets {
		status := "not found"
		if t.Found {
			status = disp(t.Selector.FQN, t.Symbol)
		}
		fmt.Printf("  target: %s:%d — %s [%s]\n",
			t.Selector.File, t.Selector.StartLine, status, t.Source)
	}
	if rep.Contexts != nil {
		for _, c := range rep.Contexts.Results {
			if !c.Found {
				continue
			}
			fmt.Printf("  context %s: callers %d · callees %d · tests %d · blast %d · graph %s\n",
				disp(c.Symbol, c.Symbol), c.CallersTotal, c.CalleesTotal, c.TestsTotal, c.BlastRadius, c.CallGraph)
		}
	}
	for _, imp := range rep.Impacts {
		fmt.Printf("  impact %s: callers %d · blast %d · tests %d · graph %s\n",
			disp(imp.Symbol, imp.Symbol), imp.DirectCallersTotal, imp.BlastRadiusTotal, imp.TestsTotal,
			imp.Impact.CallGraph)
	}
	for _, rf := range rep.RelatedFiles {
		fmt.Printf("  related to %s: %d file(s)%s\n", rf.File, rf.RelatedTotal,
			noteSuffix(fmt.Sprintf("showing %d", len(rf.Related))))
	}
	for _, n := range rep.Next {
		fmt.Printf("  → %s — %s\n", n.Tool, n.Why)
	}
}

func noteSuffix(note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return " — " + note
}
