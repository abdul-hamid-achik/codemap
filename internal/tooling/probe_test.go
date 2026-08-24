package tooling

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProbeNotFound(t *testing.T) {
	pr := Probe(context.Background(), "codemap-definitely-missing-binary-xyz", t.TempDir())
	if pr.OK || pr.Path != "" {
		t.Fatalf("expected not found, got %+v", pr)
	}
}

func TestProbeOK(t *testing.T) {
	// `go` is required to build this package; Probe uses `go version`.
	pr := Probe(context.Background(), "go", "")
	if !pr.OK {
		t.Fatalf("go version should succeed: path=%q err=%v stderr=%q", pr.Path, pr.Err, pr.Stderr)
	}
	if pr.Path == "" {
		t.Fatal("expected resolved path")
	}
}

func TestProbeRunnableRejectIsOK(t *testing.T) {
	// Simulate pyright-langserver: exits non-zero complaining about missing --stdio,
	// which still means the binary is runnable (not a dead asdf shim).
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "pyright-langserver")
	script := "#!/bin/sh\necho 'Error: Connection input stream is not set. Use --stdio' >&2\nexit 1\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pr := Probe(context.Background(), "pyright-langserver", dir)
	if !pr.OK {
		t.Fatalf("runnable reject should be OK: %+v", pr)
	}
}

func TestProbeOrClassifyNotFound(t *testing.T) {
	iss := ProbeOrClassify(context.Background(), "codemap-definitely-missing-binary-xyz", "", []string{"typescript"})
	if iss == nil || iss.Code != CodeNotFound {
		t.Fatalf("got %+v", iss)
	}
}

func TestProbeFailingShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim script")
	}
	dir := t.TempDir()
	// Fake asdf-style failure: binary on PATH exits 126 with the classic message.
	shim := filepath.Join(dir, "typescript-language-server")
	script := "#!/bin/sh\necho 'No version is set for command typescript-language-server' >&2\necho 'Consider adding one of the following versions in your config file at " + dir + "/.tool-versions' >&2\nexit 126\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tool-versions"), []byte("nodejs 24.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	iss := ProbeOrClassify(context.Background(), "typescript-language-server", dir, []string{"typescript", "javascript"})
	if iss == nil {
		t.Fatal("expected issue")
	}
	if iss.Code != CodeVersionManagerGap {
		t.Fatalf("code = %q, want %s; stderr=%q", iss.Code, CodeVersionManagerGap, iss.Stderr)
	}
	if iss.ResolvedPath == "" {
		t.Fatal("expected resolved_path")
	}
	if iss.ExitCode == nil || *iss.ExitCode != 126 {
		t.Fatalf("exit_code = %v", iss.ExitCode)
	}
}

func TestProbeUnwrapsAsdfShimToInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim script")
	}
	dir := t.TempDir()
	asdf := filepath.Join(dir, "asdf")
	realBin := filepath.Join(asdf, "installs", "nodejs", "22.22.0", "bin", "typescript-language-server")
	if err := os.MkdirAll(filepath.Dir(realBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\necho 5.1.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, "shims")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(shimDir, "typescript-language-server")
	script := "#!/usr/bin/env bash\n# asdf-plugin: nodejs 22.22.0\necho 'No version is set for command typescript-language-server' >&2\nexit 126\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ASDF_DATA_DIR", asdf)

	pr := Probe(context.Background(), "typescript-language-server", dir)
	if !pr.OK {
		t.Fatalf("expected unwrap to the real install: %+v", pr)
	}
	if pr.Path != realBin {
		t.Fatalf("Path = %q, want %q", pr.Path, realBin)
	}
	if iss := ProbeOrClassify(context.Background(), "typescript-language-server", dir, []string{"typescript"}); iss != nil {
		t.Fatalf("ProbeOrClassify after unwrap = %+v", iss)
	}
}

func TestProbeUnwrapsLiveAsdfShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("asdf shims")
	}
	shim, err := exec.LookPath("typescript-language-server")
	if err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	data, err := os.ReadFile(shim)
	if err != nil || !bytes.Contains(data, []byte("asdf-plugin")) {
		t.Skip("PATH typescript-language-server is not an asdf shim")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tool-versions"), []byte("golang 1.25.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := Probe(context.Background(), "typescript-language-server", dir)
	if !pr.OK {
		t.Fatalf("asdf shim failed under a golang-only project and was not unwrapped: %+v", pr)
	}
	if pr.Path == shim {
		t.Fatalf("still probing the dead shim %q", shim)
	}
}
