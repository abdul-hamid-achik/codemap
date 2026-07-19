package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// QueryStatus dials a running daemon's control socket and returns its live Info,
// or nil if no daemon is running (the dial fails) or it doesn't answer in time.
// It is cheap and non-blocking: a missing daemon fails the dial well under the
// 200ms timeout, so status surfaces can call it unconditionally.
func QueryStatus() *Info {
	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), 200*time.Millisecond)
	if err != nil {
		return nil
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("{\"method\":\"daemon.status\"}\n")); err != nil {
		return nil
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !sc.Scan() {
		return nil
	}
	var info Info
	if err := json.Unmarshal(sc.Bytes(), &info); err != nil {
		return nil
	}
	return &info
}

// StatusWithDaemon augments an app.StatusReport with live daemon state for the CLI
// `status` and MCP codemap_status surfaces. It is kept out of app.StatusReport to
// avoid an app→daemon import cycle, and to spare studio's frequent Status calls a
// socket dial. The embedded pointer flattens in JSON (daemon nests under "daemon").
type StatusWithDaemon struct {
	*app.StatusReport
	Daemon *Info `json:"daemon,omitempty"`
}

// AttachStatus wraps rep with the live daemon Info (nil if no daemon is running),
// so callers get one value to print or marshal.
func AttachStatus(rep *app.StatusReport) StatusWithDaemon {
	return StatusWithDaemon{StatusReport: rep, Daemon: QueryStatus()}
}

// ReindexOpts carries the index flags forwarded to a running daemon.
type ReindexOpts struct {
	Reindex      bool     // --reindex: wipe + rebuild
	Precise      bool     // --precise: exact call edges
	NoLSP        bool     // --no-lsp: skip language-server extraction
	ExcludeExtra []string // --exclude-extra: extra skip globs appended to the configured excludes
	Embed        *bool    // --no-embed inverts this: false = structure only; nil = default to daemon mode
}

// reindexReadTimeout caps how long the CLI waits for a daemon reindex. A full
// reindex with embeddings on a large tree can take many minutes, so this is
// generous; if the daemon is truly stuck the caller still gets control back.
const reindexReadTimeout = 30 * time.Minute

// Reindex delegates a reindex to a running daemon over its control socket and
// returns the daemon's IndexReport. It is used by `codemap index` when a daemon
// is already running, so the CLI never opens a second write handle (which would
// collide with the daemon's exclusive veclite lock). Returns an error if no
// daemon is running, it doesn't respond in time, or the reindex itself failed.
func Reindex(opts ReindexOpts) (*app.IndexReport, error) {
	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon not running: %w", err)
	}
	defer func() { _ = c.Close() }()
	fields := map[string]any{
		"method":  "daemon.reindex",
		"reindex": opts.Reindex,
		"precise": opts.Precise,
		"no_lsp":  opts.NoLSP,
	}
	if len(opts.ExcludeExtra) > 0 {
		fields["exclude_extra"] = opts.ExcludeExtra
	}
	if opts.Embed != nil {
		fields["embed"] = *opts.Embed
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal reindex request: %w", err)
	}
	body = append(body, '\n')
	if _, err := c.Write(body); err != nil {
		return nil, fmt.Errorf("send reindex: %w", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(reindexReadTimeout))
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("daemon reindex read failed: %w", err)
		}
		return nil, fmt.Errorf("daemon closed connection without a response")
	}
	var rep app.IndexReport
	if err := json.Unmarshal(sc.Bytes(), &rep); err != nil {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("daemon: %s", errResp.Error)
		}
		return nil, fmt.Errorf("daemon: bad response (%w)", err)
	}
	return &rep, nil
}
