package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type budgetProbe struct {
	Budget *struct {
		MaxTokens       int            `json:"max_tokens"`
		EstimatedTokens int            `json:"estimated_tokens"`
		Truncated       bool           `json:"truncated"`
		Dropped         map[string]int `json:"dropped"`
	} `json:"budget"`
}

// TestMaxTokensOnReportTools pins max_tokens on the four report-heavy tools: the
// input schema advertises it, a call without it carries no budget object, and a
// call with it returns the additive budget metadata.
func TestMaxTokensOnReportTools(t *testing.T) {
	cs, proj := taskContextToolFixture(t)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codemap_context", "codemap_explore", "codemap_impact", "codemap_task_context"} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name != name {
				continue
			}
			found = true
			raw, _ := json.Marshal(tool.InputSchema)
			var schema struct {
				Properties map[string]struct {
					Type json.RawMessage `json:"type"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(schema.Properties["max_tokens"].Type), "integer") {
				t.Fatalf("%s lacks an integer max_tokens input: %s", name, raw)
			}
		}
		if !found {
			t.Fatalf("%s is not registered", name)
		}
	}

	calls := map[string]map[string]any{
		"codemap_context":      {"path": proj, "symbol": "Hub"},
		"codemap_explore":      {"path": proj, "query": "Hub"},
		"codemap_impact":       {"path": proj, "symbol": "Hub"},
		"codemap_task_context": {"path": proj, "task": "Hub"},
	}
	for name, args := range calls {
		call := func(extra map[string]any) (*mcp.CallToolResult, budgetProbe) {
			in := map[string]any{}
			for k, v := range args {
				in[k] = v
			}
			for k, v := range extra {
				in[k] = v
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: in})
			if err != nil {
				t.Fatal(err)
			}
			var p budgetProbe
			if !res.IsError {
				if err := json.Unmarshal([]byte(textOf(res)), &p); err != nil {
					t.Fatalf("%s: parse: %v", name, err)
				}
			}
			return res, p
		}
		if res, p := call(nil); res.IsError || p.Budget != nil {
			t.Fatalf("%s: unbudgeted call = error:%v budget:%+v", name, res.IsError, p.Budget)
		}
		res, p := call(map[string]any{"max_tokens": 1})
		if res.IsError || p.Budget == nil || p.Budget.MaxTokens != 1 || !p.Budget.Truncated ||
			p.Budget.EstimatedTokens < 1 || len(p.Budget.Dropped) == 0 {
			t.Fatalf("%s: budget = %+v (%s)", name, p.Budget, textOf(res))
		}
		if res, _ := call(map[string]any{"max_tokens": -3}); !res.IsError {
			t.Fatalf("%s: a negative max_tokens must be an invalid_input error", name)
		}
	}
}
