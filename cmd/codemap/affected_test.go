/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

func TestReadPathLines(t *testing.T) {
	got, err := readPathLines(strings.NewReader("a.go\r\n\n  b/c_test.go  \n\nlast.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.go", "b/c_test.go", "last.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestAffectedCLI drives the real executable: one test path per line on
// stdout, --stdin, --filter, --json, and the not-indexed exit code.
func TestAffectedCLI(t *testing.T) {
	root := t.TempDir()
	binName := "codemap"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(root, binName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	runner := filepath.Join(root, "runner")
	project := filepath.Join(root, "project")
	cold := filepath.Join(root, "cold")
	for _, dir := range []string{runner, project, cold} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := isolatedCLIEnv(root)
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/aff\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(project, "a.go"), "package aff\n\nfunc Helper() {}\n\nfunc Run() { Helper() }\n")
	writeTestFile(t, filepath.Join(project, "a_test.go"), "package aff\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { Run() }\n")
	writeTestFile(t, filepath.Join(project, "b.go"), "package aff\n\nfunc Lonely() {}\n")
	writeTestFile(t, filepath.Join(cold, "a.go"), "package cold\n\nfunc A() {}\n")

	res := runCLI(t, bin, runner, env, "index", project, "--no-embed", "--no-lsp", "--cache=false", "--no-tips", "--json")
	if res.exit != 0 {
		t.Fatalf("index exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
	}

	t.Run("one test path per line, notes on stderr", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "affected", "-C", project, "a.go")
		if res.exit != 0 {
			t.Fatalf("exit=%d stderr=%s", res.exit, res.stderr)
		}
		if res.stdout != "a_test.go\n" {
			t.Fatalf("stdout must be exactly the test paths, got %q", res.stdout)
		}
		if !strings.Contains(res.stderr, "name-based") {
			t.Errorf("name-based coverage note should go to stderr, got %q", res.stderr)
		}
	})

	t.Run("nothing affected prints nothing", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "affected", "-C", project, "b.go")
		if res.exit != 0 || res.stdout != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%s", res.exit, res.stdout, res.stderr)
		}
	})

	t.Run("stdin and filter", func(t *testing.T) {
		res := runCLIStdin(t, bin, runner, env, "a.go\nb.go\n", "affected", "-C", project, "--stdin")
		if res.exit != 0 || res.stdout != "a_test.go\n" {
			t.Fatalf("stdin exit=%d stdout=%q stderr=%s", res.exit, res.stdout, res.stderr)
		}
		res = runCLIStdin(t, bin, runner, env, "a.go\n", "affected", "-C", project, "--stdin", "--filter", "*.ts")
		if res.exit != 0 || res.stdout != "" {
			t.Fatalf("filtered-out exit=%d stdout=%q stderr=%s", res.exit, res.stdout, res.stderr)
		}
		// Empty stdin means nothing changed, not "the working tree".
		res = runCLIStdin(t, bin, runner, env, "", "affected", "-C", project, "--stdin", "--json")
		if res.exit != 0 {
			t.Fatalf("empty stdin exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep app.AffectedReport
		mustJSON(t, res.stdout, &rep)
		if rep.Source != app.AffectedSourceFiles || len(rep.Files) != 0 || len(rep.Tests) != 0 {
			t.Fatalf("empty stdin report = %+v", rep)
		}
	})

	t.Run("json report", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "affected", "-C", project, "a.go", "README.md", "--json")
		if res.exit != 0 || res.stderr != "" {
			t.Fatalf("exit=%d stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
		}
		var rep app.AffectedReport
		mustJSON(t, res.stdout, &rep)
		if rep.SchemaVersion != 1 || len(rep.Tests) != 1 || rep.Tests[0].File != "a_test.go" ||
			len(rep.Unmapped) != 1 || rep.Unmapped[0] != "README.md" || rep.CallGraph == "" {
			t.Fatalf("unexpected report: %+v", rep)
		}
	})

	t.Run("staged and since are exclusive", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "affected", "-C", project, "--staged", "--since", "HEAD", "--json")
		assertCLIEnvelope(t, res, exitOperational, "operational")
	})

	t.Run("unindexed project is a not_indexed envelope", func(t *testing.T) {
		res := runCLI(t, bin, runner, env, "affected", "-C", cold, "a.go", "--json")
		assertCLIEnvelope(t, res, exitNotFound, "not_indexed")
	})
}

// runCLIStdin is runCLI with a stdin payload.
func runCLIStdin(t *testing.T, bin, dir string, env []string, stdin string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %v: %v", args, err)
		}
		exit = ee.ExitCode()
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}
