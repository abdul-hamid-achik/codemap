package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callAffected(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "codemap_affected", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func errCode(res *mcp.CallToolResult) any {
	errObj, _ := res.Meta["error"].(map[string]any)
	if errObj == nil {
		return nil
	}
	return errObj["code"]
}

// The MCP tool must reject what the CLI rejects (depth outside 0..10 and
// since+staged) instead of silently clamping or letting one flag win.
func TestAffectedToolRejectsInvalidInput(t *testing.T) {
	cs, proj := taskContextToolFixture(t)
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"depth too large", map[string]any{"path": proj, "files": []string{"main.go"}, "depth": 11}, "depth"},
		{"negative depth", map[string]any{"path": proj, "files": []string{"main.go"}, "depth": -1}, "depth"},
		{"since and staged", map[string]any{"path": proj, "since": "HEAD", "staged": true}, "mutually exclusive"},
		{"option-like since", map[string]any{"path": proj, "since": "--output=x"}, "since"},
	} {
		res := callAffected(t, cs, tc.args)
		if !res.IsError {
			t.Errorf("%s: expected IsError, got %s", tc.name, textOf(res))
			continue
		}
		if code := errCode(res); code != "invalid_input" {
			t.Errorf("%s: meta code = %v, want invalid_input", tc.name, code)
		}
		if !strings.Contains(textOf(res), tc.want) {
			t.Errorf("%s: text %q missing %q", tc.name, textOf(res), tc.want)
		}
	}

	// In-range input still works, including the max depth.
	res := callAffected(t, cs, map[string]any{"path": proj, "files": []string{"main.go"}, "depth": 10})
	if res.IsError || !strings.Contains(textOf(res), `"schema_version":1`) {
		t.Fatalf("depth 10 must be accepted: %s", textOf(res))
	}
}

// Outside a git repository a git-sourced run reports the not_a_repo code.
func TestAffectedToolNotARepo(t *testing.T) {
	cs, proj := taskContextToolFixture(t) // indexed, but not a git repository
	res := callAffected(t, cs, map[string]any{"path": proj})
	if !res.IsError || errCode(res) != "not_a_repo" {
		t.Fatalf("working-tree run outside git: IsError=%v code=%v text=%s", res.IsError, errCode(res), textOf(res))
	}
}
