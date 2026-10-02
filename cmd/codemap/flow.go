/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var flowCmd = newFlowCmd()

// flowCmd registers itself (rather than via main.go's AddCommand list) so the
// command is self-contained; Go runs init functions in file-name order, so this
// runs before main.go's init wraps every RunE with the --json envelope handler.
func init() { rootCmd.AddCommand(flowCmd) }

func newFlowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow [<symbol>]",
		Short: "Explain a feature: the call tree from one entry symbol, in call order, with docs",
		Long: `Return a bounded call tree from one entry symbol (a CLI RunE, an HTTP or MCP
handler, any function): callees in the order the code calls them, each with its
file:line, subsystem, one-line doc, and confidence.

Same-named definitions that name-based indexing fans out to (every Close, Name,
String …) are collapsed to the most plausible one — call-site syntax, receiver,
same directory, same subsystem — or shown once as an unexpanded "ambiguous"
placeholder. Precise edges are never collapsed. Select the entry with a
positional <symbol> (name or FQN) or --at <file>:<line>.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runFlow,
	}
	cmd.Flags().String("at", "", "select the entry definition by position: <file>:<line>")
	cmd.Flags().Int("depth", app.DefaultFlowDepth, fmt.Sprintf("maximum call depth (1-%d)", app.MaxFlowDepth))
	cmd.Flags().Int("max-nodes", app.DefaultFlowMaxNodes, fmt.Sprintf("maximum steps to emit (1-%d)", app.MaxFlowMaxNodes))
	cmd.Flags().Bool("include-tests", false, "include test functions and test-file helpers")
	return cmd
}

func runFlow(cmd *cobra.Command, args []string) error {
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
	selector, err := selectorFromAtFlag(svc, cwd, cmd)
	if err != nil {
		return err
	}
	opts := app.FlowOptions{Selector: selector}
	if len(args) == 1 {
		opts.Symbol = args[0]
	}
	opts.Depth, _ = cmd.Flags().GetInt("depth")
	opts.MaxNodes, _ = cmd.Flags().GetInt("max-nodes")
	opts.IncludeTests, _ = cmd.Flags().GetBool("include-tests")
	rep, err := svc.Flow(cwd, opts)
	if err != nil {
		return err
	}
	ambiguous := len(rep.Candidates) > 0
	if !rep.Found && !ambiguous {
		msg := "no matching definition for flow"
		if len(rep.Notes) > 0 {
			msg = rep.Notes[0]
		}
		return notFoundError(msg, "check the name, or pick a definition with --at <file>:<line>")
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if ambiguous {
		renderFlowAmbiguity(rep)
		return nil
	}
	renderFlow(rep)
	return nil
}

func renderFlowAmbiguity(rep *app.FlowReport) {
	fmt.Println(rep.Notes[0])
	for _, c := range rep.Candidates {
		fmt.Printf("  %-44s %s:%d\n", truncStr(c.Signature, 44), c.File, c.StartLine)
	}
}

type flowLine struct {
	left, right string
}

func renderFlow(rep *app.FlowReport) {
	root := rep.Root
	fmt.Printf("Flow from %s — %s:%d (%s) · depth ≤ %d · %d step(s)", flowName(root, nil), root.File, root.StartLine, rep.Project, rep.MaxDepth, rep.StepsEmitted)
	if rep.Truncated {
		fmt.Printf(" of %d", rep.StepsTotal)
	}
	fmt.Println()
	renderCallGraphReliability(rep.CallGraph, rep.Resolution, "")
	if rep.Stale {
		fmt.Println("  ⚠ index is stale — line numbers and calls may be behind the code; reindex")
	}
	for _, partial := range rep.PartialErrors {
		fmt.Printf("  ⚠ partial: %s\n", partial)
	}
	fmt.Println()

	var lines []flowLine
	collectFlowLines(root, nil, "", true, true, &lines)
	width := 0
	for i := range lines {
		lines[i].left = truncStr(lines[i].left, 58)
		if w := len([]rune(lines[i].left)); w > width {
			width = w
		}
	}
	for _, l := range lines {
		pad := width - len([]rune(l.left)) + 2
		fmt.Printf("%s%s%s\n", l.left, strings.Repeat(" ", pad), l.right)
	}

	if len(rep.Subsystems) > 0 {
		parts := make([]string, 0, len(rep.Subsystems))
		for _, s := range rep.Subsystems {
			parts = append(parts, fmt.Sprintf("%s (%d)", s.Name, s.Steps))
		}
		fmt.Printf("\nSubsystems: %s\n", strings.Join(parts, " → "))
	}
	if rep.AmbiguousCalls > 0 {
		fmt.Printf("%d step(s) involve same-named definitions; ×N marks an unexpanded ambiguous call, +N alt a pick among N others\n", rep.AmbiguousCalls)
	}
	for _, n := range rep.Notes {
		if strings.HasPrefix(n, "calls are name-based") || strings.Contains(n, "same-named definitions") {
			continue
		}
		fmt.Println("• " + n)
	}
}

// flowName renders a step the way the code reads at the call site: Go names
// drop the package when it matches the caller's and keep it otherwise
// (gosrc.New, not just New). Methods render as Type.Method.
func flowName(s, parent *app.FlowStep) string {
	if !strings.EqualFold(s.Language, "go") || s.FQN == "" {
		return s.Symbol
	}
	parts := strings.Split(s.FQN, ".")
	if len(parts) < 2 {
		return s.Symbol
	}
	if parent == nil || !strings.EqualFold(parent.Language, "go") || strings.Split(parent.FQN, ".")[0] == parts[0] {
		return strings.Join(parts[1:], ".")
	}
	return s.FQN
}

func collectFlowLines(s, parent *app.FlowStep, prefix string, last, root bool, out *[]flowLine) {
	var connector, childPrefix string
	if !root {
		connector, childPrefix = "├─ ", prefix+"│  "
		if last {
			connector, childPrefix = "└─ ", prefix+"   "
		}
	}
	label := prefix + connector
	if !root && s.CallOrder > 0 {
		label += fmt.Sprintf("%d ", s.CallOrder)
	}
	label += flowName(s, parent)

	var loc string
	if s.File != "" {
		loc = fmt.Sprintf("%s:%d", s.File, s.StartLine)
	}
	var marks []string
	switch {
	case s.LeafReason == "ambiguous":
		label += fmt.Sprintf(" ×%d", s.Alternatives)
		marks = append(marks, "(ambiguous, not expanded)")
	case s.Cycle:
		marks = append(marks, "(↻ cycle)")
	case s.RepeatOf != "":
		marks = append(marks, "(↺ repeat of "+s.RepeatOf+")")
	case s.LeafReason == "reference":
		marks = append(marks, "(⇢ value reference)")
	}
	if s.Alternatives > 0 && s.LeafReason != "ambiguous" {
		marks = append(marks, fmt.Sprintf("(+%d alt)", s.Alternatives))
	}
	switch s.LeafReason {
	case "depth":
		marks = append(marks, fmt.Sprintf("(+%d deeper)", s.ChildrenTotal))
	case "max_nodes":
		if more := s.ChildrenTotal - len(s.Children); more > 0 {
			marks = append(marks, fmt.Sprintf("(+%d more, node cap)", more))
		}
	}
	right := loc
	if s.Doc != "" {
		if right != "" {
			right += " — "
		}
		right += truncStr(s.Doc, 110)
	}
	if len(marks) > 0 {
		if right != "" {
			right += "  "
		}
		right += strings.Join(marks, " ")
	}
	*out = append(*out, flowLine{left: label, right: right})
	for i, c := range s.Children {
		collectFlowLines(c, s, childPrefix, i == len(s.Children)-1, false, out)
	}
}
