// Package daemon runs codemap as a long-lived background process for one project:
// it owns the single writable graph+vector handle, watches the working tree and
// incrementally re-indexes changes through the throttled embedder (so it never
// hammers Ollama), and serves control requests over a unix socket. Being the sole
// writer also resolves the multi-process veclite/SQLite lock contention. (Serving
// the full MCP toolset over the socket is BD.12; this exposes daemon.* control.)
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// Config tunes the daemon (a subset wired from DaemonConfig in BD.13).
type Config struct {
	IdleTimeout time.Duration        // 0 = never idle-shut-down
	Debounce    time.Duration        // watcher debounce
	Throttle    embed.ThrottleConfig // applied to the embedder when embedding is on
	NoEmbed     bool                 // structure-only (skip Ollama; used by tests)
	// Overrides, if set, is applied to the daemon's freshly-loaded config after it
	// opens its session, so CLI-flag overrides (exclude, embedding) reach the
	// daemon's own indexer/embedder — not just the debounce/throttle in this struct.
	Overrides func(*config.Config)
}

// Info is the daemon's status (also persisted to daemon.json).
type Info struct {
	PID           int    `json:"pid"`
	Socket        string `json:"socket"`
	ProjectRoot   string `json:"project_root"`
	ProjectName   string `json:"project_name"`
	Branch        string `json:"branch,omitempty"`
	StartedAt     string `json:"started_at"`
	LastReindexAt string `json:"last_reindex_at,omitempty"`
	Watching      bool   `json:"watching"`
	// LastError is the most recent unexpected error from the watcher or an
	// incremental index (B46). It is cleared by a successful reindex and
	// surfaced via daemon.status so failures are no longer silent while
	// the daemon still claims watching:true.
	LastError string `json:"last_error,omitempty"`
	// MissingServers is a language → binary map of LSP-backed languages
	// the project contains whose language server isn't on PATH or failed
	// to spawn, so the daemon watches Go only. Empty when every present
	// language-server language is wired (P0-11).
	MissingServers map[string]string `json:"missing_servers,omitempty"`
}

// Daemon is a running background indexer for one project.
type Daemon struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      Config
	root     string
	name     string
	pid      int64
	sess     *app.Session
	svc      *app.Service
	ix       *index.Indexer
	watcher  *index.Watcher
	listener net.Listener

	mu       sync.Mutex
	indexMu  sync.Mutex     // serializes index runs (watcher vs RPC reindex)
	cacheWG  sync.WaitGroup // tracks MaybeCacheAfterIndex goroutines (B44)
	info     Info
	idle     *time.Timer
	stopOnce sync.Once
	done     chan struct{}
}

// Start launches a daemon for the project at root: it (re)indexes once, opens the
// sole write session (with the embedder wrapped in the throttle unless NoEmbed),
// starts the file watcher → incremental IndexFiles, binds the unix socket, and
// writes daemon.json. It refuses to start if another daemon is already serving the
// socket. The returned Daemon runs until Stop, its context is cancelled, a
// daemon.shutdown request arrives, or it idles out. Call Wait to block on it.
func Start(parent context.Context, root string, cfg Config) (*Daemon, error) {
	// Raise the open-file limit before anything opens descriptors. The recursive
	// file watcher (created below) opens one FD per directory — on a large tree
	// that exhausts the default soft limit before the unix socket can bind,
	// surfacing as a misleading "listen unix ...: too many open files".
	// Best-effort: it logs on failure and never aborts startup.
	raiseFDLimit()

	// Refuse if a live daemon already owns the socket; clear a stale one.
	sockPath := config.DaemonSocketPath()
	if c, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("a codemap daemon is already running (socket %s)", sockPath)
	}
	_ = os.Remove(sockPath) // stale socket (no listener) — safe to replace

	sess, err := app.Open("")
	if err != nil {
		return nil, err
	}
	if cfg.Overrides != nil {
		cfg.Overrides(sess.Config) // CLI flags win over the daemon's config file + env
	}
	svc := app.NewService(sess)

	// Wrap the embedder in the throttle so background re-indexing is gentle on Ollama.
	if !cfg.NoEmbed {
		sess.SetEmbedder(embed.NewThrottled(sess.Embedder(), cfg.Throttle))
	}

	// Try restoring from a fcheap cache before the initial index — if a matching
	// cache entry exists (same tree hash + embedding profile), the restore imports
	// the graph+vectors so the incremental pass below only catches residual drift
	// instead of a full extraction+embed.
	if snapshot.FcheapAvailable() {
		restored, _, _ := svc.CacheRestore(parent, root)
		_ = restored // on hit the incremental Index below reconciles; on miss it runs normally
	}

	// One-time (incremental) index registers the project and brings it current.
	rep, err := svc.Index(parent, root, index.Options{}, !cfg.NoEmbed)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	g, err := sess.Graph()
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	p, err := g.GetProjectByName(rep.Project)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	d := &Daemon{
		ctx: ctx, cancel: cancel, cfg: cfg,
		root: rep.Root, name: rep.Project, pid: p.ID,
		sess: sess, svc: svc,
		done: make(chan struct{}),
	}

	// The incremental indexer (gosrc registered; embeds via the session's
	// possibly-throttled embedder unless structure-only).
	var vec *vector.Store
	var emb embed.Provider
	if !cfg.NoEmbed {
		emb = sess.Embedder()
		if v, verr := sess.Vectors(); verr == nil {
			vec = v
		}
	}
	d.ix = index.New(g, vec, emb, sess.Config.Index)

	// Pin P0-11: pre-fix the daemon's indexer was built with only gosrc
	// registered, so onChange's IndexFiles path silently skipped any
	// TS/JS/Python/Vue edit (`ext, ok := ix.extractors[lang]; if !ok {
	// continue }`). The daemon claimed watching:true while the index
	// for non-Go languages drifted until a manual --reindex. Spawn the
	// appropriate language-server extractors for the languages the
	// project actually contains, so IndexFiles routes those edits
	// through the same path the one-shot index used. Missing/failed
	// servers land in d.info.MissingServers for status reporting.
	if missing, lspErr := d.ix.RegisterLSPForProject(ctx, d.root); lspErr != nil {
		// Non-fatal: best-effort LSP registration. Fall back to Go-only
		// watching rather than aborting startup.
		fmt.Fprintf(os.Stderr, "codemap daemon: LSP registration error: %v\n", lspErr)
	} else {
		d.info.MissingServers = missing
	}

	w, err := index.NewWatcher(d.root, index.WatchConfig{
		Debounce: cfg.Debounce,
		Excluded: d.ix.Excluded,
	}, d.onChange)
	if err != nil {
		cancel()
		_ = sess.Close()
		return nil, err
	}
	d.watcher = w

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		cancel()
		_ = w.Close()
		_ = sess.Close()
		return nil, err
	}
	d.listener = ln

	branch := ""
	if st, gerr := git.Inspect(ctx, d.root); gerr == nil {
		branch = st.Branch
	}
	d.info = Info{
		PID: os.Getpid(), Socket: sockPath, ProjectRoot: d.root, ProjectName: d.name,
		Branch: branch, StartedAt: nowRFC3339(), Watching: true,
	}
	if err := d.writeState(); err != nil {
		_ = ln.Close()
		cancel()
		_ = w.Close()
		_ = sess.Close()
		return nil, err
	}

	if cfg.IdleTimeout > 0 {
		d.idle = time.AfterFunc(cfg.IdleTimeout, d.Stop)
	}
	// B46: capture watcher exit so an unexpected death flips info.Watching
	// to false and records last_error; pre-fix the goroutine discarded the
	// error and status kept reporting watching:true after the watcher
	// already exited.
	go func() {
		if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			d.mu.Lock()
			d.info.Watching = false
			d.info.LastError = "watcher died: " + err.Error()
			d.mu.Unlock()
			_ = d.writeState()
		}
	}()
	go d.serve()
	go func() { <-ctx.Done(); d.Stop() }() // parent cancel → clean shutdown
	return d, nil
}

// Wait blocks until the daemon has fully stopped.
func (d *Daemon) Wait() { <-d.done }

// Stop shuts the daemon down: stop watching, close the socket and session, and
// remove the socket + state files. Idempotent.
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		if d.idle != nil {
			d.idle.Stop()
		}
		d.cancel()
		if d.listener != nil {
			_ = d.listener.Close()
		}
		if d.watcher != nil {
			_ = d.watcher.Close()
		}
		// B44: wait for any in-flight MaybeCacheAfterIndex goroutine spawned
		// by onChange to finish BEFORE we close the session — that goroutine
		// reaches back into sess, so closing here would race the cache write.
		// cacheWG is added/done'd inside onChange, so this is bounded by the
		// current watcher batch and never grows unbounded.
		d.cacheWG.Wait()
		// Hold indexMu so any in-flight onChange / RPC reindex goroutine
		// (which writes the state file) finishes before we remove it. Without
		// this, a watcher that fires after d.cancel() returns can race past
		// our os.Remove and leave the state file on disk, breaking the
		// "state file should be removed after stop" assertion under -race.
		d.indexMu.Lock()
		_ = os.Remove(config.DaemonSocketPath())
		_ = os.Remove(config.DaemonStatePath())
		d.indexMu.Unlock()
		if d.sess != nil {
			_ = d.sess.Close()
		}
		close(d.done)
	})
}

// onChange is the watcher callback: incrementally (re)index the changed/removed
// watcher callback: incrementally (re)index the changed/removed files and
// record the reindex time. After a significant sync (>= minCacheSyncFiles),
// best-effort cache the index to fcheap so a restart or branch-switch restores
// quickly.
func (d *Daemon) onChange(toIndex, toRemove []string) {
	d.resetIdle()
	rels := append(append([]string{}, toIndex...), toRemove...)
	if len(rels) == 0 {
		return
	}
	d.withIndexMu(func() {
		if _, err := d.ix.IndexFiles(d.ctx, d.pid, d.name, d.root, rels, index.Options{}); err != nil {
			// B46: surface per-sync index errors via info.LastError so callers
			// (status, log scrapers) can see them; pre-fix the error was
			// silently swallowed while watching:true stayed asserted.
			d.mu.Lock()
			d.info.LastError = "index: " + err.Error()
			d.mu.Unlock()
			_ = d.writeState()
			return
		}
		d.mu.Lock()
		d.info.LastReindexAt = nowRFC3339()
		d.mu.Unlock()
		_ = d.writeState()

		// Best-effort cache after significant syncs (avoid caching on every single
		// file save — only when a batch of changes lands).
		if len(rels) >= minCacheSyncFiles && snapshot.FcheapAvailable() {
			// B44: track this goroutine with cacheWG so Stop can wait for it
			// before closing the session it reaches back into.
			d.cacheWG.Add(1)
			go func() {
				defer d.cacheWG.Done()
				_ = d.svc.MaybeCacheAfterIndex(d.ctx, d.root)
			}()
		}
	})
}

// minCacheSyncFiles is the minimum number of changed+removed files in a single
// watcher event before the daemon caches the index to fcheap. Caching on every
// single-file save would be wasteful; this thresholds to meaningful batches.
const minCacheSyncFiles = 3

func (d *Daemon) serve() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		d.resetIdle()
		var req struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(map[string]string{"error": "bad request"})
			continue
		}
		switch req.Method {
		case "daemon.status":
			_ = enc.Encode(d.snapshot())
		case "daemon.reindex":
			// Synchronous, parametrised reindex so a CLI `codemap index` that
			// finds a running daemon can delegate and get the full IndexReport
			// back (instead of opening a second write handle that would
			// collide with the daemon's exclusive veclite lock). Embed defaults
			// to the daemon's mode; an explicit embed field overrides it.
			//
			// Pin P0-07: the lock MUST be scoped to the reindex body, not the
			// connection — pre-fix `defer d.indexMu.Unlock()` ran at end of
			// handleConn (function return), so a second reindex on the same
			// connection self-deadlocked AND onChange and Stop both blocked
			// forever. Wrap in an IIFE so release happens at the case boundary,
			// and encode the response OUTSIDE the lock so a slow socket doesn't
			// stall the next reindex.
			var r struct {
				Reindex bool  `json:"reindex"`
				Precise bool  `json:"precise"`
				NoLSP   bool  `json:"no_lsp"`
				Embed   *bool `json:"embed"`
			}
			_ = json.Unmarshal(sc.Bytes(), &r)
			embed := !d.cfg.NoEmbed
			if r.Embed != nil {
				embed = *r.Embed
			}
			type reindexResult struct {
				rep       *app.IndexReport
				err       error
				reindexed bool
			}
			res := d.runWithIndexMu(func() any {
				rep, ierr := d.svc.Index(d.ctx, d.root, index.Options{Reindex: r.Reindex, Precise: r.Precise, NoLSP: r.NoLSP}, embed)
				if ierr != nil {
					return reindexResult{err: ierr}
				}
				d.mu.Lock()
				d.info.LastReindexAt = nowRFC3339()
				d.mu.Unlock()
				return reindexResult{rep: rep, reindexed: true}
			}).(reindexResult)
			if res.err != nil {
				_ = enc.Encode(map[string]string{"error": res.err.Error()})
				return
			}
			if res.reindexed {
				_ = d.writeState()
			}
			_ = enc.Encode(res.rep)
		case "daemon.shutdown":
			_ = enc.Encode(map[string]string{"status": "shutting down"})
			go d.Stop()
			return
		default:
			_ = enc.Encode(map[string]string{"error": "unknown method: " + req.Method})
		}
	}
}

func (d *Daemon) snapshot() Info {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

func (d *Daemon) writeState() error {
	b, err := json.MarshalIndent(d.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(config.DaemonStatePath(), append(b, '\n'), 0o644)
}

func (d *Daemon) resetIdle() {
	if d.idle != nil && d.cfg.IdleTimeout > 0 {
		d.idle.Reset(d.cfg.IdleTimeout)
	}
}

// withIndexMu serializes a unit of work against the indexer (B45). It pauses
// the idle-shutdown timer while work is in flight and restarts it on release,
// so a long reindex can never be cancelled by the timer firing mid-work.
// It also enforces the same watch-vs-reindex serialization indexMu already
// guarantees (see onChange / handleConn).
func (d *Daemon) withIndexMu(fn func()) {
	if d.idle != nil {
		d.idle.Stop()
	}
	d.indexMu.Lock()
	defer func() {
		d.indexMu.Unlock()
		d.resetIdle()
	}()
	fn()
}

// runWithIndexMu runs fn under indexMu with the idle timer paused, and
// returns its result. It's the value-returning sibling of withIndexMu, used
// by handleConn's daemon.reindex case (B45).
func (d *Daemon) runWithIndexMu(fn func() any) any {
	if d.idle != nil {
		d.idle.Stop()
	}
	d.indexMu.Lock()
	defer func() {
		d.indexMu.Unlock()
		d.resetIdle()
	}()
	return fn()
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
