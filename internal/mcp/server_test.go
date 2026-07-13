package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

// result() serializes compact tool payloads without HTML escaping, so agents do
// not spend tokens on indentation and <, >, & stay literal.
func TestResultJSONIsCompactAndNotHTMLEscaped(t *testing.T) {
	res, _, _ := result(map[string]any{
		"target": "A -> B & Array<string>",
		"nested": map[string]bool{"ok": true},
	}, nil)
	txt := textOf(res)
	// With HTML escaping on, this would read "A -<esc> B ..." with the angle
	// brackets/ampersand turned into \u00xx, and the literal check below would fail.
	if !strings.Contains(txt, "A -> B & Array<string>") {
		t.Errorf("result JSON should keep <, >, & literal (no HTML escaping): %s", txt)
	}
	if strings.ContainsAny(txt, "\n\r\t") || strings.Contains(txt, "  ") {
		t.Errorf("result JSON should be compact for token-efficient MCP responses: %q", txt)
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
	// with the taught tools available in every profile and the accuracy model.
	for _, want := range []string{"codemap_index", "codemap_impact", "codemap_semantic",
		"codemap_source", "codemap_references", "codemap_read_order", "codemap_status", "precise:true", "name-based",
		"selector", "start_line", "volatile database ids",
		"confirmed", "candidate", "deletion_analysis",
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
	if err := os.WriteFile(filepath.Join(proj, "handler.go"),
		[]byte("package app\n\nfunc Handler() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "hooks.go"),
		[]byte("package app\n\nvar Hook = struct{ Run func() }{Run: Handler}\n\nfunc register(func()) {}\nfunc Setup() { register(Handler) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A multi-line body big enough that dropping it (brief mode) measurably
	// shrinks the response despite the added source_omitted metadata field —
	// unlike a one-line stub, where the field can outweigh the body. Deliberately
	// calls nothing else in the project so it doesn't perturb the dependency/call
	// counts other subtests assert on.
	if err := os.WriteFile(filepath.Join(proj, "bulky.go"),
		[]byte("package app\n\n// Bulky does a lot of unremarkable work.\nfunc Bulky() {\n"+strings.Repeat("\t_ = 0\n", 40)+"}\n"), 0o644); err != nil {
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
	for _, want := range []string{"codemap_init", "codemap_index", "codemap_status", "codemap_semantic", "codemap_callers", "codemap_references", "codemap_find", "codemap_grep", "codemap_source", "codemap_context", "codemap_context_batch", "codemap_review", "codemap_read_order", "codemap_map", "codemap_explore", "codemap_traverse", "codemap_dependencies", "codemap_file_impact", "codemap_risk", "codemap_coverage", "codemap_projects", "codemap_docs", "codemap_annotate", "codemap_annotations", "codemap_unannotate", "codemap_doctor", "codemap_branch_status", "codemap_branch_switch"} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
		}
	}
	// Exact-definition tools accept a source selector projected directly from
	// the file/start_line/fqn/kind fields already present on symbol results. Once
	// selector exists, symbol must not remain schema-required.
	for _, name := range []string{"codemap_callers", "codemap_callees", "codemap_references", "codemap_impact", "codemap_risk", "codemap_source", "codemap_context"} {
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
	if raw, err := json.Marshal(toolsByName["codemap_references"].InputSchema); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(raw), `"precise"`) {
		t.Fatalf("codemap_references must not expose misleading call precision: %s", raw)
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
	if txt := textOf(res); !strings.Contains(txt, `"registered":true`) || !strings.Contains(txt, `"nodes":`) {
		t.Errorf("unexpected status payload: %s", txt)
	} else if !strings.Contains(txt, `"stale"`) {
		// Agents are told (codemap_docs) to check freshness before trusting results,
		// so the staleness object must reach them over MCP, not just the CLI.
		t.Errorf("status should carry the staleness object for agents: %s", txt)
	}

	// codemap_map is the bounded full-profile architecture overview. Its input
	// schema exposes independent caps, and its payload preserves stable v1 +
	// confidence/freshness fields rather than returning raw graph ids.
	mapTool := toolsByName["codemap_map"]
	mapSchemaJSON, err := json.Marshal(mapTool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"top_subsystems", "top_bridges", "top_hubs", "top_entrypoints"} {
		if !strings.Contains(string(mapSchemaJSON), `"`+field+`"`) {
			t.Errorf("codemap_map schema missing %s: %s", field, mapSchemaJSON)
		}
	}
	mapRes, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_map",
		Arguments: map[string]any{
			"path": proj, "top_subsystems": 1, "top_bridges": 1,
			"top_hubs": 1, "top_entrypoints": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mapText := textOf(mapRes)
	if mapRes.IsError || !strings.Contains(mapText, `"schema_version":1`) ||
		!strings.Contains(mapText, `"strategy":"source_path"`) ||
		!strings.Contains(mapText, `"subsystems"`) || !strings.Contains(mapText, `"bridges"`) ||
		!strings.Contains(mapText, `"hubs"`) || !strings.Contains(mapText, `"entrypoints"`) ||
		strings.Contains(mapText, `"id":`) {
		t.Fatalf("architecture map transport contract is incomplete or leaked ids: %s", mapText)
	}

	// codemap_explore and codemap_traverse are full-profile, bounded composition
	// tools. Explore promotes intent hits to exact source-light contexts;
	// traverse requires one durable selector and never accepts a name union.
	for name, fields := range map[string][]string{
		"codemap_explore":  {"query", "seeds", "edges", "depth"},
		"codemap_traverse": {"selector", "direction", "edge_types", "depth", "limit"},
	} {
		raw, marshalErr := json.Marshal(toolsByName[name].InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, field := range fields {
			if !strings.Contains(string(raw), `"`+field+`"`) {
				t.Errorf("%s schema missing %s: %s", name, field, raw)
			}
		}
		if name == "codemap_traverse" {
			var schema struct {
				Required   []string       `json:"required"`
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(schema.Required, "selector") {
				t.Errorf("codemap_traverse must require selector: %s", raw)
			}
			if _, hasSymbol := schema.Properties["symbol"]; hasSymbol {
				t.Errorf("codemap_traverse must not expose a name-union symbol input: %s", raw)
			}
		}
	}

	exploreRes, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_explore",
		Arguments: map[string]any{
			"path": proj, "query": "Run", "seeds": 1, "edges": 1, "depth": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	exploreText := textOf(exploreRes)
	if exploreRes.IsError || !strings.Contains(exploreText, `"schema_version":1`) ||
		!strings.Contains(exploreText, `"query":"Run"`) || !strings.Contains(exploreText, `"seeds"`) ||
		!strings.Contains(exploreText, `"contexts"`) || !strings.Contains(exploreText, `"selector"`) ||
		!strings.Contains(exploreText, `"source_omitted":true`) || strings.Contains(exploreText, `"id":`) {
		t.Fatalf("explore transport contract is incomplete, source-heavy, or leaked ids: %s", exploreText)
	}

	traverseRes, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_traverse",
		Arguments: map[string]any{
			"path": proj,
			"selector": map[string]any{
				"file": "main.go", "start_line": 3, "fqn": "app.Run", "kind": "function",
			},
			"direction": "outgoing", "edge_types": []string{"calls"}, "depth": 1, "limit": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	traverseText := textOf(traverseRes)
	if traverseRes.IsError || !strings.Contains(traverseText, `"schema_version":1`) ||
		!strings.Contains(traverseText, `"found":true`) || !strings.Contains(traverseText, `"direction":"outgoing"`) ||
		!strings.Contains(traverseText, `"edge_types":["calls"]`) || !strings.Contains(traverseText, `"hops"`) ||
		!strings.Contains(traverseText, `"parent_selector"`) || !strings.Contains(traverseText, `"domains"`) ||
		strings.Contains(traverseText, `"id":`) {
		t.Fatalf("traverse transport contract is incomplete or leaked ids: %s", traverseText)
	}

	badExplore, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_explore", Arguments: map[string]any{"path": proj, "query": "Run", "seeds": app.MaxExploreSeeds + 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badExplore.IsError || !strings.Contains(textOf(badExplore), "explore seeds must be between") {
		t.Fatalf("explore bounds must return a tool error: %s", textOf(badExplore))
	}
	badTraverse, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_traverse",
		Arguments: map[string]any{
			"path": proj, "selector": map[string]any{"file": "main.go", "start_line": 3},
			"limit": app.MaxTraverseLimit + 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badTraverse.IsError || !strings.Contains(textOf(badTraverse), "traverse limit must be between") {
		t.Fatalf("traverse bounds must return a tool error: %s", textOf(badTraverse))
	}

	// codemap_callers — a real symbol with callers reports found:true.
	res2, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_callers",
		Arguments: map[string]any{"path": proj, "symbol": "Helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(res2); !strings.Contains(txt, "Run") || !strings.Contains(txt, `"found":true`) {
		t.Errorf("callers of Helper should include Run and found:true: %s", txt)
	}

	// codemap_grep — exact text search joined onto its enclosing symbol. "Helper()"
	// appears inside both Run's and Other's bodies, so both hits resolve to an
	// enclosing symbol (not "none").
	grepRes, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_grep",
		Arguments: map[string]any{"path": proj, "pattern": "Helper()"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(grepRes); !strings.Contains(txt, `"symbol":"Run"`) || strings.Contains(txt, `"resolution":"none"`) {
		t.Errorf("grep for Helper() should resolve a hit onto Run without any resolution:none: %s", txt)
	}

	// codemap_references is callback/value wiring, not callers. It returns the
	// enclosing file + Setup scopes, bounded totals, and explicit partial
	// coverage. A selector-only call must preserve the same exact target.
	refs, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_references",
		Arguments: map[string]any{"path": proj, "symbol": "Handler"},
	})
	if err != nil {
		t.Fatal(err)
	}
	refsText := textOf(refs)
	if refs.IsError || !strings.Contains(refsText, `"references_total":2`) ||
		!strings.Contains(refsText, `"coverage":"partial"`) || !strings.Contains(refsText, `"kind":"file"`) ||
		!strings.Contains(refsText, `"symbol":"Setup"`) || strings.Contains(refsText, `"symbol":"Run"`) {
		t.Fatalf("references payload mixed calls or lost wiring honesty: %s", refsText)
	}
	exactRefs, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_references",
		Arguments: map[string]any{"path": proj, "selector": map[string]any{
			"file": "handler.go", "start_line": 3, "fqn": "app.Handler", "kind": "function",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exactRefs.IsError || !strings.Contains(textOf(exactRefs), `"selector"`) || !strings.Contains(textOf(exactRefs), `"references_total":2`) {
		t.Fatalf("selector-only references failed: %s", textOf(exactRefs))
	}
	missingRefsInput, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_references", Arguments: map[string]any{"path": proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missingRefsInput.IsError || !strings.Contains(textOf(missingRefsInput), "needs symbol or selector") {
		t.Fatalf("references should validate symbol-or-selector: %s", textOf(missingRefsInput))
	}

	ctxRefs, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "codemap_context", Arguments: map[string]any{"path": proj, "symbol": "Handler"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctxRefs.IsError || !strings.Contains(textOf(ctxRefs), `"references_total":2`) ||
		!strings.Contains(textOf(ctxRefs), `"references_coverage":"partial"`) {
		t.Fatalf("context did not embed reference wiring honesty: %s", textOf(ctxRefs))
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
	if txt := textOf(deps); deps.IsError || !strings.Contains(txt, `"evidence_total":1`) ||
		!strings.Contains(txt, `"file":"other.go"`) || !strings.Contains(txt, `"coverage"`) ||
		!strings.Contains(txt, `"confirmed_total":1`) || !strings.Contains(txt, `"candidate_total":0`) ||
		!strings.Contains(txt, `"confidence":"confirmed"`) || !strings.Contains(txt, `"confidence_reason":"same_package"`) ||
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
	if txt := textOf(res3); !strings.Contains(txt, `"found":false`) {
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

	// I05: brief:true drops the source body (keeping signature) and sets
	// source_omitted:true, on both codemap_source and codemap_context; the
	// resulting payload is smaller than the non-brief call. Uses "Bulky" (a
	// deliberately multi-line body) rather than a one-line stub: on a trivial
	// function, the added source_omitted field can outweigh the tiny body it
	// replaces, so the size assertion needs a body worth dropping.
	fullSource, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_source",
		Arguments: map[string]any{"path": proj, "symbol": "Bulky"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fullSource.IsError {
		t.Fatalf("source(Bulky) failed: %s", textOf(fullSource))
	}
	briefSource, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_source",
		Arguments: map[string]any{"path": proj, "symbol": "Bulky", "brief": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	briefSourceTxt := textOf(briefSource)
	if briefSource.IsError || !strings.Contains(briefSourceTxt, `"source_omitted":true`) ||
		!strings.Contains(briefSourceTxt, `"source":""`) || !strings.Contains(briefSourceTxt, `"signature":"func Bulky`) {
		t.Fatalf("brief source should omit the body but keep the signature: %s", briefSourceTxt)
	}
	if len(briefSourceTxt) >= len(textOf(fullSource)) {
		t.Errorf("brief source (%d bytes) should be smaller than full source (%d bytes)", len(briefSourceTxt), len(textOf(fullSource)))
	}

	fullContext, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_context",
		Arguments: map[string]any{"path": proj, "symbol": "Bulky"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fullContext.IsError {
		t.Fatalf("context(Bulky) failed: %s", textOf(fullContext))
	}
	briefContext, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_context",
		Arguments: map[string]any{"path": proj, "symbol": "Bulky", "brief": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	briefContextTxt := textOf(briefContext)
	if briefContext.IsError || !strings.Contains(briefContextTxt, `"source_omitted":true`) {
		t.Fatalf("brief context should set source_omitted:true: %s", briefContextTxt)
	}
	if len(briefContextTxt) >= len(textOf(fullContext)) {
		t.Errorf("brief context (%d bytes) should be smaller than full context (%d bytes)", len(briefContextTxt), len(textOf(fullContext)))
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
		{"codemap_grep", map[string]any{"path": proj, "pattern": "Run"}},
		{"codemap_semantic", map[string]any{"path": proj, "query": "run"}},
		{"codemap_hotspots", map[string]any{"path": proj}},
		{"codemap_map", map[string]any{"path": proj}},
		{"codemap_explore", map[string]any{"path": proj, "query": "run"}},
		{"codemap_traverse", map[string]any{"path": proj, "selector": map[string]any{"file": "main.go", "start_line": 3}}},
	} {
		res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		txt := textOf(res)
		if !strings.Contains(txt, `"indexed":false`) || !strings.Contains(txt, "codemap_index") {
			t.Errorf("%s on an unindexed project should signal not-indexed, got: %s", tc.tool, txt)
		}
	}
}

// TestHandleGrepThreadsRequestContext pins the fix for handleGrep discarding
// its request context.Context and calling the non-cancellable Service.Grep
// wrapper (context.Background() under the hood), so an MCP client
// cancellation/disconnect could not stop an in-flight grep — the scan ran to
// completion holding the server's operation lock regardless of the caller
// abandoning the call. GrepWithContext checks ctx.Err() once per indexed
// file (internal/app/service_grep.go), so a context canceled BEFORE the call
// must surface as a cancellation error on the very first file instead of a
// normal (possibly empty) grep report.
func TestHandleGrepThreadsRequestContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { _ = \"grep-ctx-marker-xyz\" }\n"), 0o644); err != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the handler must observe THIS ctx, not context.Background()

	res, _, err := srv.handleGrep(ctx, nil, grepInput{Path: proj, Pattern: "grep-ctx-marker-xyz"})
	if err != nil {
		t.Fatalf("handleGrep returned a Go error instead of an error CallToolResult: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a pre-canceled context must surface as an error result, got: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), context.Canceled.Error()) {
		t.Errorf("error result = %q, want it to mention %q (proves the request ctx reached GrepWithContext)", textOf(res), context.Canceled.Error())
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
	if txt := textOf(real); !strings.Contains(txt, `"matched":true`) {
		t.Errorf("annotating an indexed symbol should report matched true: %s", txt)
	}
	// Ghost symbol → matched false + an explanatory note so the agent doesn't
	// think it pinned knowledge that can never surface.
	ghost, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codemap_annotate",
		Arguments: map[string]any{"path": proj, "symbol": "GhostXYZ", "source": "s", "note": "n"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(ghost); !strings.Contains(txt, `"matched":false`) || !strings.Contains(txt, "won't surface") {
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
	if txt := textOf(rm); !strings.Contains(txt, `"removed":true`) {
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
	if txt := textOf(again); !strings.Contains(txt, `"removed":false`) {
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
	// Notifications and responses travel through independent SDK handlers, so a
	// final progress notification may be dispatched just after CallTool returns.
	// Never close the handler's channel here: that races a legitimate late send
	// and used to panic under coverage scheduling. Wait for the first bounded
	// notification instead; the buffered channel remains valid until the client
	// session is torn down by the deferred Close.
	var got *sdkmcp.ProgressNotificationParams
	select {
	case got = <-progressCh:
	case <-time.After(2 * time.Second):
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

// ---- I01: MCP tool profiles ----

// newProfileTestSession opens an isolated session (own XDG/data dirs) with
// no project indexed — tools/list doesn't need one.
func newProfileTestSession(t *testing.T) *app.Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// listToolNames drives a real tools/list round-trip over an in-memory
// transport and returns the registered tool names.
func listToolNames(t *testing.T, srv *Server) map[string]bool {
	t.Helper()
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.serve(ctx, serverT) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range lt.Tools {
		got[tool.Name] = true
	}
	return got
}

// fullToolNames is the exhaustive, hand-maintained list of every tool
// codemap ships under ProfileFull (42; AGENTS.md's "Current set (42)" line
// must be updated alongside this list if it ever changes).
var fullToolNames = []string{
	"codemap_init", "codemap_index", "codemap_status", "codemap_semantic",
	"codemap_callers", "codemap_callees", "codemap_references", "codemap_impact",
	"codemap_review", "codemap_read_order", "codemap_map", "codemap_explore", "codemap_traverse", "codemap_related_files", "codemap_dependencies",
	"codemap_file_impact", "codemap_risk", "codemap_symbol_at", "codemap_secret_impact",
	"codemap_required_keys", "codemap_hotspots", "codemap_orphans", "codemap_coverage",
	"codemap_path", "codemap_symbols", "codemap_find", "codemap_grep", "codemap_source",
	"codemap_context", "codemap_context_batch", "codemap_projects", "codemap_docs",
	"codemap_annotate", "codemap_annotations", "codemap_unannotate", "codemap_doctor",
	"codemap_branch_status", "codemap_branch_switch", "codemap_cache_save",
	"codemap_cache_restore", "codemap_cache_list", "codemap_cache_drop",
}

func assertExactToolSet(t *testing.T, got map[string]bool, want []string) {
	t.Helper()
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	var missing, extra []string
	for w := range wantSet {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	for g := range got {
		if !wantSet[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("missing tools: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("unexpected tools: %v", extra)
	}
}

// TestMCPToolsByProfile pins the exact registered-tool set for all profiles:
// ProfileFull remains all 42 tools, ProfileCore remains its shipped 22-tool
// inventory, and ProfileAgent is the separately versioned taught workflow.
func TestMCPToolsByProfile(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		sess := newProfileTestSession(t)
		sess.Config.MCP.Profile = ProfileFull
		got := listToolNames(t, NewServer(sess))
		assertExactToolSet(t, got, fullToolNames)
	})
	t.Run("core", func(t *testing.T) {
		sess := newProfileTestSession(t)
		sess.Config.MCP.Profile = ProfileCore
		want := make([]string, 0, len(coreTools))
		for name := range coreTools {
			want = append(want, name)
		}
		got := listToolNames(t, NewServer(sess))
		assertExactToolSet(t, got, want)
	})
	t.Run("agent", func(t *testing.T) {
		sess := newProfileTestSession(t)
		sess.Config.MCP.Profile = ProfileAgent
		want := make([]string, 0, len(agentTools))
		for name := range agentTools {
			want = append(want, name)
		}
		got := listToolNames(t, NewServer(sess))
		assertExactToolSet(t, got, want)
	})
	// The default (zero-value Config, as a hand-built test Config that never
	// ran through config.Validate would produce) must behave as ProfileFull —
	// the back-compat guarantee — not silently register zero tools.
	t.Run("empty profile defaults to full", func(t *testing.T) {
		sess := newProfileTestSession(t)
		sess.Config.MCP.Profile = ""
		got := listToolNames(t, NewServer(sess))
		assertExactToolSet(t, got, fullToolNames)
	})
}

func taughtToolSet(t *testing.T) map[string]bool {
	t.Helper()
	toolRe := regexp.MustCompile(`codemap_([a-z][a-z_]*)`)
	taught := map[string]bool{"codemap_docs": true}
	for _, src := range []string{
		app.RenderPlaybook(app.FormatClaudeSkill), // preamble + workflow + accuracy
		app.Docs("workflow"),
	} {
		for _, m := range toolRe.FindAllStringSubmatch(src, -1) {
			taught["codemap_"+m[1]] = true
		}
	}
	if len(taught) == 1 {
		t.Fatal("no codemap_<tool> tokens found in the playbook/docs — regex or source drifted")
	}
	return taught
}

// TestCoreProfileCoversTaughtTools is I01's hypothesis-2 invariant: every
// codemap_<tool> token the canonical playbook (RenderPlaybook, which embeds
// docs.go's workflow+accuracy topics verbatim — see playbook.go) and the
// docs.go workflow topic actually teach an agent to call MUST be in
// coreTools. If a future edit to docs.go/playbook.go starts teaching a new
// tool without adding it to coreTools here, ProfileCore would silently break
// the very loop it's supposed to preserve — this test fails first instead.
func TestCoreProfileCoversTaughtTools(t *testing.T) {
	taught := taughtToolSet(t)
	var missing []string
	for name := range taught {
		if !coreTools[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("tools taught by the playbook/docs but missing from coreTools (ProfileCore would silently break the taught workflow): %v", missing)
	}
}

// TestAgentProfileExactlyMatchesTaughtWorkflow makes ProfileAgent a measured
// contract rather than a hand-wavy "small" profile. It must include every
// tool named by the canonical playbook, retain codemap_docs for discovery,
// and include no untaught admin or expert surface.
func TestAgentProfileExactlyMatchesTaughtWorkflow(t *testing.T) {
	taught := taughtToolSet(t)
	if len(taught) != 22 {
		t.Fatalf("taught workflow tool count = %d, want 22; review the agent profile and its schema benchmark", len(taught))
	}
	got := map[string]bool{}
	for name := range agentTools {
		got[name] = true
	}
	want := make([]string, 0, len(taught))
	for name := range taught {
		want = append(want, name)
	}
	assertExactToolSet(t, got, want)

	for _, excluded := range []string{"codemap_init", "codemap_annotate", "codemap_map", "codemap_explore", "codemap_traverse"} {
		if agentTools[excluded] {
			t.Errorf("agent profile unexpectedly includes untaught tool %s", excluded)
		}
	}
	if !strings.Contains(instructionsFor(ProfileAgent), "profile: agent") {
		t.Error("agent instructions do not identify the selected profile")
	}
	toolRe := regexp.MustCompile(`codemap_[a-z][a-z_]*`)
	for _, name := range toolRe.FindAllString(instructionsFor(ProfileAgent), -1) {
		if !agentTools[name] {
			t.Errorf("agent instructions teach unregistered tool %s", name)
		}
	}
}
