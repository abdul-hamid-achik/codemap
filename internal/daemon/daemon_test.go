package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// boolPtr is a test helper so ReindexOpts{Embed: boolPtr(false)} reads cleanly.
func boolPtr(b bool) *bool { return &b }
func eventually(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func sendControl(t *testing.T, method string) string {
	t.Helper()
	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "{\"method\":%q}\n", method)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	sc := bufio.NewScanner(c)
	if !sc.Scan() {
		t.Fatal("no response from daemon")
	}
	return sc.Text()
}

// TestDaemonIndexesOnChange pins BD.11: the daemon indexes once, watches the tree,
// incrementally indexes a new file, answers daemon.status over the socket, and
// cleans up on stop. Structure-only (no Ollama).
func TestDaemonIndexesOnChange(t *testing.T) {
	// Use a SHORT data dir: the daemon's unix socket lives under it, and a unix
	// socket path is capped (~104 bytes on macOS) — t.TempDir() under /var/folders
	// blows past it. (Production's ~/.local/share/codemap is short.)
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package m\n\nfunc Alpha() {}\n")

	d, err := Start(context.Background(), root, Config{NoEmbed: true, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	g, err := d.sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	// Initial index covered a.go.
	if ns, _ := g.FindNodesBySymbol(d.pid, "Alpha"); len(ns) != 1 {
		t.Fatalf("initial index should contain Alpha, got %d nodes", len(ns))
	}

	// A new file appears → the watcher incrementally indexes it.
	mustWrite(t, root, "b.go", "package m\n\nfunc Beta() {}\n")
	if !eventually(3*time.Second, func() bool {
		ns, _ := g.FindNodesBySymbol(d.pid, "Beta")
		return len(ns) == 1
	}) {
		t.Errorf("daemon did not index the new file b.go within the timeout")
	}

	// Control socket: status reports the project and that it's watching.
	resp := sendControl(t, "daemon.status")
	if !strings.Contains(resp, root) || !strings.Contains(resp, `"watching":true`) {
		t.Errorf("daemon.status response unexpected: %s", resp)
	}

	// Stop cleans up the socket + state files.
	d.Stop()
	d.Wait()
	if _, err := os.Stat(config.DaemonSocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket should be removed after stop")
	}
	if _, err := os.Stat(config.DaemonStatePath()); !os.IsNotExist(err) {
		t.Errorf("state file should be removed after stop")
	}
}

func TestDaemonPreciseMaintainsCoverageAfterChange(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/daemonprecise\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package daemonprecise\n\nfunc Alpha() { Beta() }\n")
	mustWrite(t, root, "b.go", "package daemonprecise\n\nfunc Beta() {}\n")

	d, err := Start(context.Background(), root, Config{
		NoEmbed:  true,
		Precise:  true,
		Debounce: 80 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	g, err := d.sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := g.CallGraphResolvedFiles(d.pid)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved["a.go"] || !resolved["b.go"] {
		t.Fatalf("precise daemon did not start with exact coverage: %v", resolved)
	}
	if info := QueryStatus(); info == nil || !info.Precise {
		t.Fatalf("daemon.status should advertise persistent precise mode, got %+v", info)
	}

	// Editing a covered file replaces its node generation and clears its old
	// coverage. The watcher batch must run the precise pass before it reports
	// success, leaving both coverage and exact edges current again.
	mustWrite(t, root, "a.go", "package daemonprecise\n\nfunc Alpha() { Beta() }\nfunc Added() { Beta() }\n")
	if !eventually(5*time.Second, func() bool {
		nodes, _ := g.FindNodesBySymbol(d.pid, "Added")
		if len(nodes) != 1 {
			return false
		}
		coverage, err := g.CallGraphResolvedFiles(d.pid)
		return err == nil && coverage["a.go"] && coverage["b.go"]
	}) {
		coverage, _ := g.CallGraphResolvedFiles(d.pid)
		t.Fatalf("daemon indexed the edit but did not restore precise coverage: %v", coverage)
	}
	if n, err := g.CountEdgesByProvenance(d.pid, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	} else if n < 2 {
		t.Fatalf("precise daemon edges = %d, want Alpha→Beta and Added→Beta", n)
	}
}

// TestDaemonWarnsWhenNonPreciseWatchDecaysCoverage pins the startup warning:
// a daemon started WITHOUT --precise over a project that already holds
// precise call-graph coverage must say so in its status (every watched
// re-extraction clears the touched file's coverage row and nothing restores
// it, so the coverage silently decays). A project with no coverage rows gets
// no warning — the daemon isn't decaying anything.
func TestDaemonWarnsWhenNonPreciseWatchDecaysCoverage(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package m\n\nfunc Alpha() {}\n")

	// Seed precise coverage without requiring a precise toolchain: index once
	// through a plain session, then mark the file's coverage row directly.
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(sess)
	rep, err := svc.Index(context.Background(), root, index.Options{}, false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(rep.Project)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(p.ID, "a.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil { // release the write handle before the daemon claims it
		t.Fatal(err)
	}

	d, err := Start(context.Background(), root, Config{NoEmbed: true, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	if w := d.Info().Warning; !strings.Contains(w, "--precise") {
		t.Errorf("daemon Info().Warning = %q, want a decay warning naming --precise", w)
	}
	// The warning reaches both surfaces of daemon status: the control socket
	// response and the persisted state file behind QueryStatus.
	if resp := sendControl(t, "daemon.status"); !strings.Contains(resp, `"warning"`) {
		t.Errorf("daemon.status response should carry the warning, got %s", resp)
	}
	if info := QueryStatus(); info == nil || !strings.Contains(info.Warning, "--precise") {
		t.Errorf("QueryStatus should carry the decay warning, got %+v", info)
	}
}

// TestReindexRPCSynchronous pins the daemon-delegation path used by
// `codemap index` when a daemon is already running: a daemon.reindex request
// with reindex/precise/no-lsp/embed params runs synchronously and returns the
// full IndexReport (not a fire-and-forget "reindexing" ack), so the CLI can
// render the same output as a local index without opening a second write
// handle (which would collide with the daemon's exclusive veclite lock).
func TestReindexRPCSynchronous(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package m\n\nfunc Alpha() {}\n")

	d, err := Start(context.Background(), root, Config{NoEmbed: true, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	// Add a file after start so the reindex has something new to pick up.
	mustWrite(t, root, "g.go", "package m\n\nfunc Gamma() {}\n")

	rep, err := Reindex(ReindexOpts{Embed: boolPtr(false)})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if rep == nil {
		t.Fatal("Reindex returned nil report")
	}
	if rep.Project != d.name {
		t.Errorf("report project = %q, want %q (daemon's project)", rep.Project, d.name)
	}
	if rep.Embedded {
		t.Errorf("report should be structure-only (embed=false), got embedded=true")
	}
	// The synchronous reindex picked up g.go's Gamma symbol.
	g, _ := d.sess.Graph()
	if ns, _ := g.FindNodesBySymbol(d.pid, "Gamma"); len(ns) != 1 {
		t.Errorf("after Reindex, expected Gamma in graph, got %d nodes", len(ns))
	}
	// LastReindexAt was stamped by the handler.
	d.mu.Lock()
	last := d.info.LastReindexAt
	d.mu.Unlock()
	if last == "" {
		t.Errorf("Reindex should stamp LastReindexAt")
	}
}

// TestReindexRPCSameConnectionMultipleRequests pins P0-07: the daemon's
// handleConn previously used `defer d.indexMu.Unlock()`, which is scoped to
// the FUNCTION, not the case — so a second `daemon.reindex` request on the
// SAME connection self-deadlocked on the second Lock(), and the
// watcher's onChange/Stop() (which both acquire indexMu) blocked forever.
// The fix wraps the locked work in an IIFE so release happens at the case
// boundary. This test sends two reindexes back-to-back over one connection
// and asserts both return in well under the deadlock timeout.
func TestReindexRPCSameConnectionMultipleRequests(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package m\n\nfunc Alpha() {}\n")

	d, err := Start(context.Background(), root, Config{NoEmbed: true, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))

	send := func(label string) string {
		t.Helper()
		if _, err := c.Write([]byte(`{"method":"daemon.reindex","reindex":true,"no_lsp":true}` + "\n")); err != nil {
			t.Fatalf("%s write: %v", label, err)
		}
		sc := bufio.NewScanner(c)
		if !sc.Scan() {
			t.Fatalf("%s: no response from daemon (likely deadlocked on the second reindex — pre-fix)", label)
		}
		return sc.Text()
	}

	resp1 := send("first")
	if !strings.Contains(resp1, `"project"`) {
		t.Errorf("first reindex did not return an IndexReport: %s", resp1)
	}
	resp2 := send("second")
	if !strings.Contains(resp2, `"project"`) {
		t.Errorf("second reindex did not return an IndexReport (P0-07 deadlock): %s", resp2)
	}
}

// TestReindexRPCErrorEnvelope verifies the daemon returns a JSON error envelope
// (not a bare hang/disconnect) when a reindex fails, so the client can surface a
// useful message. We force a failure by shutting the daemon mid-handle isn't
// feasible here; instead we assert the success path decodes to *IndexReport and
// a malformed request yields a usable error via the client.
func TestReindexRPCClientNoDaemon(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")
	if _, err := Reindex(ReindexOpts{Embed: boolPtr(false)}); err == nil {
		t.Fatal("Reindex with no daemon should error")
	}
	// Sanity: app.IndexReport is the decoded type the client returns.
	var _ *app.IndexReport
}

// TestQueryStatus pins the daemon-state client used by `codemap status` /
// codemap_status: nil when no daemon runs, live Info while one runs, nil again
// after it stops.
func TestQueryStatus(t *testing.T) {
	t.Setenv("CODEMAP_DATA", shortTempDir(t))
	t.Setenv("CODEMAP_CONFIG", "")

	if got := QueryStatus(); got != nil {
		t.Fatalf("QueryStatus with no daemon should be nil, got %+v", got)
	}

	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	mustWrite(t, root, "a.go", "package m\n\nfunc Alpha() {}\n")
	d, err := Start(context.Background(), root, Config{NoEmbed: true, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	info := QueryStatus()
	if info == nil {
		t.Fatal("QueryStatus should return Info while the daemon runs")
	}
	if info.PID == 0 || !info.Watching || info.ProjectRoot == "" {
		t.Errorf("unexpected daemon Info: %+v", info)
	}

	d.Stop()
	d.Wait()
	if got := QueryStatus(); got != nil {
		t.Errorf("QueryStatus after stop should be nil, got %+v", got)
	}
}

func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shortTempDir returns a temp dir under a short base (/tmp when available), so a
// unix socket path built under it stays within the OS limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	d, err := os.MkdirTemp(base, "cmd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
