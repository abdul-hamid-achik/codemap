// Package index walks a project, extracts its structure, embeds node sources,
// and stores the result as a graph (SQLite) plus vectors (veclite). Indexing is
// incremental: files whose content hash is unchanged are skipped. A full
// reindex (Options.Reindex) wipes the project and rebuilds everything.
package index

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/typesrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/vuesrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
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

// FileError records a per-file failure that didn't abort the whole run.
type FileError struct {
	File string `json:"file"`
	Err  string `json:"error"`
}

// Result summarizes an index run.
type Result struct {
	FilesScanned int         `json:"files_scanned"`
	FilesIndexed int         `json:"files_indexed"`           // new or changed
	FilesSkipped int         `json:"files_skipped"`           // unchanged, too large (see Oversized), or errored (see Errors)
	FilesDeleted int         `json:"files_deleted,omitempty"` // pruned: indexed before, now gone from disk
	Nodes        int         `json:"nodes"`
	Edges        int         `json:"edges"`
	Errors       []FileError `json:"errors,omitempty"`
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
	// (the skipped-file count is in Unsupported[lang]).
	MissingServers map[string]string `json:"missing_servers,omitempty"`
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
		if _, err := exec.LookPath(spec.Cmd); err != nil {
			for _, lb := range want {
				noteMissingServer(res, lb.Lang, spec.Cmd)
			}
			continue
		}
		// Spawn the server ONCE (the first present language owns it), then bind the
		// rest to the same connection — one typescript-language-server serves both
		// TS and JS, each routed with its own languageId.
		owner, err := lspsrc.New(ctx, want[0].Lang, want[0].LangID, root, spec.Cmd, spec.Args...)
		if err != nil {
			for _, lb := range want {
				noteMissingServer(res, lb.Lang, spec.Cmd) // spawn/init failed — treat as absent
			}
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
			noteMissingServer(res, "vue", "typescript-language-server")
			return false
		}
		if _, err := exec.LookPath(spec.Cmd); err != nil {
			noteMissingServer(res, "vue", spec.Cmd)
			return false
		}
		owner, err := lspsrc.New(ctx, spec.Langs[0].Lang, spec.Langs[0].LangID, root, spec.Cmd, spec.Args...)
		if err != nil {
			noteMissingServer(res, "vue", spec.Cmd)
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
		noteMissingServer(res, "vue", "typescript-language-server")
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

// New returns an indexer with the default backends registered (currently the
// pure-Go go/parser Go backend).
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

	if opts.Reindex {
		if err := ix.graph.WipeProject(projectID); err != nil {
			return nil, err
		}
		if ix.vectors != nil {
			if _, err := ix.vectors.DeleteByProject(projectName); err != nil {
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
	// Go files (gosrc, pure go/parser — stateless and thread-safe) are extracted
	// concurrently with a bounded worker pool. Graph writes serialize naturally
	// on the single-connection pool (SetMaxOpenConns(1)), so the parallelism
	// overlaps CPU-bound parsing with I/O-bound graph writes — a 3–5x speedup on
	// Go-heavy repos. LSP-backed files (TypeScript, Python) stay sequential: the
	// language-server connection is stateful and the parseWait retry loop paces
	// codemap to the server's parse rate, so parallelism there needs careful
	// benchmarking (planned for a later iteration).
	var pending []extract.Reference
	var embedAcc []embedItem
	var mu sync.Mutex   // guards res, embedAcc, pending across parallel Go workers
	total := len(files) // == res.FilesScanned; the bar's denominator
	var fileDone int64  // atomic counter for OnFile progress reporting

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

	// LSP files: sequential (stateful server connection).
	for _, ft := range lspFiles {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if opts.OnFile != nil {
			opts.OnFile(int(atomic.AddInt64(&fileDone, 1)), total, ft.rel)
		}
		changed, refs, toEmbed, err := ix.indexFile(ctx, projectID, projectName, ft, opts, res)
		if err != nil {
			res.Errors = append(res.Errors, FileError{File: ft.rel, Err: err.Error()})
			res.FilesSkipped++
			continue
		}
		if changed {
			pending = append(pending, refs...)
			embedAcc = append(embedAcc, toEmbed...)
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
	if _, err := ix.resolveEdgesWith(projectID, pending, ni); err != nil {
		return res, err
	}

	// Pass 3 (opt-in): exact call edges. For Go, the go/types pass replaces
	// name-based edges (only run when the project has Go). For LSP-backed languages
	// (TypeScript), callHierarchy adds precise call edges where there were none.
	if opts.Precise {
		preciseStart = time.Now()
		if res.Languages["go"] > 0 {
			ix.resolvePreciseEdgesFromIndex(ctx, projectID, root, res, ni)
		}
		ix.resolveLSPCallEdgesWith(ctx, projectID, root, res, ni)
		res.PreciseMs = int(time.Since(preciseStart).Milliseconds())
	}

	// Pass 4: embed all collected nodes in large concurrent batches.
	embedStart = time.Now()
	if err := ix.embedAndStore(ctx, embedAcc, opts, res); err != nil {
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
// pruneDeleted but scoped to the watched set. Edges for the changed files are
// resolved once at the end. (Like the full incremental path, inbound name-based
// edges from UNCHANGED files into a changed file are only refreshed on a full
// reindex — the daemon can reconcile periodically.)
func (ix *Indexer) IndexFiles(ctx context.Context, projectID int64, projectName, root string, rels []string, opts Options) (*Result, error) {
	res := &Result{}
	var pending []extract.Reference
	var embedAcc []embedItem
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
				if ix.vectors != nil {
					if _, err := ix.vectors.DeleteByFile(projectName, rel); err != nil {
						return res, err
					}
				}
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
		changed, refs, toEmbed, err := ix.indexFile(ctx, projectID, projectName, fileTask{abs: abs, rel: rel, lang: lang, ext: ext}, opts, res)
		if err != nil {
			res.Errors = append(res.Errors, FileError{File: rel, Err: err.Error()})
			res.FilesSkipped++
			continue
		}
		if changed {
			pending = append(pending, refs...)
			embedAcc = append(embedAcc, toEmbed...)
		}
	}
	ni, err := ix.buildNodeIndex(projectID)
	if err != nil {
		return res, err
	}
	if _, err := ix.resolveEdgesWith(projectID, pending, ni); err != nil {
		return res, err
	}
	if err := ix.embedAndStore(ctx, embedAcc, opts, res); err != nil {
		return res, err
	}
	if ix.vectors != nil {
		if err := ix.vectors.Sync(); err != nil {
			return res, err
		}
	}
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
// any pattern. Semantics:
//
//   - A pattern WITHOUT a slash matches any single path segment, so "node_modules"
//     or "migrations" or "*.min.js" skips that file/dir at any depth.
//   - A pattern WITH a slash matches a segment-wise path PREFIX anchored at the
//     project root, so "db/migrations" skips db/migrations and everything under it
//     but not app/db/migrations.
//   - A "**/" prefix un-anchors a slash pattern so it matches that prefix starting
//     at any depth, so "**/db/migrations" also skips app/db/migrations.
//
// A bare base name (one segment, no slash) is a valid rel and matches the no-slash
// rules — so callers may pass either a full relative path or a base name.
func matchExclude(patterns []string, rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, pat := range patterns {
		pat = strings.Trim(filepath.ToSlash(pat), "/")
		if pat == "" {
			continue
		}
		if !strings.ContainsRune(pat, '/') {
			for _, s := range segs {
				if ok, _ := filepath.Match(pat, s); ok {
					return true
				}
			}
			continue
		}
		anyDepth := strings.HasPrefix(pat, "**/")
		parts := strings.Split(strings.TrimPrefix(pat, "**/"), "/")
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
		// Track the hash of this scanned-but-skipped file so staleness doesn't
		// report it as perpetually "new"; a content change re-picks it up.
		return false, nil, nil, ix.graph.SetFileHash(projectID, ft.rel, hash)
	}
	if isGenerated(content) {
		// Generated code (protoc/sqlc/stringer/…) isn't hand-written source — skip
		// it so it doesn't pollute find/symbols/orphans. Detected by the canonical
		// header regardless of filename (the *_gen.go/*.pb.go globs catch the rest).
		// Record the hash like oversized, so staleness doesn't flag it as "new".
		res.FilesSkipped++
		res.Generated = append(res.Generated, ft.rel)
		return false, nil, nil, ix.graph.SetFileHash(projectID, ft.rel, hash)
	}

	if !opts.Reindex {
		prev, err := ix.graph.FileHash(projectID, ft.rel)
		if err != nil {
			return false, nil, nil, err
		}
		if prev == hash {
			res.FilesSkipped++
			return false, nil, nil, nil
		}
	}

	// Changed: clear the old structure (edges cascade) and vectors for this file.
	// The veclite delete is outside the graph transaction (separate store).
	if ix.vectors != nil {
		if _, err := ix.vectors.DeleteByFile(projectName, ft.rel); err != nil {
			return false, nil, nil, err
		}
	}

	fr, err := ft.ext.ExtractFile(ft.rel, content)
	if err != nil {
		// The file was scanned but can't be parsed/extracted. Record its hash so
		// staleness doesn't report it as perpetually "new" (the bug: a parse-error
		// file never entered index_state, so every status showed "1 new" forever).
		// A later edit that fixes the error changes the hash and re-indexes it; its
		// old nodes were already cleared above, which is correct for a broken file.
		if herr := ix.graph.SetFileHash(projectID, ft.rel, hash); herr != nil {
			return false, nil, nil, herr
		}
		return false, nil, nil, err
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
func (ix *Indexer) embedAndStore(ctx context.Context, items []embedItem, opts Options, res *Result) error {
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
		// already stored, so keep it and report — matching the long-standing
		// "structure works without Ollama" behavior, just at the project level.
		res.EmbedNote = "embeddings skipped: " + err.Error()
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

// nodeIndex is the project-wide symbol index built once from ProjectNodes and
// reused across all edge-resolution passes (resolveEdges, resolvePreciseEdges,
// resolveLSPCallEdges). Previously each pass called ProjectNodes independently —
// 3 full table scans on a --precise index. Now we build it once and pass it.
type nodeIndex struct {
	nodes []graph.Node
	fqnTo map[string]int64
	symTo map[string][]int64
	posTo map[precisePos]int64
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
		posTo: make(map[precisePos]int64, len(nodes)),
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
		// position map (used by precise passes); collisions are resolved per-pass
		key := precisePos{n.FilePath, n.StartLine}
		if _, dup := ni.posTo[key]; dup {
			// mark ambiguous by zeroing — precise passes handle this themselves
			ni.posTo[key] = 0
		} else {
			ni.posTo[key] = n.ID
		}
	}
	return ni, nil
}

// resolveEdges links references (from changed files) to target nodes by name,
// resolveEdgesWith is the shared resolver that takes a pre-built nodeIndex,
// avoiding a redundant ProjectNodes call when the caller already built one.
func (ix *Indexer) resolveEdgesWith(projectID int64, refs []extract.Reference, ni *nodeIndex) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}

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
			if _, err := ix.graph.AddEdge(from, to, ref.Kind, weight); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// precisePos keys a node by its declaration position for the precise callee join.
type precisePos struct {
	file string
	line int
}

// resolveLSPCallEdges adds exact call edges for LSP-backed languages (TypeScript)
// by driving each registered CallResolver's callHierarchy over its files, joining
// callees to nodes by declaration position. Edges are written ProvPrecise (there's
// no name-based call extraction for these languages to supersede). Best-effort:
// errors skip a file, never abort. The servers are still alive (closed by the
// resolveLSPCallEdgesWith is the shared resolver that takes a pre-built nodeIndex.
func (ix *Indexer) resolveLSPCallEdgesWith(ctx context.Context, projectID int64, root string, res *Result, ni *nodeIndex) {
	resolvers := map[string]extract.CallResolver{} // language -> resolver
	for lang, e := range ix.extractors {
		if cr, ok := e.(extract.CallResolver); ok {
			resolvers[lang] = cr
		}
	}
	if len(resolvers) == 0 {
		return
	}
	// Rebuild posTo/fqnTo from the shared nodeIndex, scoped to LSP-language nodes.
	fqnTo := make(map[string]int64, len(ni.nodes))
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
		if n.FQN != "" {
			fqnTo[n.FQN] = n.ID
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

	// Pin P0-05: collect the LSP-language source node ids so we can delete any
	// PRIOR ProvPrecise call edges for them. The LSP languages have no
	// name-based call edges to supersede (they live entirely in --precise), but
	// without this delete-first every --precise run still doubled the
	// precise-edge count via the same bare-INSERT path the Go pass uses.
	var lspSourceIDs []int64
	for _, n := range ni.nodes {
		if _, isLSP := resolvers[n.Language]; !isLSP {
			continue
		}
		if n.Kind == graph.KindFile {
			continue
		}
		lspSourceIDs = append(lspSourceIDs, n.ID)
	}
	if len(lspSourceIDs) > 0 {
		if dErr := ix.graph.DeleteCallEdgesBySource(lspSourceIDs, graph.ProvPrecise); dErr != nil {
			res.PreciseNote = "LSP precise supersede (delete prior) failed: " + dErr.Error()
			return
		}
	}

	upgraded := 0
	for lang, cr := range resolvers {
		for file := range filesByLang[lang] {
			edges, cErr := cr.CallEdges(ctx, file)
			if cErr != nil {
				continue
			}
			for _, e := range edges {
				if e.External {
					continue // callee outside the project — no node
				}
				from, ok := fqnTo[e.FromFQN]
				if !ok {
					continue
				}
				to, ok := posTo[precisePos{e.ToFile, e.ToLine}]
				if !ok || to == from {
					continue
				}
				if _, aErr := ix.graph.AddEdgeProv(from, to, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); aErr != nil {
					return
				}
				upgraded++
			}
		}
	}
	res.PreciseUpgraded += upgraded
}

// resolvePreciseEdges runs the go/types pass and supersedes name-based call edges
// resolvePreciseEdgesFromIndex is the shared-node-index path: it does the
// go/types resolve, then calls resolvePreciseEdgesWith using the already-built
// nodeIndex (avoiding a redundant ProjectNodes call when the caller built one).
func (ix *Indexer) resolvePreciseEdgesFromIndex(ctx context.Context, projectID int64, root string, res *Result, ni *nodeIndex) {
	if _, err := exec.LookPath("go"); err != nil {
		res.PreciseNote = "precise skipped: the 'go' toolchain is required for --precise but is not on PATH; kept name-based edges"
		return
	}
	pr, err := typesrc.Resolve(ctx, root)
	if err != nil {
		res.PreciseNote = "precise unavailable: " + err.Error() + "; kept name-based edges"
		return
	}
	if pr == nil || !pr.Available {
		res.PreciseNote = "precise unavailable: project is not a buildable Go module; kept name-based edges"
		return
	}
	ix.resolvePreciseEdgesWith(ctx, projectID, root, res, ni, pr)
}

// resolvePreciseEdgesWith is the shared resolver that takes a pre-built nodeIndex
// and the go/types result.
func (ix *Indexer) resolvePreciseEdgesWith(ctx context.Context, projectID int64, root string, res *Result, ni *nodeIndex, pr *typesrc.Result) {
	fqnTo := ni.fqnTo
	posTo := make(map[precisePos]int64, len(ni.nodes))
	posCollide := map[precisePos]bool{} // (file,line) shared by >1 decl — ambiguous
	var cleanSources []int64
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
	}
	// Remove ambiguous (file,line) keys so the position join misses for them and
	// falls back to the unique FQN match — robust to multiple decls on one line.
	for key := range posCollide {
		delete(posTo, key)
	}
	// Drop the name-based call edges of every clean source, then re-insert the
	// precise ones below. Doing the delete first (on provenance='name', regardless
	// of weight) is what prevents the in-package WeightLSP=1.0 name edges from
	// surviving and double-counting against the precise 1.0 edges.
	if err := ix.graph.DeleteCallEdgesBySource(cleanSources, graph.ProvName); err != nil {
		res.PreciseNote = "precise supersede (delete name) failed: " + err.Error()
		return
	}
	// Pin P0-05: delete prior ProvPrecise edges too. The edges table has no
	// UNIQUE constraint, so a second --precise run would double-insert.
	if err := ix.graph.DeleteCallEdgesBySource(cleanSources, graph.ProvPrecise); err != nil {
		res.PreciseNote = "precise supersede (delete prior precise) failed: " + err.Error()
		return
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
		if _, err := ix.graph.AddEdgeProv(from, to, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
			res.PreciseNote = "precise edge insert failed: " + err.Error()
			return
		}
		upgraded++
	}
	res.PreciseUpgraded = upgraded
	res.PreciseSkipped = skipped
	if upgraded == 0 {
		res.PreciseNote = "precise pass resolved no in-module call edges (single-package leaf project, or all calls external/dynamic); kept name-based edges"
	}
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
