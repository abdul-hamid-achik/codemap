package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// readJSON loads a JSON file into a generic map for structural assertions.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func serverIn(t *testing.T, path, topKey, name string) bool {
	t.Helper()
	m := readJSON(t, path)
	sub, _ := m[topKey].(map[string]any)
	if sub == nil {
		return false
	}
	_, ok := sub[name]
	return ok
}

// TestSetupJSONHarnesses checks each MCP-config harness writes the right file
// with the right top-level key and a codemap entry, plus its playbook file.
func TestSetupJSONHarnesses(t *testing.T) {
	cases := []struct {
		harness     string
		configRel   string
		topKey      string
		playbook    string
		playbookHas string
	}{
		{"cursor", ".cursor/mcp.json", "mcpServers", ".cursor/rules/codemap.mdc", "alwaysApply: false"},
		{"gemini", ".gemini/settings.json", "mcpServers", "GEMINI.md", "codemap:begin"},
		{"roo", ".roo/mcp.json", "mcpServers", ".roo/rules/codemap.md", "codemap:begin"},
		{"vscode", ".vscode/mcp.json", "servers", ".github/copilot-instructions.md", "codemap:begin"},
		{"opencode", "opencode.json", "mcp", "AGENTS.md", "codemap:begin"},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			dir := t.TempDir()
			rep, err := SetupHarness(dir, c.harness, SetupOptions{})
			if err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, filepath.FromSlash(c.configRel))
			if !serverIn(t, cfg, c.topKey, "codemap") {
				t.Errorf("%s: expected codemap under %q in %s", c.harness, c.topKey, c.configRel)
			}
			pb := filepath.Join(dir, filepath.FromSlash(c.playbook))
			b, err := os.ReadFile(pb)
			if err != nil {
				t.Fatalf("%s: playbook %s not written: %v", c.harness, c.playbook, err)
			}
			if !strings.Contains(string(b), c.playbookHas) {
				t.Errorf("%s: playbook missing %q", c.harness, c.playbookHas)
			}
			if !strings.Contains(string(b), "codemap_context") {
				t.Errorf("%s: playbook should teach codemap_context", c.harness)
			}
			// The report lists the writes.
			if len(rep.Written) == 0 {
				t.Errorf("%s: report has no writes", c.harness)
			}
		})
	}
}

// opencode's command must be an array, not the command+args split.
func TestSetupOpencodeCommandIsArray(t *testing.T) {
	dir := t.TempDir()
	if _, err := SetupHarness(dir, "opencode", SetupOptions{}); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(dir, "opencode.json"))
	mcp, _ := m["mcp"].(map[string]any)
	cm, _ := mcp["codemap"].(map[string]any)
	if _, ok := cm["command"].([]any); !ok {
		t.Errorf("opencode command must be a JSON array, got %T", cm["command"])
	}
}

// TestSetupPreservesSiblings: a pre-existing unrelated server survives the merge.
func TestSetupPreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"other":{"command":"other","args":["x"]}},"someOtherKey":42}`
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetupHarness(dir, "cursor", SetupOptions{}); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, cfg)
	sub, _ := m["mcpServers"].(map[string]any)
	if _, ok := sub["other"]; !ok {
		t.Error("sibling server 'other' was clobbered")
	}
	if _, ok := sub["codemap"]; !ok {
		t.Error("codemap was not added")
	}
	if m["someOtherKey"] != float64(42) {
		t.Errorf("unrelated top-level key lost: %v", m["someOtherKey"])
	}
	// The sibling server's own value is byte-preserved.
	oth, _ := sub["other"].(map[string]any)
	if oth["command"] != "other" {
		t.Errorf("sibling server value mutated: %v", oth)
	}
}

// TestSetupIdempotent: a second identical run reports everything unchanged and
// mutates nothing.
func TestSetupIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := SetupHarness(dir, "cursor", SetupOptions{}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, dir)
	rep, err := SetupHarness(dir, "cursor", SetupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rep.Written {
		if w.Action != "unchanged" {
			t.Errorf("second run: %s reported %q, want unchanged", w.Path, w.Action)
		}
	}
	if after := snapshotTree(t, dir); after != before {
		t.Errorf("second run mutated the tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestSetupNeverClobbersJSONC: a config with comments (invalid JSON) is never
// overwritten; the merge is emitted as a snippet instead.
func TestSetupNeverClobbersJSONC(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := "{\n  // a comment the user wants kept\n  \"mcpServers\": {}\n}\n"
	if err := os.WriteFile(cfg, []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := SetupHarness(dir, "cursor", SetupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != jsonc {
		t.Error("JSONC config was rewritten; must be left untouched")
	}
	found := false
	for _, s := range rep.Snippets {
		if strings.Contains(s.Content, "codemap") {
			found = true
		}
	}
	if !found {
		t.Error("expected a snippet fallback for the un-rewritable config")
	}
}

// TestMarkedBlockReplacement: surrounding user prose is preserved while a stale
// block is refreshed.
func TestMarkedBlockReplacement(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	seed := "# My project\n\nSome house rules.\n\n" +
		markerBegin + "\nold stale text\n" + markerEnd + "\n\n## Another section\n\nkeep me\n"
	if err := os.WriteFile(agents, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetupHarness(dir, "agents-md", SetupOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(agents)
	s := string(b)
	if !strings.Contains(s, "# My project") || !strings.Contains(s, "## Another section") || !strings.Contains(s, "keep me") {
		t.Error("user prose around the block was not preserved")
	}
	if strings.Contains(s, "old stale text") {
		t.Error("stale block content was not replaced")
	}
	if strings.Count(s, markerBegin) != 1 || strings.Count(s, markerEnd) != 1 {
		t.Errorf("expected exactly one refreshed block, got begin=%d end=%d", strings.Count(s, markerBegin), strings.Count(s, markerEnd))
	}
	// aider/agents-md use the CLI form: no MCP tool names leak in.
	if strings.Contains(s, "codemap_") {
		t.Error("agents-md fallback must use CLI command forms, not codemap_*")
	}
}

// TestUnknownHarness: an unknown name errors and lists the valid ones.
func TestUnknownHarness(t *testing.T) {
	_, err := SetupHarness(t.TempDir(), "notaharness", SetupOptions{})
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	for _, name := range []string{"cursor", "vscode", "aider"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should list valid harness %q: %v", name, err)
		}
	}
}

// TestDryRunNoMutation: --dry-run writes nothing.
func TestDryRunNoMutation(t *testing.T) {
	dir := t.TempDir()
	before := snapshotTree(t, dir)
	rep, err := SetupHarness(dir, "cursor", SetupOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, dir); after != before {
		t.Errorf("dry-run mutated the tree:\n%s", after)
	}
	if len(rep.Written) == 0 {
		t.Error("dry-run should still report planned writes")
	}
}

// TestNoPlaybook: --no-playbook writes only the MCP config.
func TestNoPlaybook(t *testing.T) {
	dir := t.TempDir()
	if _, err := SetupHarness(dir, "cursor", SetupOptions{NoPlaybook: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "mcp.json")); err != nil {
		t.Error("mcp.json should still be written with --no-playbook")
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "codemap.mdc")); err == nil {
		t.Error(".mdc playbook should be skipped with --no-playbook")
	}
}

// TestDetectHarnesses: a fresh dir detects nothing registered; the registry
// covers every documented harness key.
func TestDetectHarnesses(t *testing.T) {
	dets := DetectHarnesses(t.TempDir())
	want := []string{"claude-code", "cursor", "codex", "gemini", "cline", "roo", "zed", "vscode", "opencode", "aider", "agents-md"}
	got := make([]string, len(dets))
	for i, d := range dets {
		got[i] = d.Name
		if d.Registered {
			t.Errorf("%s should not be registered in a fresh dir", d.Name)
		}
	}
	sort.Strings(want)
	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedGot)
	if strings.Join(sortedGot, ",") != strings.Join(want, ",") {
		t.Errorf("registry harness set = %v, want %v", got, want)
	}
}

// TestDetectAfterSetup: setup then detect reports codemap registered.
func TestDetectAfterSetup(t *testing.T) {
	dir := t.TempDir()
	if _, err := SetupHarness(dir, "vscode", SetupOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, d := range DetectHarnesses(dir) {
		if d.Name == "vscode" {
			if !d.Present || !d.Registered {
				t.Errorf("vscode should be present+registered after setup: %+v", d)
			}
		}
	}
}

// TestZedDefaultPrintsSnippet: without --global (and being JSONC-global), zed
// prints a snippet rather than editing the global settings file.
func TestZedDefaultPrintsSnippet(t *testing.T) {
	rep, err := SetupHarness(t.TempDir(), "zed", SetupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range rep.Snippets {
		if strings.Contains(s.Content, "context_servers") {
			found = true
		}
	}
	if !found {
		t.Error("zed without --global should print a context_servers snippet")
	}
}

// snapshotTree returns a stable text listing of dir's files + sizes for
// mutation assertions.
func snapshotTree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		lines = append(lines, rel+" "+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
