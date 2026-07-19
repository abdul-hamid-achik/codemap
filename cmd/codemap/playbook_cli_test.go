/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// wantCLIForm maps every codemap_<tool> token the canonical playbook body
// (internal/app.PlaybookMarkdown, i.e. the preamble + the "workflow" and
// "accuracy" doc topics) currently teaches to the exact `codemap <cmd>`
// top-level command name its markdown-cli rendering must use. Most entries
// are the mechanical dash-rename cliify performs by default; "context_batch"
// is the one documented exception (there is no `codemap context-batch`
// command — the CLI batch form is the multi-arg `codemap context <s1> <s2>
// ...`). Kept independently of cliify's own override table on purpose, so a
// future divergence between the two is caught by this test rather than
// silently trusted.
var wantCLIForm = map[string]string{
	"codemap_callees":       "callees",
	"codemap_callers":       "callers",
	"codemap_context":       "context",
	"codemap_context_batch": "context",
	"codemap_coverage":      "coverage",
	"codemap_dependencies":  "dependencies",
	"codemap_explore":       "explore",
	"codemap_file_impact":   "file-impact",
	"codemap_find":          "find",
	"codemap_grep":          "grep",
	"codemap_hotspots":      "hotspots",
	"codemap_impact":        "impact",
	"codemap_index":         "index",
	"codemap_orphans":       "orphans",
	"codemap_path":          "path",
	"codemap_read_order":    "read-order",
	"codemap_references":    "references",
	"codemap_related_files": "related-files",
	"codemap_review":        "review",
	"codemap_risk":          "risk",
	"codemap_semantic":      "semantic",
	"codemap_source":        "source",
	"codemap_status":        "status",
	"codemap_symbol_at":     "symbol-at",
}

var codemapTokenRe = regexp.MustCompile(`codemap_[a-z_]*`)

// TestCliifyMapsToRegisteredCommands pins the fix for cliify rewriting
// codemap_context_batch into the nonexistent `codemap context-batch` command
// (the real CLI batch form is `codemap context <s1> <s2> ...`). It verifies,
// against the ACTUAL registered cobra command tree (rootCmd, built by this
// package's init()) rather than a hand-maintained guess, that:
//  1. every codemap_<tool> token the canonical playbook body currently
//     teaches is accounted for in wantCLIForm (so a newly-taught tool can't
//     silently go unchecked), and
//  2. wantCLIForm's target command names are all real, registered commands, and
//  3. the rendered markdown-cli playbook (what `agent setup aider` /
//     `agents-md` actually writes) contains each of those `codemap <cmd>`
//     invocations and never contains the broken `codemap context-batch` form.
func TestCliifyMapsToRegisteredCommands(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		registered[c.Name()] = true
	}

	body := app.PlaybookMarkdown()
	seen := map[string]bool{}
	for _, tok := range codemapTokenRe.FindAllString(body, -1) {
		seen[tok] = true
	}
	if len(seen) == 0 {
		t.Fatal("canonical playbook body carries no codemap_<tool> tokens — extraction regex or PlaybookMarkdown broke")
	}

	var unknown []string
	for tok := range seen {
		if _, ok := wantCLIForm[tok]; !ok {
			unknown = append(unknown, tok)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Fatalf("playbook now teaches %v but wantCLIForm doesn't know their CLI form — add them (verify against cmd/codemap's registered commands)", unknown)
	}

	var stale []string
	for tok := range wantCLIForm {
		if !seen[tok] {
			stale = append(stale, tok)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("wantCLIForm still lists %v but the canonical playbook no longer teaches them — remove them", stale)
	}

	cli := app.RenderPlaybook(app.FormatMarkdownSectionCLI)
	for tok, cmdName := range wantCLIForm {
		if !registered[cmdName] {
			t.Errorf("%s -> \"codemap %s\", but %q is not a registered top-level command", tok, cmdName, cmdName)
		}
		if !strings.Contains(cli, "codemap "+cmdName) {
			t.Errorf("rendered markdown-cli playbook never contains %q (source token %s)", "codemap "+cmdName, tok)
		}
	}
	if strings.Contains(cli, "codemap context-batch") {
		t.Error("rendered markdown-cli playbook still contains the nonexistent \"codemap context-batch\" command")
	}
}
