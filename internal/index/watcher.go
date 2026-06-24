package index

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// WatchConfig tunes the file watcher. Excluded is the dir/file base-name exclusion
// predicate (e.g. an Indexer's excluded check) so the watcher ignores the same
// paths the indexer does; Debounce coalesces a burst of edits into one callback.
type WatchConfig struct {
	Debounce time.Duration
	Excluded func(name string) bool
}

// Watcher reports source-file changes under a project root via fsnotify, coalescing
// bursts over a debounce window into a single callback of project-relative paths to
// (re)index and to remove. It is the daemon's incremental-sync front end; on its own
// it just watches and reports (no indexing).
type Watcher struct {
	root     string
	cfg      WatchConfig
	fsw      *fsnotify.Watcher
	onChange func(toIndex, toRemove []string)

	mu      sync.Mutex
	pending map[string]rune // rel path -> 'i' (index) or 'r' (remove)
}

// NewWatcher creates a Watcher over root and registers watches for the existing
// directory tree (fsnotify is non-recursive, so every dir is added, skipping the
// same dirs the indexer skips). onChange is invoked on the debounce tick with the
// coalesced relative-path sets. Call Run to start the loop.
func NewWatcher(root string, cfg WatchConfig, onChange func(toIndex, toRemove []string)) (*Watcher, error) {
	if cfg.Debounce <= 0 {
		cfg.Debounce = 500 * time.Millisecond
	}
	if cfg.Excluded == nil {
		cfg.Excluded = func(string) bool { return false }
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{root: root, cfg: cfg, fsw: fsw, onChange: onChange, pending: map[string]rune{}}
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && w.skipDir(d.Name()) {
			return filepath.SkipDir
		}
		_ = w.fsw.Add(p) // best effort; a transient add failure shouldn't abort setup
		return nil
	}); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	return w, nil
}

func (w *Watcher) skipDir(name string) bool {
	return w.cfg.Excluded(name) || strings.HasPrefix(name, ".")
}

// Run drives the watch loop until ctx is cancelled or the watcher is closed,
// flushing coalesced changes once the tree has been quiet for the debounce window.
func (w *Watcher) Run(ctx context.Context) error {
	timer := time.NewTimer(w.cfg.Debounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handle(ev)
			timer.Reset(w.cfg.Debounce) // (re)arm; reset after Stop/drain is safe here
		case <-timer.C:
			w.flush()
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			// transient watch error — keep going
		}
	}
}

// Close stops watching and releases the fsnotify resources.
func (w *Watcher) Close() error { return w.fsw.Close() }

func (w *Watcher) handle(ev fsnotify.Event) {
	name := filepath.Base(ev.Name)
	switch {
	case ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename):
		// The path is gone, so we can't stat it — treat it as a removed source file by
		// extension (a non-source/excluded path is ignored).
		if extract.LanguageForPath(ev.Name) != "" && !w.cfg.Excluded(name) {
			w.mark(ev.Name, 'r')
		}
	case ev.Op.Has(fsnotify.Create) || ev.Op.Has(fsnotify.Write):
		fi, err := os.Stat(ev.Name)
		if err != nil {
			return // already gone / transient
		}
		if fi.IsDir() {
			if ev.Op.Has(fsnotify.Create) && !w.skipDir(name) {
				w.addDir(ev.Name) // watch the new dir + index any source files it arrived with
			}
			return
		}
		if extract.LanguageForPath(ev.Name) != "" && !w.cfg.Excluded(name) {
			w.mark(ev.Name, 'i')
		}
	}
}

// addDir registers watches for a newly-created directory subtree and queues any
// source files it already contains (e.g. a moved-in directory), since fsnotify only
// reports events that happen AFTER a watch is added.
func (w *Watcher) addDir(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && w.skipDir(name) {
				return filepath.SkipDir
			}
			_ = w.fsw.Add(p)
			return nil
		}
		if extract.LanguageForPath(p) != "" && !w.cfg.Excluded(name) {
			w.mark(p, 'i')
		}
		return nil
	})
}

func (w *Watcher) mark(path string, op rune) {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		rel = path
	}
	w.mu.Lock()
	w.pending[rel] = op // a later op for the same path supersedes an earlier one
	w.mu.Unlock()
}

func (w *Watcher) flush() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	var toIndex, toRemove []string
	for p, op := range w.pending {
		if op == 'r' {
			toRemove = append(toRemove, p)
		} else {
			toIndex = append(toIndex, p)
		}
	}
	w.pending = map[string]rune{}
	w.mu.Unlock()
	w.onChange(toIndex, toRemove)
}
