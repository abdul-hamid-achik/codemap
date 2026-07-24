package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRootForTest walks up from this test file to the module root (the dir with
// go.mod), so the pin test can locate the checked-in plugin skill.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}

// TestPlaybookSyncClaudeSkill pins the checked-in Claude Code SKILL.md to
// RenderPlaybook(FormatClaudeSkill), so the plugin's skill text can never drift
// from docs.go + the playbook preamble. Same pattern as TestDocs.
func TestPlaybookSyncClaudeSkill(t *testing.T) {
	path := filepath.Join(repoRootForTest(t), "integrations", "claude-code", "skills", "using-codemap", "SKILL.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in skill: %v", err)
	}
	want := RenderPlaybook(FormatClaudeSkill)
	if string(got) != want {
		t.Errorf("integrations/claude-code/skills/using-codemap/SKILL.md is stale.\n" +
			"run: go run ./cmd/codemap agent playbook --format claude-skill > integrations/claude-code/skills/using-codemap/SKILL.md")
	}
}

// TestPlaybookMarkdownTeachesReflex asserts the canonical body keeps the
// load-bearing tool-selection reflex so the preamble can't silently drop it.
func TestPlaybookMarkdownTeachesReflex(t *testing.T) {
	body := PlaybookMarkdown()
	for _, want := range []string{
		"codemap_context", "codemap_review", "codemap_read_order",
		"codemap_grep", "codemap_coverage", "candidates", "call_graph", "stale",
		"degraded", "tooling.issues", "agent_fix",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PlaybookMarkdown must teach %q", want)
		}
	}
}

// TestRenderPlaybookFormats checks each format carries its own frontmatter/markers
// plus the load-bearing tool names, and that the CLI variant is MCP-name-free.
func TestRenderPlaybookFormats(t *testing.T) {
	skill := RenderPlaybook(FormatClaudeSkill)
	if !strings.HasPrefix(skill, "---\nname: using-codemap\n") || !strings.Contains(skill, "description: When answering structural") {
		t.Error("claude skill must lead with its YAML frontmatter")
	}
	if !strings.Contains(skill, "codemap_review") {
		t.Error("claude skill must name codemap_review")
	}

	rule := RenderPlaybook(FormatCursorRule)
	if !strings.Contains(rule, "alwaysApply: false") || !strings.HasPrefix(rule, "---\ndescription: ") {
		t.Error("cursor rule must carry .mdc frontmatter with alwaysApply: false")
	}

	md := RenderPlaybook(FormatMarkdownSection)
	if !strings.HasPrefix(md, markerBegin) || !strings.Contains(md, markerEnd) {
		t.Error("markdown section must be fenced by codemap markers")
	}
	if !strings.Contains(md, "codemap_context") {
		t.Error("markdown section must name the codemap_* tools")
	}

	cli := RenderPlaybook(FormatMarkdownSectionCLI)
	if !strings.HasPrefix(cli, markerBegin) || !strings.Contains(cli, markerEnd) {
		t.Error("CLI markdown section must be fenced by codemap markers")
	}
	if strings.Contains(cli, "codemap_") {
		t.Errorf("CLI playbook must not mention any codemap_* MCP tool name")
	}
	if !strings.Contains(cli, "--json") {
		t.Error("CLI playbook must show the --json CLI form")
	}
	if !strings.Contains(cli, "codemap review") || !strings.Contains(cli, "codemap read-order") {
		t.Error("CLI playbook must use codemap <name> command forms")
	}
}
