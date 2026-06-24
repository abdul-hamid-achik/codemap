package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
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

func TestInstructionsCoverKeyCapabilities(t *testing.T) {
	// The instructions are an agent's first-contact playbook; keep them in sync
	// with the actual tools and accuracy model.
	for _, want := range []string{"codemap_index", "codemap_impact", "codemap_semantic",
		"codemap_source", "codemap_projects", "precise:true", "name-based",
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
	for _, tool := range lt.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"codemap_init", "codemap_index", "codemap_status", "codemap_semantic", "codemap_callers", "codemap_find", "codemap_source", "codemap_projects", "codemap_docs", "codemap_annotate", "codemap_annotations", "codemap_unannotate", "codemap_doctor"} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
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
	}

	// codemap_callers
	res2, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "codemap_callers",
		Arguments: map[string]any{"path": proj, "symbol": "Helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if txt := textOf(res2); !strings.Contains(txt, "Run") {
		t.Errorf("callers of Helper should include Run: %s", txt)
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
