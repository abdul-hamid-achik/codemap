/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// ctxLabel styles the `context` card's section labels (charm/lipgloss). It is
// applied only on an interactive terminal — piped and --json output stay plain.
var ctxLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#66D9EF"))

// runContext renders the one-call bundle for a symbol: definition(s), callers,
// callees, covering tests, blast-radius size, and annotations. --json carries
// the complete bundle (including source bodies) for an agent; the human card is
// a compact, scannable overview.
func runContext(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	depth, _ := cmd.Flags().GetInt("depth")
	brief, _ := cmd.Flags().GetBool("brief")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	if selector == nil && len(args) == 0 {
		return fmt.Errorf("context needs at least one <symbol> or --at <file>:<line>")
	}
	if selector != nil && len(args) > 1 {
		return fmt.Errorf("context --at selects one definition and cannot be combined with a symbol batch")
	}
	if len(args) > 1 {
		return runContextBatch(cmd, svc, cwd, args, depth, brief)
	}
	var rep *app.ContextReport
	if selector != nil {
		rep, err = svc.ContextBySelectorWithContext(cmd.Context(), cwd, *selector, depth, brief)
	} else {
		rep, err = svc.ContextWithContext(cmd.Context(), cwd, args[0], depth, brief)
	}
	if err != nil {
		return err
	}
	if !rep.Found {
		if selector != nil {
			return notFoundError("the selected definition is no longer in the index", "run: codemap index")
		}
		return notFoundError(
			fmt.Sprintf("no symbol named %q in project %s", rep.Symbol, rep.Project),
			fmt.Sprintf("run: codemap find %q", rep.Symbol))
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}

	color := term.IsTerminal(os.Stdout.Fd())
	label := func(s string) string {
		if color {
			return ctxLabel.Render(s)
		}
		return s
	}

	fmt.Printf("%s %s (%s)\n", label("Context:"), rep.Symbol, rep.Project)
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	renderCandidates(rep.Candidates)
	fmt.Printf("  %s %s\n", label("call graph:"), rep.CallGraph)
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	}
	renderContextPartialErrors(rep.PartialErrors)
	for _, d := range rep.Definitions {
		fmt.Printf("  %s %s:%d-%d  (%s)\n", label("defined"), d.File, d.StartLine, d.EndLine, d.Kind)
		if s := strings.TrimSpace(d.Signature); s != "" {
			fmt.Printf("      %s\n", s)
		}
		if doc := strings.TrimSpace(firstLine(d.Doc)); doc != "" {
			fmt.Printf("      %s\n", doc)
		}
	}

	refNames := func(refs []app.SymbolRef) []string {
		shown, _ := capList(refs, 8)
		out := make([]string, 0, len(shown))
		for _, r := range shown {
			out = append(out, disp(r.FQN, r.Symbol))
		}
		return out
	}
	// total is the true count (the report's lists are already capped); "+N" is
	// measured against it so a hub reads e.g. "callers (104): … (+96)".
	line := func(name string, shown []string, total int) {
		s := fmt.Sprintf("  %s (%d):", label(name), total)
		if len(shown) > 0 {
			s += " " + strings.Join(shown, ", ")
		}
		if more := total - len(shown); more > 0 {
			s += fmt.Sprintf(" … (+%d)", more)
		}
		fmt.Println(s)
	}

	line("callers", refNames(rep.Callers), rep.CallersTotal)
	line("callees", refNames(rep.Callees), rep.CalleesTotal)

	tShown, _ := capList(rep.Tests, 8)
	tNames := make([]string, 0, len(tShown))
	for _, t := range tShown {
		tNames = append(tNames, disp(t.FQN, t.Symbol))
	}
	line("tests", tNames, rep.TestsTotal)

	fmt.Printf("  %s %d (depth ≤ %d)\n", label("blast radius:"), rep.BlastRadius, rep.BlastDepth)
	renderAnnotations(rep.Annotations)
	if len(rep.Memories) > 0 {
		fmt.Printf("  %s (via vecgrep)\n", label("memories:"))
		for _, m := range rep.Memories {
			fmt.Printf("     · %s\n", firstLine(m.Content))
		}
	}
	return nil
}

// runContextBatch renders the one-call bundle for several symbols plus the callers
// they share (likely shared entrypoints / coupling). --json carries the full batch.
func runContextBatch(cmd *cobra.Command, svc *app.Service, cwd string, symbols []string, depth int, brief bool) error {
	rep, err := svc.ContextBatchWithContext(cmd.Context(), cwd, symbols, nil, depth, brief)
	if err != nil {
		return err
	}
	if len(rep.Results) > 0 && len(rep.NotFound) == len(rep.Results) {
		return notFoundError(
			fmt.Sprintf("none of the requested symbols were found: %s", strings.Join(rep.NotFound, ", ")),
			"check the names with codemap find")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	color := term.IsTerminal(os.Stdout.Fd())
	label := func(s string) string {
		if color {
			return ctxLabel.Render(s)
		}
		return s
	}
	fmt.Printf("%s %d symbols (%s)\n", label("Context batch:"), len(rep.Results), rep.Project)
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	if rep.SourceBudget.TruncatedDefinitions > 0 {
		fmt.Printf("  ⚠ source bodies: %d/%d bytes included (%d definition(s) truncated)\n",
			rep.SourceBudget.IncludedBytes, rep.SourceBudget.OriginalBytes, rep.SourceBudget.TruncatedDefinitions)
	}
	renderContextPartialErrors(rep.PartialErrors)
	for _, r := range rep.Results {
		if !r.Found {
			fmt.Printf("  %s — not found\n", r.Symbol)
			continue
		}
		loc := ""
		if len(r.Definitions) > 0 {
			loc = fmt.Sprintf("%s:%d", r.Definitions[0].File, r.Definitions[0].StartLine)
		}
		fmt.Printf("  %s  %s  callers %d · callees %d · tests %d · blast %d · graph %s\n",
			label(r.Symbol), loc, r.CallersTotal, r.CalleesTotal, r.TestsTotal, r.BlastRadius, r.CallGraph)
	}
	if len(rep.NotFound) > 0 {
		fmt.Printf("  not found: %s\n", strings.Join(rep.NotFound, ", "))
	}
	fmt.Printf("  %s %d (sum; shared dependents double-count)\n", label("combined blast:"), rep.CombinedBlastRadius)
	if len(rep.CommonCallers) > 0 {
		names := make([]string, 0, len(rep.CommonCallers))
		shown, more := capList(rep.CommonCallers, 8)
		for _, c := range shown {
			names = append(names, disp(c.FQN, c.Symbol))
		}
		line := strings.Join(names, ", ")
		if more > 0 {
			line += fmt.Sprintf(" … (+%d)", more)
		}
		fmt.Printf("  %s %s\n", label("shared callers:"), line)
	}
	return nil
}

func renderContextPartialErrors(errs []app.ContextPartialError) {
	shown, more := capList(errs, 5)
	for _, partial := range shown {
		symbol := ""
		if partial.Symbol != "" {
			symbol = partial.Symbol + " · "
		}
		fmt.Printf("  ⚠ partial %s%s: %s\n", symbol, partial.Component, partial.Error)
	}
	if more > 0 {
		fmt.Printf("  ⚠ … %d more partial error(s); use --json for all\n", more)
	}
}

// firstLine returns s up to its first newline — the docstring summary.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
