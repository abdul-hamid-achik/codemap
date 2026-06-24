package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

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
