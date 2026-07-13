package drivers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeDriver shells out to the `claude` CLI in headless mode and folds its
// stream-json event stream into Metrics.
//
// The stream-json schema is verified against the official docs:
//   - Headless guide:  https://code.claude.com/docs/en/headless (fetched 2026-07-11)
//   - SDK message types: https://code.claude.com/docs/en/agent-sdk/typescript
//
// Schema recap (the fields this driver reads):
//   - system/init : {type:"system", subtype:"init", model, tools[],
//     mcp_servers:[{name,status}]} — first event; we assert the codemap server
//     loaded here so a broken MCP config fails loudly instead of silently
//     degrading the codemap arm to plain file tools.
//   - assistant   : {type:"assistant", message:{content:[...]}} — one per model
//     turn; TOOL CALLS ARE tool_use CONTENT BLOCKS (count them across every
//     assistant event; num_turns undercounts because one turn can emit several
//     parallel tool_use blocks).
//   - user        : carries tool_result blocks; ignored for metrics.
//   - result      : {type:"result", subtype:"success"|"error_max_turns"|
//     "error_during_execution", total_cost_usd, usage:{input_tokens,
//     output_tokens, cache_read_input_tokens, cache_creation_input_tokens},
//     num_turns, duration_ms, is_error, result} — final event; source of tokens,
//     cost, and the final answer text.
//
// stream-json REQUIRES --verbose (the CLI errors otherwise), so the driver
// always passes both.
type ClaudeDriver struct {
	// Bin is the claude binary (default "claude", resolved on PATH).
	Bin string
}

func (d ClaudeDriver) Name() string { return "claude" }

func (d ClaudeDriver) bin() string {
	if d.Bin != "" {
		return d.Bin
	}
	return "claude"
}

// AuthMode reports how sessions will authenticate: "api-key" (ANTHROPIC_API_KEY
// set → hermetic --bare sessions, the gold standard) or "subscription" (no key →
// the operator's logged-in claude CLI). Subscription mode drops --bare but pins
// the MCP surface with --strict-mcp-config, which preserves the fairness
// property that actually decides the A/B (arm A cannot see codemap through
// ambient user config). Residual leak: user-level CLAUDE.md/hooks may still
// load — results from subscription runs are tagged and even more DIRECTIONAL.
func AuthMode() string {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "api-key"
	}
	return "subscription"
}

// Args builds the claude command line for an arm. Exposed for --dry-run so the
// exact invocation can be printed without executing it.
func (d ClaudeDriver) Args(prompt string, arm Arm) []string {
	var args []string
	if AuthMode() == "api-key" {
		// fairness: no ambient hooks/skills/plugins/MCP/CLAUDE.md leak
		args = append(args, "--bare")
	} else {
		// subscription auth cannot use --bare; pin MCP to exactly this arm's
		// config (none for the baseline arm) so ambient servers never leak in.
		args = append(args, "--strict-mcp-config")
	}
	args = append(args,
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose", // required by stream-json
		"--model", arm.Model,
		"--allowedTools", arm.AllowedTools,
	)
	if arm.MCPConfig != "" {
		args = append(args, "--mcp-config", arm.MCPConfig)
	}
	if arm.AppendSystemPrompt != "" {
		// Flag name verified against `claude --help` (2026-07-13): "--append-system-prompt
		// <prompt>  Append a system prompt to the default system prompt". Confirmed
		// compatible with --bare, which explicitly documents --append-system-prompt[-file]
		// as one of the flags that still layers on top of minimal mode.
		args = append(args, "--append-system-prompt", arm.AppendSystemPrompt)
	}
	return args
}

// Run executes one session. It streams stdout to transcriptPath (if set) while
// folding events into Metrics, measures wall-clock externally, and asserts the
// arm's MCP server actually loaded.
func (d ClaudeDriver) Run(ctx context.Context, prompt string, arm Arm, transcriptPath string) (Metrics, error) {
	if AuthMode() == "subscription" {
		fmt.Fprintf(os.Stderr, "bench: auth=subscription (no ANTHROPIC_API_KEY): --bare dropped, MCP pinned via --strict-mcp-config; sessions bill the logged-in plan\n")
	}
	cmd := exec.CommandContext(ctx, d.bin(), d.Args(prompt, arm)...)
	cmd.Dir = arm.WorkDir
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Metrics{}, err
	}

	sink := io.Writer(io.Discard)
	if transcriptPath != "" {
		f, ferr := os.Create(transcriptPath)
		if ferr != nil {
			return Metrics{}, ferr
		}
		defer func() { _ = f.Close() }()
		sink = f
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Metrics{}, err
	}
	// Tee stdout to the transcript file while folding.
	m, init, foldErr := FoldEvents(io.TeeReader(stdout, sink))
	if foldErr != nil {
		// FoldEvents can return early without reading stdout to EOF (e.g.
		// bufio.ErrTooLong on a single stream-json line over its 16 MiB
		// buffer). os/exec requires the StdoutPipe to be fully drained
		// before Wait is called, or a still-writing child blocks on a full
		// OS pipe buffer and cmd.Wait() never returns on its own — stalling
		// this session until the caller's context timeout kills the
		// process. Drain and discard whatever is left so Wait can return
		// promptly instead; the transcript is already unusable past the
		// parse failure.
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	m.WallClockMs = time.Since(start).Milliseconds()
	m.RawTranscript = transcriptPath

	if foldErr != nil {
		return m, fmt.Errorf("parse stream-json: %w", foldErr)
	}
	if waitErr != nil {
		return m, fmt.Errorf("claude exited with error: %w", waitErr)
	}
	// Assert the arm's MCP server loaded, else the codemap arm silently measures
	// nothing (degrades to file tools).
	if arm.MCPServer != "" && !init.hasServer(arm.MCPServer) {
		return m, fmt.Errorf("mcp server %q not loaded (loaded: %s) — check --mcp-config", arm.MCPServer, strings.Join(init.serverNames(), ","))
	}
	return m, nil
}

// InitInfo captures the fields of the system/init event we care about.
type InitInfo struct {
	Model      string
	MCPServers []mcpServer
}

func (i InitInfo) hasServer(name string) bool {
	for _, s := range i.MCPServers {
		if s.Name == name {
			// A connected server reports status "connected"/"ok"; treat any
			// non-"failed" status as loaded, and accept a missing status too.
			return s.Status == "" || !strings.EqualFold(s.Status, "failed")
		}
	}
	return false
}

func (i InitInfo) serverNames() []string {
	out := make([]string, 0, len(i.MCPServers))
	for _, s := range i.MCPServers {
		out = append(out, s.Name+"("+s.Status+")")
	}
	return out
}

type mcpServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// init
	MCPServers []mcpServer `json:"mcp_servers"`
	Model      string      `json:"model"`
	// assistant
	Message *struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
	// result
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        *struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheReadInputTok   int `json:"cache_read_input_tokens"`
		CacheCreateInputTok int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	DurationMs int64  `json:"duration_ms"`
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
}

// FoldEvents parses a newline-delimited stream-json event stream, counting
// tool_use blocks across assistant events and reading token/cost/answer from the
// final result event. It is the unit under test in claude_test.go.
func FoldEvents(r io.Reader) (Metrics, InitInfo, error) {
	var m Metrics
	var init InitInfo
	sawResult := false

	sc := bufio.NewScanner(r)
	// stream-json lines can be large (big tool results / final answers).
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Tolerate stray non-JSON lines rather than aborting the whole run.
			continue
		}
		switch ev.Type {
		case "system":
			if ev.Subtype == "init" {
				init.Model = ev.Model
				init.MCPServers = ev.MCPServers
			}
		case "assistant":
			if ev.Message != nil {
				for _, b := range ev.Message.Content {
					if b.Type == "tool_use" {
						m.ToolCalls++
						if strings.HasPrefix(b.Name, "mcp__") {
							m.MCPToolCalls++
						}
					}
				}
			}
		case "result":
			sawResult = true
			m.CostUSD = ev.TotalCostUSD
			m.FinalAnswer = ev.Result
			m.OK = ev.Subtype == "success" && !ev.IsError
			if ev.Usage != nil {
				m.InputTokens = ev.Usage.InputTokens
				m.OutputTokens = ev.Usage.OutputTokens
				m.CacheReadTokens = ev.Usage.CacheReadInputTok
			}
			// duration_ms is available for cross-checking against the external
			// wall-clock; the external measurement is authoritative in Run.
			if m.WallClockMs == 0 {
				m.WallClockMs = ev.DurationMs
			}
		}
	}
	if err := sc.Err(); err != nil {
		return m, init, err
	}
	if !sawResult {
		return m, init, fmt.Errorf("no result event in stream (session did not complete)")
	}
	return m, init, nil
}
