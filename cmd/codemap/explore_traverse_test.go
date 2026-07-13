/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

func TestExploreTraverseCLIContracts(t *testing.T) {
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
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/explore-traverse\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "graph.go"), "package sample\n\nfunc Start() { Mid() }\nfunc Mid() { Leaf() }\nfunc Leaf() {}\n")
	env := append(isolatedCLIEnv(root), "CODEMAP_VECGREP_ENABLED=false")

	res := runCLI(t, bin, runner, env, "init", "-C", project, "--json")
	if res.exit != 0 {
		t.Fatalf("init exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
	}
	res = runCLI(t, bin, runner, env, "index", "-C", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
	if res.exit != 0 {
		t.Fatalf("index exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
	}

	t.Run("explore json and human output", func(t *testing.T) {
		args := []string{"explore", "Start", "-C", project, "--seeds", "1", "--edges", "1", "--depth", "1"}
		res := runCLI(t, bin, runner, env, append(args, "--json")...)
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("explore --json exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			SchemaVersion int    `json:"schema_version"`
			Query         string `json:"query"`
			Indexed       bool   `json:"indexed"`
			SearchMode    string `json:"search_mode"`
			Seeds         []struct {
				Selector *app.SymbolSelector `json:"selector"`
			} `json:"seeds"`
			Contexts []struct {
				Selector    *app.SymbolSelector `json:"selector"`
				Definitions []struct {
					Source        string `json:"source"`
					SourceOmitted bool   `json:"source_omitted"`
				} `json:"definitions"`
				Callers    []any `json:"callers"`
				Callees    []any `json:"callees"`
				References []any `json:"references"`
				Tests      []any `json:"tests"`
			} `json:"contexts"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.SchemaVersion != app.ExploreSchemaVersion || rep.Query != "Start" || !rep.Indexed || rep.SearchMode != "name" || len(rep.Seeds) != 1 || len(rep.Contexts) != 1 {
			t.Fatalf("explore report identity/cardinality = %+v", rep)
		}
		if rep.Seeds[0].Selector == nil || rep.Seeds[0].Selector.File != "graph.go" || rep.Contexts[0].Selector == nil {
			t.Fatalf("explore did not promote the seed to a durable selector: %+v", rep.Seeds)
		}
		if len(rep.Contexts[0].Definitions) != 1 || !rep.Contexts[0].Definitions[0].SourceOmitted || rep.Contexts[0].Definitions[0].Source != "" {
			t.Fatalf("explore context must stay source-light: %+v", rep.Contexts[0].Definitions)
		}
		if len(rep.Contexts[0].Callers) > 1 || len(rep.Contexts[0].Callees) > 1 || len(rep.Contexts[0].References) > 1 || len(rep.Contexts[0].Tests) > 1 {
			t.Fatalf("--edges cap was not honored: %+v", rep.Contexts[0])
		}

		res = runCLI(t, bin, runner, env, args...)
		if res.exit != 0 || res.stderr != "" || !strings.Contains(res.stdout, "Explore \"Start\"") || !strings.Contains(res.stdout, "Seeds (1; 1 joined)") || !strings.Contains(res.stdout, "Contexts (1)") {
			t.Fatalf("explore human output exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}
	})

	t.Run("traverse json and human output", func(t *testing.T) {
		args := []string{"traverse", "-C", project, "--at", "graph.go:3", "--direction", "outgoing", "--edge-types", "calls", "--depth", "1", "--limit", "1"}
		res := runCLI(t, bin, runner, env, append(args, "--json")...)
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("traverse --json exit=%d stderr=%q stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep struct {
			SchemaVersion int                 `json:"schema_version"`
			Indexed       bool                `json:"indexed"`
			Found         bool                `json:"found"`
			Start         *app.SymbolSelector `json:"start"`
			Direction     string              `json:"direction"`
			EdgeTypes     []string            `json:"edge_types"`
			DepthLimit    int                 `json:"depth_limit"`
			NodeLimit     int                 `json:"node_limit"`
			Hops          []struct {
				Selector       *app.SymbolSelector `json:"selector"`
				ParentSelector *app.SymbolSelector `json:"parent_selector"`
				Depth          int                 `json:"depth"`
				EdgeType       string              `json:"edge_type"`
				Confidence     string              `json:"confidence"`
			} `json:"hops"`
			Domains []any `json:"domains"`
		}
		mustJSON(t, res.stdout, &rep)
		if rep.SchemaVersion != app.TraverseSchemaVersion || !rep.Indexed || !rep.Found || rep.Start == nil || rep.Direction != "outgoing" || len(rep.EdgeTypes) != 1 || rep.EdgeTypes[0] != "calls" || rep.DepthLimit != 1 || rep.NodeLimit != 1 || len(rep.Hops) != 1 || len(rep.Domains) != 1 {
			t.Fatalf("traverse report identity/bounds = %+v", rep)
		}
		if rep.Hops[0].Selector == nil || rep.Hops[0].ParentSelector == nil || rep.Hops[0].Depth != 1 || rep.Hops[0].EdgeType != "calls" || rep.Hops[0].Confidence == "" {
			t.Fatalf("traverse hop contract = %+v", rep.Hops[0])
		}

		res = runCLI(t, bin, runner, env, args...)
		if res.exit != 0 || res.stderr != "" || !strings.Contains(res.stdout, "Traverse from") || !strings.Contains(res.stdout, "Hops (1)") || !strings.Contains(res.stdout, "Domains (1)") || !strings.Contains(res.stdout, "calls") {
			t.Fatalf("traverse human output exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
		}
	})

	t.Run("bounds and durable selector validation", func(t *testing.T) {
		cases := [][]string{
			{"explore", "Start", "-C", project, "--seeds", "11", "--json"},
			{"explore", "Start", "-C", project, "--edges", "21", "--json"},
			{"explore", "Start", "-C", project, "--depth", "11", "--json"},
			{"traverse", "-C", project, "--at", "graph.go:3", "--direction", "sideways", "--json"},
			{"traverse", "-C", project, "--at", "graph.go:3", "--edge-types", "magic", "--json"},
			{"traverse", "-C", project, "--at", "graph.go:3", "--depth", "11", "--json"},
			{"traverse", "-C", project, "--at", "graph.go:3", "--limit", "501", "--json"},
			{"traverse", "-C", project, "--json"},
		}
		for _, args := range cases {
			res := runCLI(t, bin, runner, env, args...)
			assertCLIEnvelope(t, res, exitOperational, app.CodeOperational)
			if res.stderr != "" {
				t.Fatalf("validation leaked stderr for %v: %q", args, res.stderr)
			}
		}
	})

	t.Run("cold projects return not-indexed envelopes", func(t *testing.T) {
		for _, args := range [][]string{
			{"explore", "Start", "-C", cold, "--json"},
			{"traverse", "-C", cold, "--at", "graph.go:3", "--json"},
		} {
			res := runCLI(t, bin, runner, env, args...)
			assertCLIEnvelope(t, res, exitNotFound, codeNotIndexed)
		}
	})
}
