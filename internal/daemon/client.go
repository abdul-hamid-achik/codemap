package daemon

import (
	"bufio"
	"encoding/json"
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
	defer c.Close()
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
