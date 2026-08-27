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

func TestTaskContextCLIContracts(t *testing.T) {
	root := t.TempDir()
	binName := "codemap"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(root, binName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	runner := filepath.Join(root, "runner")
	project := filepath.Join(root, "project")
	cold := filepath.Join(root, "cold")
	for _, dir := range []string{runner, project, cold} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/taskctx-cli\n\ngo 1.25\n")
	// Hub sits on line 3 so --at main.go:3 selects it deterministically.
	writeTestFile(t, filepath.Join(project, "main.go"), "package sample\n\nfunc Hub() {}\n\nfunc Entry() { Hub() }\n")
	env := append(isolatedCLIEnv(root), "CODEMAP_VECGREP_ENABLED=false")

	res := runCLI(t, bin, runner, env, "init", "-C", project, "--json")
	if res.exit != 0 {
		t.Fatalf("init exit=%d stdout=%s", res.exit, res.stdout)
	}
	res = runCLI(t, bin, runner, env, "index", "-C", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
	if res.exit != 0 {
		t.Fatalf("index exit=%d stdout=%s", res.exit, res.stdout)
	}

	type envelope struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}

	t.Run("change mode with --at", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "task-context", "make Hub safer", "-C", project,
			"--mode", "change", "--at", "main.go:3", "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			SchemaVersion int    `json:"schema_version"`
			Mode          string `json:"mode"`
			Task          string `json:"task"`
			Indexed       bool   `json:"indexed"`
			Freshness     struct {
				Checked bool `json:"checked"`
			} `json:"freshness"`
			Targets []struct {
				Found  bool   `json:"found"`
				Source string `json:"source"`
			} `json:"targets"`
			Contexts *struct {
				Results []struct {
					Found bool `json:"found"`
				} `json:"results"`
			} `json:"contexts"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &rep); err != nil {
			t.Fatalf("parse report: %v\n%s", err, res.stdout)
		}
		if rep.SchemaVersion != 1 || rep.Mode != "change" || !rep.Indexed || !rep.Freshness.Checked {
			t.Fatalf("identity = %+v", rep)
		}
		if rep.Task != "make Hub safer" {
			t.Fatalf("task echoed = %q", rep.Task)
		}
		if len(rep.Targets) != 1 || !rep.Targets[0].Found || rep.Targets[0].Source != "selector" {
			t.Fatalf("targets = %+v", rep.Targets)
		}
		if rep.Contexts == nil || len(rep.Contexts.Results) != 1 || !rep.Contexts.Results[0].Found {
			t.Fatalf("contexts = %+v", rep.Contexts)
		}
	})

	t.Run("brief alias mirrors task-context", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "brief", "Hub", "-C", project, "--json")
		if res.exit != 0 {
			t.Fatalf("exit=%d stdout=%s", res.exit, res.stdout)
		}
		var rep struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &rep); err != nil {
			t.Fatalf("parse report: %v", err)
		}
		if rep.Mode != "understand" {
			t.Fatalf("default mode = %q, want understand", rep.Mode)
		}
	})

	t.Run("not indexed exits 2 with envelope", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "task-context", "anything", "-C", cold, "--json")
		if res.exit != 2 {
			t.Fatalf("exit=%d stdout=%s", res.exit, res.stdout)
		}
		var env1 envelope
		if err := json.Unmarshal([]byte(res.stdout), &env1); err != nil {
			t.Fatalf("parse envelope: %v\n%s", err, res.stdout)
		}
		if env1.OK || env1.Code != "not_indexed" {
			t.Fatalf("envelope = %+v", env1)
		}
	})

	t.Run("review mode is invalid input", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "task-context", "x", "-C", project, "--mode", "review", "--json")
		if res.exit != 1 {
			t.Fatalf("exit=%d stdout=%s", res.exit, res.stdout)
		}
		var env1 envelope
		if err := json.Unmarshal([]byte(res.stdout), &env1); err != nil {
			t.Fatalf("parse envelope: %v\n%s", err, res.stdout)
		}
		if env1.OK || env1.Code != "invalid_input" || env1.Error == "" {
			t.Fatalf("envelope = %+v", env1)
		}
	})

	t.Run("selectors rejected in understand mode", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "task-context", "x", "-C", project, "--at", "main.go:3", "--json")
		if res.exit != 1 {
			t.Fatalf("exit=%d stdout=%s", res.exit, res.stdout)
		}
		var env1 envelope
		if err := json.Unmarshal([]byte(res.stdout), &env1); err != nil {
			t.Fatalf("parse envelope: %v\n%s", err, res.stdout)
		}
		if env1.OK || env1.Code != "invalid_input" {
			t.Fatalf("envelope = %+v", env1)
		}
	})

	t.Run("invalid mode rejects before --at resolution", func(t *testing.T) {
		// A bogus position plus an invalid mode must fail on the mode (exit 1
		// invalid_input), not on resolving the position (exit 2 not_found).
		res := runCLI(t, bin, runner, env, "task-context", "x", "-C", project,
			"--mode", "understand", "--at", "bogus.go:9", "--json")
		if res.exit != 1 {
			t.Fatalf("exit=%d stdout=%s", res.exit, res.stdout)
		}
		var env1 envelope
		if err := json.Unmarshal([]byte(res.stdout), &env1); err != nil {
			t.Fatalf("parse envelope: %v\n%s", err, res.stdout)
		}
		if env1.OK || env1.Code != "invalid_input" {
			t.Fatalf("envelope = %+v", env1)
		}
	})
}
