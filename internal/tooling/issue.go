// Package tooling classifies external binary failures (language servers,
// toolchains) into stable, agent-repairable issue records. Index and doctor
// surface the same shape so a harness can switch on Code and follow AgentFix
// instead of parsing free-form warnings.
package tooling

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Stable issue codes. Agents should switch on these; free-form Detail/Stderr
// are evidence, not the primary control plane.
const (
	CodeNotFound          = "lsp_not_found"
	CodeNotExecutable     = "lsp_not_executable"
	CodeVersionManagerGap = "lsp_version_manager_gap"
	CodeSpawnFailed       = "lsp_spawn_failed"
	CodeInitFailed        = "lsp_init_failed"
	CodeCapabilityMissing = "lsp_capability_missing"
)

// Issue is one tooling failure that blocked or degraded a codemap capability.
// Additive JSON: consumers that only read Warning keep working.
type Issue struct {
	Code           string          `json:"code"`
	Severity       string          `json:"severity"` // "error" | "warning"
	Languages      []string        `json:"languages,omitempty"`
	FilesAffected  int             `json:"files_affected,omitempty"`
	Binary         string          `json:"binary"`
	ResolvedPath   string          `json:"resolved_path,omitempty"`
	Source         string          `json:"source,omitempty"` // path | path_shim | …
	ExitCode       *int            `json:"exit_code,omitempty"`
	Stderr         string          `json:"stderr,omitempty"`
	Detail         string          `json:"detail,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	VersionManager *VersionManager `json:"version_manager,omitempty"`
	AgentFix       *AgentFix       `json:"agent_fix,omitempty"`
}

// VersionManager describes a project-scoped runtime manager (asdf/mise/nvm)
// that can make a PATH shim resolve differently than a global install.
type VersionManager struct {
	Kind                string `json:"kind,omitempty"` // asdf | mise | nvm | unknown
	ProjectToolVersions string `json:"project_tool_versions,omitempty"`
	Hint                string `json:"hint,omitempty"`
}

// AgentFix is a short, ordered remediation an agent can run without inventing
// package-manager knowledge. Steps are best-effort; Verify re-checks success.
type AgentFix struct {
	Goal   string    `json:"goal"`
	Verify []string  `json:"verify,omitempty"`
	Steps  []FixStep `json:"steps"`
}

// FixStep is one shell-oriented remediation action.
type FixStep struct {
	ID          string   `json:"id"`
	Run         string   `json:"run,omitempty"`
	Alt         []string `json:"alt,omitempty"`
	Expect      string   `json:"expect,omitempty"`
	SuccessWhen string   `json:"success_when,omitempty"`
}

// ClassifyNotFound builds an issue when LookPath cannot resolve the binary.
func ClassifyNotFound(binary, cwd string, langs []string) Issue {
	iss := Issue{
		Code:      CodeNotFound,
		Severity:  "error",
		Languages: langs,
		Binary:    binary,
		Cwd:       cwd,
		Detail:    "binary not found on PATH",
		Source:    "path",
	}
	iss.VersionManager = DetectVersionManager(cwd)
	iss.AgentFix = BuildAgentFix(binary, CodeNotFound, cwd)
	return iss
}

// ClassifyProbeFailure builds an issue when the binary was found but a probe
// (typically `binary --version`) failed. Distinguishes version-manager shims
// that exist on PATH yet exit non-zero under a project cwd.
func ClassifyProbeFailure(binary, cwd string, langs []string, pr ProbeResult) Issue {
	code := CodeSpawnFailed
	detail := "binary found but failed to run"
	stderr := strings.TrimSpace(pr.Stderr)
	if pr.Err != nil && stderr == "" {
		stderr = pr.Err.Error()
	}
	if isVersionManagerFailure(stderr, pr.ExitCode) {
		code = CodeVersionManagerGap
		detail = "PATH shim found but failed under project runtime (version manager)"
	} else if pr.ExitCode == 126 || isNotExecutable(stderr) {
		code = CodeNotExecutable
		detail = "binary found but is not executable"
	}
	var exitPtr *int
	if pr.ExitCode != 0 || pr.Path != "" {
		ec := pr.ExitCode
		exitPtr = &ec
	}
	iss := Issue{
		Code:         code,
		Severity:     "error",
		Languages:    langs,
		Binary:       binary,
		ResolvedPath: pr.Path,
		Source:       pathSource(pr.Path),
		ExitCode:     exitPtr,
		Stderr:       truncate(stderr, 2<<10),
		Detail:       detail,
		Cwd:          cwd,
	}
	iss.VersionManager = DetectVersionManager(cwd)
	if iss.VersionManager == nil && code == CodeVersionManagerGap {
		iss.VersionManager = &VersionManager{
			Kind: detectKindFromStderr(stderr),
			Hint: "shim resolves via a version manager; the package may be missing under the project's active runtime version",
		}
	} else if iss.VersionManager != nil && code == CodeVersionManagerGap && iss.VersionManager.Hint == "" {
		iss.VersionManager.Hint = "shim resolves via project version pins; binary may exist only under another runtime version"
	}
	iss.AgentFix = BuildAgentFix(binary, code, cwd)
	return iss
}

// ClassifySpawnError builds an issue when LookPath/probe succeeded (or was
// skipped) but lspsrc.New / LSP initialize failed.
func ClassifySpawnError(binary, path, cwd string, langs []string, err error, stderr string) Issue {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	combined := strings.TrimSpace(stderr + "\n" + msg)
	code := CodeInitFailed
	detail := "language server failed to start or initialize"
	if isVersionManagerFailure(combined, 0) {
		code = CodeVersionManagerGap
		detail = "language server shim failed under project runtime (version manager)"
	} else if strings.Contains(strings.ToLower(combined), "does not advertise") {
		code = CodeCapabilityMissing
		detail = combined
	} else if err != nil && (strings.Contains(msg, "executable file not found") || strings.Contains(msg, "no such file")) {
		code = CodeNotFound
		detail = "binary disappeared or failed to exec"
	} else if err != nil && strings.Contains(strings.ToLower(msg), "permission denied") {
		code = CodeNotExecutable
		detail = "permission denied spawning language server"
	} else if stderr != "" || (err != nil && (strings.Contains(msg, "EOF") || strings.Contains(msg, "exit"))) {
		code = CodeSpawnFailed
		detail = "language server process exited before initialize completed"
	}
	iss := Issue{
		Code:         code,
		Severity:     "error",
		Languages:    langs,
		Binary:       binary,
		ResolvedPath: path,
		Source:       pathSource(path),
		Stderr:       truncate(strings.TrimSpace(stderr), 2<<10),
		Detail:       detail,
		Cwd:          cwd,
	}
	if msg != "" && iss.Stderr == "" {
		iss.Detail = detail + ": " + msg
	} else if msg != "" && !strings.Contains(iss.Detail, msg) {
		iss.Detail = detail + ": " + msg
	}
	iss.VersionManager = DetectVersionManager(cwd)
	iss.AgentFix = BuildAgentFix(binary, code, cwd)
	return iss
}

// DetectVersionManager inspects cwd for asdf/mise/nvm pin files.
func DetectVersionManager(cwd string) *VersionManager {
	if cwd == "" {
		return nil
	}
	if raw, err := os.ReadFile(filepath.Join(cwd, ".tool-versions")); err == nil {
		text := strings.TrimSpace(string(raw))
		return &VersionManager{
			Kind:                "asdf",
			ProjectToolVersions: truncate(text, 512),
			Hint:                "shim resolves via project .tool-versions; binary may exist only under another runtime version",
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, ".mise.toml")); err == nil {
		return &VersionManager{
			Kind: "mise",
			Hint: "mise project pin may select a runtime that lacks this package",
		}
	}
	if raw, err := os.ReadFile(filepath.Join(cwd, ".nvmrc")); err == nil {
		return &VersionManager{
			Kind:                "nvm",
			ProjectToolVersions: "node " + strings.TrimSpace(string(raw)),
			Hint:                "nvm project pin may select a Node that lacks this global package",
		}
	}
	if raw, err := os.ReadFile(filepath.Join(cwd, ".node-version")); err == nil {
		return &VersionManager{
			Kind:                "node",
			ProjectToolVersions: "node " + strings.TrimSpace(string(raw)),
			Hint:                "project Node pin may select a runtime that lacks this package",
		}
	}
	return nil
}

// BuildAgentFix returns ordered remediation steps for a known language-server
// binary. Unknown binaries get a generic PATH/install checklist.
func BuildAgentFix(binary, code, cwd string) *AgentFix {
	root := cwd
	if root == "" {
		root = "."
	}
	// Quote-safe for display; agents substitute the real root.
	cd := "cd " + shellQuote(root)

	fix := &AgentFix{
		Goal: fmt.Sprintf("make %s runnable under the project runtime, then reindex", binary),
		Verify: []string{
			cd + " && " + binary + " --version",
			"codemap doctor --json",
			"codemap index --json",
		},
	}

	switch binary {
	case "typescript-language-server":
		fix.Steps = []FixStep{
			{
				ID:     "probe",
				Run:    cd + " && command -v typescript-language-server && typescript-language-server --version",
				Expect: "exit 0 and a semver (not an asdf/mise 'no version is set' error)",
			},
			{
				ID:  "install_into_active_node",
				Run: cd + " && npm install -g typescript-language-server typescript && (asdf reshim nodejs 2>/dev/null || mise reshim 2>/dev/null || true)",
				Alt: []string{
					cd + " && npm install -g --prefix \"$(asdf where nodejs 2>/dev/null || dirname $(dirname $(command -v node)))\" typescript-language-server typescript",
					cd + " && corepack npm install -g typescript-language-server typescript",
				},
				Expect: "typescript-language-server --version succeeds in the project directory",
			},
			{
				ID:          "reindex",
				Run:         "codemap index --json",
				SuccessWhen: "tooling.issues has no typescript entry AND languages.typescript > 0 (or degraded is false)",
			},
		}
	case "pyright-langserver":
		fix.Steps = []FixStep{
			{
				ID:     "probe",
				Run:    cd + " && command -v pyright-langserver && pyright-langserver --version",
				Expect: "exit 0",
			},
			{
				ID:  "install",
				Run: cd + " && npm install -g pyright && (asdf reshim nodejs 2>/dev/null || true)",
				Alt: []string{cd + " && npm install -g --prefix \"$(asdf where nodejs)\" pyright"},
			},
			{
				ID:          "reindex",
				Run:         "codemap index --json",
				SuccessWhen: "languages.python present when the project has .py files; tooling.issues empty for python",
			},
		}
	case "gopls":
		fix.Steps = []FixStep{
			{ID: "install", Run: "go install golang.org/x/tools/gopls@latest"},
			{ID: "verify", Run: "gopls version"},
		}
	default:
		fix.Steps = []FixStep{
			{ID: "probe", Run: cd + " && command -v " + binary + " && " + binary + " --version"},
			{ID: "install", Run: "install " + binary + " so it is on PATH for this project's runtime"},
		}
	}

	if code == CodeVersionManagerGap {
		fix.Goal = fmt.Sprintf("install %s into the project's active runtime version (not only a global/other Node), then reindex", binary)
	}
	return fix
}

// WarningLine is a single human/agent-readable sentence for Warning prose.
// Structured fields remain the source of truth.
func WarningLine(iss Issue) string {
	n := iss.FilesAffected
	lang := strings.Join(iss.Languages, "/")
	if lang == "" {
		lang = "language"
	}
	files := ""
	if n > 0 {
		files = fmt.Sprintf("%d %s file(s) skipped — ", n, lang)
	} else {
		files = lang + " — "
	}
	switch iss.Code {
	case CodeNotFound:
		return files + fmt.Sprintf("%q not found on PATH (install it, or run with --no-lsp)", iss.Binary)
	case CodeVersionManagerGap:
		path := iss.ResolvedPath
		if path == "" {
			path = iss.Binary
		}
		extra := ""
		if iss.ExitCode != nil {
			extra = fmt.Sprintf(" (exit %d)", *iss.ExitCode)
		}
		return files + fmt.Sprintf("%q found at %s but failed under project runtime%s — install the package into the active version-manager runtime, then reindex; see tooling.issues",
			iss.Binary, path, extra)
	case CodeNotExecutable:
		return files + fmt.Sprintf("%q is not executable — check permissions/arch; see tooling.issues", iss.Binary)
	case CodeCapabilityMissing:
		return files + iss.Detail
	default:
		path := iss.ResolvedPath
		if path == "" {
			path = iss.Binary
		}
		return files + fmt.Sprintf("%q failed to start (%s) — see tooling.issues for stderr and agent_fix", path, iss.Code)
	}
}

func isVersionManagerFailure(stderr string, exitCode int) bool {
	s := strings.ToLower(stderr)
	if strings.Contains(s, "no version is set for command") ||
		strings.Contains(s, "no version is set") ||
		strings.Contains(s, "asdf") && strings.Contains(s, "version") ||
		strings.Contains(s, "mise") && (strings.Contains(s, "not installed") || strings.Contains(s, "version")) ||
		strings.Contains(s, "nvm") && strings.Contains(s, "version") ||
		strings.Contains(s, "consider adding one of the following versions") ||
		strings.Contains(s, ".tool-versions") {
		return true
	}
	// asdf often exits 126 when the shim cannot resolve a version for the cmd.
	if exitCode == 126 && (strings.Contains(s, "version") || strings.Contains(s, "nodejs") || strings.Contains(s, "node")) {
		return true
	}
	return false
}

func isNotExecutable(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "permission denied") || strings.Contains(s, "not executable") || strings.Contains(s, "exec format error")
}

func detectKindFromStderr(stderr string) string {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "asdf") || strings.Contains(s, ".tool-versions"):
		return "asdf"
	case strings.Contains(s, "mise"):
		return "mise"
	case strings.Contains(s, "nvm"):
		return "nvm"
	default:
		return "unknown"
	}
}

func pathSource(path string) string {
	if path == "" {
		return "path"
	}
	if strings.Contains(path, "/shims/") || strings.Contains(path, "asdf") || strings.Contains(path, "mise") {
		return "path_shim"
	}
	return "path"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
