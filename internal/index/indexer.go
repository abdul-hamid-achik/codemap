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
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/typesrc"
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
	// Oversized lists recognized source files skipped for exceeding
	// index.max_file_bytes — surfaced so a silently-missing file (often generated)
	// is explained, not just counted in FilesSkipped.
	Oversized []string `json:"oversized,omitempty"`
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
	extractors map[string]extract.Extractor
	closers    []io.Closer // stateful extractors (e.g. spawned language servers) to shut down
}

// registerLSP spawns and registers a language-server-backed extractor for each
// DefaultServers spec whose language is actually present in the project (present
// is the post-walk unsupported map: recognized languages with no extractor yet).
// A language whose server isn't on PATH, or fails to spawn, is recorded in
// res.MissingServers and skipped — never fatal. Returns true if it registered at
// least one extractor (so the caller re-walks to route those files).
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
	return registered
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
		extractors: map[string]extract.Extractor{},
	}
	ix.Register(gosrc.New())
	return ix
}

// Register adds (or replaces) the extractor for a language.
func (ix *Indexer) Register(e extract.Extractor) { ix.extractors[e.Language()] = e }

type fileTask struct {
	abs  string
	rel  string
	lang string
	ext  extract.Extractor
}

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

	// Pass 1: extract + store nodes (and embeddings) for changed files. Collect
	// the references emitted by changed files for edge resolution.
	var pending []extract.Reference
	total := len(files) // == res.FilesScanned; the bar's denominator
	for i, ft := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if opts.OnFile != nil {
			opts.OnFile(i+1, total, ft.rel)
		}
		changed, refs, err := ix.indexFile(ctx, projectID, projectName, ft, opts, res)
		if err != nil {
			// An errored file (e.g. a language server that timed out) wasn't indexed —
			// count it as skipped so scanned = indexed + skipped, and record why.
			res.Errors = append(res.Errors, FileError{File: ft.rel, Err: err.Error()})
			res.FilesSkipped++
			continue
		}
		if changed {
			pending = append(pending, refs...)
		}
	}

	// Pass 2: resolve references into edges against the project-wide symbol map.
	if _, err := ix.resolveEdges(projectID, pending); err != nil {
		return res, err
	}

	// Pass 3 (opt-in): exact call edges. For Go, the go/types pass replaces
	// name-based edges (only run when the project has Go). For LSP-backed languages
	// (TypeScript), callHierarchy adds precise call edges where there were none.
	if opts.Precise {
		if res.Languages["go"] > 0 {
			ix.resolvePreciseEdges(ctx, projectID, root, res)
		}
		ix.resolveLSPCallEdges(ctx, projectID, root, res)
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
		if d.IsDir() {
			if path != root && (ix.excluded(name) || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if ix.excluded(name) {
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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		files = append(files, fileTask{abs: path, rel: rel, lang: lang, ext: ext})
		return nil
	})
	return files, unsupported, err
}

// excluded reports whether a file or directory base name matches any configured
// exclude glob (e.g. "node_modules", "*.min.js").
func (ix *Indexer) excluded(name string) bool {
	for _, pat := range ix.cfg.Exclude {
		if ok, _ := filepath.Match(pat, name); ok {
			return true
		}
	}
	return false
}

func (ix *Indexer) indexFile(ctx context.Context, projectID int64, projectName string, ft fileTask, opts Options, res *Result) (bool, []extract.Reference, error) {
	content, err := os.ReadFile(ft.abs)
	if err != nil {
		return false, nil, err
	}
	hash := sha256hex(content)
	if ix.cfg.MaxFileBytes > 0 && len(content) > ix.cfg.MaxFileBytes {
		res.FilesSkipped++
		res.Oversized = append(res.Oversized, ft.rel)
		// Track the hash of this scanned-but-skipped file so staleness doesn't
		// report it as perpetually "new"; a content change re-picks it up.
		return false, nil, ix.graph.SetFileHash(projectID, ft.rel, hash)
	}

	if !opts.Reindex {
		prev, err := ix.graph.FileHash(projectID, ft.rel)
		if err != nil {
			return false, nil, err
		}
		if prev == hash {
			res.FilesSkipped++
			return false, nil, nil
		}
	}

	// Changed: clear the old structure (edges cascade) and vectors for this file.
	if err := ix.graph.DeleteNodesInFile(projectID, ft.rel); err != nil {
		return false, nil, err
	}
	if ix.vectors != nil {
		if _, err := ix.vectors.DeleteByFile(projectName, ft.rel); err != nil {
			return false, nil, err
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
			return false, nil, herr
		}
		return false, nil, err
	}

	// File node.
	lines := bytes.Count(content, []byte("\n")) + 1
	fileID, err := ix.graph.AddNode(&graph.Node{
		ProjectID: projectID, FilePath: ft.rel, Kind: graph.KindFile,
		Language: ft.lang, StartLine: 1, EndLine: lines, SourceHash: hash,
	})
	if err != nil {
		return false, nil, err
	}

	type embedItem struct {
		nodeID  int64
		content string
		meta    vector.NodeMeta
	}
	var toEmbed []embedItem

	for _, sym := range fr.Symbols {
		nid, err := ix.graph.AddNode(&graph.Node{
			ProjectID: projectID, FilePath: ft.rel, Symbol: sym.Name, FQN: sym.FQN,
			Kind: sym.Kind, Language: sym.Language, StartLine: sym.StartLine, EndLine: sym.EndLine,
			Signature: sym.Signature, Docstring: sym.Docstring, SourceHash: sha256hex([]byte(sym.Source)),
		})
		if err != nil {
			return false, nil, err
		}
		if _, err := ix.graph.AddEdge(fileID, nid, graph.EdgeDefines, graph.WeightLSP); err != nil {
			return false, nil, err
		}
		if ix.embedder != nil && ix.vectors != nil {
			toEmbed = append(toEmbed, embedItem{
				nodeID:  nid,
				content: embedText(sym),
				meta: vector.NodeMeta{
					NodeID: nid, Project: projectName, File: ft.rel, Symbol: sym.Name,
					FQN: sym.FQN, Kind: sym.Kind, Language: sym.Language,
					StartLine: sym.StartLine, EndLine: sym.EndLine,
				},
			})
		}
	}

	if len(toEmbed) > 0 {
		texts := make([]string, len(toEmbed))
		for i := range toEmbed {
			texts[i] = toEmbed[i].content
		}
		vecs, err := ix.embedder.Embed(ctx, texts)
		if err != nil {
			return false, nil, fmt.Errorf("embed %s: %w", ft.rel, err)
		}
		if len(vecs) != len(toEmbed) {
			return false, nil, fmt.Errorf("embed %s: got %d vectors for %d symbols", ft.rel, len(vecs), len(toEmbed))
		}
		for i, item := range toEmbed {
			vid, err := ix.vectors.Insert(vecs[i], item.content, item.meta)
			if err != nil {
				return false, nil, err
			}
			if err := ix.graph.UpdateNodeVecID(item.nodeID, strconv.FormatUint(vid, 10)); err != nil {
				return false, nil, err
			}
		}
	}

	if err := ix.graph.SetFileHash(projectID, ft.rel, hash); err != nil {
		return false, nil, err
	}
	res.FilesIndexed++
	return true, fr.References, nil
}

// resolveEdges links references (from changed files) to target nodes by name,
// against the project-wide symbol index. Same-named targets all get an edge at
// tree-sitter/parser confidence (0.7); precise resolution arrives with the LSP
// backend. Self-edges are skipped.
func (ix *Indexer) resolveEdges(projectID int64, refs []extract.Reference) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	nodes, err := ix.graph.ProjectNodes(projectID)
	if err != nil {
		return 0, err
	}
	fqnTo := make(map[string]int64, len(nodes))
	symTo := make(map[string][]int64, len(nodes))
	dirOf := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		if n.FQN != "" {
			fqnTo[n.FQN] = n.ID
		}
		// File-scope references (function values in top-level decls) are attributed
		// to the file path; key file nodes by path so those refs resolve. Paths have
		// slashes and FQNs have dots, so the two key spaces never collide.
		if n.Kind == graph.KindFile {
			fqnTo[n.FilePath] = n.ID
		}
		if n.Symbol != "" {
			symTo[n.Symbol] = append(symTo[n.Symbol], n.ID)
		}
		dirOf[n.ID] = filepath.Dir(n.FilePath)
	}

	count := 0
	for _, ref := range refs {
		from, ok := fqnTo[ref.From]
		if !ok {
			continue
		}
		candidates := symTo[ref.To]
		// An unqualified call (Foo()) resolves within the caller's package, so
		// restrict to same-directory targets — precise, and avoids cross-package
		// false edges to same-named symbols. Fall back to all matches only if the
		// same-package restriction finds nothing.
		if !ref.Qualified {
			if same := samePackage(candidates, dirOf, dirOf[from]); len(same) > 0 {
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
// deferred ix.Close() after this runs).
func (ix *Indexer) resolveLSPCallEdges(ctx context.Context, projectID int64, root string, res *Result) {
	resolvers := map[string]extract.CallResolver{} // language -> resolver
	for lang, e := range ix.extractors {
		if cr, ok := e.(extract.CallResolver); ok {
			resolvers[lang] = cr
		}
	}
	if len(resolvers) == 0 {
		return
	}
	nodes, err := ix.graph.ProjectNodes(projectID)
	if err != nil {
		return
	}
	fqnTo := make(map[string]int64, len(nodes))
	posTo := make(map[precisePos]int64, len(nodes))
	posCollide := map[precisePos]bool{}
	filesByLang := map[string]map[string]bool{}
	for _, n := range nodes {
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
// with exact ones for cleanly type-checked packages. It mutates res (PreciseUpgraded
// /Skipped/Note) and never returns an error: any failure degrades to the name
// baseline, which is already in place. The invariant is "a clean source either gets
// its precise edges or keeps its name edges entirely" — supersede only deletes a
// source's name edges in the same pass that re-inserts its precise ones.
func (ix *Indexer) resolvePreciseEdges(ctx context.Context, projectID int64, root string, res *Result) {
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

	nodes, err := ix.graph.ProjectNodes(projectID)
	if err != nil {
		res.PreciseNote = "precise failed loading nodes: " + err.Error()
		return
	}
	fqnTo := make(map[string]int64, len(nodes))
	posTo := make(map[precisePos]int64, len(nodes))
	posCollide := map[precisePos]bool{} // (file,line) shared by >1 decl — ambiguous
	var cleanSources []int64
	for _, n := range nodes {
		if n.FQN != "" {
			fqnTo[n.FQN] = n.ID
		}
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
		res.PreciseNote = "precise supersede (delete) failed: " + err.Error()
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
// source, so meaning and structure both inform the vector.
func embedText(s extract.Symbol) string {
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
	return b.String()
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
