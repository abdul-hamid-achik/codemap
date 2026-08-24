package tooling

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultProbeTimeout bounds a health check so a hung binary cannot stall
// doctor/index startup.
const DefaultProbeTimeout = 3 * time.Second

// ProbeResult is the outcome of resolving and running a short health check on
// an external binary.
type ProbeResult struct {
	Path     string // absolute path from LookPath, empty if not found
	OK       bool
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// Resolve returns the LookPath location of name, or an error if missing.
func Resolve(name string) (string, error) {
	return exec.LookPath(name)
}

// Probe resolves name on PATH and runs a short health check with cwd set (when
// non-empty) so version-manager shims (asdf/mise/nvm) evaluate project pins.
//
// A missing binary yields OK=false. A version-manager dead shim (exit 126 +
// asdf/mise message) yields OK=false. A binary that starts and only rejects
// CLI flags (e.g. pyright-langserver without --stdio, gopls without a known
// flag) is treated as OK — the process is runnable, which is what index needs.
func Probe(ctx context.Context, name, cwd string) ProbeResult {
	path, err := exec.LookPath(name)
	if err != nil {
		return ProbeResult{Err: err, ExitCode: 127}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pr := runProbe(ctx, path, probeArgs(name), cwd)
	if probeUsable(pr) {
		return markProbeOK(pr)
	}
	// asdf/mise shims on PATH exit 126 when the project doesn't pin the
	// plugin's runtime (e.g. typescript-language-server installed under
	// nodejs 22.22.0, cwd only pins golang). The real binary still lives
	// under the version-manager install tree — spawn that instead of
	// skipping the language.
	if alt := unwrapVersionManagerShim(path); alt != "" && alt != path {
		pr2 := runProbe(ctx, alt, probeArgs(name), cwd)
		if probeUsable(pr2) {
			return markProbeOK(pr2)
		}
	}
	// Binary ran and rejected the probe args → still usable for LSP spawn.
	if isRunnableReject(pr.Stderr, pr.Stdout, pr.ExitCode) {
		return markProbeOK(pr)
	}
	// Some tools only speak stdio; a near-instant exit with empty stderr after
	// --version can still mean "not the real server". Leave OK=false.
	return pr
}

func probeUsable(pr ProbeResult) bool {
	return pr.OK || isRunnableReject(pr.Stderr, pr.Stdout, pr.ExitCode)
}

func markProbeOK(pr ProbeResult) ProbeResult {
	pr.OK = true
	pr.Err = nil
	return pr
}

// unwrapVersionManagerShim maps an asdf shim to the real install binary
// (…/installs/<plugin>/<version>/bin/<name>) when the shim itself cannot run
// under the project cwd. Empty when this is not an asdf shim or no install
// exists.
func unwrapVersionManagerShim(shimPath string) string {
	data, err := os.ReadFile(shimPath)
	if err != nil || len(data) == 0 || len(data) > 8<<10 {
		return ""
	}
	text := string(data)
	if !strings.Contains(text, "asdf exec") && !strings.Contains(text, "asdf-plugin") {
		return ""
	}
	base := filepath.Base(shimPath)
	asdfData := os.Getenv("ASDF_DATA_DIR")
	if asdfData == "" {
		home, herr := os.UserHomeDir()
		if herr != nil || home == "" {
			return ""
		}
		asdfData = filepath.Join(home, ".asdf")
	}
	if plugin, version := parseAsdfPluginComment(text); plugin != "" && version != "" {
		cand := filepath.Join(asdfData, "installs", plugin, version, "bin", base)
		if isExecutableFile(cand) {
			return cand
		}
	}
	matches, _ := filepath.Glob(filepath.Join(asdfData, "installs", "*", "*", "bin", base))
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		if isExecutableFile(matches[i]) {
			return matches[i]
		}
	}
	return ""
}

func parseAsdfPluginComment(text string) (plugin, version string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "# asdf-plugin:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) >= 2 {
			return fields[0], fields[1]
		}
	}
	return "", ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// ProbeOrClassify is a convenience for doctor/index: resolve+probe and return
// either nil (healthy) or a classified Issue.
func ProbeOrClassify(ctx context.Context, binary, cwd string, langs []string) *Issue {
	pr := Probe(ctx, binary, cwd)
	if pr.Path == "" {
		iss := ClassifyNotFound(binary, cwd, langs)
		return &iss
	}
	if pr.OK {
		return nil
	}
	iss := ClassifyProbeFailure(binary, cwd, langs, pr)
	return &iss
}

func probeArgs(name string) []string {
	base := filepath.Base(name)
	switch base {
	case "go", "gopls":
		return []string{"version"}
	default:
		// typescript-language-server supports --version; pyright-langserver
		// does not, but isRunnableReject treats its connection error as healthy.
		return []string{"--version"}
	}
}

func runProbe(ctx context.Context, path string, args []string, cwd string) ProbeResult {
	pctx, cancel := context.WithTimeout(ctx, DefaultProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(pctx, path, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	pr := ProbeResult{
		Path:   path,
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
		Err:    runErr,
	}
	if runErr == nil {
		pr.OK = true
		return pr
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		pr.ExitCode = ee.ExitCode()
	} else if errors.Is(runErr, context.DeadlineExceeded) || pctx.Err() != nil {
		pr.ExitCode = -1
		if pr.Stderr == "" {
			pr.Stderr = "probe timed out"
		}
	} else {
		pr.ExitCode = 126
	}
	return pr
}

// isRunnableReject reports that the binary started and rejected our probe
// args — evidence it is executable and not a dead version-manager shim.
func isRunnableReject(stderr, stdout string, exitCode int) bool {
	s := strings.ToLower(stderr + "\n" + stdout)
	if isVersionManagerFailure(s, exitCode) {
		return false
	}
	patterns := []string{
		"connection input stream is not set",
		"use arguments of createconnection",
		"--stdio",
		"flag provided but not defined",
		"unknown flag",
		"unknown option",
		"unknown command",
		"invalid option",
		"usage:",
		"usage of",
	}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
