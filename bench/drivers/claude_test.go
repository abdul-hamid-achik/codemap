package drivers

import (
	"context"
	"os"
	"strings"
	"testing"
)

// These tests lock the stream-json parser against Claude Code output-format
// drift using recorded transcript fixtures — no API key, no network.

func TestFoldEvents_Success(t *testing.T) {
	f, err := os.Open("testdata/transcript_success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	m, init, err := FoldEvents(f)
	if err != nil {
		t.Fatalf("FoldEvents: %v", err)
	}
	// Three tool_use blocks across two assistant turns (2 parallel + 1). This is
	// the headline metric; num_turns (3) would undercount the parallel turn.
	if m.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", m.ToolCalls)
	}
	if m.InputTokens != 15230 || m.OutputTokens != 842 || m.CacheReadTokens != 4096 {
		t.Errorf("token totals wrong: in=%d out=%d cache=%d", m.InputTokens, m.OutputTokens, m.CacheReadTokens)
	}
	if m.CostUSD != 0.0731 {
		t.Errorf("CostUSD = %v, want 0.0731", m.CostUSD)
	}
	if !m.OK {
		t.Error("OK should be true for subtype=success")
	}
	if !strings.Contains(m.FinalAnswer, "\"callers\"") {
		t.Errorf("FinalAnswer missing answer json: %q", m.FinalAnswer)
	}
	// duration_ms is used only as a fallback when the external wall-clock is 0.
	if m.WallClockMs != 12345 {
		t.Errorf("WallClockMs fallback = %d, want 12345", m.WallClockMs)
	}
	if !init.hasServer("codemap") {
		t.Error("init should report codemap MCP server loaded")
	}
	if init.Model != "claude-sonnet-5" {
		t.Errorf("init.Model = %q", init.Model)
	}
}

func TestFoldEvents_NoResultIsError(t *testing.T) {
	f, err := os.Open("testdata/transcript_no_result.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	m, init, err := FoldEvents(f)
	if err == nil {
		t.Fatal("expected error when the stream has no result event")
	}
	// The stray non-JSON line must be tolerated, and the one tool_use counted.
	if m.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (stray line tolerated)", m.ToolCalls)
	}
	if init.hasServer("codemap") {
		t.Error("codemap should NOT be reported loaded (empty mcp_servers)")
	}
}

func TestInitInfo_HasServer_StatusHandling(t *testing.T) {
	i := InitInfo{MCPServers: []mcpServer{{Name: "codemap", Status: "failed"}}}
	if i.hasServer("codemap") {
		t.Error("a failed server must not count as loaded")
	}
	i = InitInfo{MCPServers: []mcpServer{{Name: "codemap", Status: "connected"}}}
	if !i.hasServer("codemap") {
		t.Error("a connected server must count as loaded")
	}
}

func TestClaudeArgs_RequiredFlags(t *testing.T) {
	d := ClaudeDriver{}
	arm := Arm{Name: "codemap", AllowedTools: "Read,Grep,Glob,mcp__codemap", MCPConfig: "/x/mcp.json", Model: "claude-sonnet-5"}
	args := d.Args("do the thing", arm)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--bare", "-p do the thing", "--output-format stream-json", "--verbose", "--model claude-sonnet-5", "--allowedTools Read,Grep,Glob,mcp__codemap", "--mcp-config /x/mcp.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	// baseline arm: no --mcp-config
	base := d.Args("q", Arm{Name: "baseline", AllowedTools: "Read,Grep,Glob", Model: "m"})
	if strings.Contains(strings.Join(base, " "), "--mcp-config") {
		t.Error("baseline must not pass --mcp-config")
	}
}

func TestStubDriversNotImplemented(t *testing.T) {
	for _, d := range []Driver{CodexDriver{}, GeminiDriver{}} {
		if _, err := d.Run(context.Background(), "q", Arm{}, ""); err != ErrNotImplemented {
			t.Errorf("%s.Run should return ErrNotImplemented, got %v", d.Name(), err)
		}
	}
}
