package app

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestOrphansResolutionPin pins P1-08 (B71): pre-fix Orphans on a
// project with no call graph (TS/JS/Python without --precise)
// silently returned Found:false for every function as "dead code" —
// a confidently-wrong answer for the agent audience. The fix: the
// report carries Resolution="none" + a Note explaining what to run
// to make the list reliable.
func TestOrphansResolutionPin(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustExec(t, "git", "-C", proj, "init", "-q")
	mustExec(t, "git", "-C", proj, "config", "user.email", "t@t")
	mustExec(t, "git", "-C", proj, "config", "user.name", "t")
	mustWrite(t, proj, "a.go", "package a\n\nfunc Run() {}\nfunc Other() { Run() }\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := NewService(sess).Orphans(proj, 50)
	if err != nil {
		t.Fatal(err)
	}
	// Go has name-based call resolution by default, so the orphan
	// list is meaningful (Run is called by Other → not orphan).
	// Resolution should be "" (or "name") — the list is reliable.
	if rep.Resolution == "none" {
		t.Errorf("P1-08: Go project with name-based edges shouldn't have Resolution='none' (no call graph unavailable)")
	}
}

// mustExec runs a shell command in dir, failing the test on error.
func mustExec(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// mustWrite writes rel=content in dir, failing the test on error.
func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := dir + "/" + rel
	if err := os.MkdirAll(parentDir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
