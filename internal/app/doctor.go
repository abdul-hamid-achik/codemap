package app

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
)

// DoctorCheck is one environment check: whether a toolchain, language server, or
// service codemap can use is present, with a remediation hint when it isn't.
type DoctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"` // remediation, set only when !OK
}

// DoctorReport is the result of `codemap doctor` — the data location plus a list
// of environment checks (toolchains, language servers, embeddings). Nothing here
// is required for the core graph; each missing piece just disables one capability.
type DoctorReport struct {
	DataDir string        `json:"data_dir"`
	Checks  []DoctorCheck `json:"checks"`
}

// availabler is the optional reachability probe an embedding provider may expose
// (Ollama does); other providers (e.g. a test fake) simply skip the network check.
type availabler interface {
	Available(context.Context) error
}

// Doctor inspects the local environment so a user can see, before indexing, which
// languages and features are ready: the go toolchain (`--precise` Go), gopls
// (one-off callers/callees `--precise`), each language server (TypeScript/JavaScript, Python), and Ollama
// embeddings (semantic search). It makes no changes and needs no index.
func (svc *Service) Doctor(ctx context.Context) *DoctorReport {
	rep := &DoctorReport{DataDir: config.DataDir()}
	add := func(name string, ok bool, detail, hint string) {
		c := DoctorCheck{Name: name, OK: ok, Detail: detail}
		if !ok {
			c.Hint = hint
		}
		rep.Checks = append(rep.Checks, c)
	}

	// Data directory (created on first index; a missing dir is fine).
	dir := config.DataDir()
	_, statErr := os.Stat(dir)
	add("data directory", statErr == nil || os.IsNotExist(statErr), dir,
		"check permissions on the data directory")

	// Toolchains for the Go precise paths.
	tool := func(name, bin, purpose, hint string) {
		if path, err := exec.LookPath(bin); err == nil {
			add(name, true, path, "")
		} else {
			add(name, false, "not found — "+purpose, hint)
		}
	}
	tool("go toolchain", "go", "index --precise on Go", "install Go: https://go.dev/dl")
	tool("gopls", "gopls", "callers/callees --precise on Go", "go install golang.org/x/tools/gopls@latest")

	// Language servers for the LSP-backed languages.
	for _, spec := range lspsrc.DefaultServers {
		langs := specLangs(spec)
		if path, err := exec.LookPath(spec.Cmd); err == nil {
			add(spec.Cmd+" ("+langs+")", true, path, "")
		} else {
			add(spec.Cmd+" ("+langs+")", false, "not found", "install "+spec.Cmd+" to index "+langs)
		}
	}

	// Embeddings (Ollama) for semantic search — structure/queries work without it.
	embedder := svc.s.Embedder()
	model := ""
	if embedder != nil {
		model = embedder.Profile().Model
	}
	switch a, isProbe := embedder.(availabler); {
	case embedder == nil:
		add("embeddings (Ollama)", false, "no embedder configured",
			"configure an embedding model for semantic search")
	case isProbe:
		ectx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := a.Available(ectx); err != nil {
			add("embeddings (Ollama)", false, err.Error(),
				"start Ollama and `ollama pull "+model+"` for semantic search (structure works without it)")
		} else {
			add("embeddings (Ollama)", true, "model "+model, "")
		}
	default:
		add("embeddings (Ollama)", true, "model "+model, "")
	}

	// Embedding auth: report only whether a key is configured, never its value
	// — the key itself must never appear in doctor output (config file, env
	// CODEMAP_OLLAMA_API_KEY, no CLI flag; see docs/configuration.md).
	if svc.s.Config.Embedding.APIKey != "" {
		add("embedding auth", true, "configured", "")
	} else {
		add("embedding auth", true, "not set (fine for a local, unauthenticated Ollama)", "")
	}

	// Background daemon (optional): is one watching the tree + keeping the index
	// fresh? A plain socket-connect probe — app can't import the daemon package
	// (that would cycle: daemon imports app), and connectivity is enough for a
	// health check. It also avoids resetting the daemon's idle timer.
	if daemonReachable() {
		add("background daemon", true, "running — keeping the index fresh", "")
	} else {
		add("background daemon", false, "not running (optional)",
			"run 'codemap daemon start' to watch the tree and reindex automatically")
	}

	return rep
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
