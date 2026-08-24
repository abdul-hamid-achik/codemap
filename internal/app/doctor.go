package app

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
)

// DoctorCheck is one environment check: whether a toolchain, language server, or
// service codemap can use is present, with a remediation hint when it isn't.
// Code / Probe / AgentFix are additive agent-facing fields: OK is still the
// primary boolean, but agents should prefer Code + AgentFix over parsing Hint.
type DoctorCheck struct {
	Name     string            `json:"name"`
	OK       bool              `json:"ok"`
	Detail   string            `json:"detail,omitempty"`
	Hint     string            `json:"hint,omitempty"` // remediation, set only when !OK
	Code     string            `json:"code,omitempty"`
	Probe    *DoctorProbe      `json:"probe,omitempty"`
	AgentFix *tooling.AgentFix `json:"agent_fix,omitempty"`
}

// DoctorProbe is the executable check behind a language-server DoctorCheck
// (path + --version under project cwd when known).
type DoctorProbe struct {
	Path     string `json:"path,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
}

// DoctorReport is the result of `codemap doctor` — the data location plus a list
// of environment checks (toolchains, language servers, embeddings). Nothing here
// is required for the core graph; each missing piece just disables one capability.
type DoctorReport struct {
	DataDir string `json:"data_dir"`
	// ProjectRoot, when set, is the directory used as cwd for language-server
	// probes so asdf/mise project pins are evaluated.
	ProjectRoot string        `json:"project_root,omitempty"`
	Checks      []DoctorCheck `json:"checks"`
}

// availabler is the optional reachability probe an embedding provider may expose
// (Ollama does); other providers (e.g. a test fake) simply skip the network check.
type availabler interface {
	Available(context.Context) error
}

// Doctor inspects the local environment so a user can see, before indexing, which
// languages and features are ready. Equivalent to DoctorAt(ctx, "").
func (svc *Service) Doctor(ctx context.Context) *DoctorReport {
	return svc.DoctorAt(ctx, "")
}

// DoctorAt is Doctor with an optional project root. When cwd is non-empty,
// language-server probes run with that directory as the process cwd so
// version-manager shims (asdf .tool-versions, mise, nvm) are evaluated the
// same way registerLSP does during index — a global LookPath success is not
// enough if the project pin lacks the package.
func (svc *Service) DoctorAt(ctx context.Context, cwd string) *DoctorReport {
	root := strings.TrimSpace(cwd)
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	rep := &DoctorReport{DataDir: config.DataDir(), ProjectRoot: root}
	add := func(c DoctorCheck) {
		rep.Checks = append(rep.Checks, c)
	}
	addSimple := func(name string, ok bool, detail, hint string) {
		c := DoctorCheck{Name: name, OK: ok, Detail: detail}
		if !ok {
			c.Hint = hint
		}
		add(c)
	}

	// Data directory (created on first index; a missing dir is fine).
	dir := config.DataDir()
	_, statErr := os.Stat(dir)
	addSimple("data directory", statErr == nil || os.IsNotExist(statErr), dir,
		"check permissions on the data directory")

	// Toolchains for the Go precise paths — probe --version when present so a
	// broken shim doesn't look healthy.
	addTool(ctx, add, "go toolchain", "go", root, "index --precise on Go", "install Go: https://go.dev/dl")
	addTool(ctx, add, "gopls", "gopls", root, "callers/callees --precise on Go", "go install golang.org/x/tools/gopls@latest")

	// Language servers for the LSP-backed languages.
	for _, spec := range lspsrc.DefaultServers {
		langs := specLangs(spec)
		name := spec.Cmd + " (" + langs + ")"
		addLanguageServer(ctx, add, name, spec.Cmd, root, strings.Split(langs, "/"))
	}

	// Embeddings (Ollama) for semantic search — structure/queries work without it.
	embedder := svc.s.Embedder()
	model := ""
	if embedder != nil {
		model = embedder.Profile().Model
	}
	switch a, isProbe := embedder.(availabler); {
	case embedder == nil:
		addSimple("embeddings (Ollama)", false, "no embedder configured",
			"configure an embedding model for semantic search")
	case isProbe:
		ectx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := a.Available(ectx); err != nil {
			addSimple("embeddings (Ollama)", false, err.Error(),
				"start Ollama and `ollama pull "+model+"` for semantic search (structure works without it)")
		} else {
			addSimple("embeddings (Ollama)", true, "model "+model, "")
		}
	default:
		addSimple("embeddings (Ollama)", true, "model "+model, "")
	}

	// Embedding auth: report only whether a key is configured, never its value
	// — the key itself must never appear in doctor output (config file, env
	// CODEMAP_OLLAMA_API_KEY, no CLI flag; see docs/configuration.md).
	if svc.s.Config.Embedding.APIKey != "" {
		addSimple("embedding auth", true, "configured", "")
	} else {
		addSimple("embedding auth", true, "not set (fine for a local, unauthenticated Ollama)", "")
	}

	// Background daemon (optional): is one watching the tree + keeping the index
	// fresh? A plain socket-connect probe — app can't import the daemon package
	// (that would cycle: daemon imports app), and connectivity is enough for a
	// health check. It also avoids resetting the daemon's idle timer.
	if daemonReachable() {
		addSimple("background daemon", true, "running — keeping the index fresh", "")
	} else {
		addSimple("background daemon", false, "not running (optional)",
			"run 'codemap daemon start' to watch the tree and reindex automatically")
	}

	return rep
}

func addTool(ctx context.Context, add func(DoctorCheck), name, bin, cwd, purpose, installHint string) {
	pr := tooling.Probe(ctx, bin, cwd)
	if !pr.OK {
		iss := classifyProbeResult(bin, cwd, nil, pr)
		c := DoctorCheck{
			Name:     name,
			OK:       false,
			Detail:   doctorDetail(&iss, purpose),
			Hint:     doctorHint(&iss, installHint),
			Code:     iss.Code,
			AgentFix: iss.AgentFix,
			Probe:    doctorProbeFromIssue(&iss),
		}
		add(c)
		return
	}
	path := pr.Path
	if path == "" {
		path, _ = exec.LookPath(bin)
	}
	add(DoctorCheck{Name: name, OK: true, Detail: path})
}

func addLanguageServer(ctx context.Context, add func(DoctorCheck), name, bin, cwd string, langs []string) {
	pr := tooling.Probe(ctx, bin, cwd)
	if !pr.OK {
		iss := classifyProbeResult(bin, cwd, langs, pr)
		c := DoctorCheck{
			Name:     name,
			OK:       false,
			Detail:   doctorDetail(&iss, "index "+strings.Join(langs, "/")),
			Hint:     doctorHint(&iss, "install "+bin+" to index "+strings.Join(langs, "/")),
			Code:     iss.Code,
			AgentFix: iss.AgentFix,
			Probe:    doctorProbeFromIssue(&iss),
		}
		add(c)
		return
	}
	path := pr.Path
	if path == "" {
		path, _ = exec.LookPath(bin)
	}
	detail := path
	if cwd != "" {
		detail = path + " (probe ok under " + cwd + ")"
	}
	add(DoctorCheck{Name: name, OK: true, Detail: detail})
}

func classifyProbeResult(bin, cwd string, langs []string, pr tooling.ProbeResult) tooling.Issue {
	if pr.Path == "" {
		return tooling.ClassifyNotFound(bin, cwd, langs)
	}
	return tooling.ClassifyProbeFailure(bin, cwd, langs, pr)
}

func doctorDetail(iss *tooling.Issue, purpose string) string {
	if iss == nil {
		return ""
	}
	switch iss.Code {
	case tooling.CodeNotFound:
		return "not found — " + purpose
	case tooling.CodeVersionManagerGap:
		p := iss.ResolvedPath
		if p == "" {
			p = iss.Binary
		}
		msg := p + " failed under project runtime"
		if iss.ExitCode != nil {
			msg += " (exit " + strconv.Itoa(*iss.ExitCode) + ")"
		}
		if iss.Stderr != "" {
			// First line of stderr is usually enough in the detail column.
			line := strings.Split(iss.Stderr, "\n")[0]
			msg += ": " + line
		}
		return msg
	default:
		if iss.Detail != "" {
			return iss.Detail
		}
		return "failed — " + purpose
	}
}

func doctorHint(iss *tooling.Issue, fallback string) string {
	if iss == nil {
		return fallback
	}
	if iss.Code == tooling.CodeVersionManagerGap {
		return "install " + iss.Binary + " into the project's active version-manager runtime (not only a global/other version), then re-run doctor from the project root; see agent_fix"
	}
	if iss.Code == tooling.CodeNotFound {
		return fallback
	}
	if iss.AgentFix != nil && iss.AgentFix.Goal != "" {
		return iss.AgentFix.Goal
	}
	return fallback
}

func doctorProbeFromIssue(iss *tooling.Issue) *DoctorProbe {
	if iss == nil {
		return nil
	}
	p := &DoctorProbe{Path: iss.ResolvedPath, Stderr: iss.Stderr}
	if iss.ExitCode != nil {
		p.ExitCode = *iss.ExitCode
	}
	return p
}

// daemonReachable reports whether a codemap daemon is listening on its control
// socket. A stale socket file with no listener fails the dial, so this is true
// only for a live daemon.
func daemonReachable() bool {
	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func specLangs(spec lspsrc.ServerSpec) string {
	names := make([]string, len(spec.Langs))
	for i, lb := range spec.Langs {
		names[i] = lb.Lang
	}
	return strings.Join(names, "/")
}
