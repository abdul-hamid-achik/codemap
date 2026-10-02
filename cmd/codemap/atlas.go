/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var atlasCmd = newAtlasCmd()

func init() { rootCmd.AddCommand(atlasCmd) }

func newAtlasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "atlas",
		Short: "The repo as a described directory tree: metrics, roles, summaries, key symbols",
		Long: `Return the project as a hierarchical directory/file tree. Every node carries
size metrics, coarse roles (source/tests/docs/config/...), a plain-language
summary lifted from README files, package docs, docstrings or leading comments,
the call-graph key symbols inside it, and inbound/outbound coupling.

Summaries are real project text, never generated. Use --prefix to zoom into a
directory and --files to include file leaves.`,
		Args: cobra.NoArgs,
		RunE: runAtlas,
	}
	cmd.Flags().String("prefix", "", "zoom into a project-relative directory (default: project root)")
	cmd.Flags().Int("depth", app.DefaultAtlasDepth, fmt.Sprintf("directory levels below the prefix to expand (max %d)", app.MaxAtlasDepth))
	cmd.Flags().Bool("files", false, "include file leaves in every expanded directory")
	cmd.Flags().Int("max-nodes", app.DefaultAtlasMaxNodes, fmt.Sprintf("maximum tree nodes to emit (max %d)", app.MaxAtlasMaxNodes))
	cmd.Flags().Int("key-symbols", app.DefaultAtlasKeySymbols, fmt.Sprintf("key symbols per directory/file (max %d)", app.MaxAtlasKeySymbols))
	return cmd
}

func runAtlas(cmd *cobra.Command, _ []string) error {
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
	depth, _ := cmd.Flags().GetInt("depth")
	files, _ := cmd.Flags().GetBool("files")
	maxNodes, _ := cmd.Flags().GetInt("max-nodes")
	keySymbols, _ := cmd.Flags().GetInt("key-symbols")
	rep, err := svc.Atlas(cwd, app.AtlasOptions{
		Prefix: prefix, Depth: depth, Files: files, MaxNodes: maxNodes, KeySymbols: keySymbols,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderAtlas(rep)
	return nil
}

func renderAtlas(rep *app.AtlasReport) {
	if rep.Tree == nil {
		fmt.Println("nothing to show")
		return
	}
	summary := rep.Summary
	if summary == "" {
		summary = "(no summary)"
	}
	fmt.Printf("%s — %s", rep.Project, truncStr(summary, atlasTerminalWidth()-len(rep.Project)-20))
	if rep.SummarySource != "" {
		fmt.Printf(" (%s)", rep.SummarySource)
	}
	fmt.Println()
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before treating this atlas as current")
	}
	if rep.Resolution != "" {
		fmt.Printf("⚠ %s\n", rep.Resolution)
	}
	for _, partial := range rep.PartialErrors {
		fmt.Printf("⚠ partial: %s\n", partial)
	}
	t := rep.Totals
	fmt.Printf("%s dirs · %s files · %s symbols · %s lines · %s tests · call graph: %s\n",
		commas(t.Dirs), commas(t.Files), commas(t.Symbols), commas(t.Lines), commas(t.Tests), rep.CallGraph)
	if rep.Prefix != "" {
		fmt.Printf("zoomed into %s/\n", rep.Prefix)
	}
	fmt.Println()
	renderAtlasNode(rep.Tree, "", true, true, atlasTerminalWidth())
	if rep.Truncated {
		fmt.Printf("\n… atlas truncated at %d nodes; raise --max-nodes or zoom in with --prefix\n", rep.NodesEmitted)
	}
}

func renderAtlasNode(n *app.AtlasNode, indent string, last, root bool, width int) {
	label := n.Name
	if n.Type == "dir" && !root {
		label += "/"
	}
	branch, childIndent := "", indent
	if !root {
		if last {
			branch, childIndent = "└─ ", indent+"   "
		} else {
			branch, childIndent = "├─ ", indent+"│  "
		}
	}
	nameCol := indent + branch + label
	stats := fmt.Sprintf("%7s sym %5s files", commas(n.Symbols), commas(n.Files))
	if n.Type == "file" {
		stats = fmt.Sprintf("%7s sym %5s lines", commas(n.Symbols), commas(n.Lines))
	}
	roles := ""
	if len(n.Roles) > 0 {
		roles = " [" + strings.Join(n.Roles, ",") + "]"
	}
	line := fmt.Sprintf("%-34s %s%s", truncStr(nameCol, 34), stats, roles)
	if n.Summary != "" {
		room := width - len([]rune(line)) - 2
		if room > 20 {
			line += "  " + truncStr(n.Summary, room)
		}
	}
	fmt.Println(strings.TrimRight(line, " "))
	for i, c := range n.Children {
		renderAtlasNode(c, childIndent, i == len(n.Children)-1 && !n.ChildrenTrunc, false, width)
	}
	if n.ChildrenTrunc {
		fmt.Printf("%s└─ … %d more\n", childIndent, n.ChildrenTotal-len(n.Children))
	}
}

// atlasCommas formats n with thousands separators.
func commas(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return s
	}
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	return strings.Join(out, ",")
}

// atlasTerminalWidth honours $COLUMNS and otherwise assumes a 120-column terminal.
func atlasTerminalWidth() int {
	if w := os.Getenv("COLUMNS"); w != "" {
		var n int
		if _, err := fmt.Sscanf(w, "%d", &n); err == nil && n >= 60 {
			return n
		}
	}
	return 120
}
