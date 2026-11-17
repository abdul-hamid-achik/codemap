package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDeepSeekAPI scripts a sequence of chat-completions responses so the
// driver's agent loop runs hermetically. Each queued reply is one model turn.
type fakeDeepSeekAPI struct {
	turns   []string // raw JSON bodies, one per POST
	calls   int
	fail500 int // fail the first N POSTs with 500 to exercise retry
	t       *testing.T
}

func toolTurn(id, name, args string) string {
	quoted, _ := json.Marshal(args)
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_cache_hit_tokens":40}}`, id, name, string(quoted))
}

func textTurn(text string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":200,"completion_tokens":30,"prompt_cache_hit_tokens":80}}`, text)
}

func (f *fakeDeepSeekAPI) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	if f.fail500 > 0 {
		f.fail500--
		w.WriteHeader(500)
		return
	}
	if f.calls >= len(f.turns) {
		f.t.Fatalf("unexpected extra chat call %d (scripted %d)", f.calls, len(f.turns))
	}
	body := f.turns[f.calls]
	f.calls++
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// fakeMCPServerMain is the re-exec entry: a minimal newline-delimited
// JSON-RPC MCP server answering initialize/tools-list/tools-call.
func fakeMCPServerMain() {
	type req struct {
		ID     *int           `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var m req
		if err := dec.Decode(&m); err != nil {
			return
		}
		if m.ID == nil {
			continue
		}
		var result any
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "context", "description": "one-call symbol context",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"symbol": map[string]any{"type": "string"}}, "required": []string{"symbol"}},
			}}}
		case "tools/call":
			name, _ := m.Params["name"].(string)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "context of " + name + ": definition+callers"}}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": result})
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("FAKE_MCP_SERVER") == "1" {
		fakeMCPServerMain()
		return
	}
	os.Exit(m.Run())
}

// TestDeepSeekDriverLoopAndMetrics pins the whole driver path without any
// network: scripted API turns (incl. one 500 to exercise retry), native file
// tools executed against a temp repo, one MCP tool call through the re-exec
// fake server, and a transcript that FoldEvents parses into Metrics — residual
// context bytes and MCP attribution included.
func TestDeepSeekDriverLoopAndMetrics(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "plumbing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "plumbing", "hash.go"),
		[]byte("package plumbing\n\nfunc NewHash() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mcpConfig := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(mcpConfig, []byte(fmt.Sprintf(`{"mcpServers":{"codemap":{"command":%q,"args":[],"env":{"FAKE_MCP_SERVER":"1"}}}}`, os.Args[0])), 0o644); err != nil {
		t.Fatal(err)
	}

	api := &fakeDeepSeekAPI{
		t:       t,
		fail500: 1, // first POST 500s; the driver must retry and proceed
		turns: []string{
			toolTurn("t1", "Grep", `{"pattern":"NewHash"}`),
			toolTurn("t2", "Read", `{"file_path":"plumbing/hash.go"}`),
			toolTurn("t3", "mcp__codemap__context", `{"symbol":"NewHash"}`),
			textTurn("done:\n```json\n{\"file\": \"plumbing/hash.go\", \"line\": 3}\n```"),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(api.handler))
	defer srv.Close()

	d := DeepSeekDriver{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	}
	tr := filepath.Join(t.TempDir(), "transcript.jsonl")
	arm := Arm{
		Name:         "codemap",
		AllowedTools: "Read,Grep,Glob,mcp__codemap",
		MCPConfig:    mcpConfig,
		MCPServer:    "codemap",
		WorkDir:      repo,
		Model:        "deepseek-flash",
	}
	m, err := d.Run(context.Background(), "find NewHash", arm, tr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if m.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", m.ToolCalls)
	}
	if m.MCPToolCalls != 1 {
		t.Errorf("MCPToolCalls = %d, want 1", m.MCPToolCalls)
	}
	if !m.OK {
		t.Error("OK = false, want true")
	}
	if !strings.Contains(m.FinalAnswer, "plumbing/hash.go") {
		t.Errorf("FinalAnswer = %q", m.FinalAnswer)
	}
	// Tokens: turns are (100,40h)+(100,40h)+(100,40h)+(200,80h) prompt
	// (fresh = total-hit): 60+60+60+120 = 300 fresh; cache 40+40+40+80 = 200.
	if m.InputTokens != 300 || m.CacheReadTokens != 200 || m.OutputTokens != 60 {
		t.Errorf("tokens = in %d cache %d out %d, want 300/200/60", m.InputTokens, m.CacheReadTokens, m.OutputTokens)
	}
	if m.CostUSD <= 0 {
		t.Errorf("CostUSD = %v, want > 0", m.CostUSD)
	}
	// Residual context: the Grep hit line, the Read body, and the MCP answer
	// are all present as tool_result text and counted; only the MCP one is
	// mcp-attributed.
	if m.ToolResultBytes <= 0 {
		t.Errorf("ToolResultBytes = %d, want > 0", m.ToolResultBytes)
	}
	if m.MCPResultBytes <= 0 || m.MCPResultBytes >= m.ToolResultBytes {
		t.Errorf("MCPResultBytes = %d of %d, want 0 < mcp < total", m.MCPResultBytes, m.ToolResultBytes)
	}
	// The native tools really executed against the temp repo.
	raw, err := os.ReadFile(tr)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "plumbing/hash.go:3: func NewHash() {}") {
		t.Errorf("transcript missing the real Grep result:\n%s", firstLines(s, 12))
	}
	if !strings.Contains(s, "context of context: definition+callers") {
		t.Errorf("transcript missing the MCP tool result:\n%s", firstLines(s, 12))
	}
}

// TestDeepSeekDriverBaselineArmHasNoMCP asserts an arm without MCPConfig never
// spawns a server and completes with native tools only.
func TestDeepSeekDriverBaselineArmHasNoMCP(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	api := &fakeDeepSeekAPI{
		t: t,
		turns: []string{
			toolTurn("t1", "Glob", `{"pattern":"*.go"}`),
			textTurn("```json\n{\"file\": \"a.go\"}\n```"),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(api.handler))
	defer srv.Close()

	d := DeepSeekDriver{BaseURL: srv.URL, APIKey: "k"}
	m, err := d.Run(context.Background(), "q", Arm{
		Name: "baseline", AllowedTools: "Read,Grep,Glob", WorkDir: repo, Model: "deepseek-flash",
	}, filepath.Join(t.TempDir(), "t.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if m.MCPToolCalls != 0 || m.MCPResultBytes != 0 {
		t.Errorf("baseline arm saw MCP: %+v", m)
	}
	if m.ToolCalls != 1 || !m.OK {
		t.Errorf("metrics = %+v", m)
	}
}

// TestNativeToolJail pins the path jail: no absolute paths, no traversal.
func TestNativeToolJail(t *testing.T) {
	dir := t.TempDir()
	if _, err := nativeRead(dir, map[string]any{"file_path": "/etc/passwd"}); err == nil {
		t.Error("absolute path accepted")
	}
	if _, err := nativeRead(dir, map[string]any{"file_path": "../../etc/passwd"}); err == nil {
		t.Error("traversal accepted")
	}
	if _, err := nativeRead(dir, map[string]any{"file_path": "a.go"}); err == nil {
		t.Error("missing file should error, not return empty ok")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
