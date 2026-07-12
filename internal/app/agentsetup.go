package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// This file is the harness registry behind `codemap agent setup/list/playbook`:
// it detects AI coding harnesses, merges the codemap MCP server into each one's
// native config (read-modify-write, NEVER clobbering other servers), and writes
// the rendered playbook to the harness's guidance surface. ALL logic lives here;
// cmd/codemap/agent.go is a thin flag-parsing wrapper. No DB/session is opened —
// agent setup must work before any index exists.
//
// Harness formats re-verified 2026-07-12 against primary docs; see the per-entry
// comments.

// SetupOptions controls a single `codemap agent setup` run.
type SetupOptions struct {
	Global     bool // write user-level config where the harness has one
	DryRun     bool // compute + report planned writes, mutate nothing
	NoPlaybook bool // register the MCP server only, skip the guidance file
}

// FileAction records one planned or performed write. Action is one of
// created|updated|unchanged.
type FileAction struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// Snippet is a print-don't-write fallback: the exact content and destination the
// user should apply by hand (global-only config, JSONC we won't rewrite, or a
// harness CLI that isn't installed).
type Snippet struct {
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// SetupReport is the machine-readable result of a setup run.
type SetupReport struct {
	Harness  string       `json:"harness"`
	Written  []FileAction `json:"written,omitempty"`
	Snippets []Snippet    `json:"snippets,omitempty"`
	Notes    []string     `json:"notes,omitempty"`
	DryRun   bool         `json:"dry_run,omitempty"`
}

// Detection is one row of `codemap agent list`.
type Detection struct {
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	ConfigPath string `json:"config_path,omitempty"`
	Registered bool   `json:"registered"`
}

// HarnessSetup is a registry entry: how to detect a harness and how to wire
// codemap into it.
type HarnessSetup struct {
	Name        string
	Description string
	Detect      func(dir string, global bool) Detection
	Setup       func(dir string, opts SetupOptions) (SetupReport, error)
}

// mcpServerValue is the codemap stdio-server value shared by the command+args
// harnesses (Cursor, Gemini, Cline, Roo, VS Code). Fresh map per call so callers
// never alias it into a config tree.
func mcpServerValue() map[string]any {
	return map[string]any{"command": "codemap", "args": []string{"serve"}}
}

// harnesses is the registry. DetectHarnesses/SetupHarness iterate/dispatch on it.
var harnesses = []HarnessSetup{
	{
		// Claude Code — flagship. The in-repo plugin owns BOTH the MCP server and
		// the using-codemap skill, so there is no JSON to edit: prefer the plugin.
		// Verified 2026-07-12 against https://code.claude.com/docs/en/plugins-reference.
		Name:        "claude-code",
		Description: "Claude Code (installs the codemap plugin: MCP server + using-codemap skill)",
		Detect: func(dir string, global bool) Detection {
			_, err := exec.LookPath("claude")
			return Detection{Name: "claude-code", Present: err == nil, ConfigPath: "claude plugin (marketplace: abdul-hamid-achik/codemap)"}
		},
		Setup: setupClaudeCode,
	},
	{
		// Cursor — project .cursor/mcp.json wins over ~/.cursor/mcp.json; rules are
		// agent-requested .mdc. Verified 2026-07-12 against https://cursor.com/docs/cli/mcp.
		Name:        "cursor",
		Description: "Cursor (.cursor/mcp.json + .cursor/rules/codemap.mdc)",
		Detect:      detectFromJSON("cursor", ".cursor", ".cursor/mcp.json", "mcpServers"),
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "cursor", DryRun: opts.DryRun}
			path := filepath.Join(dir, ".cursor", "mcp.json")
			if opts.Global {
				path = filepath.Join(homeDir(), ".cursor", "mcp.json")
			}
			if err := doJSONServer(&rep, path, "mcpServers", "codemap", mcpServerValue(), opts.DryRun); err != nil {
				return rep, err
			}
			if err := doPlaybook(&rep, opts, filepath.Join(dir, ".cursor", "rules", "codemap.mdc"), RenderPlaybook(FormatCursorRule), true); err != nil {
				return rep, err
			}
			rep.Notes = append(rep.Notes, "cursor caps tools at ~40 across all MCP servers; codemap ships 39, so adding another server may hide tools")
			return rep, nil
		},
	},
	{
		// OpenAI Codex CLI — global ~/.codex/config.toml (or project .codex in
		// trusted projects). Table is [mcp_servers.<name>] (underscore). We never
		// write TOML: shell out to `codex mcp add` or print the block. Verified
		// 2026-07-12 against https://developers.openai.com/codex/config-reference.
		Name:        "codex",
		Description: "OpenAI Codex CLI (codex mcp add / ~/.codex/config.toml + AGENTS.md)",
		Detect: func(dir string, global bool) Detection {
			path := filepath.Join(homeDir(), ".codex", "config.toml")
			present := false
			if _, err := exec.LookPath("codex"); err == nil {
				present = true
			}
			registered := false
			if b, err := os.ReadFile(path); err == nil {
				present = true
				registered = strings.Contains(string(b), "[mcp_servers.codemap]")
			}
			return Detection{Name: "codex", Present: present, ConfigPath: path, Registered: registered}
		},
		Setup: setupCodex,
	},
	{
		// Gemini CLI — project .gemini/settings.json (mcpServers). Verified
		// 2026-07-12 against https://geminicli.com/docs/tools/mcp-server/.
		Name:        "gemini",
		Description: "Gemini CLI (.gemini/settings.json + GEMINI.md)",
		Detect:      detectFromJSON("gemini", ".gemini", ".gemini/settings.json", "mcpServers"),
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "gemini", DryRun: opts.DryRun}
			path := filepath.Join(dir, ".gemini", "settings.json")
			if opts.Global {
				path = filepath.Join(homeDir(), ".gemini", "settings.json")
			}
			if err := doJSONServer(&rep, path, "mcpServers", "codemap", mcpServerValue(), opts.DryRun); err != nil {
				return rep, err
			}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, "GEMINI.md"), FormatMarkdownSection); err != nil {
				return rep, err
			}
			return rep, nil
		},
	},
	{
		// Cline — MCP config lives in VS Code globalStorage, per-OS/per-fork.
		// Verified 2026-07-12 (macOS path) against https://docs.cline.bot/mcp/configuring-mcp-servers.
		Name:        "cline",
		Description: "Cline (VS Code globalStorage cline_mcp_settings.json + .clinerules)",
		Detect: func(dir string, global bool) Detection {
			path := clineSettingsPath()
			registered := path != "" && jsonHasServer(path, "mcpServers", "codemap")
			present := path != "" && fileExists(path)
			return Detection{Name: "cline", Present: present, ConfigPath: path, Registered: registered}
		},
		Setup: setupCline,
	},
	{
		// Roo Code — project .roo/mcp.json (documented, shareable). Verified
		// 2026-07-12 against https://docs.roocode.com/features/mcp/using-mcp-in-roo.
		Name:        "roo",
		Description: "Roo Code (.roo/mcp.json + .roo/rules/codemap.md)",
		Detect:      detectFromJSON("roo", ".roo", ".roo/mcp.json", "mcpServers"),
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "roo", DryRun: opts.DryRun}
			if err := doJSONServer(&rep, filepath.Join(dir, ".roo", "mcp.json"), "mcpServers", "codemap", mcpServerValue(), opts.DryRun); err != nil {
				return rep, err
			}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, ".roo", "rules", "codemap.md"), FormatMarkdownSection); err != nil {
				return rep, err
			}
			return rep, nil
		},
	},
	{
		// Zed — global ~/.config/zed/settings.json is JSONC (comments legal), key
		// context_servers. A strict parse of a commented file fails; we then print
		// rather than strip comments. Verified 2026-07-12 against https://zed.dev/docs/ai/mcp
		// (.rules still the auto-included project rules file).
		Name:        "zed",
		Description: "Zed (~/.config/zed/settings.json context_servers + .rules)",
		Detect: func(dir string, global bool) Detection {
			path := filepath.Join(configHome(), "zed", "settings.json")
			registered := jsonHasServer(path, "context_servers", "codemap")
			return Detection{Name: "zed", Present: fileExists(path), ConfigPath: path, Registered: registered}
		},
		Setup: setupZed,
	},
	{
		// VS Code Copilot agent mode — .vscode/mcp.json, top-level key is `servers`
		// (NOT mcpServers — the #1 copy-paste mistake). Verified 2026-07-12 against
		// https://code.visualstudio.com/docs/agents/reference/mcp-configuration.
		Name:        "vscode",
		Description: "VS Code Copilot agent mode (.vscode/mcp.json [servers] + .github/copilot-instructions.md)",
		Detect:      detectFromJSON("vscode", ".vscode", ".vscode/mcp.json", "servers"),
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "vscode", DryRun: opts.DryRun}
			if err := doJSONServer(&rep, filepath.Join(dir, ".vscode", "mcp.json"), "servers", "codemap", mcpServerValue(), opts.DryRun); err != nil {
				return rep, err
			}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, ".github", "copilot-instructions.md"), FormatMarkdownSection); err != nil {
				return rep, err
			}
			return rep, nil
		},
	},
	{
		// OpenCode — opencode.json `mcp` block; command is an ARRAY here, unlike
		// everyone else. Verified 2026-07-12 against https://opencode.ai/docs/mcp-servers/.
		Name:        "opencode",
		Description: "OpenCode (opencode.json mcp + AGENTS.md)",
		Detect: func(dir string, global bool) Detection {
			path := filepath.Join(dir, "opencode.json")
			return Detection{Name: "opencode", Present: fileExists(path), ConfigPath: path, Registered: jsonHasServer(path, "mcp", "codemap")}
		},
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "opencode", DryRun: opts.DryRun}
			server := map[string]any{"type": "local", "command": []string{"codemap", "serve"}, "enabled": true}
			if err := doJSONServer(&rep, filepath.Join(dir, "opencode.json"), "mcp", "codemap", server, opts.DryRun); err != nil {
				return rep, err
			}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, "AGENTS.md"), FormatMarkdownSection); err != nil {
				return rep, err
			}
			return rep, nil
		},
	},
	{
		// aider — no native MCP (open RFC only); the CLI-form playbook goes into
		// CONVENTIONS.md, loaded via --read / .aider.conf.yml. Verified 2026-07-12
		// against https://aider.chat/docs/usage/conventions.html.
		Name:        "aider",
		Description: "aider (no MCP; CLI playbook -> CONVENTIONS.md)",
		Detect: func(dir string, global bool) Detection {
			path := filepath.Join(dir, "CONVENTIONS.md")
			present := fileExists(path) || fileExists(filepath.Join(dir, ".aider.conf.yml"))
			return Detection{Name: "aider", Present: present, ConfigPath: path, Registered: markedBlockPresent(path)}
		},
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "aider", DryRun: opts.DryRun}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, "CONVENTIONS.md"), FormatMarkdownSectionCLI); err != nil {
				return rep, err
			}
			rep.Notes = append(rep.Notes, "add to .aider.conf.yml:  read: [CONVENTIONS.md]  (or launch aider with --read CONVENTIONS.md)")
			return rep, nil
		},
	},
	{
		// agents-md — harness-agnostic escape hatch: the CLI-form playbook into
		// AGENTS.md only, for any AGENTS.md-reading harness we don't model.
		Name:        "agents-md",
		Description: "any AGENTS.md-reading harness (CLI playbook -> AGENTS.md)",
		Detect: func(dir string, global bool) Detection {
			path := filepath.Join(dir, "AGENTS.md")
			return Detection{Name: "agents-md", Present: fileExists(path), ConfigPath: path, Registered: markedBlockPresent(path)}
		},
		Setup: func(dir string, opts SetupOptions) (SetupReport, error) {
			rep := SetupReport{Harness: "agents-md", DryRun: opts.DryRun}
			if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, "AGENTS.md"), FormatMarkdownSectionCLI); err != nil {
				return rep, err
			}
			return rep, nil
		},
	},
}

// harnessNames lists the registry keys in order (for errors + `agent list`).
func harnessNames() []string {
	names := make([]string, len(harnesses))
	for i, h := range harnesses {
		names[i] = h.Name
	}
	return names
}

// aliases maps user-friendly synonyms to registry keys.
var harnessAliases = map[string]string{"conventions": "aider", "agents": "agents-md", "claude": "claude-code", "codexcli": "codex"}

func lookupHarness(name string) (*HarnessSetup, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if a, ok := harnessAliases[name]; ok {
		name = a
	}
	for i := range harnesses {
		if harnesses[i].Name == name {
			return &harnesses[i], true
		}
	}
	return nil, false
}

// DetectHarnesses reports every known harness and whether it is present in dir
// (or globally) and already has codemap registered.
func DetectHarnesses(dir string) []Detection {
	out := make([]Detection, 0, len(harnesses))
	for _, h := range harnesses {
		out = append(out, h.Detect(dir, false))
	}
	return out
}

// SetupHarness wires codemap into the named harness. An unknown name returns an
// error listing the valid ones, so the command is self-documenting.
func SetupHarness(dir, name string, opts SetupOptions) (SetupReport, error) {
	h, ok := lookupHarness(name)
	if !ok {
		return SetupReport{}, fmt.Errorf("unknown harness %q — valid: %s", name, strings.Join(harnessNames(), ", "))
	}
	return h.Setup(dir, opts)
}

// --- per-harness setups that shell out or print rather than write JSON ---

func setupClaudeCode(dir string, opts SetupOptions) (SetupReport, error) {
	rep := SetupReport{Harness: "claude-code", DryRun: opts.DryRun}
	cmds := []string{
		"claude plugin marketplace add abdul-hamid-achik/codemap",
		"claude plugin install codemap@codemap",
	}
	_, haveClaude := exec.LookPath("claude")
	if haveClaude == nil && !opts.DryRun {
		for _, c := range cmds {
			parts := strings.Fields(c)
			out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput() //nolint:gosec // fixed argv, no user input
			if err != nil {
				// Never a hard failure: fall back to printing the commands.
				rep.Snippets = append(rep.Snippets, Snippet{Content: strings.Join(cmds, "\n"), Reason: "run these to install the codemap plugin (`claude` invocation failed: " + strings.TrimSpace(string(out)) + ")"})
				return rep, nil
			}
		}
		rep.Notes = append(rep.Notes, "installed the codemap plugin (MCP server + using-codemap skill)")
		return rep, nil
	}
	rep.Snippets = append(rep.Snippets, Snippet{Content: strings.Join(cmds, "\n"), Reason: "run these to install the codemap plugin (MCP server + using-codemap skill), or in Claude Code: /plugin marketplace add abdul-hamid-achik/codemap"})
	return rep, nil
}

func setupCodex(dir string, opts SetupOptions) (SetupReport, error) {
	rep := SetupReport{Harness: "codex", DryRun: opts.DryRun}
	_, haveCodex := exec.LookPath("codex")
	if haveCodex == nil && !opts.DryRun {
		out, err := exec.Command("codex", "mcp", "add", "codemap", "--", "codemap", "serve").CombinedOutput() //nolint:gosec // fixed argv
		if err != nil {
			rep.Snippets = append(rep.Snippets, codexSnippet("`codex mcp add` failed: "+strings.TrimSpace(string(out))))
		} else {
			rep.Notes = append(rep.Notes, "registered codemap via `codex mcp add`")
		}
	} else {
		rep.Snippets = append(rep.Snippets, codexSnippet("Codex config is global TOML; run the command or add the block yourself (codemap never writes TOML)"))
	}
	if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, "AGENTS.md"), FormatMarkdownSection); err != nil {
		return rep, err
	}
	return rep, nil
}

func codexSnippet(reason string) Snippet {
	return Snippet{
		Path:    filepath.Join(homeDir(), ".codex", "config.toml"),
		Content: "codex mcp add codemap -- codemap serve\n\n# ...or add to ~/.codex/config.toml:\n[mcp_servers.codemap]\ncommand = \"codemap\"\nargs = [\"serve\"]",
		Reason:  reason,
	}
}

func setupCline(dir string, opts SetupOptions) (SetupReport, error) {
	rep := SetupReport{Harness: "cline", DryRun: opts.DryRun}
	path := clineSettingsPath()
	// Only write when the globalStorage file already exists — never guess-create
	// the per-OS/per-fork directory.
	if path != "" && fileExists(path) {
		if err := doJSONServer(&rep, path, "mcpServers", "codemap", mcpServerValue(), opts.DryRun); err != nil {
			return rep, err
		}
	} else {
		rep.Snippets = append(rep.Snippets, Snippet{
			Path:    path,
			Content: jsonServerSnippet("mcpServers", "codemap", mcpServerValue()),
			Reason:  "Cline's MCP config lives in VS Code globalStorage and wasn't found; use Cline's \"Configure MCP Servers\" UI or add this to cline_mcp_settings.json",
		})
	}
	if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, ".clinerules"), FormatMarkdownSection); err != nil {
		return rep, err
	}
	return rep, nil
}

func setupZed(dir string, opts SetupOptions) (SetupReport, error) {
	rep := SetupReport{Harness: "zed", DryRun: opts.DryRun}
	path := filepath.Join(configHome(), "zed", "settings.json")
	server := map[string]any{"source": "custom", "command": "codemap", "args": []string{"serve"}, "env": map[string]any{}}
	// Zed settings are JSONC and global. Only write with --global AND a clean
	// strict parse; otherwise print, never strip the user's comments.
	if opts.Global && cleanJSON(path) {
		if err := doJSONServer(&rep, path, "context_servers", "codemap", server, opts.DryRun); err != nil {
			return rep, err
		}
	} else {
		reason := "Zed's settings.json is global JSONC; add this by hand"
		if opts.Global {
			reason = "Zed's settings.json has comments (JSONC) we won't rewrite; add this by hand"
		}
		rep.Snippets = append(rep.Snippets, Snippet{Path: path, Content: jsonServerSnippet("context_servers", "codemap", server), Reason: reason})
	}
	if err := doMarkedPlaybook(&rep, opts, filepath.Join(dir, ".rules"), FormatMarkdownSection); err != nil {
		return rep, err
	}
	return rep, nil
}

// --- shared write helpers ---

// doJSONServer merges server under topKey.serverName in a JSON config, appending
// the FileAction (or a never-clobber Snippet) to rep.
func doJSONServer(rep *SetupReport, path, topKey, serverName string, server any, dry bool) error {
	action, snip, err := upsertJSONServer(path, topKey, serverName, server, dry)
	if err != nil {
		return err
	}
	if snip != nil {
		rep.Snippets = append(rep.Snippets, *snip)
		return nil
	}
	rep.Written = append(rep.Written, action)
	return nil
}

// doPlaybook writes a fully-generated playbook file (e.g. Cursor .mdc) unless
// NoPlaybook. gen==true means the whole file is generated (byte-compare), used
// for .mdc where there is no user content to preserve.
func doPlaybook(rep *SetupReport, opts SetupOptions, path, content string, gen bool) error {
	if opts.NoPlaybook {
		return nil
	}
	action, err := writeGenerated(path, content, opts.DryRun)
	if err != nil {
		return err
	}
	rep.Written = append(rep.Written, action)
	return nil
}

// doMarkedPlaybook writes the marked-block playbook into a guidance file unless
// NoPlaybook, replacing any existing block in place.
func doMarkedPlaybook(rep *SetupReport, opts SetupOptions, path string, f PlaybookFormat) error {
	if opts.NoPlaybook {
		return nil
	}
	action, err := upsertMarkedBlock(path, RenderPlaybook(f), opts.DryRun)
	if err != nil {
		return err
	}
	rep.Written = append(rep.Written, action)
	return nil
}

// upsertJSONServer reads path into a map (preserving every sibling key/server),
// creates-or-replaces only topKey.serverName, and writes it back 2-space indented
// with HTML escaping off. A parse error (e.g. JSONC) NEVER overwrites — it returns
// a snippet fallback. Idempotent: a re-run that would produce byte-identical
// output reports "unchanged" and writes nothing.
func upsertJSONServer(path, topKey, serverName string, server any, dry bool) (FileAction, *Snippet, error) {
	orig, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return FileAction{}, nil, readErr
	}
	root := map[string]any{}
	if existed {
		if err := json.Unmarshal(orig, &root); err != nil {
			return FileAction{}, &Snippet{
				Path:    path,
				Content: jsonServerSnippet(topKey, serverName, server),
				Reason:  "existing config isn't plain JSON (comments or syntax codemap won't rewrite); add this by hand to preserve it",
			}, nil
		}
	}
	sub, _ := root[topKey].(map[string]any)
	if sub == nil {
		sub = map[string]any{}
	}
	sub[serverName] = server
	root[topKey] = sub
	out, err := marshalJSON(root)
	if err != nil {
		return FileAction{}, nil, err
	}
	action := "created"
	if existed {
		if bytes.Equal(bytes.TrimRight(orig, "\n"), bytes.TrimRight(out, "\n")) {
			action = "unchanged"
		} else {
			action = "updated"
		}
	}
	if action != "unchanged" && !dry {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return FileAction{}, nil, err
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return FileAction{}, nil, err
		}
	}
	return FileAction{Path: path, Action: action}, nil, nil
}

// upsertMarkedBlock inserts/replaces the codemap marked block in path. If the
// file has a `<!-- codemap:begin … <!-- codemap:end -->` span it is replaced in
// place; otherwise the block is appended after a blank line; the file is created
// if absent. Idempotent by construction.
func upsertMarkedBlock(path, block string, dry bool) (FileAction, error) {
	orig, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return FileAction{}, readErr
	}
	var next string
	if existed {
		s := string(orig)
		if i := strings.Index(s, markerBeginPrefix); i >= 0 {
			if j := strings.Index(s[i:], markerEnd); j >= 0 {
				end := i + j + len(markerEnd)
				next = s[:i] + strings.TrimRight(block, "\n") + s[end:]
			} else {
				next = joinBlock(s, block)
			}
		} else {
			next = joinBlock(s, block)
		}
	} else {
		next = block
	}
	action := "created"
	if existed {
		if strings.TrimRight(string(orig), "\n") == strings.TrimRight(next, "\n") {
			action = "unchanged"
		} else {
			action = "updated"
		}
	}
	if action != "unchanged" && !dry {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return FileAction{}, err
		}
		if err := os.WriteFile(path, []byte(ensureTrailingNewline(next)), 0o644); err != nil {
			return FileAction{}, err
		}
	}
	return FileAction{Path: path, Action: action}, nil
}

// writeGenerated writes a fully-generated file, reporting created/updated/
// unchanged from a byte compare.
func writeGenerated(path, content string, dry bool) (FileAction, error) {
	orig, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return FileAction{}, readErr
	}
	action := "created"
	if existed {
		if string(orig) == content {
			action = "unchanged"
		} else {
			action = "updated"
		}
	}
	if action != "unchanged" && !dry {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return FileAction{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return FileAction{}, err
		}
	}
	return FileAction{Path: path, Action: action}, nil
}

// --- small helpers ---

func joinBlock(existing, block string) string {
	e := strings.TrimRight(existing, "\n")
	if e == "" {
		return block
	}
	return e + "\n\n" + strings.TrimRight(block, "\n")
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// marshalJSON encodes v with 2-space indent and HTML escaping off (matching the
// CLI's printJSON conventions).
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsonServerSnippet renders just the {topKey:{serverName:server}} object for a
// print-don't-write fallback.
func jsonServerSnippet(topKey, serverName string, server any) string {
	b, _ := marshalJSON(map[string]any{topKey: map[string]any{serverName: server}})
	return strings.TrimRight(string(b), "\n")
}

// detectFromJSON builds a Detect func for a project-scoped JSON harness: present
// when the signature dir exists, registered when topKey.codemap exists.
func detectFromJSON(name, sigDir, relConfig, topKey string) func(string, bool) Detection {
	return func(dir string, global bool) Detection {
		cfg := filepath.Join(dir, filepath.FromSlash(relConfig))
		return Detection{
			Name:       name,
			Present:    dirExists(filepath.Join(dir, sigDir)) || fileExists(cfg),
			ConfigPath: cfg,
			Registered: jsonHasServer(cfg, topKey, "codemap"),
		}
	}
}

// jsonHasServer reports whether path parses as JSON with topKey.serverName set.
func jsonHasServer(path, topKey, serverName string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return false
	}
	sub, _ := root[topKey].(map[string]any)
	if sub == nil {
		return false
	}
	_, ok := sub[serverName]
	return ok
}

// markedBlockPresent reports whether path already carries a codemap marked block.
func markedBlockPresent(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), markerBeginPrefix)
}

// cleanJSON reports whether path is absent or strictly valid JSON (so writing it
// won't clobber JSONC comments). Absent is clean — we can create it.
func cleanJSON(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return json.Valid(b)
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// configHome resolves $XDG_CONFIG_HOME (fallback ~/.config).
func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(homeDir(), ".config")
}

// clineSettingsPath returns the per-OS VS Code globalStorage path for Cline's
// MCP settings (verified macOS 2026-07-12; Linux/Windows mirror the VS Code
// user-data layout). Empty when the home dir is unknown.
func clineSettingsPath() string {
	home := homeDir()
	if home == "" {
		return ""
	}
	const rel = "globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json"
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", filepath.FromSlash(rel))
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Code", "User", filepath.FromSlash(rel))
		}
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", filepath.FromSlash(rel))
	default:
		return filepath.Join(configHome(), "Code", "User", filepath.FromSlash(rel))
	}
}
