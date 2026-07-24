// Package index walks a project, extracts its structure, embeds node sources,
// and stores the result as a graph (SQLite) plus vectors (veclite). Indexing is
// incremental: files whose content hash is unchanged are skipped. A full
// reindex (Options.Reindex) wipes the project and rebuilds everything.
package index

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/csssrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/htmlsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/luasrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/rubysrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/typesrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/vuesrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// Options controls an index run.
type Options struct {
	// Reindex wipes the project and rebuilds all nodes, edges, and vectors.
	// Without it, files with an unchanged content hash are skipped.
	Reindex bool
	// Precise runs the opt-in go/types pass that replaces name-based call edges
	// with exact ones for cleanly type-checked packages. Requires the `go`
	// toolchain and a buildable module; degrades to name-based otherwise.
	Precise bool
	// NoLSP disables auto-registration of language-server-backed extractors
	// (TypeScript, …). Indexing then covers only the built-in Go backend.
	NoLSP bool
	// ExcludeExtra appends per-call skip globs to the configured excludes
	// (cfg.Exclude + cfg.ExcludeExtra) for this run only — e.g. extra paths an
	// MCP codemap_index caller wants skipped without editing the config file.
	// Same glob semantics as config (bare = any depth, slash = root-anchored,
	// **/ = any depth).
	ExcludeExtra []string
	// OnFile, if non-nil, is called once per scanned file just before it is
	// indexed: done is the 1-based position, total is the number of scanned files
	// (== Result.FilesScanned), rel is the project-relative path. Used only by the
	// interactive CLI progress bar; studio and MCP leave it nil. It runs inline on
	// the single indexing goroutine, so it must be cheap and non-blocking.
	OnFile func(done, total int, rel string)
	// OnEmbed, if non-nil, reports progress through the embedding phase: done is the
	// number of nodes embedded so far, total the count to embed. Embedding is the
	// long part of a reindex, so the CLI bar switches to it after the parse pass.
	// It is called concurrently from embed workers, so it must be cheap and
	// goroutine-safe (the CLI just forwards to a thread-safe Bubble Tea Send).
	OnEmbed func(done, total int)
}

// FileError records a per-file failure that didn't abort the whole run. It may
// describe extraction (the file was structurally skipped) or precise coverage
// degradation (structure remains indexed, but the file is not marked resolved).
type FileError struct {
	File string `json:"file"`
	Err  string `json:"error"`
}

// Result summarizes an index run.
type Result struct {
	FilesScanned   int         `json:"files_scanned"`
	FilesIndexed   int         `json:"files_indexed"`           // new or changed
	FilesSkipped   int         `json:"files_skipped"`           // oversized, generated, or extraction-errored — NOT unchanged or precise-only degradation
	FilesUnchanged int         `json:"files_unchanged"`         // P2-07 (O108): hash-matched files that were skipped because they're up-to-date (not a failure)
	FilesDeleted   int         `json:"files_deleted,omitempty"` // pruned: indexed before, now gone from disk
	Nodes          int         `json:"nodes"`
	Edges          int         `json:"edges"`
	Errors         []FileError `json:"errors,omitempty"`
	// Phase timing (wall-clock milliseconds). Extract covers Pass 1 (walk + parse +
	// graph writes); Embed covers Pass 4 (Ollama + vector inserts); Precise covers
	// the opt-in go/types + LSP callHierarchy passes. Total is the end-to-end wall
	// clock. Zero when timing is not applicable (e.g. a no-op incremental run).
	ExtractMs int `json:"extract_ms,omitempty"`
	EmbedMs   int `json:"embed_ms,omitempty"`
	PreciseMs int `json:"precise_ms,omitempty"`
	TotalMs   int `json:"total_ms,omitempty"`
	// EmbedNote, when set, explains why semantic vectors were not written (e.g.
	// Ollama unreachable). The structural index still succeeded; only semantic
	// search is unavailable until a reindex with the embedder reachable.
	EmbedNote string `json:"embed_note,omitempty"`
	// Oversized lists recognized source files skipped for exceeding
	// index.max_file_bytes — surfaced so a silently-missing file (often generated)
	// is explained, not just counted in FilesSkipped.
	Oversized []string `json:"oversized,omitempty"`
	// Generated lists source files skipped because they carry the canonical
	// "// Code generated ... DO NOT EDIT." marker (protoc, sqlc, stringer, …) —
	// detected by header regardless of filename, on top of the *_gen.go/*.pb.go
	// exclude globs. Surfaced so a skipped generated file is explained.
	Generated []string `json:"generated,omitempty"`
	// Unsupported maps a recognized source language (e.g. "typescript") to the
	// number of files skipped because codemap has no extractor for it yet (v0.1
	// indexes Go). Lets callers explain a "0 indexed" result.
	Unsupported map[string]int `json:"unsupported,omitempty"`
	// Precise* report the opt-in go/types pass (Options.Precise). PreciseUpgraded
	// is the number of exact call edges that superseded name-based ones;
	// PreciseSkipped counts resolved callees with no graph node (interface methods,
	// edges the position join missed); PreciseNote explains a degraded/no-op pass.
	PreciseUpgraded int    `json:"precise_upgraded,omitempty"`
	PreciseSkipped  int    `json:"precise_skipped,omitempty"`
	PreciseNote     string `json:"precise_note,omitempty"`
	// MissingServers maps a recognized language present in the project to the
	// language-server binary that would index it but isn't on PATH (e.g.
	// "typescript" -> "typescript-language-server"), so callers can advise the user
	// (the skipped-file count is in Unsupported[lang]). Kept for back-compat;
	// prefer ServerIssues for agent-repairable detail (path, stderr, agent_fix).
	MissingServers map[string]string `json:"missing_servers,omitempty"`
	// ServerIssues are structured tooling failures for language servers that
	// were needed for present files but not found, not runnable under the
	// project cwd (asdf/mise shims), or failed to spawn/initialize. One entry
	// per binary (languages folded together).
	ServerIssues []tooling.Issue `json:"server_issues,omitempty"`
	// Languages maps each indexed language to its file count (e.g. "go" -> 36),
	// so callers can tailor advice (the --precise tip applies only to Go).
	Languages map[string]int `json:"languages,omitempty"`
}

// Indexer turns project files into a stored graph + vectors. The vector store
// and embedder may be nil to index structure only (no semantic search).
type Indexer struct {
	graph      *graph.Store
	vectors    *vector.Store
	embedder   embed.Provider
	cfg        config.IndexConfig
	exclude    []string // effective skip globs: cfg.Exclude + cfg.ExcludeExtra
	extractors map[string]extract.Extractor
	closers    []io.Closer // stateful extractors (e.g. spawned language servers) to shut down
	// cachedNI is the incrementally-maintained node index reused across a
	// long-lived Indexer's IndexFiles calls (the daemon's), avoiding a full
	// ProjectNodes reload on every watcher event (P7). Nil until first built;
	// InvalidateNodeIndex drops it after an external full reindex so it never
	// serves stale nodes.
	cachedNI *nodeIndex
}

// registerLSP spawns and registers a language-server-backed extractor for each
// DefaultServers spec whose language is actually present in the project (present
// is the post-walk unsupported map: recognized languages with no extractor yet).
// A language whose server isn't on PATH, or fails to spawn, is recorded in
// res.MissingServers and skipped — never fatal. Returns true if it registered at
// least one extractor (so the caller re-walks to route those files). Vue (.vue
// SFCs) is handled separately by registerVue below: its files aren't served by
// any DefaultServers spec directly — a vuesrc.Extractor delegates their
// <script>/<script setup> block content to the TypeScript/JavaScript extractor
// this loop registers.
func (ix *Indexer) registerLSP(ctx context.Context, root string, present map[string]int, res *Result) bool {
	registered := false
	for _, spec := range lspsrc.DefaultServers {
		// Which of this server's languages does the project actually contain?
		var want []lspsrc.LangBinding
		for _, lb := range spec.Langs {
			if present[lb.Lang] > 0 {
				want = append(want, lb)
			}
		}
		if len(want) == 0 {
			continue
		}
		langs := make([]string, len(want))
		for i, lb := range want {
			langs[i] = lb.Lang
		}
		// Probe under the project root so asdf/mise shims evaluate .tool-versions
		// (LookPath alone treats a dead shim as success).
		if iss := tooling.ProbeOrClassify(ctx, spec.Cmd, root, langs); iss != nil {
			noteServerIssue(res, *iss)
			continue
		}
		path, _ := exec.LookPath(spec.Cmd)
		// Spawn the server ONCE (the first present language owns it), then bind the
		// rest to the same connection — one typescript-language-server serves both
		// TS and JS, each routed with its own languageId.
		owner, err := lspsrc.New(ctx, want[0].Lang, want[0].LangID, root, spec.Cmd, spec.Args...)
		if err != nil {
			noteServerIssue(res, tooling.ClassifySpawnError(spec.Cmd, path, root, langs, err, ""))
			continue
		}
		ix.Register(owner)
		ix.closers = append(ix.closers, owner) // only the owner closes the server
		for _, lb := range want[1:] {
			ix.Register(owner.Bind(lb.Lang, lb.LangID))
		}
		registered = true
	}
	if present["vue"] > 0 && ix.registerVue(ctx, root, res) {
		registered = true
	}
	return registered
}

// registerVue wires a vuesrc extractor for "vue" — it delegates script-block
// parsing to the TypeScript/JavaScript extractor(s) the loop above registered.
// A project with .vue files but no plain .ts/.js files never triggers the
// typescript-language-server spec above (present["typescript"] and
// present["javascript"] are both 0 in that case), so this spawns it here if
// needed, purely to serve Vue's <script>/<script setup> blocks. If only one of
// typescript/javascript ended up registered (e.g. real .js files present but no
// real .ts files), the other is bound on the SAME server connection too — a
// .vue file's script blocks can use either lang regardless of what plain files
// happen to exist in the project, and binding is cheap (one more languageId on
// an already-running process). Never fatal: a missing/failed server is
// recorded in res.MissingServers["vue"] and .vue files stay in Unsupported,
// same as any other missing-server language.
func (ix *Indexer) registerVue(ctx context.Context, root string, res *Result) bool {
	ts := ix.extractors["typescript"]
	js := ix.extractors["javascript"]

	if ts == nil && js == nil {
		spec := tsServerSpec()
		if spec.Cmd == "" {
			noteServerIssue(res, tooling.ClassifyNotFound("typescript-language-server", root, []string{"vue"}))
			return false
		}
		langs := []string{"vue"}
		for _, lb := range spec.Langs {
			langs = append(langs, lb.Lang)
		}
		if iss := tooling.ProbeOrClassify(ctx, spec.Cmd, root, langs); iss != nil {
			// Vue-only project: keep languages focused on vue when the shared
			// TS/JS servers were not otherwise required by present plain files.
			iss.Languages = []string{"vue"}
			noteServerIssue(res, *iss)
			return false
		}
		path, _ := exec.LookPath(spec.Cmd)
		owner, err := lspsrc.New(ctx, spec.Langs[0].Lang, spec.Langs[0].LangID, root, spec.Cmd, spec.Args...)
		if err != nil {
			noteServerIssue(res, tooling.ClassifySpawnError(spec.Cmd, path, root, []string{"vue"}, err, ""))
			return false
		}
		ix.Register(owner)
		ix.closers = append(ix.closers, owner)
		for _, lb := range spec.Langs[1:] {
			ix.Register(owner.Bind(lb.Lang, lb.LangID))
		}
		ts = ix.extractors["typescript"]
		js = ix.extractors["javascript"]
	} else {
		if ts == nil {
			if b := bindOther(js, "typescript", "typescript"); b != nil {
				ts = b
				ix.Register(ts)
			}
		}
		if js == nil {
			if b := bindOther(ts, "javascript", "javascript"); b != nil {
				js = b
				ix.Register(js)
			}
		}
	}

	if ts == nil && js == nil {
		noteServerIssue(res, tooling.ClassifyNotFound("typescript-language-server", root, []string{"vue"}))
		return false
	}
	ix.Register(vuesrc.New(ts, js))
	return true
}

// bindOther binds an additional codemap language onto the SAME language-server
// connection e already uses, via lspsrc.Extractor.Bind. Returns nil if e isn't
// an *lspsrc.Extractor (defensive — every current registration of "typescript"/
// "javascript" is, but this degrades gracefully rather than panicking if that
// ever changes).
func bindOther(e extract.Extractor, lang, langID string) extract.Extractor {
	if le, ok := e.(*lspsrc.Extractor); ok {
		return le.Bind(lang, langID)
	}
	return nil
}

// tsServerSpec returns the lspsrc.DefaultServers entry that serves
// "typescript" (the typescript-language-server spec), or the zero value if
// none is configured (Cmd == "" — callers treat that as "no server").
func tsServerSpec() lspsrc.ServerSpec {
	for _, s := range lspsrc.DefaultServers {
		for _, lb := range s.Langs {
			if lb.Lang == "typescript" {
				return s
			}
		}
	}
	return lspsrc.ServerSpec{}
}

// pruneDeleted removes nodes, vectors, and index state for files that were
// indexed previously but no longer exist on disk (deleted or renamed) — otherwise
// incremental reindex leaves ghost symbols that show up in find/callers/search. It
// checks each indexed file with os.Stat (not the walk result), so a file that's
// still on disk but currently unsupported (server uninstalled, or --no-lsp) is
// kept, never wiped; only genuinely-gone files are pruned.
func (ix *Indexer) pruneDeleted(projectID int64, projectName, root string, res *Result) error {
	indexed, err := ix.graph.IndexedFiles(projectID)
	if err != nil {
		return err
	}
	for _, rel := range indexed {
		if _, statErr := os.Stat(filepath.Join(root, rel)); statErr == nil {
			continue // still on disk — keep it (even if its extractor is unavailable now)
		} else if !os.IsNotExist(statErr) {
			continue // other stat error — be conservative, don't delete
		}
		if err := ix.graph.DeleteNodesInFile(projectID, rel); err != nil {
			return err
		}
		if err := ix.graph.DeleteFileHash(projectID, rel); err != nil {
			return err
		}
		if err := ix.graph.ClearCallGraphResolved(projectID, rel); err != nil {
			return err
		}
		if ix.vectors != nil {
			if _, err := ix.vectors.DeleteByFile(projectName, rel); err != nil {
				return err
			}
		}
		res.FilesDeleted++
	}
	return nil
}

func noteMissingServer(res *Result, lang, cmd string) {
	if res.MissingServers == nil {
		res.MissingServers = map[string]string{}
	}
	res.MissingServers[lang] = cmd
}

// noteServerIssue records a structured tooling failure and keeps MissingServers
// in sync (one binary → each affected language) for back-compat consumers.
func noteServerIssue(res *Result, iss tooling.Issue) {
	if len(iss.Languages) == 0 && iss.Binary != "" {
		// Defensive: always pair with MissingServers for something.
		iss.Languages = []string{"unknown"}
	}
	for _, lang := range iss.Languages {
		if lang == "" || lang == "unknown" {
			continue
		}
		noteMissingServer(res, lang, iss.Binary)
	}
	// Dedupe by binary: later failures for the same cmd replace earlier ones
	// (probe then spawn would otherwise double-report).
	for i, existing := range res.ServerIssues {
		if existing.Binary == iss.Binary {
			// Union languages.
			seen := map[string]bool{}
			var langs []string
			for _, l := range existing.Languages {
				if !seen[l] {
					seen[l] = true
					langs = append(langs, l)
				}
			}
			for _, l := range iss.Languages {
				if l != "" && !seen[l] {
					seen[l] = true
					langs = append(langs, l)
				}
			}
			iss.Languages = langs
			res.ServerIssues[i] = iss
			return
		}
	}
	res.ServerIssues = append(res.ServerIssues, iss)
}

// Close shuts down any stateful resources the indexer spawned (language-server
// subprocesses). Best-effort: it closes every registered closer and returns the
// first error. Safe to call when nothing was spawned.
func (ix *Indexer) Close() error {
	var firstErr error
	for _, c := range ix.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	ix.closers = nil
	return firstErr
}

// New returns an indexer with the default pure-Go backends registered:
// go/parser for Go, and the line-scanner backends for Ruby, Lua,
// CSS/SCSS/Sass/Less selectors, and HTML class references. LSP-backed
// languages (TypeScript/JavaScript/Python, plus Vue delegation) are registered
// per project by registerLSP when their files are present.
func New(g *graph.Store, vec *vector.Store, emb embed.Provider, cfg config.IndexConfig) *Indexer {
	ix := &Indexer{
		graph:      g,
		vectors:    vec,
		embedder:   emb,
		cfg:        cfg,
		exclude:    append(append([]string{}, cfg.Exclude...), cfg.ExcludeExtra...),
		extractors: map[string]extract.Extractor{},
	}
	ix.Register(gosrc.New())
	ix.Register(rubysrc.New())
	ix.Register(luasrc.New())
	for _, lang := range []string{"css", "scss", "sass", "less"} {
		ix.Register(csssrc.New(lang))
	}
	ix.Register(htmlsrc.New())
	return ix
}

// Register adds (or replaces) the extractor for a language.
func (ix *Indexer) Register(e extract.Extractor) { ix.extractors[e.Language()] = e }

// Excluded reports whether a project-relative path (or a bare base name) matches
// a configured exclude glob — exported so the daemon's watcher ignores the same
// paths the indexer does. A relative path lets slash-anchored patterns
// ("db/migrations") work; a bare name still matches segment globs ("node_modules").
func (ix *Indexer) Excluded(relOrName string) bool { return ix.excluded(relOrName) }

type fileTask struct {
	abs  string
	rel  string
	lang string
	ext  extract.Extractor
	// importIndex is the project-wide package→file map built once per index
	// operation and shared by both IndexProject and IndexFiles tasks.
	importIndex *importIndex
}

// changedFilePaths returns the files whose current content differs from the
// last successfully indexed hash. It runs before any node replacement so the
// caller graph can still be inspected for inbound sources that also need to be
// refreshed.
func (ix *Indexer) changedFilePaths(projectID int64, files []fileTask) ([]string, error) {
	state, err := ix.graph.ProjectIndexState(projectID)
	if err != nil {
		return nil, err
	}
	indexed := make(map[string]string, len(state))
	for _, entry := range state {
		indexed[entry.FilePath] = entry.FileHash
	}

	changed := make([]string, 0)
	for _, ft := range files {
		content, err := os.ReadFile(ft.abs)
		if err != nil {
			continue // indexFile will surface the read error in the normal pass
		}
		if indexed[ft.rel] != sha256hex(content) {
			changed = append(changed, ft.rel)
		}
	}
	return changed, nil
}

// expandWithInboundSources adds every file whose current outbound graph edges
// target one of rels. The returned added slice is the subset whose cached hash
// must be cleared so unchanged callers/importers are actually re-extracted.
// This is shared by full incremental indexing and the watcher path.
func (ix *Indexer) expandWithInboundSources(projectID int64, rels []string) (expanded, added []string, err error) {
	expanded = append([]string(nil), rels...)
	seen := make(map[string]bool, len(rels))
	for _, rel := range rels {
		seen[rel] = true
	}
	// Walk the inbound closure, not only the first hop. Re-extracting a direct
	// caller replaces its nodes too, which cascades edges from callers above it.
	// The seen set both dedupes and terminates cycles.
	for i := 0; i < len(expanded); i++ {
		rel := expanded[i]
		sources, queryErr := ix.graph.SourceFilesTargeting(projectID, rel)
		if queryErr != nil {
			return nil, nil, queryErr
		}
		for _, source := range sources {
			if seen[source] {
				continue
			}
			seen[source] = true
			expanded = append(expanded, source)
			added = append(added, source)
		}
	}
	return expanded, added, nil
}

// incrementalImportIndex builds the project-wide import lookup without walking
// the whole tree on every watcher event. Previously indexed files plus the
// event's paths are sufficient to resolve imports to graph file nodes.
func incrementalImportIndex(root string, rels []string) *importIndex {
	seen := make(map[string]bool, len(rels))
	files := make([]fileTask, 0, len(rels))
	for _, rel := range rels {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		abs := filepath.Join(root, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		lang := extract.LanguageForPath(abs)
		if lang == "" {
			continue
		}
		files = append(files, fileTask{abs: abs, rel: rel, lang: lang})
	}
	return newImportIndex(root, files)
}

// embedItem is one node awaiting a semantic vector: its graph node id, the text
// to embed, and the vector-store metadata. indexFile collects these across all
// files so embedAndStore can embed them in large concurrent batches.
type embedItem struct {
	nodeID  int64
	content string
	meta    vector.NodeMeta
}

// Embedding-phase defaults. Apple-silicon nomic-embed-text peaks around batch
// 64–128; a handful of concurrent requests overlaps HTTP/queue latency without
// overwhelming Ollama. Both are configurable (IndexConfig + CODEMAP_EMBED_*).
const (
	defaultEmbedBatchSize   = 64
	defaultEmbedConcurrency = 4
)

// IndexProject indexes root for the given registered project.
func (ix *Indexer) IndexProject(ctx context.Context, projectID int64, projectName, root string, opts Options) (*Result, error) {
	res := &Result{}
	defer func() { _ = ix.Close() }() // shut down any language servers spawned below

	// Per-call exclude_extra (e.g. from codemap_index over MCP) appends to the
	// configured excludes for this run only. The indexer is built fresh per
	// Index call, so this never leaks into a later run.
	if len(opts.ExcludeExtra) > 0 {
		ix.exclude = append(ix.exclude, opts.ExcludeExtra...)
	}

	// A structure-only run is an explicit project-wide semantic mode change, not
	// merely "do not update changed vectors". Clear the full project scope before
	// any hash short-circuit or cancellation can leave old embeddings queryable.
	if ix.embedder == nil && ix.vectors != nil {
		if err := ix.clearProjectVectors(projectName); err != nil {
			return nil, err
		}
	}

	if opts.Reindex {
		if err := ix.graph.WipeProject(projectID); err != nil {
			return nil, err
		}
		if ix.vectors != nil && ix.embedder != nil {
			if err := ix.clearProjectVectors(projectName); err != nil {
				return nil, err
			}
		}
	}

	indexStart := time.Now()
	var extractStart, embedStart, preciseStart time.Time

	files, unsupported, err := ix.walk(root)
	if err != nil {
		return nil, err
	}
	// Auto-register language-server-backed extractors for recognized languages that
	// are actually present (so a Go-only repo never spawns a server), then re-walk
	// to route their files to the new extractor. Skipped entirely under --no-lsp.
	if !opts.NoLSP && ix.registerLSP(ctx, root, unsupported, res) {
		if files, unsupported, err = ix.walk(root); err != nil {
			return nil, err
		}
	}
	res.FilesScanned = len(files)
	if len(unsupported) > 0 {
		res.Unsupported = unsupported
	}
	for _, f := range files {
		if res.Languages == nil {
			res.Languages = map[string]int{}
		}
		res.Languages[f.lang]++
	}

	// A repeat Go --precise run must be able to downgrade a package that now
	// type-fails. Re-extract every previously-resolved Go file first so it gets a
	// fresh parser/name graph; the precise pass then supersedes only CleanFiles.
	// Without this, unchanged package mates retain stale precise edges after a
	// different file introduces a package-wide type error.
	if opts.Precise && !opts.Reindex {
		resolved, err := ix.graph.CallGraphResolvedFiles(projectID)
		if err != nil {
			return nil, err
		}
		for _, ft := range files {
			if ft.lang == "go" && resolved[ft.rel] {
				if err := ix.graph.DeleteFileHash(projectID, ft.rel); err != nil {
					return nil, err
				}
			}
		}
	}

	// Before replacing any changed file nodes, remember which unchanged files
	// point into them. Replacing a target cascades those inbound edges; forcing
	// their source files through extraction lets the final resolution pass rebuild
	// the edges against the replacement nodes.
	if !opts.Reindex {
		changed, err := ix.changedFilePaths(projectID, files)
		if err != nil {
			return nil, err
		}
		_, inbound, err := ix.expandWithInboundSources(projectID, changed)
		if err != nil {
			return nil, err
		}
		present := make(map[string]bool, len(files))
		for _, ft := range files {
			present[ft.rel] = true
		}
		for _, rel := range inbound {
			if present[rel] {
				if err := ix.graph.DeleteFileHash(projectID, rel); err != nil {
					return nil, err
				}
			}
		}
	}

	// Incremental only: prune files that were indexed before but are gone from disk
	// (deleted/renamed), so they don't leave ghost symbols. A full --reindex already
	// wiped everything above.
	if !opts.Reindex {
		if err := ix.pruneDeleted(projectID, projectName, root, res); err != nil {
			return nil, err
		}
	}

	// Pass 1: extract + store nodes for changed files, collecting the references
	// for edge resolution and the nodes to embed (embedded together in Pass 4, not
	// per-file, so the slow Ollama calls batch and run concurrently).
	//
	// Go files (gosrc, pure go/parser — stateless and thread-safe) and LSP-backed
	// files (TypeScript, JavaScript, Python, Vue) are both extracted concurrently
	// with a bounded worker pool. Graph writes serialize naturally on the
	// single-connection pool (SetMaxOpenConns(1)), so the parallelism overlaps the
	// slow per-file work (go/parser; the LSP DidOpen+documentSymbol round trips)
	// with I/O-bound graph writes. The language-server connection is safe for
	// concurrent requests — each JSON-RPC call has a unique id, writes serialize
	// on the conn mutex, and the read loop routes responses by id — and the
	// Extractor holds no shared mutable state, so a bounded worker pool is safe;
	// the server's parseWait retry already paces codemap to the parse rate under
	// load (P4).
	var pending []extract.Reference
	var embedAcc []embedItem
	var mu sync.Mutex   // guards res, embedAcc, pending across parallel Go workers
	total := len(files) // == res.FilesScanned; the bar's denominator
	var fileDone int64  // atomic counter for OnFile progress reporting
	// P2-04 (O30): build the project-wide import index once after the
	// LSP re-walk and before the Go/LSP split, so every indexFile call
	// (both the parallel Go workers and the sequential LSP pass) shares
	// the same goFiles / relFiles maps. Pure data — no DB access, no
	// mutation after construction — so it's safe to share across the
	// errgroup without coordination.
	impIdx := newImportIndex(root, files)
	for i := range files {
		files[i].importIndex = impIdx
	}

	// Split into Go files (parallel) and LSP files (sequential).
	var goFiles, lspFiles []fileTask
	for _, ft := range files {
		if ft.lang == "go" {
			goFiles = append(goFiles, ft)
		} else {
			lspFiles = append(lspFiles, ft)
		}
	}

	extractStart = time.Now()

	// LSP files: bounded concurrency (P4). Same pattern as the Go pass — a
	// per-worker local Result merged under mu — overlapping the slow
	// DidOpen+documentSymbol round trips while graph writes serialize on the
	// single DB connection.
	lspConcurrency := ix.cfg.ExtractConcurrency
	if lspConcurrency < 1 {
		lspConcurrency = 4
	}
	if lspConcurrency > len(lspFiles) {
		lspConcurrency = len(lspFiles)
	}
	if lspConcurrency > 0 {
		leg, lgctx := errgroup.WithContext(ctx)
		leg.SetLimit(lspConcurrency)
		for _, ft := range lspFiles {
			ft := ft // capture
			leg.Go(func() error {
				if lgctx.Err() != nil {
					return lgctx.Err()
				}
				if opts.OnFile != nil {
					opts.OnFile(int(atomic.AddInt64(&fileDone, 1)), total, ft.rel)
				}
				localRes := &Result{}
				changed, refs, toEmbed, err := ix.indexFile(lgctx, projectID, projectName, ft, opts, localRes)
				mu.Lock()
				res.FilesIndexed += localRes.FilesIndexed
				res.FilesSkipped += localRes.FilesSkipped
				res.FilesUnchanged += localRes.FilesUnchanged
				res.Oversized = append(res.Oversized, localRes.Oversized...)
				res.Generated = append(res.Generated, localRes.Generated...)
				if len(localRes.Errors) > 0 {
					res.Errors = append(res.Errors, localRes.Errors...)
				}
				if err != nil {
					res.Errors = append(res.Errors, FileError{File: ft.rel, Err: err.Error()})
					// P2-07 (O108): unchanged LSP files are up-to-date too.
					res.FilesUnchanged++
					mu.Unlock()
					return nil // don't fail the group — record and continue
				}
				if changed {
					pending = append(pending, refs...)
					embedAcc = append(embedAcc, toEmbed...)
				}
				mu.Unlock()
				return nil
			})
		}
		if err := leg.Wait(); err != nil {
			return res, err
		}
	}

	// Go files: parallel with a bounded errgroup. The gosrc extractor is
	// stateless (pure go/parser), and graph writes serialize on the single
	// connection — so N goroutines overlap parsing with writes safely.
	// Each worker gets its own local Result to avoid races on res; the
	// results are merged under a mutex after each file completes.
	concurrency := ix.cfg.ExtractConcurrency
	if concurrency < 1 {
		concurrency = 4
	}
	// Cap by file count — no point spinning up more workers than files.
	if concurrency > len(goFiles) {
		concurrency = len(goFiles)
	}
	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(concurrency)
	for _, ft := range goFiles {
		ft := ft // capture
		eg.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			if opts.OnFile != nil {
				opts.OnFile(int(atomic.AddInt64(&fileDone, 1)), total, ft.rel)
			}
			// Use a per-worker local result to avoid races on res.
			// indexFile writes FilesSkipped, FilesIndexed, Oversized,
			// Generated, Errors on res — all of which would race.
			localRes := &Result{}
			changed, refs, toEmbed, err := ix.indexFile(gctx, projectID, projectName, ft, opts, localRes)
			mu.Lock()
			// Merge localRes into the shared res.
			res.FilesIndexed += localRes.FilesIndexed
			res.FilesSkipped += localRes.FilesSkipped
			res.FilesUnchanged += localRes.FilesUnchanged
			res.Oversized = append(res.Oversized, localRes.Oversized...)
			res.Generated = append(res.Generated, localRes.Generated...)
			if len(localRes.Errors) > 0 {
				res.Errors = append(res.Errors, localRes.Errors...)
			}
			if err != nil {
				res.Errors = append(res.Errors, FileError{File: ft.rel, Err: err.Error()})
				res.FilesSkipped++
				mu.Unlock()
				return nil // don't fail the group — record and continue
			}
			if changed {
				pending = append(pending, refs...)
				embedAcc = append(embedAcc, toEmbed...)
			}
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return res, err
	}
	res.ExtractMs = int(time.Since(extractStart).Milliseconds())
	if res.ExtractMs == 0 {
		res.ExtractMs = 1 // sub-millisecond; show in breakdown not nothing
	}

	// P2-04 (O30): write the file→file EdgeImports edges in a final
	// pass. Doing it inside indexFile races with the target file's
	// own indexFile call — a concurrent DeleteNodesInFileTx (the
	// first step of every indexFile) cascades to delete the
	// imports edge that the previous worker just wrote, because
	// the file node it points to gets removed. The final pass runs
	// after all workers join, so every file node is settled.
	// Re-extraction is intentional: cheap, no graph writes, and it
	// keeps the worker pipeline simple (workers stay oblivious to
	// import resolution).
	if err := ix.writeImportEdgesForFiles(ctx, projectID, files, impIdx); err != nil {
		return res, err
	}

	// Build the shared project-wide node index once and reuse it across all
	// edge-resolution passes (resolveEdges, resolvePreciseEdges,
	// resolveLSPCallEdges). On a --precise index all three run, and previously each
	// called ProjectNodes independently — 3 full table scans + 3 map builds. Now we
	// load once and pass the same index to all three.
	ni, err := ix.buildNodeIndex(projectID)
	if err != nil {
		return res, err
	}

	// Pass 2: resolve references into edges against the project-wide symbol map.
	if _, err := ix.resolveEdgesWith(ctx, projectID, pending, ni); err != nil {
		return res, err
	}

	// Pass 3 (opt-in): exact call edges.
	if opts.Precise {
		preciseStart = time.Now()
		if err := ix.resolveAllPreciseEdges(ctx, projectID, root, res, ni); err != nil {
			return res, err
		}
		res.PreciseMs = int(time.Since(preciseStart).Milliseconds())
	}

	// Pass 4: embed all collected nodes in large concurrent batches.
	embedStart = time.Now()
	if err := ix.embedAndStore(ctx, projectName, embedAcc, opts, res); err != nil {
		return res, err
	}
	res.EmbedMs = int(time.Since(embedStart).Milliseconds())
	if res.EmbedMs == 0 && ix.embedder != nil && len(embedAcc) > 0 {
		res.EmbedMs = 1 // sub-millisecond but embedding did run
	}

	if ix.vectors != nil {
		if err := ix.vectors.Sync(); err != nil {
			return res, err
		}
	}

	// Report authoritative project totals (correct under incremental runs too).
	st, err := ix.graph.Stats(projectID)
	if err != nil {
		return res, err
	}
	res.Nodes = st.Nodes
	res.Edges = st.Edges
	// P1-13 (O114): refresh query-planner stats after an index so
	// subsequent Callers/Callees/Impact queries benefit from the
	// new index statistics (340× speedup on a 100k-node graph).
	_ = ix.graph.OptimizeStats()
	res.TotalMs = int(time.Since(indexStart).Milliseconds())
	if res.TotalMs == 0 {
		res.TotalMs = 1 // sub-millisecond; show "<1ms" not nothing
	}
	return res, nil
}

// IndexFiles incrementally (re)indexes just the given project-relative paths — the
// daemon's watcher target. Each existing source file runs through indexFile (which
// hash-skips unchanged files and clears + re-extracts + embeds changed ones); each
// path that's GONE on disk is pruned (nodes + index-state + vectors), exactly like
// pruneDeleted but scoped to the watched set. The changed set expands through
// the existing inbound-edge closure, then calls and imports are resolved once
// all replacement nodes are settled.
func (ix *Indexer) IndexFiles(ctx context.Context, projectID int64, projectName, root string, rels []string, opts Options) (*Result, error) {
	res := &Result{}
	var pending []extract.Reference
	var embedAcc []embedItem
	var importFiles []fileTask
	var touchedFiles []string // files whose nodes changed — used to refresh the cached node index (P7)
	preciseRelevant, goTouched := false, false
	for _, rel := range rels {
		lang := extract.LanguageForPath(rel)
		ext, ok := ix.extractors[lang]
		if !ok {
			continue
		}
		if lang == "go" {
			goTouched = true
			preciseRelevant = true
			continue
		}
		if _, ok := ext.(extract.CallResolver); ok {
			preciseRelevant = true
		}
	}

	// A Go edit can make every other file in its package stop type-checking.
	// Mirror IndexProject's precise downgrade path: force every previously
	// resolved Go file through parser extraction first, restoring fresh name
	// edges, then let go/types supersede only the packages that remain clean.
	// Without this, an unchanged package mate could retain stale precise edges
	// after the edited file introduced a package-wide type error.
	if opts.Precise && goTouched {
		resolved, err := ix.graph.CallGraphResolvedFiles(projectID)
		if err != nil {
			return res, err
		}
		seen := make(map[string]bool, len(rels)+len(resolved))
		for _, rel := range rels {
			seen[rel] = true
		}
		for rel := range resolved {
			if extract.LanguageForPath(rel) != "go" {
				continue
			}
			if err := ix.graph.DeleteFileHash(projectID, rel); err != nil {
				return res, err
			}
			if !seen[rel] {
				rels = append(rels, rel)
				seen[rel] = true
			}
		}
	}

	// Match IndexProject's structure-only contract for direct/watcher callers:
	// unchanged files must not retain vectors from an earlier embedded run.
	if ix.embedder == nil && ix.vectors != nil {
		if err := ix.clearProjectVectors(projectName); err != nil {
			return res, err
		}
	}

	// Capture the project file set before clearing any inbound source hashes.
	// It supplies the package/file lookup used to resolve imports below.
	indexedFiles, err := ix.graph.IndexedFiles(projectID)
	if err != nil {
		return res, err
	}

	// Pin P0-04: expand the changed set with every file that currently has
	// edges TARGETING nodes in a changed file. Pre-fix the FK-cascaded
	// inbound edges (from unchanged files into a changed file) were never
	// rebuilt on incremental reindex, so callers-of-edited-symbols returned
	// a confidently-empty answer. The expansion below re-extracts those
	// source files so their outbound refs end up in `pending` and the
	// resolve pass rebuilds the now-dangling inbound edges.
	if len(rels) > 0 {
		var added []string
		rels, added, err = ix.expandWithInboundSources(projectID, rels)
		if err != nil {
			return res, err
		}
		// Force re-extraction of the inbound-expanded files: their content
		// didn't change, so the hash short-circuit inside indexFile would
		// otherwise skip the refs needed to rebuild cascaded inbound edges.
		for _, rel := range added {
			if err := ix.graph.DeleteFileHash(projectID, rel); err != nil {
				return res, err
			}
		}
	}
	impIdx := incrementalImportIndex(root, append(indexedFiles, rels...))

	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		abs := filepath.Join(root, rel)
		fi, statErr := os.Stat(abs)
		if statErr != nil {
			if os.IsNotExist(statErr) { // gone on disk → prune (mirror pruneDeleted)
				if err := ix.graph.DeleteNodesInFile(projectID, rel); err != nil {
					return res, err
				}
				if err := ix.graph.DeleteFileHash(projectID, rel); err != nil {
					return res, err
				}
				if err := ix.graph.ClearCallGraphResolved(projectID, rel); err != nil {
					return res, err
				}
				if ix.vectors != nil {
					if _, err := ix.vectors.DeleteByFile(projectName, rel); err != nil {
						return res, err
					}
				}
				touchedFiles = append(touchedFiles, rel)
				res.FilesDeleted++
			}
			continue // a non-NotExist stat error: be conservative, skip
		}
		if fi.IsDir() || ix.excluded(rel) {
			continue
		}
		lang := extract.LanguageForPath(abs)
		ext, ok := ix.extractors[lang]
		if !ok {
			continue // a language codemap doesn't index (or no server registered) — skip
		}
		res.FilesScanned++
		ft := fileTask{abs: abs, rel: rel, lang: lang, ext: ext, importIndex: impIdx}
		changed, refs, toEmbed, err := ix.indexFile(ctx, projectID, projectName, ft, opts, res)
		if err != nil {
			res.Errors = append(res.Errors, FileError{File: rel, Err: err.Error()})
			res.FilesSkipped++
			continue
		}
		if changed {
			pending = append(pending, refs...)
			embedAcc = append(embedAcc, toEmbed...)
			touchedFiles = append(touchedFiles, rel)
		}
		importFiles = append(importFiles, ft)
	}
	// Import edges need the same deferred treatment as IndexProject: write them
	// only after every changed target file node is settled.
	if err := ix.writeImportEdgesForFiles(ctx, projectID, importFiles, impIdx); err != nil {
		return res, err
	}
	// Refresh the incrementally-maintained node index for the files whose nodes
	// changed, instead of reloading every node from the DB (P7). The first call
	// builds the cache fully; later calls drop+re-add only the touched files.
	if err := ix.refreshCachedNodeIndex(projectID, touchedFiles); err != nil {
		return res, err
	}
	if _, err := ix.resolveEdgesWith(ctx, projectID, pending, ix.cachedNI); err != nil {
		return res, err
	}
	ni := ix.cachedNI
	if opts.Precise && preciseRelevant {
		start := time.Now()
		if err := ix.resolveAllPreciseEdges(ctx, projectID, root, res, ni); err != nil {
			return res, err
		}
		res.PreciseMs = int(time.Since(start).Milliseconds())
	}
	if err := ix.embedAndStore(ctx, projectName, embedAcc, opts, res); err != nil {
		return res, err
	}
	if ix.vectors != nil {
		if err := ix.vectors.Sync(); err != nil {
			return res, err
		}
	}
	// P1-13 (O114): refresh query-planner stats after incremental too.
	_ = ix.graph.OptimizeStats()
	return res, nil
}

func (ix *Indexer) walk(root string) ([]fileTask, map[string]int, error) {
	var files []fileTask
	unsupported := map[string]int{} // recognized language → count of files with no extractor yet
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		name := d.Name()
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if d.IsDir() {
			if path != root && (ix.excluded(rel) || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if ix.excluded(rel) {
			return nil
		}
		lang := extract.LanguageForPath(path)
		ext, ok := ix.extractors[lang]
		if !ok {
			if lang != "" {
				unsupported[lang]++ // a source language codemap recognizes but doesn't index yet
			}
			return nil
		}
		files = append(files, fileTask{abs: path, rel: rel, lang: lang, ext: ext})
		return nil
	})
	return files, unsupported, err
}

// excluded reports whether a project-relative path matches any effective exclude
// glob (cfg.Exclude + cfg.ExcludeExtra). See matchExclude for the glob semantics.
func (ix *Indexer) excluded(rel string) bool { return matchExclude(ix.exclude, rel) }

// matchExclude reports whether the (slash-normalized) relative path rel matches
// any pattern. Semantics (P1-11: the previous implementation trimmed a pattern's
// slashes BEFORE deciding whether it "had a slash", so a root-anchoring pattern
// written with only a trailing slash — e.g. "env/" — silently collapsed back to
// the bare, any-depth form and matched internal/env, pkg/env, etc. The slash
// check below now runs before any trimming, so a lone leading or trailing slash
// still counts as "has a slash" and anchors the pattern):
//
//   - A pattern with NO slash anywhere matches any single path segment, so
//     "node_modules" or "migrations" or "*.min.js" skips that file/dir at any depth.
//   - A pattern with a slash ANYWHERE — leading, trailing, or embedded — anchors at
//     the project root. "db/migrations" skips db/migrations and everything under it
//     but not app/db/migrations. A lone trailing slash ("env/") or leading slash
//     ("/env") anchors a single segment the same way: it skips a root-level env/
//     and everything under it, but leaves internal/env/ alone. A pattern with both
//     ("a/b/") anchors the full multi-segment prefix "a/b".
//   - A leading "./" is stripped as a redundant "explicitly rooted" marker, so
//     "./env" is equivalent to "env/" (root-anchored), NOT the bare/any-depth form.
//   - A "**/" prefix un-anchors a slash pattern so it matches that prefix starting
//     at any depth, so "**/db/migrations" also skips app/db/migrations, and
//     "**/testdata" (single segment) matches testdata at any depth — same effect
//     as the bare form but written explicitly.
//   - An absolute-looking pattern ("/dist") is treated identically to a
//     leading-slash-stripped root-anchored pattern ("dist/"); codemap excludes are
//     always project-relative, so there is no meaningful "true absolute" form.
//   - A pattern that normalizes to empty (e.g. "/", "**/", "./") is a no-op.
//
// A bare base name (one segment, no slash) is a valid rel and matches the no-slash
// rules — so callers may pass either a full relative path or a base name.
func matchExclude(patterns []string, rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, raw := range patterns {
		pat := filepath.ToSlash(raw)
		if pat == "" {
			continue
		}
		anyDepth := strings.HasPrefix(pat, "**/")
		if anyDepth {
			pat = strings.TrimPrefix(pat, "**/")
		}
		// hasSlash must be decided BEFORE trimming leading/trailing slashes —
		// that trim is what previously erased the root-anchoring signal on a
		// pattern like "env/" whose only slash was trailing.
		hasSlash := anyDepth || strings.ContainsRune(pat, '/')
		pat = strings.TrimPrefix(pat, "./")
		pat = strings.Trim(pat, "/")
		if pat == "" {
			continue
		}
		if !hasSlash {
			for _, s := range segs {
				if ok, _ := filepath.Match(pat, s); ok {
					return true
				}
			}
			continue
		}
		parts := strings.Split(pat, "/")
		last := 0 // anchored: only try matching the prefix at the root
		if anyDepth {
			last = len(segs) - 1 // un-anchored: try every starting segment
		}
		for i := 0; i <= last && i < len(segs); i++ {
			if segPrefixMatch(parts, segs[i:]) {
				return true
			}
		}
	}
	return false
}

// segPrefixMatch reports whether each glob in parts matches the corresponding
// leading segment of segs (parts being no longer than segs).
func segPrefixMatch(parts, segs []string) bool {
	if len(parts) > len(segs) {
		return false
	}
	for i, p := range parts {
		if ok, _ := filepath.Match(p, segs[i]); !ok {
			return false
		}
	}
	return true
}

func (ix *Indexer) indexFile(ctx context.Context, projectID int64, projectName string, ft fileTask, opts Options, res *Result) (bool, []extract.Reference, []embedItem, error) {
	content, err := os.ReadFile(ft.abs)
	if err != nil {
		return false, nil, nil, err
	}
	hash := sha256hex(content)
	if ix.cfg.MaxFileBytes > 0 && len(content) > ix.cfg.MaxFileBytes {
		res.FilesSkipped++
		res.Oversized = append(res.Oversized, ft.rel)
		// P1-01 (B16): clear the file's old nodes + vectors BEFORE
		// recording the hash, so a previously-indexed file that grew past
		// the byte cap (or that we already had indexed as a normal file)
		// doesn't leave ghost symbols in find/callers/orphans. Pre-fix
		// the SetFileHash ran first and the deletes after, so the
		// ghost survived indefinitely.
		_ = ix.graph.DeleteNodesInFile(projectID, ft.rel)
		if err := ix.graph.ClearCallGraphResolved(projectID, ft.rel); err != nil {
			return false, nil, nil, err
		}
		if ix.vectors != nil {
			_, _ = ix.vectors.DeleteByFile(projectName, ft.rel)
		}
		// Track the hash so staleness doesn't report it as perpetually "new";
		// a content change re-picks it up.
		return false, nil, nil, ix.graph.SetFileHash(projectID, ft.rel, hash)
	}
	if isGenerated(content) {
		// P1-01 (B16): same ghost-node hygiene as the oversized branch
		// — a file that GAINS a generated header (e.g. was edited to
		// regenerate) must lose its previously-indexed symbols.
		_ = ix.graph.DeleteNodesInFile(projectID, ft.rel)
		if err := ix.graph.ClearCallGraphResolved(projectID, ft.rel); err != nil {
			return false, nil, nil, err
		}
		if ix.vectors != nil {
			_, _ = ix.vectors.DeleteByFile(projectName, ft.rel)
		}
		res.FilesSkipped++
		res.Generated = append(res.Generated, ft.rel)
		// Record the hash like oversized, so staleness doesn't flag it as "new".
		return false, nil, nil, ix.graph.SetFileHash(projectID, ft.rel, hash)
	}

	if !opts.Reindex {
		prev, err := ix.graph.FileHash(projectID, ft.rel)
		if err != nil {
			return false, nil, nil, err
		}
		if prev == hash {
			// P2-07 (O108): unchanged files are "up-to-date", not "skipped".
			// Pre-fix this read as failure ("112 skipped") on a clean
			// incremental index where nothing changed.
			res.FilesUnchanged++
			return false, nil, nil, nil
		}
	}

	fr, err := ft.ext.ExtractFile(ft.rel, content)
	if err != nil {
		// Keep the last-good hash, graph, and vectors intact. The mismatch makes
		// staleness honest and ensures every subsequent index retries and reports
		// the failure instead of silently treating stale nodes as fresh.
		return false, nil, nil, err
	}

	// Extraction succeeded: clear vectors for the prior node generation before
	// atomically replacing its graph nodes below. Delaying this until after parse
	// success preserves the complete last-good state on extraction failures.
	if ix.vectors != nil {
		if _, err := ix.vectors.DeleteByFile(projectName, ft.rel); err != nil {
			return false, nil, nil, err
		}
	}

	// Transaction-batched graph writes: the file node + all symbol nodes + all
	// defines edges + the file hash + the old-node delete are one BEGIN/COMMIT.
	// This amortizes SQLite's fsync from ~60 per file (one per INSERT) to 1,
	// which with synchronous=NORMAL is the single biggest write-speedup.
	lines := bytes.Count(content, []byte("\n")) + 1
	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return false, nil, nil, err
	}
	defer func() { _ = tx.Rollback() }() // safe: Commit renders this a no-op

	if err := graph.DeleteNodesInFileTx(tx, projectID, ft.rel); err != nil {
		return false, nil, nil, err
	}
	// A successful normal extraction replaces this file's node/edge generation.
	// Its prior precise coverage is no longer valid until this run's precise pass
	// succeeds for the file. Extraction failures return before this transaction,
	// preserving the complete last-good generation and its coverage.
	if err := graph.ClearCallGraphResolvedTx(tx, projectID, ft.rel); err != nil {
		return false, nil, nil, err
	}

	fileNode := &graph.Node{
		ProjectID: projectID, FilePath: ft.rel, Kind: graph.KindFile,
		Language: ft.lang, StartLine: 1, EndLine: lines, SourceHash: hash,
	}
	fileID, err := graph.AddNodeTx(tx, fileNode)
	if err != nil {
		return false, nil, nil, err
	}

	var toEmbed []embedItem
	for _, sym := range fr.Symbols {
		nid, err := graph.AddNodeTx(tx, &graph.Node{
			ProjectID: projectID, FilePath: ft.rel, Symbol: sym.Name, FQN: sym.FQN,
			Kind: sym.Kind, Language: sym.Language, StartLine: sym.StartLine, EndLine: sym.EndLine,
			Signature: sym.Signature, Docstring: sym.Docstring, SourceHash: sha256hex([]byte(sym.Source)),
		})
		if err != nil {
			return false, nil, nil, err
		}
		if _, err := graph.AddEdgeProvTx(tx, fileID, nid, graph.EdgeDefines, graph.WeightLSP, graph.ProvName); err != nil {
			return false, nil, nil, err
		}
		if ix.embedder != nil && ix.vectors != nil {
			toEmbed = append(toEmbed, embedItem{
				nodeID:  nid,
				content: embedText(sym, ix.cfg.EmbedMaxChars),
				meta: vector.NodeMeta{
					NodeID: nid, Project: projectName, File: ft.rel, Symbol: sym.Name,
					FQN: sym.FQN, Kind: sym.Kind, Language: sym.Language,
					StartLine: sym.StartLine, EndLine: sym.EndLine,
				},
			})
		}
	}
	// P2-04 (O30): the import edge write is deferred to a final pass
	// (writeAllImportEdges) after all file nodes exist. Writing inside
	// indexFile races with the target file's own indexFile call — a
	// concurrent DeleteNodesInFileTx (the first step of every
	// indexFile) cascades to delete the imports edge that the previous
	// worker just wrote, because the file node it points to gets
	// removed. The final pass runs after all workers join, so every
	// file node is settled and the edge survives.

	if err := graph.SetFileHashTx(tx, projectID, ft.rel, hash); err != nil {
		return false, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return false, nil, nil, err
	}

	res.FilesIndexed++
	return true, fr.References, toEmbed, nil
}

// embedAndStore embeds every collected item and stores its vector. This is the
// heart of reindex performance: instead of one Ollama round-trip per file done
// sequentially (embedding is ~98% of a reindex), it sends large batches
// concurrently, then inserts the vectors serially (veclite/SQLite writes aren't
// safe to parallelize). On any embed error it degrades gracefully — the
// structural index is already complete — recording a note instead of failing.
func (ix *Indexer) embedAndStore(ctx context.Context, projectName string, items []embedItem, opts Options, res *Result) error {
	if ix.embedder == nil || ix.vectors == nil || len(items) == 0 {
		return nil
	}
	batchSize := ix.cfg.EmbedBatchSize
	if batchSize <= 0 {
		batchSize = defaultEmbedBatchSize
	}
	concurrency := ix.cfg.EmbedConcurrency
	if concurrency <= 0 {
		concurrency = defaultEmbedConcurrency
	}

	vecs := make([][]float32, len(items))
	var done int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for start := 0; start < len(items); start += batchSize {
		start, end := start, min(start+batchSize, len(items))
		g.Go(func() error {
			texts := make([]string, end-start)
			for i := start; i < end; i++ {
				texts[i-start] = items[i].content
			}
			out, err := ix.embedder.Embed(gctx, texts)
			if err != nil {
				return err
			}
			if len(out) != len(texts) {
				return fmt.Errorf("got %d vectors for %d inputs", len(out), len(texts))
			}
			copy(vecs[start:end], out)
			if opts.OnEmbed != nil {
				opts.OnEmbed(int(atomic.AddInt64(&done, int64(len(texts)))), len(items))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// Embeddings failed (e.g. Ollama unreachable). The structural index is
		// already stored, so keep it and report. Because changed-file vectors were
		// removed before extraction, retaining unchanged-file vectors here would
		// expose a silently partial semantic index. Degrade the whole project to
		// structure-only instead.
		res.EmbedNote = "embeddings skipped: " + err.Error()
		if clearErr := ix.clearProjectVectors(projectName); clearErr != nil {
			return fmt.Errorf("clear project vectors after embedding failure: %w", clearErr)
		}
		return nil
	}

	// Serial insert: veclite + SQLite writes aren't safe to run concurrently.
	// Insert all vectors first (veclite), then batch the node vec_id updates in
	// one graph transaction — turning N SQLite UPDATEs into a single fsync.
	type vecUpdate struct {
		nodeID int64
		vecID  string
	}
	updates := make([]vecUpdate, 0, len(items))
	for i, item := range items {
		vid, err := ix.vectors.Insert(vecs[i], item.content, item.meta)
		if err != nil {
			return err
		}
		updates = append(updates, vecUpdate{nodeID: item.nodeID, vecID: strconv.FormatUint(vid, 10)})
	}
	if len(updates) > 0 {
		tx, err := ix.graph.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for _, u := range updates {
			if err := graph.UpdateNodeVecIDTx(tx, u.nodeID, u.vecID); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// clearProjectVectors makes a project-level semantic mode transition durable
// immediately. It deliberately leaves other projects in the shared collection
// untouched.
func (ix *Indexer) clearProjectVectors(projectName string) error {
	if ix.vectors == nil {
		return nil
	}
	if _, err := ix.vectors.DeleteByProject(projectName); err != nil {
		return err
	}
	return ix.vectors.Sync()
}

// nodeIndex is the project-wide symbol index built once from ProjectNodes and
// reused across all edge-resolution passes (resolveEdges, resolvePreciseEdges,
// resolveLSPCallEdges). Previously each pass called ProjectNodes independently —
// 3 full table scans on a --precise index. Now we build it once and pass it.
type nodeIndex struct {
	nodes []graph.Node
	fqnTo map[string]int64
	symTo map[string][]int64
	dirOf map[int64]string
}

// buildNodeIndex loads all project nodes once and builds the fqn/symbol/position
// maps that all three edge-resolution passes need.
func (ix *Indexer) buildNodeIndex(projectID int64) (*nodeIndex, error) {
	nodes, err := ix.graph.ProjectNodes(projectID)
	if err != nil {
		return nil, err
	}
	ni := &nodeIndex{
		nodes: nodes,
		fqnTo: make(map[string]int64, len(nodes)),
		symTo: make(map[string][]int64, len(nodes)),
		dirOf: make(map[int64]string, len(nodes)),
	}
	for _, n := range nodes {
		if n.FQN != "" {
			ni.fqnTo[n.FQN] = n.ID
		}
		// File-scope references (function values in top-level decls) are attributed
		// to the file path; key file nodes by path so those refs resolve. Paths have
		// slashes and FQNs have dots, so the two key spaces never collide.
		if n.Kind == graph.KindFile {
			ni.fqnTo[n.FilePath] = n.ID
		}
		if n.Symbol != "" {
			ni.symTo[n.Symbol] = append(ni.symTo[n.Symbol], n.ID)
		}
		ni.dirOf[n.ID] = filepath.Dir(n.FilePath)
	}
	return ni, nil
}

// addNodes folds nodes into the index, updating every map. Used to refresh the
// cached index with a file's freshly-indexed nodes during an incremental update.
func (ni *nodeIndex) addNodes(nodes []graph.Node) {
	for _, n := range nodes {
		ni.nodes = append(ni.nodes, n)
		if n.FQN != "" {
			ni.fqnTo[n.FQN] = n.ID
		}
		if n.Kind == graph.KindFile {
			ni.fqnTo[n.FilePath] = n.ID
		}
		if n.Symbol != "" {
			ni.symTo[n.Symbol] = append(ni.symTo[n.Symbol], n.ID)
		}
		ni.dirOf[n.ID] = filepath.Dir(n.FilePath)
	}
}

// removeFiles drops every cached node whose FilePath is in files and rebuilds
// the lookup maps from the survivors. Used to discard a changed file's stale
// nodes before re-adding its fresh nodes during an incremental cache refresh.
func (ni *nodeIndex) removeFiles(files []string) {
	if len(files) == 0 {
		return
	}
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	kept := make([]graph.Node, 0, len(ni.nodes))
	for _, n := range ni.nodes {
		if !fileSet[n.FilePath] {
			kept = append(kept, n)
		}
	}
	ni.nodes = kept
	ni.fqnTo = make(map[string]int64, len(kept))
	ni.symTo = make(map[string][]int64, len(kept))
	ni.dirOf = make(map[int64]string, len(kept))
	for _, n := range kept {
		if n.FQN != "" {
			ni.fqnTo[n.FQN] = n.ID
		}
		if n.Kind == graph.KindFile {
			ni.fqnTo[n.FilePath] = n.ID
		}
		if n.Symbol != "" {
			ni.symTo[n.Symbol] = append(ni.symTo[n.Symbol], n.ID)
		}
		ni.dirOf[n.ID] = filepath.Dir(n.FilePath)
	}
}

// refreshCachedNodeIndex keeps ix.cachedNI consistent with the DB after an
// incremental index touched files: it drops their stale nodes and re-adds their
// current nodes. With no cache yet it builds one fully. This is what lets a
// long-lived Indexer (the daemon) reuse the node index across watcher events
// instead of reloading every node each time (P7).
func (ix *Indexer) refreshCachedNodeIndex(projectID int64, touchedFiles []string) error {
	if ix.cachedNI == nil {
		ni, err := ix.buildNodeIndex(projectID)
		if err != nil {
			return err
		}
		ix.cachedNI = ni
		return nil
	}
	ix.cachedNI.removeFiles(touchedFiles)
	for _, f := range touchedFiles {
		nodes, err := ix.graph.NodesInFile(projectID, f)
		if err != nil {
			return err
		}
		ix.cachedNI.addNodes(nodes)
	}
	return nil
}

// InvalidateNodeIndex drops the cached node index so the next use rebuilds it
// from the DB. The daemon calls this after a full reindex (performed by a
// separate Indexer on the same DB) so the incremental cache never serves stale
// nodes.
func (ix *Indexer) InvalidateNodeIndex() {
	ix.cachedNI = nil
}

// resolveEdges links references (from changed files) to target nodes by name,
// resolveEdgesWith is the shared resolver that takes a pre-built nodeIndex,
// avoiding a redundant ProjectNodes call when the caller already built one.
func (ix *Indexer) resolveEdgesWith(ctx context.Context, projectID int64, refs []extract.Reference, ni *nodeIndex) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	// Batch every resolved reference into ONE transaction instead of one
	// autocommit INSERT per edge. Every other write path in the indexer is
	// tx-batched (amortizing SQLite's fsync to ~1 per pass); this pass was the
	// lone exception, paying a separate implicit transaction per edge — tens of
	// thousands of them on a large repo (P6).
	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }() // safe: Commit renders this a no-op

	count := 0
	for _, ref := range refs {
		from, ok := ni.fqnTo[ref.From]
		if !ok {
			continue
		}
		candidates := ni.symTo[ref.To]
		// An unqualified call (Foo()) resolves within the caller's package, so
		// restrict to same-directory targets — precise, and avoids cross-package
		// false edges to same-named symbols. Fall back to all matches only if the
		// same-package restriction finds nothing.
		if !ref.Qualified {
			if same := samePackage(candidates, ni.dirOf, ni.dirOf[from]); len(same) > 0 {
				candidates = same
			}
		}
		weight := graph.WeightTreeSitter
		if !ref.Qualified {
			weight = graph.WeightLSP // same-package resolution is precise
		}
		for _, to := range candidates {
			if to == from {
				continue
			}
			if _, err := graph.AddEdgeProvTx(tx, from, to, ref.Kind, weight, graph.ProvName); err != nil {
				return count, err
			}
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

// precisePos keys a node by its declaration position for the precise callee join.
type precisePos struct {
	file string
	line int
}

const (
	preciseResolverGoTypes = "go/types"
	preciseResolverLSP     = "lsp"
)

// resolveAllPreciseEdges is the shared project-wide exact-resolution pass used
// by both a one-shot index and the daemon's incremental IndexFiles path. Precise
// resolution is deliberately project-wide: go/types checks packages as a unit,
// while LSP callHierarchy coverage must delete and rebuild the complete set for
// every registered language atomically so an edit cannot leave stale exact
// edges in an unchanged package mate.
func (ix *Indexer) resolveAllPreciseEdges(ctx context.Context, projectID int64, root string, res *Result, ni *nodeIndex) error {
	hasGo := false
	for _, n := range ni.nodes {
		if n.Language == "go" && n.Kind == graph.KindFile {
			hasGo = true
			break
		}
	}
	if hasGo {
		if err := ix.resolvePreciseEdgesFromIndex(ctx, projectID, root, res, ni); err != nil {
			return err
		}
	}

	// Pin P0-06: wrap the LSP precise pass in a transaction so a partial
	// failure (server died mid-call, DB error, etc.) never leaves a
	// half-written set of precise edges. Roll back on any error.
	lsptx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin LSP precise transaction: %w", err)
	}
	defer func() { _ = lsptx.Rollback() }()
	if err := ix.resolveLSPCallEdgesWith(ctx, lsptx, projectID, root, res, ni); err != nil {
		return err
	}
	if err := lsptx.Commit(); err != nil {
		return fmt.Errorf("commit LSP precise transaction: %w", err)
	}
	return nil
}

// resolveLSPCallEdges adds exact call edges for LSP-backed languages (TypeScript)
// by driving each registered CallResolver's callHierarchy over its files, joining
// both callers and callees to nodes by declaration position. Edges are written
// ProvPrecise (there's no name-based call extraction for these languages to
// supersede). Best-effort:
// callHierarchy errors downgrade that file's coverage and continue; database
// errors abort and roll back the pass. The servers remain alive until Indexer.Close.
// resolveLSPCallEdgesWith is the shared resolver that takes a pre-built nodeIndex.
func (ix *Indexer) resolveLSPCallEdgesWith(ctx context.Context, tx *sql.Tx, projectID int64, root string, res *Result, ni *nodeIndex) error {
	resolvers := map[string]extract.CallResolver{} // language -> resolver
	for lang, e := range ix.extractors {
		if cr, ok := e.(extract.CallResolver); ok {
			resolvers[lang] = cr
		}
	}
	if len(resolvers) == 0 {
		return nil
	}
	// Build an exact declaration-position index, scoped to LSP-language nodes.
	// FQN alone is not an identity here: legal flat documentSymbol responses may
	// produce the same unqualified FQN in several files.
	posTo := make(map[precisePos]int64, len(ni.nodes))
	posCollide := map[precisePos]bool{}
	filesByLang := map[string]map[string]bool{}
	for _, n := range ni.nodes {
		if _, isLSP := resolvers[n.Language]; !isLSP {
			continue
		}
		if filesByLang[n.Language] == nil {
			filesByLang[n.Language] = map[string]bool{}
		}
		filesByLang[n.Language][n.FilePath] = true
		if n.Kind == graph.KindFile {
			continue // a file node shares line 1 with the first symbol; never a call target
		}
		key := precisePos{n.FilePath, n.StartLine}
		if _, dup := posTo[key]; dup {
			posCollide[key] = true
		} else {
			posTo[key] = n.ID
		}
	}
	for k := range posCollide {
		delete(posTo, k) // ambiguous (same-line decls) — drop, don't mis-route
	}

	type preciseFile struct {
		lang string
		file string
	}
	files := make([]preciseFile, 0)
	for lang, byFile := range filesByLang {
		for file := range byFile {
			files = append(files, preciseFile{lang: lang, file: file})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].lang != files[j].lang {
			return files[i].lang < files[j].lang
		}
		return files[i].file < files[j].file
	})

	type preciseEdgeIDs struct{ from, to int64 }

	// Phase 1 (P5): gather callHierarchy edges for every file concurrently — the
	// slow LSP round trips — without touching the DB. posTo is read-only here and
	// the position join stays sequential in phase 2, so only the CallEdges fan-out
	// is parallel. The documents were opened during extraction and stay open, so
	// callHierarchy can run before the transactional supersede below.
	type fileCallEdges struct {
		edges []extract.CallEdge
		err   error
	}
	gathered := make([]fileCallEdges, len(files))
	lspConcurrency := ix.cfg.ExtractConcurrency
	if lspConcurrency < 1 {
		lspConcurrency = 4
	}
	if lspConcurrency > len(files) {
		lspConcurrency = len(files)
	}
	if lspConcurrency > 0 {
		eg, gctx := errgroup.WithContext(ctx)
		eg.SetLimit(lspConcurrency)
		for i, current := range files {
			i, current := i, current
			eg.Go(func() error {
				edges, cErr := resolvers[current.lang].CallEdges(gctx, current.file)
				gathered[i] = fileCallEdges{edges: edges, err: cErr}
				return nil
			})
		}
		_ = eg.Wait()
	}

	// Phase 2: apply each file's gathered edges to the single transaction
	// sequentially — supersede prior coverage + exact calls, join positions, write
	// the new exact edges, and re-mark coverage — preserving the per-language
	// atomicity and coverage-downgrade logic exactly.
	upgraded, skipped, failedFiles := 0, 0, 0
	for i, current := range files {
		file := current.file
		// Supersede prior coverage for this file, then re-mark it only after
		// every indexed callable and every internal position join succeeds. An
		// empty result remains a successful precise resolution for a leaf file.
		if clearErr := graph.ClearCallGraphResolvedTx(tx, projectID, file); clearErr != nil {
			res.PreciseNote = "LSP precise coverage clear failed: " + clearErr.Error()
			return clearErr
		}
		// Replace outgoing exact calls file-by-file in the same transaction as
		// coverage. This includes unchanged files whose nodes were not re-extracted:
		// if their callHierarchy request now fails, stale confirmed edges must not
		// survive while the file itself is correctly downgraded to uncovered.
		if clearErr := deleteLSPPreciseCallsInFileTx(tx, projectID, file); clearErr != nil {
			res.PreciseNote = "LSP precise supersede failed: " + clearErr.Error()
			return clearErr
		}
		fe := gathered[i]
		if fe.err != nil {
			appendPreciseFileError(res, file, fmt.Errorf("LSP call hierarchy: %w", fe.err))
			failedFiles++
			continue
		}
		edges := fe.edges
		pending := make([]preciseEdgeIDs, 0, len(edges))
		joinFailures := 0
		joinSamples := make([]string, 0, 3)
		for _, e := range edges {
			if e.External {
				skipped++
				continue // callee outside the project — no node
			}
			if filepath.Clean(e.FromFile) != filepath.Clean(file) {
				joinFailures++
				skipped++
				if len(joinSamples) < cap(joinSamples) {
					joinSamples = append(joinSamples, fmt.Sprintf("source %s:%d belongs to %s", e.FromFile, e.FromLine, file))
				}
				continue
			}
			from, ok := posTo[precisePos{e.FromFile, e.FromLine}]
			if !ok {
				joinFailures++
				skipped++
				if len(joinSamples) < cap(joinSamples) {
					joinSamples = append(joinSamples, fmt.Sprintf("source %s:%d (%s)", e.FromFile, e.FromLine, e.FromFQN))
				}
				continue
			}
			to, ok := posTo[precisePos{e.ToFile, e.ToLine}]
			if !ok {
				joinFailures++
				skipped++
				if len(joinSamples) < cap(joinSamples) {
					joinSamples = append(joinSamples, fmt.Sprintf("target %s:%d", e.ToFile, e.ToLine))
				}
				continue
			}
			if to == from {
				continue
			}
			pending = append(pending, preciseEdgeIDs{from: from, to: to})
		}
		if joinFailures > 0 {
			appendPreciseFileError(res, file, fmt.Errorf(
				"LSP precise coverage incomplete: %d internal call edge(s) did not join indexed definitions (%s)",
				joinFailures, strings.Join(joinSamples, "; ")))
			failedFiles++
			continue
		}
		// Stage before writing so one missing internal join cannot leave a
		// partially rebuilt exact graph for a file whose coverage is unresolved.
		for _, edge := range pending {
			if _, aErr := graph.AddEdgeProvTx(tx, edge.from, edge.to, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); aErr != nil {
				res.PreciseNote = "LSP precise edge insert failed: " + aErr.Error()
				res.PreciseUpgraded += upgraded
				return aErr
			}
			upgraded++
		}
		if markErr := graph.MarkCallGraphResolvedTx(tx, projectID, file, preciseResolverLSP); markErr != nil {
			res.PreciseNote = "LSP precise coverage mark failed: " + markErr.Error()
			res.PreciseUpgraded += upgraded
			return markErr
		}
	}
	res.PreciseUpgraded += upgraded
	res.PreciseSkipped += skipped
	if failedFiles > 0 {
		appendPreciseNote(res, fmt.Sprintf("LSP precise coverage incomplete for %d file(s); see errors", failedFiles))
	}
	return nil
}

func deleteLSPPreciseCallsInFileTx(tx *sql.Tx, projectID int64, file string) error {
	_, err := tx.Exec(`
		DELETE FROM edges
		WHERE edge_type = ? AND provenance = ?
		  AND source_id IN (
			SELECT id FROM nodes WHERE project_id = ? AND file_path = ?
		  )`, graph.EdgeCalls, graph.ProvPrecise, projectID, file)
	if err != nil {
		return fmt.Errorf("delete prior precise calls for %s: %w", file, err)
	}
	return nil
}

func appendPreciseFileError(res *Result, file string, err error) {
	res.Errors = append(res.Errors, FileError{File: file, Err: "precise: " + err.Error()})
}

func appendPreciseNote(res *Result, note string) {
	if res.PreciseNote == "" {
		res.PreciseNote = note
		return
	}
	res.PreciseNote += "; " + note
}

// resolvePreciseEdges runs the go/types pass and supersedes name-based call edges
// resolvePreciseEdgesFromIndex is the shared-node-index path: it does the
// go/types resolve, then calls resolvePreciseEdgesWith using the already-built
// nodeIndex (avoiding a redundant ProjectNodes call when the caller built one).
func (ix *Indexer) resolvePreciseEdgesFromIndex(ctx context.Context, projectID int64, root string, res *Result, ni *nodeIndex) error {
	if _, err := exec.LookPath("go"); err != nil {
		res.PreciseNote = "precise skipped: the 'go' toolchain is required for --precise but is not on PATH; kept name-based edges"
		return nil
	}
	pr, err := typesrc.Resolve(ctx, root)
	if err != nil {
		res.PreciseNote = "precise unavailable: " + err.Error() + "; kept name-based edges"
		return nil
	}
	if pr == nil || !pr.Available {
		res.PreciseNote = "precise unavailable: project is not a buildable Go module; kept name-based edges"
		return nil
	}
	if pr.ErrorPkgs > 0 {
		res.PreciseNote = fmt.Sprintf("precise skipped for %d package(s) with type errors; kept name-based edges for those packages", pr.ErrorPkgs)
	}
	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		res.PreciseNote = "precise transaction failed: " + err.Error()
		return fmt.Errorf("begin Go precise transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ix.resolvePreciseEdgesWith(tx, projectID, res, ni, pr); err != nil {
		return fmt.Errorf("write Go precise graph: %w", err)
	}
	if err := tx.Commit(); err != nil {
		res.PreciseNote = "precise commit failed: " + err.Error()
		return fmt.Errorf("commit Go precise transaction: %w", err)
	}
	return nil
}

// resolvePreciseEdgesWith is the shared resolver that takes a pre-built nodeIndex
// and the go/types result.
func (ix *Indexer) resolvePreciseEdgesWith(tx *sql.Tx, projectID int64, res *Result, ni *nodeIndex, pr *typesrc.Result) error {
	fqnTo := ni.fqnTo
	posTo := make(map[precisePos]int64, len(ni.nodes))
	posCollide := map[precisePos]bool{} // (file,line) shared by >1 decl — ambiguous
	var cleanSources []int64
	goFiles := map[string]bool{}
	for _, n := range ni.nodes {
		key := precisePos{n.FilePath, n.StartLine}
		if _, dup := posTo[key]; dup {
			posCollide[key] = true // e.g. two decls on one line (un-gofmt'd)
		} else {
			posTo[key] = n.ID
		}
		if pr.CleanFiles[n.FilePath] {
			cleanSources = append(cleanSources, n.ID)
		}
		if n.Language == "go" && n.Kind == graph.KindFile {
			goFiles[n.FilePath] = true
		}
	}
	// Remove ambiguous (file,line) keys so the position join misses for them and
	// falls back to the unique FQN match — robust to multiple decls on one line.
	for key := range posCollide {
		delete(posTo, key)
	}
	// A completed go/types run gives a clean/error partition. Clear prior
	// coverage for every indexed Go file; only CleanFiles are re-marked below.
	// This truthfully downgrades packages that now fail type checking.
	for file := range goFiles {
		if err := graph.ClearCallGraphResolvedTx(tx, projectID, file); err != nil {
			res.PreciseNote = "precise coverage clear failed: " + err.Error()
			return err
		}
	}

	// Drop the name-based call edges of every clean source, then re-insert the
	// precise ones below. Doing the delete first (on provenance='name', regardless
	// of weight) is what prevents the in-package WeightLSP=1.0 name edges from
	// surviving and double-counting against the precise 1.0 edges.
	if err := graph.DeleteCallEdgesBySourceTx(tx, cleanSources, graph.ProvName); err != nil {
		res.PreciseNote = "precise supersede (delete name) failed: " + err.Error()
		return err
	}
	// Pin P0-05: delete prior ProvPrecise edges too. The edges table has no
	// UNIQUE constraint, so a second --precise run would double-insert.
	if err := graph.DeleteCallEdgesBySourceTx(tx, cleanSources, graph.ProvPrecise); err != nil {
		res.PreciseNote = "precise supersede (delete prior precise) failed: " + err.Error()
		return err
	}

	upgraded, skipped := 0, 0
	for _, e := range pr.Edges {
		if e.External {
			continue // stdlib/dep callee — no codemap node to point at
		}
		from, ok := fqnTo[e.CallerFQN]
		if !ok {
			skipped++
			continue
		}
		to, ok := posTo[precisePos{e.CalleeFile, e.CalleeLine}]
		if !ok {
			// Position join missed (rare) — fall back to the FQN; still a miss for
			// e.g. an interface method, which has no declaration node in slice 1.
			if to, ok = fqnTo[e.CalleeFQN]; !ok {
				skipped++
				continue
			}
		}
		if to == from {
			continue
		}
		if _, err := graph.AddEdgeProvTx(tx, from, to, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
			res.PreciseNote = "precise edge insert failed: " + err.Error()
			return err
		}
		upgraded++
	}
	for file := range pr.CleanFiles {
		if err := graph.MarkCallGraphResolvedTx(tx, projectID, file, preciseResolverGoTypes); err != nil {
			res.PreciseNote = "precise coverage mark failed: " + err.Error()
			return err
		}
	}
	res.PreciseUpgraded = upgraded
	res.PreciseSkipped = skipped
	if upgraded == 0 && res.PreciseNote == "" {
		res.PreciseNote = "precise pass completed; no in-module call edges found (leaf project, or all calls external/dynamic)"
	}
	return nil
}

func samePackage(ids []int64, dirOf map[int64]string, dir string) []int64 {
	var out []int64
	for _, id := range ids {
		if dirOf[id] == dir {
			out = append(out, id)
		}
	}
	return out
}

// embedText builds the text embedded for a symbol: docstring + signature +
// source, so meaning and structure both inform the vector. maxChars > 0 caps the
// result (keeping the semantically-dense docstring+signature, truncating a long
// body) — embedding cost is ~linear in tokens, so a cap trades some body recall
// for a faster reindex. The leading docstring+signature are never truncated when
// they alone fit, so a cap only ever drops the tail of a long source body.
func embedText(s extract.Symbol, maxChars int) string {
	var b strings.Builder
	if s.Docstring != "" {
		b.WriteString(s.Docstring)
		b.WriteByte('\n')
	}
	if s.Signature != "" {
		b.WriteString(s.Signature)
		b.WriteByte('\n')
	}
	b.WriteString(s.Source)
	out := b.String()
	if maxChars > 0 && len(out) > maxChars {
		// Truncate on a rune boundary so the embedder never sees a split UTF-8 rune.
		out = out[:maxChars]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// generatedHeaderRE matches the canonical machine-generated-file marker that
// Go tooling (protoc-gen-go, sqlc, stringer, mockgen, …) emits and that `go`
// itself recognizes: a line `// Code generated <by what>. DO NOT EDIT.`.
var generatedHeaderRE = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// isGenerated reports whether src carries the canonical generated-file header.
// Per the Go convention the marker appears before the package clause, so only
// the leading region is scanned (cheap, and avoids a stray match deeper in a
// file). Catches generated code regardless of filename, complementing the
// *_gen.go / *.pb.go exclude globs.
func isGenerated(src []byte) bool {
	limit := len(src)
	if limit > 4096 {
		limit = 4096
	}
	for _, line := range strings.Split(string(src[:limit]), "\n") {
		line = strings.TrimSpace(line)
		if generatedHeaderRE.MatchString(line) {
			return true
		}
		if strings.HasPrefix(line, "package ") { // real code started; marker must precede it
			break
		}
	}
	return false
}

// RegisterLSPForProject is the public entry point for spawning and registering
// language-server-backed extractors for the languages the project at root
// actually contains. The daemon calls this on startup so its IndexFiles path
// (which only routes to registered extractors) covers TS/JS/Python/Vue, not
// just Go (P0-11: pre-fix the daemon watched Go only, so any non-Go edit
// silently went stale while info.Watching stayed true). Returns a
// MissingServers map for languages whose server isn't on PATH or fails to
// spawn, so the daemon can surface "watching Go only; install
// typescript-language-server for TS" in its status.
//
// The actual loop body is the same code registerLSP runs inside
// IndexProject, lifted into a public method so the daemon (which builds
// its own indexer and never calls IndexProject after the first index) can
// reuse it.
func (ix *Indexer) RegisterLSPForProject(ctx context.Context, root string) (map[string]string, error) {
	res := &Result{}
	if !ix.detectPresentLanguagesForLSP(root, res) {
		return res.MissingServers, nil
	}
	ix.registerLSP(ctx, root, res.Unsupported, res)
	return res.MissingServers, nil
}

// detectPresentLanguagesForLSP does a single WalkDir to learn which
// language-server languages the project contains, matching the indexer's
// walk semantics so the daemon's view of "present languages" matches
// what an index pass would have seen. Extracted from registerLSP so the
// loop body is testable in isolation.
func (ix *Indexer) detectPresentLanguagesForLSP(root string, res *Result) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		if ix.excluded(rel) {
			return nil
		}
		lang := extract.LanguageForPath(path)
		if lang == "" {
			return nil
		}
		if res.Unsupported == nil {
			res.Unsupported = map[string]int{}
		}
		res.Unsupported[lang]++
		// A recognized language that has an LSP server backing it counts
		// as "present" — even if no extractor is wired yet, registerLSP
		// will pick it up.
		for _, spec := range lspsrc.DefaultServers {
			for _, lb := range spec.Langs {
				if lb.Lang == lang {
					found = true
					return nil
				}
			}
		}
		// vue is a separate case in registerLSP.
		if lang == "vue" {
			found = true
		}
		return nil
	})
	return found
}
