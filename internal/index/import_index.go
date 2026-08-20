package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// importIndex maps a Go import spec (e.g. "github.com/foo/bar/baz") and a
// relative specifier (e.g. "./utils") to the project file the import
// resolves to. It's read-only after construction and shared across the
// indexer worker pool — one allocation per IndexProject call.
//
// P2-04 (O30): the `imports` edge type was declared in the schema but had
// zero writers. This index is the missing piece: a deferred pass at the
// end of IndexProject resolves fr.Imports against it (per file, in one
// tx) and writes a file→file EdgeImports. Without the index, every
// per-file resolution would re-walk the project looking for the imported
// package, and parallel indexers (Go files) would race on shared state.
type importIndex struct {
	root string

	// goModulePath is the project's go.mod `module` line, or "" when
	// the project has no go.mod (a vendored snippet, a test fixture).
	goModulePath string

	// goFiles maps an importable package key to one canonical project
	// file in that package. The value is a slash-normalized
	// project-relative file path. Both with and without the modPath
	// prefix are stored so a vendored snippet (no modPath) and a
	// normal project both resolve their bare-dir imports.
	goFiles map[string]string

	// relFiles is every project's relative path (slash-normalized)
	// for fast relative-specifier resolution. The TS/JS backends
	// emit "./foo" / "../bar/baz" — we normalize and look it up.
	relFiles map[string]bool

	// jsPackages maps a workspace package.json "name" (e.g.
	// "@cartographer/domain") to its root-relative directory, and
	// jsPkgEntry maps that same name to its resolved entry file —
	// so a monorepo's bare cross-package imports become file→file
	// edges (the apps↔packages bridges in codemap_map) instead of
	// being dropped as external.
	jsPackages map[string]string
	jsPkgEntry map[string]string
}

func newImportIndex(root string, files []fileTask) *importIndex {
	idx := &importIndex{
		root:         root,
		goModulePath: goModulePath(root),
		goFiles:      map[string]string{},
		relFiles:     map[string]bool{},
		jsPackages:   map[string]string{},
		jsPkgEntry:   map[string]string{},
	}
	jsDirs := map[string]bool{}
	for _, ft := range files {
		rel := filepath.ToSlash(ft.rel)
		idx.relFiles[rel] = true
		if ft.lang != "go" {
			for dir := filepath.ToSlash(filepath.Dir(rel)); ; dir = parentDir(dir) {
				jsDirs[dir] = true
				if dir == "" {
					break
				}
			}
			continue
		}
		// A Go file at "a/b.go" declares package "b" — the
		// directory, not the filename. We record the directory
		// the first time we see it; one entry per package is
		// enough for EdgeImports (file → file), since the edge
		// only needs to point at one canonical file in the
		// imported package.
		dir := filepath.ToSlash(filepath.Dir(ft.rel))
		if dir == "." {
			dir = ""
		}
		// Store BOTH the modPath-prefixed form (canonical) and the
		// bare-dir form (vendored snippet, short import).
		if idx.goModulePath != "" {
			modKey := idx.goModulePath + "/" + dir
			if _, ok := idx.goFiles[modKey]; !ok {
				idx.goFiles[modKey] = rel
			}
		}
		if _, ok := idx.goFiles[dir]; !ok {
			idx.goFiles[dir] = rel
		}
	}
	// Discover workspace packages: any ancestor directory of an indexed
	// non-Go source file that carries a package.json with a "name". Bounded
	// by the number of distinct source directories (node_modules is excluded
	// by the walk), so this is a handful of stats per index run. Iterate in
	// sorted order: map order is randomized, and when two directories declare
	// the same package name the winner must be deterministic across reindexes
	// (lexical, parents before children — not whichever the runtime yields first).
	sortedDirs := make([]string, 0, len(jsDirs))
	for dir := range jsDirs {
		sortedDirs = append(sortedDirs, dir)
	}
	sort.Strings(sortedDirs)
	for _, dir := range sortedDirs {
		name := jsPackageName(root, dir)
		if name == "" {
			continue
		}
		if _, dup := idx.jsPackages[name]; dup {
			continue // same name declared twice — keep the first, don't flip-flop
		}
		idx.jsPackages[name] = dir
	}
	for name, dir := range idx.jsPackages {
		if entry := jsPackageEntry(root, dir, idx); entry != "" {
			idx.jsPkgEntry[name] = entry
		}
	}
	return idx
}

// parentDir returns the slash-form parent of a root-relative directory, with
// "" for the project root ("." and "" both terminate).
func parentDir(dir string) string {
	if dir == "" || dir == "." {
		return ""
	}
	p := filepath.ToSlash(filepath.Dir(dir))
	if p == "." {
		return ""
	}
	return p
}

// goModulePath returns the project's go.mod module path, or "" when
// the project has no go.mod (e.g. a vendored snippet, a test fixture).
// It reads only the first 4 KB of the file (more than enough for a
// `module` line) so a huge go.mod is never loaded in full.
func goModulePath(root string) string {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 4096)
	n, err := readFull(f, buf)
	if err != nil && err != errUnexpectedEOF && err != errEOF {
		return ""
	}
	buf = buf[:n]
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// aliases to keep imports tight; readFull / errUnexpectedEOF / errEOF
// are tiny shims over the stdlib so the file's import block is short.
func readFull(f *os.File, buf []byte) (int, error) { return f.Read(buf) }

var errUnexpectedEOF = io.ErrUnexpectedEOF
var errEOF = io.EOF

// resolveImportFile maps a single import spec (as written in the
// file) to a project-relative file path, or "" if the target isn't a
// project file. language is the importing file's language so we can
// pick the right rule (Go uses module path; TS/JS use relative
// specifiers). idx is the project-wide index built in IndexProject;
// resolveImportFile is pure (no DB access) so it's safe to call from
// parallel indexer workers without coordination.
func resolveImportFile(language, fromRel, spec string, idx *importIndex) string {
	if spec == "" || idx == nil {
		return ""
	}
	switch language {
	case "go":
		return resolveGoImport(spec, idx)
	case "typescript", "javascript", "vue":
		return resolveJSImport(fromRel, spec, idx)
	case "ruby":
		return resolveRubyImport(fromRel, spec, idx)
	case "lua":
		return resolveLuaImport(spec, idx)
	case "css", "scss", "sass", "less":
		return resolveCSSImport(fromRel, spec, idx)
	}
	return ""
}

// resolveCSSImport maps a stylesheet @import/@use/@forward spec to a project
// file. Sass resolves bare specs relative to the importing file first, so
// relative and bare specs share one candidate walk: the spec as written, the
// spec with each stylesheet extension, the Sass partial (_name.scss/_name.sass,
// also for extension-bearing specs), and directory index files. Anything else
// (node_modules packages, `~` webpack specs, absolute paths) is external.
func resolveCSSImport(fromRel, spec string, idx *importIndex) string {
	if strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~") {
		return ""
	}
	fromDir := parentDir(filepath.ToSlash(fromRel))
	base := normalizeSlashPath(joinSlash(fromDir, filepath.ToSlash(spec)))
	if base == "" {
		return ""
	}
	dir := parentDir(base)
	name := base
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		name = base[i+1:]
	}
	candidates := []string{
		base,
		base + ".css", base + ".scss", base + ".sass", base + ".less",
		joinSlash(dir, "_"+name), // partial with an extension already in the spec
		joinSlash(dir, "_"+name+".scss"), joinSlash(dir, "_"+name+".sass"),
		base + "/index.scss", base + "/_index.scss",
	}
	for _, cand := range candidates {
		if idx.relFiles[cand] {
			return cand
		}
	}
	return ""
}

// normalizeSlashPath collapses "."/".." segments of a root-relative slash
// path; "" when the path escapes the project root.
func normalizeSlashPath(p string) string {
	var segs []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(segs) == 0 {
				return ""
			}
			segs = segs[:len(segs)-1]
		default:
			segs = append(segs, seg)
		}
	}
	return strings.Join(segs, "/")
}

// resolveRubyImport maps a require/require_relative spec to a project file.
// rubysrc prefixes require_relative specs with "./" so they resolve against
// the requiring file; plain require load-path specs are tried against the
// project root and the conventional lib/ root.
func resolveRubyImport(fromRel, spec string, idx *importIndex) string {
	try := func(base string) string {
		base = strings.TrimSuffix(base, ".rb")
		if idx.relFiles[base+".rb"] {
			return base + ".rb"
		}
		return ""
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		fromDir := filepath.ToSlash(filepath.Dir(fromRel))
		base := filepath.ToSlash(filepath.Clean(filepath.Join(fromDir, spec)))
		return try(strings.TrimPrefix(base, "./"))
	}
	if fp := try(spec); fp != "" {
		return fp
	}
	return try("lib/" + spec)
}

// resolveLuaImport maps a require("a.b.c") module spec to a project file,
// trying the standard package.path shapes: a/b/c.lua, a/b/c/init.lua, and the
// conventional lua/ and src/ roots (Neovim plugins and busted projects).
func resolveLuaImport(spec string, idx *importIndex) string {
	rel := strings.ReplaceAll(spec, ".", "/")
	for _, cand := range []string{
		rel + ".lua", rel + "/init.lua",
		"lua/" + rel + ".lua", "lua/" + rel + "/init.lua",
		"src/" + rel + ".lua", "src/" + rel + "/init.lua",
	} {
		if idx.relFiles[cand] {
			return cand
		}
	}
	return ""
}

// resolveGoImport maps a Go module-path import to a project file. The
// modPath prefix is stripped to get the in-module package path; that
// path is looked up under idx.goFiles. Returns "" for relative
// imports (Go forbids them in `import`) and for any spec outside the
// project's modPath.
func resolveGoImport(spec string, idx *importIndex) string {
	if idx.goModulePath == "" {
		return ""
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		return ""
	}
	spec = stripVersionSuffix(strings.TrimRight(spec, "/"))
	var rel string
	switch {
	case spec == idx.goModulePath:
		// "import modpath" — the root package.
		rel = ""
	case strings.HasPrefix(spec, idx.goModulePath+"/"):
		rel = spec[len(idx.goModulePath)+1:]
	default:
		// External dep or a different module.
		return ""
	}
	// Try the modPath-prefixed form first (canonical), then the
	// bare dir form (for vendored snippets and short imports).
	if fp, ok := idx.goFiles[idx.goModulePath+"/"+rel]; ok {
		return fp
	}
	if fp, ok := idx.goFiles[rel]; ok {
		return fp
	}
	return ""
}

// resolveJSImport maps a TS/JS/Vue specifier to a project file:
//   - relative ("./foo", "../bar/baz") and package-rooted ("/abs") paths;
//   - "@/rest" and "~/rest" source aliases (the near-universal tsconfig
//     `paths` convention mapping to an app's src/ or root), resolved by
//     walking the importing file's ancestor directories;
//   - bare workspace-package specifiers ("@scope/pkg", "@scope/pkg/sub")
//     resolved through the project's own package.json names.
//
// Anything else (an npm dependency) is external and resolves to "".
func resolveJSImport(fromRel, spec string, idx *importIndex) string {
	fromDir := filepath.ToSlash(filepath.Dir(fromRel))

	// tsconfig-style source alias: "@/lib/x" or "~/lib/x". The alias target
	// is almost always the importing app's src/ (or package root); walking
	// ancestors deepest-first finds the nearest match in a monorepo where
	// several apps each define their own "@/*".
	if rest, ok := strings.CutPrefix(spec, "@/"); ok || strings.HasPrefix(spec, "~/") {
		if !ok {
			rest = strings.TrimPrefix(spec, "~/")
		}
		if rest == "" {
			return ""
		}
		for dir := fromDir; ; dir = parentDir(dir) {
			if fp := lookupJSFile(idx, joinSlash(dir, "src", rest)); fp != "" {
				return fp
			}
			if fp := lookupJSFile(idx, joinSlash(dir, rest)); fp != "" {
				return fp
			}
			if dir == "" {
				return ""
			}
		}
	}

	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") && !strings.HasPrefix(spec, "/") {
		return resolveJSWorkspaceImport(spec, idx)
	}

	var base string
	if strings.HasPrefix(spec, "/") {
		base = strings.TrimPrefix(spec, "/")
	} else {
		base = filepath.ToSlash(filepath.Join(fromDir, spec))
	}
	base = filepath.Clean(base)
	base = strings.TrimPrefix(base, "./")
	if base == "" {
		return ""
	}
	return lookupJSFile(idx, base)
}

// resolveJSWorkspaceImport maps a bare specifier to a workspace package's
// entry (or subpath) file when the project itself declares that package.
// External npm dependencies resolve to "".
func resolveJSWorkspaceImport(spec string, idx *importIndex) string {
	best := ""
	for name := range idx.jsPackages {
		if (spec == name || strings.HasPrefix(spec, name+"/")) && len(name) > len(best) {
			best = name
		}
	}
	if best == "" {
		return ""
	}
	if spec == best {
		return idx.jsPkgEntry[best]
	}
	dir := idx.jsPackages[best]
	sub := strings.TrimPrefix(spec[len(best):], "/")
	if fp := lookupJSFile(idx, joinSlash(dir, sub)); fp != "" {
		return fp
	}
	// Packages routinely export "pkg/sub" from src/sub via the exports map.
	return lookupJSFile(idx, joinSlash(dir, "src", sub))
}

// lookupJSFile resolves an extensionless-or-not root-relative base path to an
// indexed project file: exact (a specifier may include its extension), the
// extension-swapped form ("a.js" emitted for a source "a.ts"), then a
// directory's index file.
func lookupJSFile(idx *importIndex, base string) string {
	if base == "" {
		return ""
	}
	// A specifier MAY include a file extension ("./a.ts" is valid
	// TS) — keep the spec-as-file attempt first.
	if idx.relFiles[base] {
		return base
	}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue"} {
		if idx.relFiles[base+ext] {
			return base + ext
		}
	}
	// Then the extension-stripped form ("./a" → "a.ts", "./a.js" → "a.ts").
	stripped := base
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue"} {
		if strings.HasSuffix(stripped, ext) {
			stripped = strings.TrimSuffix(stripped, ext)
			break
		}
	}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue"} {
		if idx.relFiles[stripped+ext] {
			return stripped + ext
		}
	}
	// Finally, directory with an index file.
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue"} {
		idxPath := filepath.ToSlash(filepath.Join(stripped, "index"+ext))
		if idx.relFiles[idxPath] {
			return idxPath
		}
	}
	return ""
}

// joinSlash joins root-relative slash path segments, skipping empties.
func joinSlash(parts ...string) string {
	keep := parts[:0]
	for _, p := range parts {
		if p != "" && p != "." {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "/")
}

// jsPackageManifest is the subset of package.json the import resolver reads.
type jsPackageManifest struct {
	Name    string `json:"name"`
	Main    string `json:"main"`
	Module  string `json:"module"`
	Exports any    `json:"exports"`
}

// readJSPackageManifest parses <root>/<dir>/package.json, or nil when absent
// or malformed (never fatal — a broken manifest just means no bare-specifier
// resolution for that directory).
func readJSPackageManifest(root, dir string) *jsPackageManifest {
	p := filepath.Join(root, filepath.FromSlash(dir), "package.json")
	data, err := os.ReadFile(p)
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	var m jsPackageManifest
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return &m
}

// jsPackageName returns the package.json "name" declared in dir, or "".
func jsPackageName(root, dir string) string {
	if m := readJSPackageManifest(root, dir); m != nil {
		return m.Name
	}
	return ""
}

// jsPackageEntry resolves a workspace package's entry file: the manifest's
// exports["."]/module/main when it names a project file, else the
// conventional index / src/index fallbacks.
func jsPackageEntry(root, dir string, idx *importIndex) string {
	m := readJSPackageManifest(root, dir)
	if m == nil {
		return ""
	}
	var candidates []string
	if e := exportsDotTarget(m.Exports); e != "" {
		candidates = append(candidates, e)
	}
	if m.Module != "" {
		candidates = append(candidates, m.Module)
	}
	if m.Main != "" {
		candidates = append(candidates, m.Main)
	}
	for _, c := range candidates {
		c = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(c)), "./")
		if fp := lookupJSFile(idx, joinSlash(dir, c)); fp != "" {
			return fp
		}
	}
	if fp := lookupJSFile(idx, joinSlash(dir, "index")); fp != "" {
		return fp
	}
	return lookupJSFile(idx, joinSlash(dir, "src", "index"))
}

// exportsDotTarget digs the "." entry out of a package.json "exports" value:
// a bare string, a {".": "./x"} map, or a conditions object ({".": {"import":
// "./x", "default": "./y"}}). Anything unrecognized yields "".
func exportsDotTarget(exports any) string {
	switch e := exports.(type) {
	case string:
		return e
	case map[string]any:
		v, ok := e["."]
		if !ok {
			// The exports map may itself be a conditions object.
			v = e
		}
		switch t := v.(type) {
		case string:
			return t
		case map[string]any:
			for _, cond := range []string{"import", "require", "default", "types"} {
				if s, ok := t[cond].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// stripVersionSuffix removes a "/v2", "/v3" ... suffix from a Go
// import path so the import lookup can find the package's canonical
// path. Module-resolution considers vN suffixes equivalent; we do too.
func stripVersionSuffix(s string) string {
	i := strings.LastIndex(s, "/v")
	if i < 0 {
		return s
	}
	rest := s[i+2:]
	if rest == "" {
		return s
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return s
		}
	}
	return s[:i]
}

// writeImportEdgesForFiles writes file→file EdgeImports edges in a single
// transaction, after all indexFile workers have joined. IndexProject passes the
// whole project; IndexFiles passes the incrementally processed set. Doing it
// inside indexFile races with the target file's
// own indexFile call — a concurrent DeleteNodesInFileTx (the first
// step of every indexFile) cascades to delete the imports edge that
// the previous worker just wrote, because the file node it points to
// gets removed. The final pass runs after all workers join, so every
// file node is settled.
//
// Re-extracting each file here is intentional: the workers stay
// oblivious to import resolution (the worker pipeline stays simple),
// and re-extract is cheap — no graph writes, no I/O beyond the
// already-warm file cache.
func (ix *Indexer) writeImportEdgesForFiles(ctx context.Context, projectID int64, files []fileTask, impIdx *importIndex) error {
	if impIdx == nil {
		return nil
	}
	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // safe: Commit renders this a no-op
	for _, ft := range files {
		ft.importIndex = impIdx
		// Find the importing file's existing node — every
		// file was visited by the parallel pass, so it MUST
		// exist. If it doesn't (an unsupported file with
		// no `fr.Symbols` survived the walk but produced no
		// node), the import edge has no source — skip.
		fromID, found := findExistingFileNodeInTx(tx, projectID, ft.rel)
		if !found {
			continue
		}
		// Re-extract to recover fr.Imports (the worker
		// didn't keep them; the result struct is dropped
		// at the end of indexFile).
		content, oversized, rerr := readFileUnderLimit(ft.abs, ix.cfg.MaxFileBytes)
		if rerr != nil {
			continue
		}
		if oversized {
			continue
		}
		fr, eerr := ft.ext.ExtractFile(ft.rel, content)
		if eerr != nil || fr == nil {
			continue
		}
		if err := writeImportEdgesForFileTx(tx, projectID, ft, fromID, fr); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// writeImportEdgesForFileTx writes one EdgeImports edge per resolved
// import spec, in the same transaction as the rest of the import pass.
// External imports (no project file matches the spec) are skipped —
// a dangling edge to "" is worse than no edge. The targets map dedupes
// when a file imports the same package twice. By the time this is
// called every project file has its own file node, so no fresh tiny
// file node is needed for the target — the real file node (with its
// real line range) is used.
func writeImportEdgesForFileTx(tx *sql.Tx, projectID int64, ft fileTask, fromID int64, fr *extract.FileResult) error {
	if ft.importIndex == nil {
		return nil
	}
	// Replace, don't append: the full incremental pass visits unchanged files
	// too, and the watcher may receive duplicate events. Deleting first also
	// removes imports that disappeared from a changed source file.
	if _, err := tx.Exec("DELETE FROM edges WHERE source_id=? AND edge_type=?", fromID, graph.EdgeImports); err != nil {
		return err
	}
	if len(fr.Imports) == 0 {
		return nil
	}
	targets := make(map[string]int64, len(fr.Imports))
	for _, imp := range fr.Imports {
		t := resolveImportFile(ft.lang, ft.rel, imp, ft.importIndex)
		if t == "" {
			continue
		}
		if _, ok := targets[t]; ok {
			continue
		}
		nid, found := findExistingFileNodeInTx(tx, projectID, t)
		if !found {
			continue
		}
		targets[t] = nid
	}
	for _, nid := range targets {
		if _, err := graph.AddEdgeProvTx(tx, fromID, nid, graph.EdgeImports, graph.WeightLSP, graph.ProvName); err != nil {
			return err
		}
	}
	return nil
}

// findExistingFileNodeInTx looks up a file-kind node by path and
// returns its id if present. Used by the deferred import-edges pass —
// by then every project file has a node from the parallel pass, so
// the lookup is always a hit.
func findExistingFileNodeInTx(tx *sql.Tx, projectID int64, file string) (int64, bool) {
	var id int64
	err := tx.QueryRow(
		"SELECT id FROM nodes WHERE project_id=? AND file_path=? AND kind=? LIMIT 1",
		projectID, file, graph.KindFile,
	).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}
