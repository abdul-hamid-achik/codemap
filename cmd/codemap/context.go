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
	defer sess.Close()
	cwd, _ := os.Getwd()
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	if len(args) > 1 {
		return runContextBatch(cmd, svc, cwd, args, depth)
	}
	rep, err := svc.Context(cwd, args[0], depth)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		printNoSymbol(rep.Symbol, rep.Project)
		return nil
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
func runContextBatch(cmd *cobra.Command, svc *app.Service, cwd string, symbols []string, depth int) error {
	rep, err := svc.ContextBatch(cwd, symbols, depth)
	if err != nil {
		return err
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
	for _, r := range rep.Results {
		if !r.Found {
			fmt.Printf("  %s — not found\n", r.Symbol)
			continue
		}
		loc := ""
		if len(r.Definitions) > 0 {
			loc = fmt.Sprintf("%s:%d", r.Definitions[0].File, r.Definitions[0].StartLine)
		}
		fmt.Printf("  %s  %s  callers %d · callees %d · tests %d · blast %d\n",
			label(r.Symbol), loc, r.CallersTotal, r.CalleesTotal, r.TestsTotal, r.BlastRadius)
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

// firstLine returns s up to its first newline — the docstring summary.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
