package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"codemap_init", "codemap_index", "codemap_status", "codemap_semantic", "codemap_callers"} {
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
