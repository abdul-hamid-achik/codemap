package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// taskContextToolFixture isolates codemap dirs, indexes a small Go project, and
// returns a connected client session plus the project path.
func taskContextToolFixture(t *testing.T) (*mcp.ClientSession, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/taskctx-mcp\n\ngo 1.25\n",
		"main.go": "package sample\n\nfunc Hub() {}\n\nfunc Entry() { Hub() }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep.Enabled = false
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(sess)
	clientT, serverT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.serve(ctx, serverT) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, proj
}

func callTaskContext(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "codemap_task_context", Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestTaskContextToolChangeMode(t *testing.T) {
	cs, proj := taskContextToolFixture(t)
	res := callTaskContext(t, cs, map[string]any{
		"path": proj, "task": "understand Hub", "mode": "change",
		"selectors": []map[string]any{{"file": "main.go", "start_line": 3}},
	})
	if res.IsError {
		t.Fatalf("unexpected error result: %s", textOf(res))
	}
	var rep struct {
		SchemaVersion int    `json:"schema_version"`
		Mode          string `json:"mode"`
		Indexed       bool   `json:"indexed"`
		Freshness     struct {
			Checked bool `json:"checked"`
			Stale   bool `json:"stale"`
		} `json:"freshness"`
		Targets []struct {
			Found  bool   `json:"found"`
			Source string `json:"source"`
		} `json:"targets"`
		Contexts *struct {
			Results []struct {
				Found        bool `json:"found"`
				CallersTotal int  `json:"callers_total"`
			} `json:"results"`
		} `json:"contexts"`
		Impacts []struct {
			DirectCallersTotal int `json:"direct_callers_total"`
		} `json:"impacts"`
	}
	if err := json.Unmarshal([]byte(textOf(res)), &rep); err != nil {
		t.Fatalf("parse payload: %v\n%s", err, textOf(res))
	}
	if rep.SchemaVersion != 1 || rep.Mode != "change" || !rep.Indexed || !rep.Freshness.Checked {
		t.Fatalf("identity/freshness = %+v", rep)
	}
	if len(rep.Targets) != 1 || !rep.Targets[0].Found || rep.Targets[0].Source != "selector" {
		t.Fatalf("targets = %+v", rep.Targets)
	}
	if rep.Contexts == nil || len(rep.Contexts.Results) != 1 || !rep.Contexts.Results[0].Found {
		t.Fatalf("contexts = %+v", rep.Contexts)
	}
	if len(rep.Impacts) == 0 || rep.Impacts[0].DirectCallersTotal < 1 {
		t.Fatalf("impacts = %+v", rep.Impacts)
	}
}

func TestTaskContextToolValidation(t *testing.T) {
	cs, proj := taskContextToolFixture(t)
	cases := []struct {
		name string
		args map[string]any
		want string // substring of the visible text
	}{
		{"blank task", map[string]any{"path": proj, "task": "  "}, "task"},
		{"review mode", map[string]any{"path": proj, "task": "x", "mode": "review"}, "codemap_review"},
		{"selectors with understand", map[string]any{
			"path": proj, "task": "x",
			"selectors": []map[string]any{{"file": "main.go", "start_line": 3}},
		}, "change or debug"},
	}
	for _, tc := range cases {
		res := callTaskContext(t, cs, tc.args)
		if !res.IsError {
			t.Fatalf("%s: expected IsError", tc.name)
		}
		// The client SDK decodes Meta["error"] to a generic map.
		errObj, _ := res.Meta["error"].(map[string]any)
		if errObj == nil {
			t.Fatalf("%s: result carries no structured error meta: %v", tc.name, res.Meta)
		}
		if errObj["code"] != "invalid_input" {
			t.Fatalf("%s: meta code = %v", tc.name, errObj["code"])
		}
		if !strings.Contains(textOf(res), tc.want) {
			t.Fatalf("%s: text %q missing %q", tc.name, textOf(res), tc.want)
		}
	}
}

func TestTaskContextToolNotIndexed(t *testing.T) {
	cs, _ := taskContextToolFixture(t)
	cold := t.TempDir()
	res := callTaskContext(t, cs, map[string]any{"path": cold, "task": "anything"})
	if res.IsError {
		t.Fatalf("not-indexed must stay a success-shaped result: %s", textOf(res))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["indexed"] != false {
		t.Fatalf("payload = %s", textOf(res))
	}
}

// TestTaskContextProfileGating pins the registration decision: the composite
// is a full-profile expert surface; the taught agent/core profiles must not
// grow, or the profile invariants (taught-workflow tests, Cursor tool ceiling)
// silently drift.
func TestTaskContextProfileGating(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	for _, profile := range []string{ProfileCore, ProfileAgent} {
		sess, err := app.Open("")
		if err != nil {
			t.Fatal(err)
		}
		sess.Config.MCP.Profile = profile
		srv := NewServer(sess)
		if srv.include("codemap_task_context") {
			t.Fatalf("%s profile must not register codemap_task_context", profile)
		}
		_ = sess.Close()
	}
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	srv := NewServer(sess)
	if !srv.include("codemap_task_context") {
		t.Fatal("full profile must register codemap_task_context")
	}
}
