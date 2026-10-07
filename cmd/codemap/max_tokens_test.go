/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMaxTokensCLIFlag pins --max-tokens on context, impact, explore and
// task-context: absent means no budget object, present trims and reports, and a
// negative value is an invalid-input failure (exit 1).
func TestMaxTokensCLIFlag(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codemap")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	runner := filepath.Join(root, "runner")
	project := filepath.Join(root, "project")
	for _, dir := range []string{runner, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/budget-cli\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "main.go"), "package sample\n\nfunc Hub() {}\n\nfunc Entry() { Hub() }\n")
	env := append(isolatedCLIEnv(root), "CODEMAP_VECGREP_ENABLED=false")
	if res := runCLI(t, bin, runner, env, "init", "-C", project, "--json"); res.exit != 0 {
		t.Fatalf("init exit=%d stdout=%s", res.exit, res.stdout)
	}
	if res := runCLI(t, bin, runner, env, "index", "-C", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json"); res.exit != 0 {
		t.Fatalf("index exit=%d stdout=%s", res.exit, res.stdout)
	}

	withFlags := func(args []string, extra ...string) []string {
		out := append([]string{}, args...)
		out = append(out, "-C", project)
		out = append(out, extra...)
		return append(out, "--json")
	}
	for _, args := range [][]string{
		{"context", "Hub"},
		{"impact", "Hub"},
		{"explore", "Hub"},
		{"task-context", "Hub"},
	} {
		name := args[0]
		var probe struct {
			Budget *struct {
				MaxTokens int            `json:"max_tokens"`
				Truncated bool           `json:"truncated"`
				Dropped   map[string]int `json:"dropped"`
			} `json:"budget"`
		}
		plain := runCLI(t, bin, runner, env, withFlags(args)...)
		if plain.exit != 0 {
			t.Fatalf("%s exit=%d stdout=%s", name, plain.exit, plain.stdout)
		}
		if err := json.Unmarshal([]byte(plain.stdout), &probe); err != nil || probe.Budget != nil {
			t.Fatalf("%s: budget without --max-tokens (err=%v)", name, err)
		}
		res := runCLI(t, bin, runner, env, withFlags(args, "--max-tokens", "1")...)
		if res.exit != 0 {
			t.Fatalf("%s --max-tokens exit=%d stdout=%s", name, res.exit, res.stdout)
		}
		if err := json.Unmarshal([]byte(res.stdout), &probe); err != nil {
			t.Fatalf("%s: parse: %v\n%s", name, err, res.stdout)
		}
		if probe.Budget == nil || probe.Budget.MaxTokens != 1 || !probe.Budget.Truncated || len(probe.Budget.Dropped) == 0 {
			t.Fatalf("%s: budget = %+v", name, probe.Budget)
		}
		bad := runCLI(t, bin, runner, env, withFlags(args, "--max-tokens=-1")...)
		if bad.exit != 1 {
			t.Fatalf("%s negative --max-tokens exit=%d stdout=%s", name, bad.exit, bad.stdout)
		}
	}
}
