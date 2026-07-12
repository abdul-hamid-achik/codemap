/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// The `codemap agent` family registers codemap with an AI coding harness: it
// merges the codemap MCP server into the harness's native config and drops the
// canonical playbook into its guidance surface. Handlers are thin — all
// detection/merge/render logic lives in internal/app (agentsetup.go, playbook.go)
// and NO session/DB is opened, so setup works before any index exists.

var (
	agentCmd = &cobra.Command{
		Use:   "agent",
		Short: "Register codemap with an AI coding harness (Claude Code, Cursor, Codex, Gemini, Cline/Roo, Zed, VS Code, OpenCode, aider)",
	}
	agentListCmd = &cobra.Command{
		Use:   "list",
		Short: "List known harnesses, whether each is detected here, and if codemap is registered",
		Args:  cobra.NoArgs,
		RunE:  runAgentList,
	}
	agentSetupCmd = &cobra.Command{
		Use:   "setup <harness>",
		Short: "Wire codemap (MCP server + playbook) into a harness' config and guidance files",
		Args:  cobra.ExactArgs(1),
		RunE:  runAgentSetup,
	}
	agentPlaybookCmd = &cobra.Command{
		Use:   "playbook",
		Short: "Print the canonical 'when to use codemap' playbook (for wiring an unlisted harness by hand)",
		Args:  cobra.NoArgs,
		RunE:  runAgentPlaybook,
	}
)

func init() {
	agentSetupCmd.Flags().Bool("global", false, "write user-level config where the harness has one (default: project-scoped files)")
	agentSetupCmd.Flags().Bool("dry-run", false, "print every planned write, change nothing")
	agentSetupCmd.Flags().Bool("no-playbook", false, "register the MCP server only, skip the guidance file")
	agentPlaybookCmd.Flags().String("format", "markdown", "output format: markdown | markdown-cli | claude-skill | cursor-rule")
	agentCmd.AddCommand(agentListCmd, agentSetupCmd, agentPlaybookCmd)
}

func runAgentList(cmd *cobra.Command, _ []string) error {
	dets := app.DetectHarnesses(targetDir(cmd))
	if jsonOut(cmd) {
		return printJSON(dets)
	}
	fmt.Printf("%-12s  %-9s  %-10s  %s\n", "HARNESS", "DETECTED", "CODEMAP", "CONFIG")
	for _, d := range dets {
		fmt.Printf("%-12s  %-9s  %-10s  %s\n", d.Name, yesno(d.Present), yesno(d.Registered), d.ConfigPath)
	}
	return nil
}

func runAgentSetup(cmd *cobra.Command, args []string) error {
	global, _ := cmd.Flags().GetBool("global")
	dry, _ := cmd.Flags().GetBool("dry-run")
	noPlaybook, _ := cmd.Flags().GetBool("no-playbook")
	rep, err := app.SetupHarness(targetDir(cmd), args[0], app.SetupOptions{Global: global, DryRun: dry, NoPlaybook: noPlaybook})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if dry {
		fmt.Printf("dry-run: %s (nothing written)\n", rep.Harness)
	} else {
		fmt.Printf("codemap agent setup: %s\n", rep.Harness)
	}
	for _, w := range rep.Written {
		fmt.Printf("  %-9s %s\n", w.Action, w.Path)
	}
	for _, s := range rep.Snippets {
		fmt.Printf("\n  ! %s\n", s.Reason)
		if s.Path != "" {
			fmt.Printf("    file: %s\n", s.Path)
		}
		for _, line := range strings.Split(s.Content, "\n") {
			fmt.Printf("    %s\n", line)
		}
	}
	for _, n := range rep.Notes {
		fmt.Printf("\n  note: %s\n", n)
	}
	return nil
}

func runAgentPlaybook(cmd *cobra.Command, _ []string) error {
	format, _ := cmd.Flags().GetString("format")
	var f app.PlaybookFormat
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "claude-skill":
		f = app.FormatClaudeSkill
	case "cursor-rule":
		f = app.FormatCursorRule
	case "markdown-cli", "cli":
		f = app.FormatMarkdownSectionCLI
	case "markdown", "":
		f = app.FormatMarkdownSection
	default:
		return fmt.Errorf("unknown format %q — valid: markdown, markdown-cli, claude-skill, cursor-rule", format)
	}
	fmt.Print(app.RenderPlaybook(f))
	return nil
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
