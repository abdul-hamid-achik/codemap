package app

import (
	"context"
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
// (`--lsp`), each language server (TypeScript/JavaScript, Python), and Ollama
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
	tool("gopls", "gopls", "callers/callees --lsp on Go", "go install golang.org/x/tools/gopls@latest")

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

	return rep
}

func specLangs(spec lspsrc.ServerSpec) string {
	names := make([]string, len(spec.Langs))
	for i, lb := range spec.Langs {
		names[i] = lb.Lang
	}
	return strings.Join(names, "/")
}
