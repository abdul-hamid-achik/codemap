package index

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/tsscan"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// bindImportRefs finishes the TS/JS call candidates tsscan could only half
// resolve: a reference carrying an ImportSpec names a binding imported from a
// module, and only the indexer's project-wide import resolver (relative paths,
// @/ ~/ aliases, workspace packages) can say which project file that is. The
// reference is rewritten to target that file (ToFile) — never a same-named
// symbol elsewhere. A specifier that resolves to no project file (an npm
// dependency, a builtin) drops the reference: a bare package import yields no
// edge. A default-import reference additionally learns the imported file's
// default-export name so the lookup is by the real symbol, not the local alias.
func (ix *Indexer) bindImportRefs(ft fileTask, refs []extract.Reference) []extract.Reference {
	needs := false
	for i := range refs {
		if refs[i].ImportSpec != "" {
			needs = true
			break
		}
	}
	if !needs {
		return refs
	}
	out := make([]extract.Reference, 0, len(refs))
	for _, r := range refs {
		if r.ImportSpec == "" {
			out = append(out, r)
			continue
		}
		target := resolveImportFile(ft.lang, ft.rel, r.ImportSpec, ft.importIndex)
		if target == "" {
			continue
		}
		if r.DefaultExport {
			name := ix.defaultExportOf(ft.importIndex, target)
			if name == "" {
				continue
			}
			if r.To != "" {
				name += "." + r.To
			}
			r.To = name
			r.DefaultExport = false
		}
		r.ToFile, r.ImportSpec = target, ""
		out = append(out, r)
	}
	return out
}

// defaultExportOf returns the identifier file's default export names, or "".
// The answer is cached on the import index for the lifetime of one index run,
// so each imported file is read at most once however many files import it.
func (ix *Indexer) defaultExportOf(idx *importIndex, file string) string {
	if idx == nil {
		return ""
	}
	if v, ok := idx.defaultExports.Load(file); ok {
		return v.(string)
	}
	name := ""
	switch strings.ToLower(filepath.Ext(file)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		if src, oversized, err := readFileUnderLimit(filepath.Join(idx.root, filepath.FromSlash(file)), ix.cfg.MaxFileBytes); err == nil && !oversized {
			name = tsscan.DefaultExportName(src)
		}
	}
	idx.defaultExports.Store(file, name)
	return name
}

// fileScopeIndex maps file → FQN → node ids, built once per edge-resolution
// pass (lazily — only when a file-scoped reference is present). TS/JS FQNs
// carry no file prefix, so project-wide FQN lookup is ambiguous; scoping the
// lookup to one file is what keeps these candidates exact.
type fileScopeIndex map[string]map[string][]int64

func buildFileScopeIndex(ni *nodeIndex) fileScopeIndex {
	idx := fileScopeIndex{}
	for _, n := range ni.nodes {
		if n.FQN == "" || n.Kind == graph.KindFile {
			continue
		}
		byFQN := idx[n.FilePath]
		if byFQN == nil {
			byFQN = map[string][]int64{}
			idx[n.FilePath] = byFQN
		}
		byFQN[n.FQN] = append(byFQN[n.FQN], n.ID)
	}
	startOf := make(map[int64]int, len(ni.nodes))
	for _, n := range ni.nodes {
		startOf[n.ID] = n.StartLine
	}
	for _, byFQN := range idx {
		for _, ids := range byFQN {
			if len(ids) > 1 {
				sort.Slice(ids, func(i, j int) bool {
					if startOf[ids[i]] != startOf[ids[j]] {
						return startOf[ids[i]] < startOf[ids[j]]
					}
					return ids[i] < ids[j]
				})
			}
		}
	}
	return idx
}

// scopedCallableKind reports whether a node kind can be the target of a
// file-scoped call candidate.
func scopedCallableKind(kind string) bool {
	switch kind {
	case graph.KindFunction, graph.KindClass, graph.KindVariable, graph.KindMethod, graph.KindTest:
		return true
	}
	return false
}

// resolveScopedRef writes the edge(s) for one reference whose source and/or
// target is scoped to a file (see extract.Reference.FromFile / ToFile). The
// source is the first node of that FQN in FromFile (or the file node when From
// is the file path); the targets are the callable nodes of that FQN in ToFile.
// Edges are name-provenance candidates at weight 0.7.
func resolveScopedRef(tx *sql.Tx, ref extract.Reference, ni *nodeIndex, byID map[int64]graph.Node, scope fileScopeIndex) (int, error) {
	var from int64
	switch {
	case ref.FromFile == "":
		id, ok := ni.fqnTo[ref.From]
		if !ok {
			return 0, nil
		}
		from = id
	case ref.From == ref.FromFile:
		id, ok := ni.fqnTo[ref.FromFile]
		if !ok {
			return 0, nil
		}
		from = id
	default:
		ids := pickByLine(scope[ref.FromFile][ref.From], ref.FromLine, byID)
		if len(ids) == 0 {
			return 0, nil
		}
		from = ids[0]
	}
	if ref.ToFile == "" {
		return 0, nil
	}
	count := 0
	for _, to := range pickByLine(scope[ref.ToFile][ref.To], ref.ToLine, byID) {
		if to == from || !scopedCallableKind(byID[to].Kind) {
			continue
		}
		if _, err := graph.AddEdgeProvTx(tx, from, to, ref.Kind, graph.WeightTreeSitter, graph.ProvName); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// pickByLine narrows same-FQN candidates to the declaration starting at line
// when one matches; otherwise (line 0, or a drifted line) it keeps them all.
func pickByLine(ids []int64, line int, byID map[int64]graph.Node) []int64 {
	if line <= 0 || len(ids) < 2 {
		return ids
	}
	for _, id := range ids {
		if byID[id].StartLine == line {
			return []int64{id}
		}
	}
	return ids
}

// deleteNameCallsInFileTx drops the name-provenance call candidates whose
// source is a symbol of file. The LSP precise pass calls it once a file's
// callHierarchy answer is complete and non-empty: the exact edges replace the
// candidates, the same supersede the go/types pass applies per clean file.
// Candidates sourced from the file node itself (module-level calls) stay:
// callHierarchy roots at callables and would never re-emit them.
func deleteNameCallsInFileTx(tx *sql.Tx, projectID int64, file string) error {
	_, err := tx.Exec(`
		DELETE FROM edges
		WHERE edge_type = ? AND provenance = ?
		  AND source_id IN (
			SELECT id FROM nodes WHERE project_id = ? AND file_path = ? AND kind <> 'file'
		  )`, graph.EdgeCalls, graph.ProvName, projectID, file)
	if err != nil {
		return err
	}
	return nil
}
