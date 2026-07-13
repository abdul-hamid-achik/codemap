// Package drivers defines the pluggable agent-driver interface for the codemap
// benchmark and ships the Claude Code headless driver plus offline stubs.
//
// A driver's whole job is: "run a task prompt in an arm, return transcript
// metrics." That contract is intentionally small so Codex CLI / Gemini CLI /
// OpenCode headless can slot in later. v1 ships only the Claude driver (real)
// and a smoke driver (offline); codex/gemini are ErrNotImplemented stubs.
package drivers

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by stub drivers.
var ErrNotImplemented = errors.New("driver not implemented in v1 (Claude-only)")

// Arm describes one side of the A/B comparison.
type Arm struct {
	Name               string // "baseline" | "codemap"
	AllowedTools       string // e.g. "Read,Grep,Glob" or "Read,Grep,Glob,mcp__codemap"
	MCPConfig          string // path to an MCP config JSON, or "" for none
	MCPServer          string // MCP server name to assert loaded (e.g. "codemap"), or ""
	WorkDir            string // fixture repo root — cwd for the agent
	Model              string
	AppendSystemPrompt string // when set, appended via claude's --append-system-prompt (the
	// codemap tool-selection playbook, injected on the codemap arm only when
	// --playbook is passed; empty for the baseline arm and for un-playbooked runs)
}

// Metrics is what a driver extracts from one session's transcript.
type Metrics struct {
	ToolCalls       int
	MCPToolCalls    int // subset of ToolCalls whose tool name starts with "mcp__"
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	WallClockMs     int64
	CostUSD         float64
	FinalAnswer     string // raw final assistant text; grader extracts the JSON block
	RawTranscript   string // path to the saved stream-json file (artifact)
	OK              bool   // result event subtype == "success"
}

// Driver runs a single agent session.
type Driver interface {
	Name() string
	// Run executes prompt in arm and returns the session metrics. TranscriptPath,
	// when non-empty, is where the raw stream-json should be written.
	Run(ctx context.Context, prompt string, arm Arm, transcriptPath string) (Metrics, error)
}
