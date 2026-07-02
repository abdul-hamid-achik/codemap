package index

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
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
}

func newImportIndex(root string, files []fileTask) *importIndex {
	idx := &importIndex{
		root:         root,
		goModulePath: goModulePath(root),
		goFiles:      map[string]string{},
		relFiles:     map[string]bool{},
	}
	for _, ft := range files {
		rel := filepath.ToSlash(ft.rel)
		idx.relFiles[rel] = true
		if ft.lang != "go" {
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
	return idx
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

// resolveJSImport maps a TS/JS/Vue relative specifier ("./foo",
// "../bar/baz", or a package-rooted "/abs") to a project file. Bare
// specifiers ("foo", "@scope/pkg") are package imports, not project
// files, and resolve to "".
func resolveJSImport(fromRel, spec string, idx *importIndex) string {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") && !strings.HasPrefix(spec, "/") {
		return ""
	}
	fromDir := filepath.ToSlash(filepath.Dir(fromRel))
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
	// A specifier MAY include a file extension ("./a.ts" is valid
	// TS) — keep the spec-as-file attempt first.
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue"} {
		if idx.relFiles[base+ext] {
			return base + ext
		}
	}
	// Then the extension-stripped form ("./a" → "a.ts").
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

// writeAllImportEdges writes file→file EdgeImports edges in a single
// project-wide transaction, after all parallel indexFile workers
// have joined. Doing it inside indexFile races with the target file's
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
func (ix *Indexer) writeAllImportEdges(ctx context.Context, projectID int64, files []fileTask, impIdx *importIndex) error {
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
		content, rerr := os.ReadFile(ft.abs)
		if rerr != nil {
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
	if len(fr.Imports) == 0 || ft.importIndex == nil {
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
