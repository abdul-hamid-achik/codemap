/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
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
	// A multi-line body big enough that dropping it (brief mode) measurably
	// shrinks the response despite the added source_omitted metadata field —
	// unlike a one-line stub, where the field can outweigh the body. Deliberately
	// calls nothing else in the project so it doesn't perturb the dependency/call
	// counts other subtests assert on.
	writeTestFile(t, filepath.Join(project, "bulky.go"), "package main\n\n"+
		"// Bulky does a lot of unremarkable work.\n"+
		"func Bulky() {\n"+strings.Repeat("\t_ = 0\n", 40)+"}\n")
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

	// I05: --brief drops the source body (signature/doc stay, source_omitted:true)
	// on both `source` and `context`, and the resulting JSON is strictly smaller.
	// Uses "Bulky" (a deliberately multi-line body) rather than a one-line stub:
	// on a trivial function, the added source_omitted field can outweigh the tiny
	// body it replaces, so the size assertion needs a body worth dropping.
	t.Run("brief drops source bodies on source and context", func(t *testing.T) {
		full := runCLI(t, bin, runner, env, "source", "Bulky", "-C", project, "--json")
		if full.exit != 0 || full.stderr != "" {
			t.Fatalf("source exit=%d stderr=%q", full.exit, full.stderr)
		}
		brief := runCLI(t, bin, runner, env, "source", "Bulky", "--brief", "-C", project, "--json")
		if brief.exit != 0 || brief.stderr != "" {
			t.Fatalf("source --brief exit=%d stderr=%q", brief.exit, brief.stderr)
		}
		var briefRep struct {
			Matches []struct {
				Symbol        string `json:"symbol"`
				Signature     string `json:"signature"`
				Source        string `json:"source"`
				SourceOmitted bool   `json:"source_omitted"`
			} `json:"matches"`
		}
		mustJSON(t, brief.stdout, &briefRep)
		if len(briefRep.Matches) != 1 || briefRep.Matches[0].Source != "" || !briefRep.Matches[0].SourceOmitted || briefRep.Matches[0].Signature == "" {
			t.Fatalf("source --brief report = %+v", briefRep)
		}
		if len(brief.stdout) >= len(full.stdout) {
			t.Fatalf("source --brief (%d bytes) should be smaller than source (%d bytes)", len(brief.stdout), len(full.stdout))
		}

		fullCtx := runCLI(t, bin, runner, env, "context", "Bulky", "-C", project, "--json")
		if fullCtx.exit != 0 || fullCtx.stderr != "" {
			t.Fatalf("context exit=%d stderr=%q", fullCtx.exit, fullCtx.stderr)
		}
		briefCtx := runCLI(t, bin, runner, env, "context", "Bulky", "--brief", "-C", project, "--json")
		if briefCtx.exit != 0 || briefCtx.stderr != "" {
			t.Fatalf("context --brief exit=%d stderr=%q", briefCtx.exit, briefCtx.stderr)
		}
		var briefCtxRep struct {
			Definitions []struct {
				Signature     string `json:"signature"`
				Source        string `json:"source"`
				SourceOmitted bool   `json:"source_omitted"`
			} `json:"definitions"`
			CallersTotal int `json:"callers_total"`
		}
		mustJSON(t, briefCtx.stdout, &briefCtxRep)
		if len(briefCtxRep.Definitions) != 1 || briefCtxRep.Definitions[0].Source != "" || !briefCtxRep.Definitions[0].SourceOmitted || briefCtxRep.Definitions[0].Signature == "" {
			t.Fatalf("context --brief report = %+v", briefCtxRep)
		}
		if len(briefCtx.stdout) >= len(fullCtx.stdout) {
			t.Fatalf("context --brief (%d bytes) should be smaller than context (%d bytes)", len(briefCtx.stdout), len(fullCtx.stdout))
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

	t.Run("grep resolves text hits to their enclosing symbol", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "grep", "Helper()", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("grep exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			Total int `json:"total"`
			Hits  []struct {
				File       string `json:"file"`
				Line       int    `json:"line"`
				Symbol     string `json:"symbol"`
				Resolution string `json:"resolution"`
				Selector   *struct {
					File string `json:"file"`
					FQN  string `json:"fqn"`
				} `json:"selector"`
			} `json:"hits"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.Total == 0 || len(rep.Hits) == 0 {
			t.Fatalf("grep report = %+v, want at least one hit", rep)
		}
		var sawMain bool
		for _, h := range rep.Hits {
			if h.Resolution == "none" {
				t.Fatalf("grep hit %+v should resolve onto a symbol", h)
			}
			if h.Symbol == "Main" {
				sawMain = true
				if h.Selector == nil || h.Selector.FQN == "" {
					t.Fatalf("grep hit for Main should carry a populated selector: %+v", h)
				}
			}
		}
		if !sawMain {
			t.Fatalf("grep for Helper() should resolve a hit onto Main's call site: %+v", rep.Hits)
		}

		// --regex opts into RE2 syntax a literal search would not match.
		res = runCLI(t, bin, runner, env, "grep", "--regex", `Help\w+\(\)`, "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("grep --regex exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var regexRep struct {
			Total int  `json:"total"`
			Regex bool `json:"regex"`
		}
		mustJSON(t, res.stdout, &regexRep)
		if !regexRep.Regex || regexRep.Total == 0 {
			t.Fatalf("grep --regex report = %+v, want regex:true and at least one hit", regexRep)
		}

		// --ignore-case matches a differently-cased query.
		res = runCLI(t, bin, runner, env, "grep", "-i", "HELPER()", "-C", project, "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("grep -i exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var ciRep struct {
			Total      int  `json:"total"`
			IgnoreCase bool `json:"ignore_case"`
		}
		mustJSON(t, res.stdout, &ciRep)
		if !ciRep.IgnoreCase || ciRep.Total == 0 {
			t.Fatalf("grep -i report = %+v, want ignore_case:true and at least one hit", ciRep)
		}

		// A pattern with zero matches is a not-found exit, same taxonomy as find.
		res = runCLI(t, bin, runner, env, "grep", "definitely-not-present-xyz", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotFound)

		// Invalid regex syntax is an operational error, not a not-found miss.
		res = runCLI(t, bin, runner, env, "grep", "--regex", "(unterminated[", "-C", project, "--json")
		assertCLIEnvelope(t, res, exitOperational, "operational")
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

		res = runCLI(t, bin, runner, env, "coverage", "-C", cold, "--json")
		assertCLIEnvelope(t, res, exitNotFound, codeNotIndexed)

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

	t.Run("agent commands need no index and report structured results", func(t *testing.T) {
		// agent list --json returns a well-formed array (works with no index).
		res := runCLI(t, bin, runner, env, "agent", "list", "-C", cold, "--json")
		if res.exit != 0 {
			t.Fatalf("agent list exit=%d stderr=%s", res.exit, res.stderr)
		}
		var dets []map[string]any
		mustJSON(t, res.stdout, &dets)
		if len(dets) == 0 {
			t.Fatalf("agent list should report harnesses, got %s", res.stdout)
		}

		// A dry-run setup into a fresh project writes nothing but reports the plan.
		res = runCLI(t, bin, runner, env, "agent", "setup", "vscode", "-C", cold, "--dry-run", "--json")
		if res.exit != 0 {
			t.Fatalf("agent setup --dry-run exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		if _, err := os.Stat(filepath.Join(cold, ".vscode", "mcp.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry-run must not write config, got err=%v", err)
		}

		// An unknown harness is an operational failure with the standard envelope.
		res = runCLI(t, bin, runner, env, "agent", "setup", "nope", "-C", cold, "--json")
		assertCLIEnvelope(t, res, exitOperational, "operational")
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

// TestReviewRiskGateExitCode drives the real executable (like TestCLIContracts)
// to pin the I10 gate contract: --fail-on-risk/--fail-on-untested print the
// normal, unchanged output and only change the process exit code (dedicated
// exitGateFailed = 6), never synthesizing a {"ok":false,...} failure envelope.
// It also pins the pre-commit-hook degradation path: an unindexed repo or a
// non-git directory must exit 0, never block a commit on missing infra.
func TestReviewRiskGateExitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
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
	project := filepath.Join(root, "project") // indexed repo, staged untested+risky change
	cold := filepath.Join(root, "cold")       // git repo, never indexed
	nonRepo := filepath.Join(root, "nonrepo") // not a git repository at all
	for _, dir := range []string{runner, project, cold, nonRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := isolatedCLIEnv(root)

	// Hub() has 8 direct callers and NO covering test: changing it trips both
	// --fail-on-untested (untested_symbols non-empty) and --fail-on-risk high
	// (untested alone combines to a high score).
	var b strings.Builder
	b.WriteString("package gate\n\nfunc Hub() {}\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "func C%d() { Hub() }\n", i)
	}
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/gate\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "a.go"), b.String())
	gateGit(t, project, "init")
	gateGit(t, project, "config", "user.email", "t@t")
	gateGit(t, project, "config", "user.name", "t")
	gateGit(t, project, "config", "commit.gpgsign", "false")
	gateGit(t, project, "add", "-A")
	gateGit(t, project, "commit", "-m", "init")

	res := runCLI(t, bin, runner, env, "index", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
	if res.exit != 0 {
		t.Fatalf("index exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
	}

	// Touch Hub's body without shifting line numbers, then STAGE it — the
	// documented pre-commit hook reviews --staged.
	edited := strings.Replace(b.String(), "func Hub() {}", "func Hub() { _ = 1 }", 1)
	writeTestFile(t, filepath.Join(project, "a.go"), edited)
	gateGit(t, project, "add", "-A")

	t.Run("fail-on-untested trips exit 6 and leaves json body unchanged", func(t *testing.T) {
		base := runCLI(t, bin, runner, env, "review", "-C", project, "--staged", "--json")
		if base.exit != 0 || base.stderr != "" {
			t.Fatalf("baseline review exit=%d stderr=%s stdout=%s", base.exit, base.stderr, base.stdout)
		}
		var baseRep app.ReviewReport
		mustJSON(t, base.stdout, &baseRep)
		if len(baseRep.UntestedSymbols) == 0 {
			t.Fatalf("fixture must produce an untested changed symbol, got %+v", baseRep)
		}

		gated := runCLI(t, bin, runner, env, "review", "-C", project, "--staged", "--json", "--fail-on-untested")
		if gated.exit != exitGateFailed {
			t.Fatalf("--fail-on-untested exit=%d, want %d\nstderr=%s\nstdout=%s", gated.exit, exitGateFailed, gated.stderr, gated.stdout)
		}
		if gated.stderr != "" {
			t.Fatalf("gate must not print to stderr: %q", gated.stderr)
		}
		if gated.stdout != base.stdout {
			t.Fatalf("--json body changed when the gate tripped:\n--- base ---\n%s\n--- gated ---\n%s", base.stdout, gated.stdout)
		}
		var envelope map[string]any
		if err := json.Unmarshal([]byte(gated.stdout), &envelope); err != nil {
			t.Fatalf("gated stdout is not valid JSON: %v", err)
		}
		if _, hasOK := envelope["ok"]; hasOK {
			t.Fatalf("gate must stay the normal success shape (no ok field), got %+v", envelope)
		}
	})

	t.Run("fail-on-risk high trips on the same fixture", func(t *testing.T) {
		gated := runCLI(t, bin, runner, env, "review", "-C", project, "--staged", "--json", "--fail-on-risk", "high")
		if gated.exit != exitGateFailed {
			t.Fatalf("--fail-on-risk high exit=%d, want %d\nstdout=%s", gated.exit, exitGateFailed, gated.stdout)
		}
	})

	t.Run("invalid fail-on-risk value is an operational error, not a gate", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "review", "-C", project, "--staged", "--json", "--fail-on-risk", "critical")
		assertCLIEnvelope(t, res, exitOperational, "operational")
	})

	t.Run("risk command gate mirrors review and leaves json body unchanged", func(t *testing.T) {
		base := runCLI(t, bin, runner, env, "risk", "Hub", "-C", project, "--json")
		if base.exit != 0 || base.stderr != "" {
			t.Fatalf("baseline risk exit=%d stderr=%s stdout=%s", base.exit, base.stderr, base.stdout)
		}
		var baseRep app.RiskReport
		mustJSON(t, base.stdout, &baseRep)
		if baseRep.Level != "high" {
			t.Fatalf("fixture risk level = %q, want high (untested 8-caller hub)", baseRep.Level)
		}

		gated := runCLI(t, bin, runner, env, "risk", "Hub", "-C", project, "--json", "--fail-on-risk", "high")
		if gated.exit != exitGateFailed {
			t.Fatalf("risk --fail-on-risk exit=%d, want %d\nstdout=%s", gated.exit, exitGateFailed, gated.stdout)
		}
		if gated.stdout != base.stdout {
			t.Fatalf("risk --json body changed when the gate tripped:\n--- base ---\n%s\n--- gated ---\n%s", base.stdout, gated.stdout)
		}
	})

	t.Run("hook path degrades to exit 0 on an unindexed or non-git repo", func(t *testing.T) {
		// cold: a real git repo with a staged change, but never indexed.
		writeTestFile(t, filepath.Join(cold, "a.go"), "package cold\n\nfunc F() {}\n")
		gateGit(t, cold, "init")
		gateGit(t, cold, "config", "user.email", "t@t")
		gateGit(t, cold, "config", "user.name", "t")
		gateGit(t, cold, "config", "commit.gpgsign", "false")
		gateGit(t, cold, "add", "-A")
		gateGit(t, cold, "commit", "-m", "init")
		writeTestFile(t, filepath.Join(cold, "a.go"), "package cold\n\nfunc F() { _ = 1 }\n")
		gateGit(t, cold, "add", "-A")

		res := runCLI(t, bin, runner, env, "review", "-C", cold, "--staged", "--json", "--fail-on-untested")
		if res.exit != 0 {
			t.Fatalf("unindexed repo review --fail-on-untested exit=%d, want 0 (never block a commit on missing infra)\nstderr=%s\nstdout=%s", res.exit, res.stderr, res.stdout)
		}

		// nonRepo: not a git repository at all.
		res = runCLI(t, bin, runner, env, "review", "-C", nonRepo, "--staged", "--json", "--fail-on-untested")
		if res.exit != 0 {
			t.Fatalf("non-repo review --fail-on-untested exit=%d, want 0\nstderr=%s\nstdout=%s", res.exit, res.stderr, res.stdout)
		}
	})
}

// gateGit runs a git command in dir, failing the test on error.
func gateGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
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

// TestGrepJSONStaysPureJSONWhenStale pins the fix for runGrep printing the
// "⚠ index is stale" line to stdout unconditionally, ahead of the --json
// gate: a stale index (any file added/changed/deleted since the last index,
// the common state mid-edit) used to corrupt the machine-readable stdout
// stream for `codemap grep --json` with a leading non-JSON line.
func TestGrepJSONStaysPureJSONWhenStale(t *testing.T) {
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
	for _, dir := range []string{runner, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := isolatedCLIEnv(root)

	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/stale\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "a.go"), "package stale\n\nfunc Needle() {}\n")

	res := runCLI(t, bin, runner, env, "index", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
	if res.exit != 0 {
		t.Fatalf("index exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
	}

	// Add a new file without reindexing — the index is now stale (New > 0).
	writeTestFile(t, filepath.Join(project, "b.go"), "package stale\n\nfunc Other() { Needle() }\n")

	res = runCLI(t, bin, runner, env, "grep", "Needle", "-C", project, "--json")
	if res.exit != 0 || res.stderr != "" {
		t.Fatalf("grep exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(res.stdout), "⚠") {
		t.Fatalf("stdout must be pure JSON, got a leading warning line:\n%s", res.stdout)
	}
	var rep app.GrepReport
	mustJSON(t, res.stdout, &rep)
	if !rep.Stale {
		t.Fatalf("fixture must produce a stale report (New file added post-index), got %+v", rep)
	}
	if len(rep.Hits) == 0 {
		t.Fatalf("grep report = %+v, want at least one hit", rep)
	}
}
