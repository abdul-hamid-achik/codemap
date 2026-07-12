/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type cliResult struct {
	stdout string
	stderr string
	exit   int
}

// TestCLIContracts builds and drives the real executable. These checks live
// above handler-unit level on purpose: the regressions they pin were caused by
// public Cobra wiring (--path ignored, nested RunE unwrapped, --precise a no-op),
// which direct runX tests cannot catch.
func TestCLIContracts(t *testing.T) {
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
	project := filepath.Join(root, "sample")
	cold := filepath.Join(root, "cold")
	for _, dir := range []string{runner, project, cold} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/sample\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc Main() { Helper() }\nfunc Helper() {}\n")
	writeTestFile(t, filepath.Join(project, "wire.go"), "package main\n\nfunc Wire() { Helper() }\n")
	writeTestFile(t, filepath.Join(project, "model.go"), "package main\n\ntype Item struct{}\nfunc (Item) Touch() {}\n")
	writeTestFile(t, filepath.Join(project, "candidate.go"), "package main\n\nfunc Candidate() { Item{}.Touch() }\n")
	writeTestFile(t, filepath.Join(project, "handler.go"), "package main\n\nfunc Handler() {}\n")
	writeTestFile(t, filepath.Join(project, "hooks.go"), "package main\n\nvar Hook = struct{ Run func() }{Run: Handler}\n\nfunc Register(func()) {}\nfunc Setup() { Register(Handler) }\n")
	// A project-local value proves that -C affects config discovery too, not only
	// the final service call.
	writeTestFile(t, filepath.Join(project, "codemap.yaml"), "embedding:\n  dimensions: 321\n")

	env := isolatedCLIEnv(root)

	t.Run("single config command and honest provider help", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "--help")
		if res.exit != 0 {
			t.Fatalf("help exit=%d stderr=%s", res.exit, res.stderr)
		}
		if got := strings.Count(res.stdout, "\n  config          "); got != 1 {
			t.Fatalf("config command count = %d, want 1\n%s", got, res.stdout)
		}
		if !strings.Contains(res.stdout, "embedding provider (currently only ollama)") {
			t.Fatalf("provider help over-promises implementations:\n%s", res.stdout)
		}
	})

	t.Run("cobra validation failures honor json anywhere before terminator", func(t *testing.T) {
		cases := [][]string{
			{"callers", "--json"},
			{"--json", "callers"},
			{"callers", "Main", "--definitely-unknown", "--json"},
		}
		for _, args := range cases {
			res := runCLI(t, bin, runner, env, args...)
			assertCLIEnvelope(t, res, exitOperational, "operational")
			if res.stderr != "" {
				t.Fatalf("JSON validation failure leaked plain stderr for %v: %q", args, res.stderr)
			}
		}

		res := runCLI(t, bin, runner, env, "callers", "--json=false")
		if res.exit != exitOperational || res.stdout != "" || !strings.Contains(res.stderr, "Error:") {
			t.Fatalf("--json=false must preserve text errors: exit=%d stdout=%q stderr=%q", res.exit, res.stdout, res.stderr)
		}

		for _, args := range [][]string{
			{"source", "Helper", "--at", "main.go:4", "--json"},
			{"context", "Helper", "--at", "main.go:4", "--json"},
			{"references", "Handler", "--at", "handler.go:3", "--json"},
		} {
			res = runCLI(t, bin, runner, env, args...)
			assertCLIEnvelope(t, res, exitOperational, "operational")
			if !strings.Contains(res.stdout, "mutually exclusive") || res.stderr != "" {
				t.Fatalf("--at conflict must be an actionable JSON envelope for %v: stdout=%q stderr=%q", args, res.stdout, res.stderr)
			}
		}
	})

	t.Run("global path and positional index path", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "init", "-C", project, "--json")
		if res.exit != 0 {
			t.Fatalf("init -C exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}

		// The optional positional path must be consumed, not merely advertised.
		res = runCLI(t, bin, runner, env, "index", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
		if res.exit != 0 {
			t.Fatalf("index [path] exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var indexed struct {
			Root string `json:"root"`
		}
		mustJSON(t, res.stdout, &indexed)
		if indexed.Root != project {
			t.Fatalf("indexed root = %q, want %q", indexed.Root, project)
		}

		res = runCLI(t, bin, runner, env, "status", "-C", project, "--json")
		if res.exit != 0 {
			t.Fatalf("status -C exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var status struct {
			Path string `json:"path"`
		}
		mustJSON(t, res.stdout, &status)
		if status.Path != project {
			t.Fatalf("status path = %q, want %q", status.Path, project)
		}

		res = runCLI(t, bin, runner, env, "config", "show", "-C", project, "--json")
		if res.exit != 0 {
			t.Fatalf("config show -C exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var shown struct {
			Embedding struct {
				Dimensions int `json:"Dimensions"`
			} `json:"Embedding"`
		}
		mustJSON(t, res.stdout, &shown)
		if shown.Embedding.Dimensions != 321 {
			t.Fatalf("project-local dimensions = %d, want 321; -C did not drive config discovery", shown.Embedding.Dimensions)
		}

		// `config path` intentionally reports the global writable config path
		// (or CODEMAP_CONFIG), while `config show -C` above resolves project layers.
		res = runCLI(t, bin, runner, env, "config", "path", "-C", project, "--json")
		if res.exit != 0 {
			t.Fatalf("config path -C exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var configPath struct {
			File string `json:"config_file"`
		}
		mustJSON(t, res.stdout, &configPath)
		wantConfigPath := filepath.Join(root, "config", "config.yaml")
		if configPath.File != wantConfigPath {
			t.Fatalf("global writable config path = %q, want %q", configPath.File, wantConfigPath)
		}

		// -C is the explicit override when both path forms are supplied.
		res = runCLI(t, bin, runner, env, "index", cold, "-C", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
		if res.exit != 0 {
			t.Fatalf("index positional + -C exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		mustJSON(t, res.stdout, &indexed)
		if indexed.Root != project {
			t.Fatalf("-C precedence root = %q, want %q", indexed.Root, project)
		}
	})

	t.Run("precise is canonical and lsp stays hidden", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "callers", "--help")
		if res.exit != 0 {
			t.Fatalf("callers help exit=%d stderr=%s", res.exit, res.stderr)
		}
		if !strings.Contains(res.stdout, "--precise") {
			t.Fatalf("canonical --precise missing:\n%s", res.stdout)
		}
		if strings.Contains(res.stdout, "--lsp") {
			t.Fatalf("legacy --lsp should remain hidden:\n%s", res.stdout)
		}
		for _, flag := range []string{"--precise", "--lsp"} {
			res = runCLI(t, bin, runner, env, "callers", "DoesNotExist", flag, "-C", project, "--json")
			assertCLIEnvelope(t, res, exitNotFound, codeNotFound)
		}

		res = runCLI(t, bin, runner, env, "path", "--help")
		if res.exit != 0 || !strings.Contains(res.stdout, "app.Controller.Run app.Store.Save") || !strings.Contains(res.stdout, "call-graph") {
			t.Fatalf("path help must explain exact FQNs and confidence: exit=%d stderr=%q\n%s", res.exit, res.stderr, res.stdout)
		}
	})

	t.Run("source position selects one definition without a name argument", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "source", "--at", "main.go:4", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("source --at exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			Symbol   string `json:"symbol"`
			Selector struct {
				File      string `json:"file"`
				StartLine int    `json:"start_line"`
				FQN       string `json:"fqn"`
				Kind      string `json:"kind"`
			} `json:"selector"`
			Matches []struct {
				Symbol string `json:"symbol"`
				Source string `json:"source"`
			} `json:"matches"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.Symbol != "Helper" || rep.Selector.File != "main.go" || rep.Selector.StartLine != 4 || len(rep.Matches) != 1 || !strings.Contains(rep.Matches[0].Source, "func Helper") {
			t.Fatalf("source --at report = %+v", rep)
		}
	})

	t.Run("symbol-at accepts a batch of positions", func(t *testing.T) {
		// A lone position keeps today's single-result JSON shape (no batch wrapper).
		res := runCLI(t, bin, runner, env, "symbol-at", "main.go:4", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("symbol-at single exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var single struct {
			Symbol     string `json:"symbol"`
			Resolution string `json:"resolution"`
		}
		mustJSON(t, res.stdout, &single)
		if single.Symbol != "Helper" || single.Resolution != "exact" {
			t.Fatalf("symbol-at single report = %+v", single)
		}

		// Several positions (including a miss) return a batch report in one call.
		res = runCLI(t, bin, runner, env, "symbol-at", "main.go:3", "main.go:4", "main.go:999", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("symbol-at batch exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var batch struct {
			Requested int `json:"requested"`
			Results   []struct {
				Symbol     string `json:"symbol"`
				Resolution string `json:"resolution"`
			} `json:"results"`
		}
		mustJSON(t, res.stdout, &batch)
		if batch.Requested != 3 || len(batch.Results) != 3 {
			t.Fatalf("symbol-at batch report = %+v", batch)
		}
		if batch.Results[0].Symbol != "Main" || batch.Results[0].Resolution != "exact" {
			t.Fatalf("symbol-at batch[0] = %+v, want Main/exact", batch.Results[0])
		}
		if batch.Results[1].Symbol != "Helper" || batch.Results[1].Resolution != "exact" {
			t.Fatalf("symbol-at batch[1] = %+v, want Helper/exact", batch.Results[1])
		}
		if batch.Results[2].Resolution != "none" {
			t.Fatalf("symbol-at batch[2] (miss) = %+v, want resolution=none", batch.Results[2])
		}
	})

	t.Run("value references are distinct, bounded, and selector-aware", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "references", "Handler", "-C", project)
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("references exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		for _, want := range []string{
			"Value references to Handler", "not callers", "2 total", "coverage:      partial",
			"file scope — hooks.go", "main.Setup", "enclosing scopes:",
		} {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("references text missing %q:\n%s", want, res.stdout)
			}
		}
		if strings.Contains(res.stdout, "file scope — hooks.go:1") || strings.Contains(res.stdout, "--precise") {
			t.Fatalf("references text fabricated a lexical file line or suggested call precision:\n%s", res.stdout)
		}

		res = runCLI(t, bin, runner, env, "references", "Handler", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("references JSON exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			SchemaVersion       int              `json:"schema_version"`
			Found               bool             `json:"found"`
			References          []map[string]any `json:"references"`
			ReferencesTotal     int              `json:"references_total"`
			ReferencesTruncated int              `json:"references_truncated"`
			Coverage            string           `json:"coverage"`
			CallGraph           string           `json:"call_graph"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.SchemaVersion != 1 || !rep.Found || rep.ReferencesTotal != 2 || len(rep.References) != 2 ||
			rep.ReferencesTruncated != 0 || rep.Coverage != "partial" || rep.CallGraph != "name" {
			t.Fatalf("references JSON contract = %+v", rep)
		}

		res = runCLI(t, bin, runner, env, "references", "--at", "handler.go:3", "-C", project, "--json")
		if res.exit != 0 || !strings.Contains(res.stdout, `"fqn": "main.Handler"`) || !strings.Contains(res.stdout, `"references_total": 2`) {
			t.Fatalf("exact references selector failed: exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		res = runCLI(t, bin, runner, env, "references", "DoesNotExist", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotFound)
	})

	t.Run("dependency confidence is visible in human reports", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "dependencies", "main.go", "-C", project)
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("dependencies exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		for _, want := range []string{
			"confidence:    1 confirmed · 0 candidate",
			"calls:1 (1 confirmed)",
			"confidence: confirmed (same package)",
		} {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("dependencies text missing %q:\n%s", want, res.stdout)
			}
		}

		res = runCLI(t, bin, runner, env, "file-impact", "main.go", "-C", project)
		if res.exit != 0 || !strings.Contains(res.stdout, "confirmed indexed dependency evidence") {
			t.Fatalf("file-impact must identify its unsafe proof: exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}

		res = runCLI(t, bin, runner, env, "dependencies", "model.go", "-C", project)
		if res.exit != 0 || !strings.Contains(res.stdout, "confidence:    0 confirmed · 1 candidate") ||
			!strings.Contains(res.stdout, "confidence: candidate (name fanout)") {
			t.Fatalf("candidate dependency must stay visibly non-confirmed: exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}
	})

	t.Run("disconnected path is an answered json report", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "path", "Main", "Helper", "-C", project)
		if res.exit != 0 || res.stderr != "" || !strings.Contains(res.stdout, "Main → Helper") || !strings.Contains(res.stdout, "call graph: name") {
			t.Fatalf("found text path must disclose confidence: exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}

		res = runCLI(t, bin, runner, env, "path", "Helper", "Main", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("valid no-path query exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			Found     bool              `json:"found"`
			CallGraph string            `json:"call_graph"`
			Path      []json.RawMessage `json:"path"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.Found || rep.CallGraph != "name" || len(rep.Path) != 0 {
			t.Fatalf("no-path report = %+v, want found:false call_graph:name path:[]", rep)
		}

		res = runCLI(t, bin, runner, env, "path", "Helper", "DoesNotExist", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotFound)

		writeTestFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc Main() { Helper() }\nfunc Helper() {}\n// drift\n")
		res = runCLI(t, bin, runner, env, "path", "Main", "Helper", "-C", project)
		if res.exit != 0 || !strings.Contains(res.stdout, "index is stale") {
			t.Fatalf("found stale path must disclose drift: exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}
	})

	t.Run("query misses and cold projects use exit two envelopes", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "find", "DoesNotExist", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotFound)

		res = runCLI(t, bin, runner, env, "find", "Main", "-C", cold, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotIndexed)
		var envelope jsonEnvelope
		mustJSON(t, res.stdout, &envelope)
		if !strings.Contains(envelope.Hint, "codemap index") {
			t.Fatalf("not-indexed hint = %q", envelope.Hint)
		}

		res = runCLI(t, bin, runner, env, "callers", "DoesNotExist", "-C", project)
		if res.exit != exitNotFound || !strings.Contains(res.stderr, "codemap find") {
			t.Fatalf("text symbol miss must suggest find: exit=%d stderr=%q", res.exit, res.stderr)
		}
		res = runCLI(t, bin, runner, env, "find", "Main", "-C", cold)
		if res.exit != exitNotFound || !strings.Contains(res.stderr, "codemap index") {
			t.Fatalf("text cold project must suggest index: exit=%d stderr=%q", res.exit, res.stderr)
		}
	})

	t.Run("nested RunE and post-flag validation", func(t *testing.T) {
		// cache drop is a grandchild of root; it used to bypass jsonHandler.
		res := runCLI(t, bin, runner, env, "cache", "drop", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitOperational, "operational")

		// config.Load validates file/env values, but CLI overrides are applied
		// afterward and therefore need a second validation pass.
		res = runCLI(t, bin, runner, env, "status", "-C", project, "--embed-provider", "openai", "--json")
		assertCLIEnvelope(t, res, exitOperational, "operational")
		if !strings.Contains(res.stdout, "only \\\"ollama\\\" is supported") {
			t.Fatalf("invalid provider error is not actionable: %s", res.stdout)
		}
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolatedCLIEnv(root string) []string {
	overrides := map[string]string{
		"CODEMAP_DATA":               filepath.Join(root, "data"),
		"CODEMAP_CACHE":              filepath.Join(root, "cache"),
		"CODEMAP_CONFIG_DIR":         filepath.Join(root, "config"),
		"CODEMAP_EMBEDDING_PROVIDER": "ollama",
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(key, "CODEMAP_") {
			env = append(env, item)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func runCLI(t *testing.T, bin, dir string, env []string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %v: %v", args, err)
		}
		exit = ee.ExitCode()
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}

func assertCLIEnvelope(t *testing.T, res cliResult, wantExit int, wantCode string) {
	t.Helper()
	if res.exit != wantExit {
		t.Fatalf("exit=%d, want %d\nstderr=%s\nstdout=%s", res.exit, wantExit, res.stderr, res.stdout)
	}
	var env jsonEnvelope
	mustJSON(t, res.stdout, &env)
	if env.OK || env.Code != wantCode || env.Error == "" {
		t.Fatalf("bad envelope: %+v", env)
	}
}

func mustJSON(t *testing.T, text string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), dst); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, text)
	}
}
