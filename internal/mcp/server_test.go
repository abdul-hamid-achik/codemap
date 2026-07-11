package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func textOf(res *sdkmcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// result() serializes tool payloads without HTML escaping, so <, >, & stay
// literal — agents (and humans reading the JSON) see "A -> B", not "A -> B",
// and TypeScript generics like Array<string> read cleanly.
func TestResultJSONNotHTMLEscaped(t *testing.T) {
	res, _, _ := result(map[string]string{"target": "A -> B & Array<string>"}, nil)
	txt := textOf(res)
	// With HTML escaping on, this would read "A -<esc> B ..." with the angle
	// brackets/ampersand turned into \u00xx, and the literal check below would fail.
	if !strings.Contains(txt, "A -> B & Array<string>") {
		t.Errorf("result JSON should keep <, >, & literal (no HTML escaping): %s", txt)
	}
}

func TestResultPreservesCodedErrorForAgents(t *testing.T) {
	err := &app.CodedError{Code: app.CodeMissing, Hint: "run: codemap index", Err: fmt.Errorf("index unavailable")}
	res, _, _ := result(nil, err)
	if !res.IsError {
		t.Fatal("coded error result should set IsError")
	}
	if got := textOf(res); !strings.Contains(got, "index unavailable") || !strings.Contains(got, "run: codemap index") {
		t.Fatalf("visible error should include message + remediation hint, got %q", got)
	}
	raw, ok := res.Meta["error"].(json.RawMessage)
	if !ok {
		t.Fatalf("Meta[error] = %T, want json.RawMessage", res.Meta["error"])
	}
	var envelope mcpError
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != app.CodeMissing || envelope.Hint != "run: codemap index" {
		t.Fatalf("structured error = %+v", envelope)
	}
}

func TestInstructionsCoverKeyCapabilities(t *testing.T) {
	// The instructions are an agent's first-contact playbook; keep them in sync
	// with the actual tools and accuracy model.
	for _, want := range []string{"codemap_index", "codemap_impact", "codemap_semantic",
		"codemap_source", "codemap_projects", "precise:true", "name-based",
		"selector", "start_line", "volatile database ids",
		`"indexed": false`, "degrades to name-based",
		// precise:true spans engines — agents on an LSP-language project must know
		// it's the only way to get a call graph (callHierarchy), not just a Go fix.
		"TypeScript", "JavaScript", "Python", "callHierarchy"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("MCP instructions should mention %q", want)
		}
	}
}

func TestMCPServer(t *testing.T) {
	// Isolate all codemap dirs.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "other.go"),
		[]byte("package app\n\nfunc Other() { Helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// Index (structure-only) via the same session so the server sees data.
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	// tools/list
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	toolsByName := map[string]*sdkmcp.Tool{}
	for _, tool := range lt.Tools {
		got[tool.Name] = true
		toolsByName[tool.Name] = tool
	}
	for _, want := range []string{"codemap_init", "codemap_index", "codemap_status", "codemap_semantic", "codemap_callers", "codemap_find", "codemap_source", "codemap_context", "codemap_context_batch", "codemap_review", "codemap_read_order", "codemap_dependencies", "codemap_file_impact", "codemap_risk", "codemap_projects", "codemap_docs", "codemap_annotate", "codemap_annotations", "codemap_unannotate", "codemap_doctor", "codemap_branch_status", "codemap_branch_switch"} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
		}
	}
	// Exact-definition tools accept a source selector projected directly from
	// the file/start_line/fqn/kind fields already present on symbol results. Once
	// selector exists, symbol must not remain schema-required.
	for _, name := range []string{"codemap_callers", "codemap_callees", "codemap_impact", "codemap_risk", "codemap_source", "codemap_context"} {
		tool := toolsByName[name]
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required   []string       `json:"required"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["selector"]; !ok {
			t.Errorf("%s schema has no selector: %s", name, raw)
		}
		for _, required := range schema.Required {
			if required == "symbol" {
				t.Errorf("%s still requires symbol, preventing selector-only calls: %s", name, raw)
			}
		}
	}
	// Inventory-only calls must be representable: keys is optional when
	// via_vault supplies value-free key names, and both inventory fields must be
	// present on secret-impact and required-keys.
	for _, name := range []string{"codemap_secret_impact", "codemap_required_keys"} {
		tool := toolsByName[name]
		if tool == nil {
			t.Fatalf("missing tool %q", name)
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required   []string       `json:"required"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		_, viaVault := schema.Properties["via_vault"]
		_, prefix := schema.Properties["prefix"]
		if !viaVault || !prefix {
			t.Errorf("%s schema must expose via_vault + prefix: %s", name, raw)
		}
		for _, required := range schema.Required {
			if required == "keys" {
				t.Errorf("%s schema requires keys, preventing a via_vault-only call: %s", name, raw)
			}
		}
	}

	// codemap_status
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_status",
		Arguments: map[string]any{"path": proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("status tool error: %s", textOf(res))
	}
	if txt := textOf(res); !strings.Contains(txt, `"registered": true`) || !strings.Contains(txt, `"nodes":`) {
		t.Errorf("unexpected status payload: %s", txt)
	} else if !strings.Contains(txt, `"stale"`) {
		// Agents are told (codemap_docs) to check freshness before trusting results,
		// so the staleness object must reach them over MCP, not just the CLI.
		t.Errorf("status should carry the staleness object for agents: %s", txt)
	}

	// codemap_callers — a real symbol with callers reports found:true.
	res2, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_callers",
		Arguments: map[string]any{"path": proj, "symbol": "Helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(res2); !strings.Contains(txt, "Run") || !strings.Contains(txt, `"found": true`) {
		t.Errorf("callers of Helper should include Run and found:true: %s", txt)
	}

	// codemap_dependencies is the thin MCP twin of Service.Dependencies. The
	// cross-file Other→Helper call must survive the transport with grouped
	// evidence and explicit completeness instead of a raw edge/id dump.
	deps, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_dependencies",
		Arguments: map[string]any{"path": proj, "file": "main.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(deps); deps.IsError || !strings.Contains(txt, `"evidence_total": 1`) ||
		!strings.Contains(txt, `"file": "other.go"`) || !strings.Contains(txt, `"coverage"`) ||
		strings.Contains(txt, `"id":`) {
		t.Fatalf("dependencies transport payload is incomplete or leaked an id: %s", txt)
	}

	// A nonexistent symbol must report found:false so an agent can tell a typo
	// from a real symbol with no callers (both have empty results).
	res3, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_callers",
		Arguments: map[string]any{"path": proj, "symbol": "NoSuchSymbolXYZ"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(res3); !strings.Contains(txt, `"found": false`) {
		t.Errorf("callers of a nonexistent symbol should report found:false: %s", txt)
	}

	// Selector-only source is the exact drill-down after find/symbols/context.
	exactSource, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_source",
		Arguments: map[string]any{
			"path": proj,
			"selector": map[string]any{
				"file": "main.go", "start_line": 5, "fqn": "app.Helper", "kind": "function",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exactSource.IsError || !strings.Contains(textOf(exactSource), `"selector"`) || !strings.Contains(textOf(exactSource), "func Helper") {
		t.Fatalf("selector-only source failed: %s", textOf(exactSource))
	}
}

func TestMCPNotIndexedSignal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	// A real project that is deliberately never indexed.
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Every query tool must report the project isn't indexed rather than
	// returning empty results that read as a real "no results" answer.
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"codemap_callers", map[string]any{"path": proj, "symbol": "Run"}},
		{"codemap_impact", map[string]any{"path": proj, "symbol": "Run"}},
		{"codemap_context", map[string]any{"path": proj, "symbol": "Run"}},
		{"codemap_dependencies", map[string]any{"path": proj, "file": "main.go"}},
		{"codemap_find", map[string]any{"path": proj, "query": "Run"}},
		{"codemap_semantic", map[string]any{"path": proj, "query": "run"}},
		{"codemap_hotspots", map[string]any{"path": proj}},
	} {
		res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		txt := textOf(res)
		if !strings.Contains(txt, `"indexed": false`) || !strings.Contains(txt, "codemap_index") {
			t.Errorf("%s on an unindexed project should signal not-indexed, got: %s", tc.tool, txt)
		}
	}
}

func TestMCPAnnotateUnknownTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Real() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Real symbol → matched true, no warning note.
	real, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_annotate",
		Arguments: map[string]any{"path": proj, "symbol": "Real", "source": "s", "note": "n"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(real); !strings.Contains(txt, `"matched": true`) {
		t.Errorf("annotating an indexed symbol should report matched true: %s", txt)
	}
	// Ghost symbol → matched false + an explanatory note so the agent doesn't
	// think it pinned knowledge that can never surface.
	ghost, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_annotate",
		Arguments: map[string]any{"path": proj, "symbol": "GhostXYZ", "source": "s", "note": "n"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(ghost); !strings.Contains(txt, `"matched": false`) || !strings.Contains(txt, "won't surface") {
		t.Errorf("annotating an unknown symbol should warn via matched false + note: %s", txt)
	}
}

// TestMCPUnannotate verifies agents can remove annotations, not only create
// them — the knowledge layer must be prunable (CLI/MCP parity with `annotations
// --rm`). Round-trip: annotate → unannotate by id → gone → second remove is a
// graceful no-op.
func TestMCPUnannotate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Real() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create an annotation and capture its id.
	ann, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_annotate",
		Arguments: map[string]any{"path": proj, "symbol": "Real", "note": "removable-note"}})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(textOf(ann)), &created); err != nil || created.ID == 0 {
		t.Fatalf("annotate should return a numeric id, got %s (%v)", textOf(ann), err)
	}

	// Remove it.
	rm, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_unannotate",
		Arguments: map[string]any{"path": proj, "id": created.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(rm); !strings.Contains(txt, `"removed": true`) {
		t.Errorf("unannotate should report removed true: %s", txt)
	}

	// It's gone from the list.
	list, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_annotations",
		Arguments: map[string]any{"path": proj, "symbol": "Real"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textOf(list), "removable-note") {
		t.Errorf("annotation should be gone after unannotate: %s", textOf(list))
	}

	// Removing again is a graceful no-op (removed false + note), not an error.
	again, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_unannotate",
		Arguments: map[string]any{"path": proj, "id": created.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(again); !strings.Contains(txt, `"removed": false`) {
		t.Errorf("removing a missing annotation should report removed false, not error: %s", txt)
	}
}

// TestMCPDoctor verifies agents can diagnose the environment via codemap_doctor —
// the structured report lists the data dir and each toolchain/language-server/
// embeddings check, regardless of which are installed on the host.
func TestMCPDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	sess, err := app.Open("") // no index needed — doctor checks the environment
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_doctor", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	txt := textOf(res)
	for _, want := range []string{"data_dir", "go toolchain", "pyright-langserver", "embeddings"} {
		if !strings.Contains(txt, want) {
			t.Errorf("doctor report should include %q, got: %s", want, txt)
		}
	}
}

func TestMCPPreciseCallers(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	// Isolate only the data dir; gopls uses the real (persistent) cache.
	t.Setenv("CODEMAP_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEMAP_CONFIG", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package m\n\ntype T struct{}\n\nfunc (t T) Helper() {}\n\nfunc Run() { var x T; x.Helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_callers",
		Arguments: map[string]any{"path": proj, "symbol": "Helper", "precise": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("precise callers tool error: %s", textOf(res))
	}
	if txt := textOf(res); !strings.Contains(txt, "Run") {
		t.Errorf("precise callers of Helper should include Run: %s", txt)
	}
}

// TestRegisteredToolsAppearInDocsCommands pins P1-18 (O77/O79): the
// in-band codemap_docs agent guide is the single source of truth for
// agents, so a tool that ships without being listed there is invisible
// to every agent audience. This test walks every registered MCP
// tool name and asserts each appears in the commands topic of
// internal/app/docs.go — so a future tool that forgets to add itself
// fails at lint/test time instead of silently disappearing from the
// agent guide.
func TestRegisteredToolsAppearInDocsCommands(t *testing.T) {
	// Build a registry of all tool names the MCP server registers.
	registry := map[string]bool{}
	// Named function so the recursive walk() call below resolves.
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			path := dir + "/" + name
			if e.IsDir() {
				walk(path)
				continue
			}
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			// Look for sdkmcp.AddTool(... "codemap_<name>", ...)
			re := regexp.MustCompile(`(?s)sdkmcp\.AddTool\(.*?Name:\s*"codemap_([a-z_]+)"`)
			for _, m := range re.FindAllStringSubmatch(string(data), -1) {
				registry[m[1]] = true
			}
		}
	}
	walk(".")
	if len(registry) == 0 {
		t.Fatal("no MCP tools discovered in internal/mcp — is the regex still right?")
	}
	// Load the commands topic from docs.go.
	docsBytes, err := os.ReadFile("../../internal/app/docs.go")
	if err != nil {
		t.Fatal(err)
	}
	// Slice the docs.go content to the "commands" topic body.
	docs := string(docsBytes)
	start := strings.Index(docs, `{"commands", `)
	if start < 0 {
		t.Fatal("docs.go has no {\"commands\", ...} topic; the test must be updated")
	}
	end := strings.Index(docs[start:], "}\n\n")
	if end < 0 {
		t.Fatal("could not find end of commands topic in docs.go")
	}
	commandsTopic := docs[start : start+end]
	// Every registered tool name must appear (with or without its codemap_ prefix).
	missing := []string{}
	for tool := range registry {
		// Tools surface in docs.go as either "codemap_<name>" or as the
		// bare <name> in the MCP-tools list at the end of the topic.
		if !strings.Contains(commandsTopic, tool) {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("P1-18: %d MCP tools are registered but missing from the docs.go commands topic: %v. Update internal/app/docs.go's commands topic so the in-band agent guide stays in sync.", len(missing), missing)
	}
}

// TestMCPIndexProgressNotifications pins P2-02 (O42): when the client
// supplies a progress token, codemap_index must report per-file
// and per-embed progress to the client via ServerSession.NotifyProgress
// so a multi-minute reindex doesn't look hung. The handler is
// called from the indexer's parallel Go workers, so the notification
// path must be goroutine-safe and throttled (not 60Hz on a 10k-file
// repo). The test asserts (a) ≥1 progress notification arrives and
// (b) the final tool result is the same shape as without a token.
func TestMCPIndexProgressNotifications(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	// Build a client whose ProgressNotificationHandler records every
	// notification that arrives. Synchronized via a channel so the
	// tool call (which blocks until the reindex completes) sees every
	// notification that landed before the final result returned.
	progressCh := make(chan *sdkmcp.ProgressNotificationParams, 64)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, &sdkmcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *sdkmcp.ProgressNotificationClientRequest) {
			// non-blocking send: skip the notification if the
			// channel is full rather than stall the handler
			// (which the SDK invokes under a session lock).
			select {
			case progressCh <- req.Params:
			default:
			}
		},
	})
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Build a Meta map with a progress token — the SDK reads it
	// from CallToolParams.Meta on the wire.
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_index",
		Meta: sdkmcp.Meta{"progressToken": "p2-02-test"},
		Arguments: map[string]any{
			"path":     proj,
			"no_embed": true, // structure-only — no Ollama round-trips in the test env
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("index tool error: %s", textOf(res))
	}
	if txt := textOf(res); !strings.Contains(txt, `"nodes":`) {
		t.Errorf("index result should carry a node count: %s", txt)
	}
	close(progressCh)
	var got *sdkmcp.ProgressNotificationParams
	for n := range progressCh {
		if got == nil {
			got = n
		}
	}
	if got == nil {
		t.Fatal("P2-02 (O42): codemap_index produced no progress notifications when the client supplied a progress token — a multi-minute reindex will look hung")
	}
	if got.ProgressToken != "p2-02-test" {
		t.Errorf("progress notification token = %v, want p2-02-test", got.ProgressToken)
	}
	if got.Message == "" {
		t.Errorf("progress notification should carry a human-readable message")
	}
}

type coordinatingEmbedder struct {
	started chan struct{}
	release <-chan struct{}
}

func (e coordinatingEmbedder) Profile() embed.EmbeddingProfile {
	return embed.EmbeddingProfile{
		Provider: "coordinator-test", Model: "deterministic", Dimensions: 4, Distance: "cosine",
	}
}

func (e coordinatingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.started != nil {
		select {
		case e.started <- struct{}{}:
		default:
		}
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}

func TestMCPIndexCoordinatesSessionOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(coordinatingEmbedder{started: started, release: release})

	srv := NewServer(sess)
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	indexDone := make(chan *sdkmcp.CallToolResult, 1)
	indexErr := make(chan error, 1)
	go func() {
		res, callErr := cs.CallTool(ctx, &sdkmcp.CallToolParams{
			Name: "codemap_index", Arguments: map[string]any{"path": proj},
		})
		if callErr != nil {
			indexErr <- callErr
			return
		}
		indexDone <- res
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("index never reached the embedding phase")
	}

	// A second owner must still be rejected while the serving process actively
	// owns the writer. This is intentionally not bypassed merely because both
	// sessions happen to share the test process PID.
	external, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	external.SetEmbedder(coordinatingEmbedder{})
	if _, err := external.Vectors(); err == nil {
		_ = external.ReleaseVectors()
		t.Fatal("independent writer acquired the database during active MCP index")
	} else if !strings.Contains(err.Error(), "locked") {
		t.Fatalf("independent writer error = %v, want lock contention", err)
	}

	// An ordinary request arriving concurrently must wait rather than touch
	// Session's graph/vector resources while index owns them.
	findDone := make(chan *sdkmcp.CallToolResult, 1)
	findErr := make(chan error, 1)
	go func() {
		res, callErr := cs.CallTool(ctx, &sdkmcp.CallToolParams{
			Name: "codemap_find", Arguments: map[string]any{"path": proj, "query": "Run"},
		})
		if callErr != nil {
			findErr <- callErr
			return
		}
		findDone <- res
	}()
	select {
	case <-findDone:
		t.Fatal("concurrent read completed while index still owned the session")
	case err := <-findErr:
		t.Fatalf("concurrent read failed instead of waiting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-indexErr:
		t.Fatal(err)
	case res := <-indexDone:
		if res.IsError {
			t.Fatalf("index tool error: %s", textOf(res))
		}
	case <-ctx.Done():
		t.Fatal("index did not finish after releasing embedder")
	}
	select {
	case err := <-findErr:
		t.Fatal(err)
	case res := <-findDone:
		if res.IsError {
			t.Fatalf("queued read error: %s", textOf(res))
		}
	case <-ctx.Done():
		t.Fatal("queued read did not resume after index")
	}

	// The writer lock is operation-scoped: an independent owner can acquire it
	// immediately while the MCP server and its Session remain alive.
	if _, err := external.Vectors(); err != nil {
		t.Fatalf("independent writer blocked after MCP index completed: %v", err)
	}
	if err := external.ReleaseVectors(); err != nil {
		t.Fatal(err)
	}

	// Open the server's shared read handle, then index again through that same
	// server. This is the original self-lock sequence.
	semantic, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_semantic", Arguments: map[string]any{"path": proj, "query": "run helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if semantic.IsError {
		t.Fatalf("semantic read error: %s", textOf(semantic))
	}
	second, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_index", Arguments: map[string]any{"path": proj, "reindex": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.IsError {
		t.Fatalf("same-server reindex after read error: %s", textOf(second))
	}
	after, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_find", Arguments: map[string]any{"path": proj, "query": "Helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.IsError {
		t.Fatalf("read after same-server reindex error: %s", textOf(after))
	}
}
