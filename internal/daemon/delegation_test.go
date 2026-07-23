package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestDelegationAllowed pins P0-08: the daemon-control-socket is global per
// data dir (one socket for all projects), so without an identity check the
// first daemon answering QueryStatus is delegated to regardless of its project
// root — silently reindexing the WRONG project. DelegationAllowed is the
// single source of truth used by both the CLI and MCP surfaces.
func TestDelegationAllowed(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(t.TempDir(), "sibling_project")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "..")
	info := &Info{ProjectRoot: root, ProjectName: "test"}

	cases := []struct {
		name      string
		cwd       string
		wantOK    bool
		wantInMsg string // substring expected in the reason (only checked when !wantOK)
	}{
		{"exact root", root, true, ""},
		{"subdir", sub, true, ""},
		{"sibling", sibling, false, "test"},
		{"parent", parent, false, "test"},
	}
	// (The "cwd doesn't exist" case is intentionally NOT tested: EvalSymlinks
	// on a missing path may partially resolve and return an error mid-walk,
	// giving different results across platforms. The guard's real correctness
	// is covered by the sibling + parent cases above.)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, msg := DelegationAllowed(tc.cwd, info)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (msg=%q)", ok, tc.wantOK, msg)
			}
			if !tc.wantOK && !strings.Contains(msg, tc.wantInMsg) {
				t.Errorf("msg %q should mention %q", msg, tc.wantInMsg)
			}
		})
	}
}

// TestDelegationAllowedSymlinkNormalization pins the macOS /var ↔ /private
// edge case: a daemon serving /private/var/a and a CLI at /var/a must resolve
// to the same project. Without EvalSymlinks on both sides this fails and a
// legitimate delegation is refused.
func TestDelegationAllowedSymlinkNormalization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "delegated")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a symlink under /tmp pointing at our real root; the daemon
	// recorded its ProjectRoot as the symlink path, the CLI is at the
	// symlink target. Symlinks (EvalSymlinks) on both sides must resolve
	// them to the same canonical path.
	link := "/tmp/codemap_daemon_delegation_test"
	_ = os.Remove(link)
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("cannot create test symlink: %v", err)
	}
	defer func() { _ = os.Remove(link) }()

	info := &Info{ProjectRoot: link, ProjectName: "alias"}
	ok, msg := DelegationAllowed(root, info)
	if !ok {
		t.Fatalf("symlinked cwd should delegate to a daemon whose ProjectRoot is the symlink target; got reason: %q", msg)
	}
}

// TestDelegationAllowedNil guards the nil-info fast path — callers can pass
// nil if QueryStatus raced (daemon stopped); must not panic and must return !ok.
func TestDelegationAllowedNil(t *testing.T) {
	ok, _ := DelegationAllowed(os.TempDir(), nil)
	if ok {
		t.Error("nil info must return !ok (no daemon to delegate to)")
	}
}

// serveFakeDaemonStatus starts a one-shot unix-socket listener on the daemon
// control path that answers a single daemon.status request with info, so
// ReindexViaDaemon's guard can be exercised without spawning a real daemon.
func serveFakeDaemonStatus(t *testing.T, info Info) {
	t.Helper()
	_ = os.Remove(config.DaemonSocketPath())
	ln, err := net.Listen("unix", config.DaemonSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.Remove(config.DaemonSocketPath()) })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		sc := bufio.NewScanner(c)
		if !sc.Scan() {
			return
		}
		body, _ := json.Marshal(info)
		_, _ = c.Write(append(body, '\n'))
	}()
}

// TestReindexViaDaemonNoDaemon pins the common studio case: with no daemon
// running the guard falls through (neither Delegated nor Refused) so the caller
// indexes in-process. This is the path studio's ctrl+r relies on.
func TestReindexViaDaemonNoDaemon(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	d := ReindexViaDaemon(t.TempDir(), ReindexOpts{})
	if d.Delegated || d.Refused {
		t.Errorf("no daemon running: want fallthrough, got %+v", d)
	}
}

// TestReindexViaDaemonRefusesWrongProject pins the bug the shared guard fixes:
// studio previously skipped the guard entirely, so with a daemon serving another
// project a ctrl+r would collide on the veclite lock / reindex the wrong tree.
// The guard must refuse with a reason naming the daemon's project.
func TestReindexViaDaemonRefusesWrongProject(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	served := t.TempDir() // project the fake daemon claims to serve
	serveFakeDaemonStatus(t, Info{PID: 4242, ProjectRoot: served, ProjectName: "other"})
	cwd := t.TempDir() // a different project the daemon does NOT serve
	d := ReindexViaDaemon(cwd, ReindexOpts{})
	if !d.Refused {
		t.Fatalf("daemon serving %q should refuse cwd %q, got %+v", served, cwd, d)
	}
	if !strings.Contains(d.Reason, "other") {
		t.Errorf("refusal reason %q should name the daemon's project", d.Reason)
	}
}
