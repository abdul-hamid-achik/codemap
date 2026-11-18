package drivers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DeepSeekDriver runs one agent session against the DeepSeek (OpenAI-compatible)
// chat-completions API with function calling. There is no CLI agent in the
// middle: the driver itself executes the file tools (Read/Grep/Glob, bounded,
// jailed to the fixture workdir) and, for arms with an MCP config, speaks the
// codemap MCP stdio protocol directly (newline-delimited JSON-RPC). The
// transcript is written in the claude stream-json event shape so FoldEvents —
// and its residual-context accounting — parses it unchanged.
//
// The A/B fairness property is the same one the claude driver pins with
// --bare/--strict-mcp-config: both arms see exactly the tools in
// arm.AllowedTools and nothing ambient (no user config, no extra servers).
type DeepSeekDriver struct {
	// BaseURL defaults to https://api.deepseek.com (DEEPSEEK_BASE_URL).
	BaseURL string
	// APIKey defaults to $DEEPSEEK_API_KEY.
	APIKey string
	// Prices per 1M tokens for the cost column (DIRECTIONAL): defaults are
	// DeepSeek's listed ballpark and can be overridden with
	// DEEPSEEK_PRICE_IN / _CACHE_READ / _OUT. Cost is reported, never
	// compared across drivers.
	PriceIn, PriceCacheRead, PriceOut float64
	// MaxTurns bounds the tool loop (a runaway session must not bill forever).
	MaxTurns int

	client *http.Client
}

func (d DeepSeekDriver) Name() string { return "deepseek" }

func (d DeepSeekDriver) baseURL() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	if v := os.Getenv("DEEPSEEK_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.deepseek.com"
}

func (d DeepSeekDriver) apiKey() string {
	if d.APIKey != "" {
		return d.APIKey
	}
	return os.Getenv("DEEPSEEK_API_KEY")
}

func (d DeepSeekDriver) price(in, cache, out float64) (float64, float64, float64) {
	get := func(env string, def, v float64) float64 {
		if v > 0 {
			return v
		}
		if s := os.Getenv(env); s != "" {
			if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0 {
				return f
			}
		}
		return def
	}
	return get("DEEPSEEK_PRICE_IN", 0.28, d.PriceIn),
		get("DEEPSEEK_PRICE_CACHE_READ", 0.028, d.PriceCacheRead),
		get("DEEPSEEK_PRICE_OUT", 0.42, d.PriceOut)
}

func (d DeepSeekDriver) maxTurns() int {
	if d.MaxTurns > 0 {
		return d.MaxTurns
	}
	return 60
}

// DeepSeekAuthMode reports how deepseek sessions authenticate: "api-key" when
// DEEPSEEK_API_KEY is set, "" (unknown/not applicable) otherwise.
func DeepSeekAuthMode() string {
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		return "api-key"
	}
	return ""
}

// --- chat-completions wire types (the subset this driver uses) ---

type dsMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []dsToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"` // role:"tool" replies
}

type dsToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type dsTool struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type dsChatRequest struct {
	Model       string      `json:"model"`
	Messages    []dsMessage `json:"messages"`
	Tools       []dsTool    `json:"tools,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
}

type dsChatResponse struct {
	Choices []struct {
		Message dsMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Run executes one session and returns its metrics. The transcript artifact is
// claude-stream-json shaped, so Metrics come from FoldEvents — one code path
// for both drivers, residual-context bytes included.
func (d DeepSeekDriver) Run(ctx context.Context, prompt string, arm Arm, transcriptPath string) (Metrics, error) {
	key := d.apiKey()
	if key == "" {
		return Metrics{}, errors.New("deepseek driver needs DEEPSEEK_API_KEY (or a driver APIKey)")
	}
	if d.client == nil {
		d.client = &http.Client{Timeout: 120 * time.Second}
	}

	// Tools: the arm's AllowedTools split. Native file tools are executed
	// in-driver; every mcp__ name must exist on the configured server.
	allowed := strings.Split(arm.AllowedTools, ",")
	tools := make([]dsTool, 0, len(allowed))
	native := map[string]bool{}
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if t, ok := nativeToolSpec(name); ok {
			tools = append(tools, t)
			native[name] = true
		}
	}

	// MCP server (codemap arm only): spawn from the resolved config, expose
	// its tools under mcp__<server>__<tool> like the claude CLI names them.
	var mcp *mcpStdioClient
	if arm.MCPConfig != "" {
		var err error
		mcp, err = dialMCPConfig(ctx, arm.MCPConfig)
		if err != nil {
			return Metrics{}, fmt.Errorf("mcp dial: %w", err)
		}
		defer mcp.close()
		list, err := mcp.toolsList(ctx)
		if err != nil {
			return Metrics{}, fmt.Errorf("mcp tools/list: %w", err)
		}
		if arm.MCPServer != "" {
			found := false
			for _, s := range mcp.servers() {
				if s == arm.MCPServer {
					found = true
				}
			}
			if !found {
				return Metrics{}, fmt.Errorf("mcp config does not define server %q", arm.MCPServer)
			}
		}
		for _, t := range list {
			fn := "mcp__" + arm.MCPServer + "__" + t.Name
			if !allowedName(allowed, fn) {
				continue
			}
			dt := dsTool{Type: "function"}
			dt.Function.Name = fn
			dt.Function.Description = t.Description
			if t.InputSchema != nil {
				dt.Function.Parameters = t.InputSchema
			} else {
				dt.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, dt)
		}
	}

	// Transcript sink: claude stream-json shaped events.
	sink := io.Writer(io.Discard)
	if transcriptPath != "" {
		f, ferr := os.Create(transcriptPath)
		if ferr != nil {
			return Metrics{}, ferr
		}
		defer func() { _ = f.Close() }()
		sink = f
	}
	tr := &dsTranscript{w: sink}
	init := map[string]any{"type": "system", "subtype": "init", "model": arm.Model, "tools": toolNames(tools)}
	if mcp != nil {
		init["mcp_servers"] = []map[string]string{{"name": arm.MCPServer, "status": "connected"}}
	}
	tr.emit(init)

	system := "You are a coding agent answering questions about the repository in your working directory. " +
		"Explore with the available tools, then answer exactly in the format the task requests. " +
		"Be precise: verify claims against the source before answering."
	if arm.AppendSystemPrompt != "" {
		system += "\n\n" + arm.AppendSystemPrompt
	}

	messages := []dsMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}

	priceIn, priceCache, priceOut := d.price(0, 0, 0)
	var inFresh, inCache, outTokens int
	var costUSD float64
	start := time.Now()

	final := ""
	subtype := "success"
loop:
	for range d.maxTurns() {
		req := dsChatRequest{Model: arm.Model, Messages: messages, Temperature: 0.0}
		if len(tools) > 0 {
			req.Tools = tools
		}
		var resp dsChatResponse
		if err := d.postJSON(ctx, "/chat/completions", &req, &resp); err != nil {
			return Metrics{}, fmt.Errorf("deepseek api: %w", err)
		}
		if resp.Error != nil {
			return Metrics{}, fmt.Errorf("deepseek api error: %s", resp.Error.Message)
		}
		if len(resp.Choices) == 0 {
			return Metrics{}, errors.New("deepseek api returned no choices")
		}
		msg := resp.Choices[0].Message
		u := resp.Usage
		hit := u.PromptCacheHitTokens
		if hit == 0 && u.PromptCacheMissTokens > 0 && u.PromptTokens >= u.PromptCacheMissTokens {
			hit = u.PromptTokens - u.PromptCacheMissTokens
		}
		if hit > u.PromptTokens {
			hit = u.PromptTokens
		}
		inCache += hit
		inFresh += u.PromptTokens - hit
		outTokens += u.CompletionTokens
		costUSD += (float64(u.PromptTokens-hit)*priceIn + float64(hit)*priceCache + float64(u.CompletionTokens)*priceOut) / 1_000_000

		// assistant event: text + tool_use blocks, claude-shaped.
		content := []map[string]any{}
		if msg.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			var input any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
		}
		tr.emit(map[string]any{"type": "assistant", "message": map[string]any{"content": content}})

		messages = append(messages, dsMessage{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls})

		if len(msg.ToolCalls) == 0 {
			final = msg.Content
			break loop
		}
		for _, tc := range msg.ToolCalls {
			result, terr := d.execTool(ctx, native, mcp, arm, tc)
			if terr != nil {
				result = "tool error: " + terr.Error()
			}
			messages = append(messages, dsMessage{Role: "tool", ToolCallID: tc.ID, Content: result})
			// user event carrying the tool_result, claude-shaped (string
			// content — FoldEvents counts exactly these bytes).
			tr.emit(map[string]any{"type": "user", "message": map[string]any{"content": []map[string]any{
				{"type": "tool_result", "tool_use_id": tc.ID, "content": result},
			}}})
		}
	}
	if final == "" {
		subtype = "error_max_turns"
	}

	tr.emit(map[string]any{
		"type":           "result",
		"subtype":        subtype,
		"is_error":       subtype != "success",
		"total_cost_usd": costUSD,
		"usage": map[string]int{
			"input_tokens":                inFresh,
			"output_tokens":               outTokens,
			"cache_read_input_tokens":     inCache,
			"cache_creation_input_tokens": 0,
		},
		"duration_ms": time.Since(start).Milliseconds(),
		"result":      final,
	})

	// Fold the transcript we just wrote: one metrics code path for every driver.
	f, ferr := os.Open(transcriptPath)
	if ferr != nil {
		return Metrics{}, ferr
	}
	defer func() { _ = f.Close() }()
	m, _, err := FoldEvents(f)
	if err != nil {
		return m, err
	}
	m.WallClockMs = time.Since(start).Milliseconds()
	m.RawTranscript = transcriptPath
	m.OK = subtype == "success"
	m.FinalAnswer = final
	if mcp != nil {
		m.CostUSD = costUSD
	}
	return m, nil
}

func (d DeepSeekDriver) execTool(ctx context.Context, native map[string]bool, mcp *mcpStdioClient, arm Arm, tc dsToolCall) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("tool arguments: %w", err)
	}
	if strings.HasPrefix(tc.Function.Name, "mcp__") {
		if mcp == nil {
			return "", errors.New("no mcp server on this arm")
		}
		name := strings.TrimPrefix(tc.Function.Name, "mcp__"+arm.MCPServer+"__")
		return mcp.toolCall(ctx, name, args)
	}
	switch tc.Function.Name {
	case "Read":
		return nativeRead(arm.WorkDir, args)
	case "Grep":
		return nativeGrep(arm.WorkDir, args)
	case "Glob":
		return nativeGlob(arm.WorkDir, args)
	default:
		return "", fmt.Errorf("tool %q not allowed on this arm", tc.Function.Name)
	}
}

func (d DeepSeekDriver) postJSON(ctx context.Context, path string, req any, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	url := d.baseURL() + path
	var lastErr error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 3 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+d.apiKey())
		resp, err := d.client.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, string(data))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("http %d: %s", resp.StatusCode, string(data))
		}
		return json.Unmarshal(data, out)
	}
	return lastErr
}

// --- transcript writer ---

type dsTranscript struct{ w io.Writer }

func (t *dsTranscript) emit(ev any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = t.w.Write(append(b, '\n'))
}

// --- native file tools, jailed to workdir and bounded ---

func nativeToolSpec(name string) (dsTool, bool) {
	t := dsTool{Type: "function"}
	switch name {
	case "Read":
		t.Function.Name = "Read"
		t.Function.Description = "Read a text file from the repository. Returns 1-based numbered lines (cat -n style)."
		t.Function.Parameters = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "path relative to the repo root"},
				"offset":    map[string]any{"type": "integer", "description": "1-based first line (optional)"},
				"limit":     map[string]any{"type": "integer", "description": "max lines to return (optional)"},
			},
			"required": []string{"file_path"},
		}
	case "Grep":
		t.Function.Name = "Grep"
		t.Function.Description = "Search file contents with a regular expression. Returns path:line: text matches."
		t.Function.Parameters = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Go regular expression"},
				"path":    map[string]any{"type": "string", "description": "subdirectory to search (optional, default repo root)"},
			},
			"required": []string{"pattern"},
		}
	case "Glob":
		t.Function.Name = "Glob"
		t.Function.Description = "List files matching a glob pattern (e.g. **/*.go). Paths are relative to the repo root."
		t.Function.Parameters = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
			},
			"required": []string{"pattern"},
		}
	default:
		return dsTool{}, false
	}
	return t, true
}

const (
	nativeReadMaxLines = 2000
	nativeReadMaxBytes = 128 << 10
	nativeGrepMaxHits  = 200
	nativeGlobMaxHits  = 500
)

func jailPath(workdir, rel string) (string, error) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "./")
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the repo root", rel)
	}
	full := filepath.Join(workdir, filepath.FromSlash(rel))
	absRoot, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if abs != absRoot && !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the repository", rel)
	}
	return abs, nil
}

func nativeRead(workdir string, args map[string]any) (string, error) {
	rel, _ := args["file_path"].(string)
	full, err := jailPath(workdir, rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	offset := 1
	if v, ok := args["offset"].(float64); ok && int(v) > 0 {
		offset = int(v)
	}
	limit := nativeReadMaxLines
	if v, ok := args["limit"].(float64); ok && int(v) > 0 && int(v) < limit {
		limit = int(v)
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	n := 0
	total := 0
	for i, line := range lines {
		ln := i + 1
		if ln < offset {
			continue
		}
		if n >= limit || total >= nativeReadMaxBytes {
			fmt.Fprintf(&b, "… (%d more lines; re-read with offset/limit)\n", len(lines)-ln+1)
			break
		}
		row := fmt.Sprintf("%6d\t%s\n", ln, line)
		if total+len(row) > nativeReadMaxBytes {
			fmt.Fprintf(&b, "… (%d more lines; file is large — use Grep to narrow)\n", len(lines)-ln+1)
			break
		}
		b.WriteString(row)
		total += len(row)
		n++
	}
	return b.String(), nil
}

func nativeGrep(workdir string, args map[string]any) (string, error) {
	pattern, _ := args["pattern"].(string)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("pattern: %w", err)
	}
	sub := ""
	if v, ok := args["path"].(string); ok {
		sub = v
	}
	root := workdir
	if sub != "" {
		root, err = jailPath(workdir, sub)
		if err != nil {
			return "", err
		}
	}
	var hits []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(hits) >= nativeGrepMaxHits {
			return filepath.SkipAll
		}
		info, serr := d.Info()
		if serr != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(workdir, path)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				trimmed := line
				if len(trimmed) > 240 {
					trimmed = trimmed[:240] + "…"
				}
				hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, trimmed))
				if len(hits) >= nativeGrepMaxHits {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "no matches", nil
	}
	out := strings.Join(hits, "\n")
	if len(hits) >= nativeGrepMaxHits {
		out += fmt.Sprintf("\n… (capped at %d matches)", nativeGrepMaxHits)
	}
	return out, nil
}

func nativeGlob(workdir string, args map[string]any) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	var hits []string
	err := filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(hits) >= nativeGlobMaxHits {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(workdir, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(hits)
	if len(hits) == 0 {
		return "no matches", nil
	}
	return strings.Join(hits, "\n"), nil
}

// globMatch matches a **-aware glob against a slash-separated relative path.
func globMatch(pattern, path string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := filepath.Match(pattern, path)
		return err == nil && ok
	}
	// a/**/b matches a/b and a/x/y/b
	parts := strings.Split(pattern, "**")
	if len(parts) != 2 {
		ok, err := filepath.Match(pattern, path)
		return err == nil && ok
	}
	head, tail := parts[0], parts[1]
	head = strings.TrimSuffix(head, "/")
	tail = strings.TrimPrefix(tail, "/")
	if head != "" {
		if !strings.HasPrefix(path, head+"/") {
			return false
		}
		path = path[len(head)+1:]
	}
	if tail == "" {
		return true
	}
	ok, err := filepath.Match(tail, path)
	if err == nil && ok {
		return true
	}
	// allow tail to match the last segments (a/**/*.go → a/x/y.go)
	idx := strings.Index(path, "/")
	for idx >= 0 {
		path = path[idx+1:]
		ok, err := filepath.Match(tail, path)
		if err == nil && ok {
			return true
		}
		idx = strings.Index(path, "/")
	}
	return false
}

func allowedName(allowed []string, name string) bool {
	for _, a := range allowed {
		if strings.TrimSpace(a) == name {
			return true
		}
		if strings.TrimSpace(a) == "mcp__"+name[strings.Index(name, "__")+2:] {
			return true
		}
	}
	// "mcp__codemap" (server-level grant) allows every tool on that server.
	for _, a := range allowed {
		if strings.TrimSpace(a) == "mcp__"+strings.Split(name, "__")[1] {
			return true
		}
	}
	return false
}

func toolNames(tools []dsTool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Function.Name)
	}
	return out
}

// --- minimal newline-delimited JSON-RPC MCP stdio client ---

type mcpToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpStdioClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
	srv    string
}

type mcpConfigFile struct {
	MCPServers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
}

func dialMCPConfig(ctx context.Context, configPath string) (*mcpStdioClient, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	if len(cfg.MCPServers) != 1 {
		return nil, fmt.Errorf("expected exactly one mcp server in %s", configPath)
	}
	for name, srv := range cfg.MCPServers {
		c := &mcpStdioClient{srv: name}
		c.cmd = exec.CommandContext(ctx, srv.Command, srv.Args...)
		env := os.Environ()
		for k, v := range srv.Env {
			env = append(env, k+"="+v)
		}
		c.cmd.Env = env
		c.cmd.Stderr = os.Stderr
		var err error
		if c.stdin, err = c.cmd.StdinPipe(); err != nil {
			return nil, err
		}
		out, err := c.cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		c.stdout = bufio.NewReaderSize(out, 16<<20)
		if err := c.cmd.Start(); err != nil {
			return nil, err
		}
		if err := c.initialize(ctx); err != nil {
			c.close()
			return nil, err
		}
		return c, nil
	}
	return nil, errors.New("unreachable")
}

func (c *mcpStdioClient) servers() []string { return []string{c.srv} }

func (c *mcpStdioClient) initialize(ctx context.Context) error {
	var result json.RawMessage
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "codemap-bench", "version": "1.0"},
	}, &result); err != nil {
		return err
	}
	return c.notify("notifications/initialized")
}

func (c *mcpStdioClient) toolsList(ctx context.Context) ([]mcpToolInfo, error) {
	var resp struct {
		Tools []mcpToolInfo `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Tools, nil
}

func (c *mcpStdioClient) toolCall(ctx context.Context, name string, args map[string]any) (string, error) {
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &resp); err != nil {
		return "", err
	}
	texts := make([]string, 0, len(resp.Content))
	for _, b := range resp.Content {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	out := strings.Join(texts, "\n")
	if resp.IsError {
		return out, errors.New("mcp tool reported an error")
	}
	return out, nil
}

func (c *mcpStdioClient) call(ctx context.Context, method string, params any, out any) error {
	c.nextID++
	id := c.nextID
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	type rpcError struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("mcp read %s: %w", method, err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil || msg.ID == nil {
			continue // notification or noise
		}
		if *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("mcp %s: %s", method, msg.Error.Message)
		}
		return json.Unmarshal(msg.Result, out)
	}
}

func (c *mcpStdioClient) notify(method string) error {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *mcpStdioClient) close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	}
}

// AuthModeFor reports the auth-mode label for a driver name, so summaries do
// not claim claude's auth mode for other drivers.
func AuthModeFor(driver string) string {
	switch driver {
	case "claude":
		return AuthMode()
	case "deepseek":
		return DeepSeekAuthMode()
	default:
		return ""
	}
}
