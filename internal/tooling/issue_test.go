package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyNotFound(t *testing.T) {
	iss := ClassifyNotFound("typescript-language-server", "/proj", []string{"typescript", "javascript"})
	if iss.Code != CodeNotFound {
		t.Fatalf("code = %q", iss.Code)
	}
	if iss.AgentFix == nil || len(iss.AgentFix.Steps) == 0 {
		t.Fatal("expected agent_fix steps")
	}
	line := WarningLine(iss)
	if !strings.Contains(line, "not found on PATH") {
		t.Fatalf("warning = %q", line)
	}
}

func TestClassifyProbeFailureVersionManager(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tool-versions"), []byte("nodejs 24.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit := 126
	pr := ProbeResult{
		Path:     "/home/u/.asdf/shims/typescript-language-server",
		ExitCode: exit,
		Stderr:   "No version is set for command typescript-language-server\nConsider adding one of the following versions in your config file at " + dir + "/.tool-versions\nnodejs 24.18.0\n",
		Err:      errExit(126),
	}
	iss := ClassifyProbeFailure("typescript-language-server", dir, []string{"typescript"}, pr)
	if iss.Code != CodeVersionManagerGap {
		t.Fatalf("code = %q, want %s", iss.Code, CodeVersionManagerGap)
	}
	if iss.VersionManager == nil || iss.VersionManager.Kind != "asdf" {
		t.Fatalf("version_manager = %+v", iss.VersionManager)
	}
	if iss.VersionManager.ProjectToolVersions == "" || !strings.Contains(iss.VersionManager.ProjectToolVersions, "24.17.0") {
		t.Fatalf("project pins = %q", iss.VersionManager.ProjectToolVersions)
	}
	if iss.ExitCode == nil || *iss.ExitCode != 126 {
		t.Fatalf("exit_code = %v", iss.ExitCode)
	}
	if iss.Source != "path_shim" {
		t.Fatalf("source = %q", iss.Source)
	}
	if iss.AgentFix == nil || !strings.Contains(iss.AgentFix.Goal, "active runtime") {
		t.Fatalf("agent_fix.goal = %v", iss.AgentFix)
	}
	line := WarningLine(iss)
	if !strings.Contains(line, "tooling.issues") || !strings.Contains(line, "failed under project runtime") {
		t.Fatalf("warning should point at tooling.issues, got %q", line)
	}
}

func TestClassifyProbeFailureNotExecutable(t *testing.T) {
	pr := ProbeResult{
		Path:     "/usr/local/bin/typescript-language-server",
		ExitCode: 126,
		Stderr:   "permission denied",
	}
	iss := ClassifyProbeFailure("typescript-language-server", "", []string{"typescript"}, pr)
	if iss.Code != CodeNotExecutable {
		t.Fatalf("code = %q", iss.Code)
	}
}

func TestDetectVersionManager(t *testing.T) {
	dir := t.TempDir()
	if DetectVersionManager(dir) != nil {
		t.Fatal("empty dir should have no version manager")
	}
	if err := os.WriteFile(filepath.Join(dir, ".tool-versions"), []byte("nodejs 24.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := DetectVersionManager(dir)
	if vm == nil || vm.Kind != "asdf" {
		t.Fatalf("got %+v", vm)
	}
}

func TestClassifySpawnErrorCapability(t *testing.T) {
	iss := ClassifySpawnError("pyright-langserver", "/bin/pyright-langserver", "/p",
		[]string{"python"}, errString("pyright-langserver does not advertise textDocument/documentSymbol"), "")
	if iss.Code != CodeCapabilityMissing {
		t.Fatalf("code = %q", iss.Code)
	}
}

// errExit implements error for tests without importing os/exec.ExitError construction.
type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit status " + itoa(e.code) }
func errExit(code int) error    { return exitErr{code: code} }

type strErr string

func (e strErr) Error() string { return string(e) }
func errString(s string) error { return strErr(s) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
