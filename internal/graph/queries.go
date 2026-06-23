package graph

import (
	"errors"
	"sort"
	"strings"
)

// NodeDepth pairs a node with its distance (in hops) from the query symbol.
type NodeDepth struct {
	Node  Node
	Depth int
}

func (s *Store) startNodeIDs(projectID int64, symbol string) ([]int64, error) {
	return s.scanIDs("SELECT id FROM nodes WHERE project_id=? AND symbol=?", projectID, symbol)
}

func (s *Store) callerIDs(targetID int64) ([]int64, error) {
	return s.scanIDs("SELECT source_id FROM edges WHERE target_id=? AND edge_type=?", targetID, EdgeCalls)
}

func (s *Store) scanIDs(query string, args ...any) ([]int64, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Annotation is user-attached knowledge (a note or external data) pinned to a
// symbol (kind="node") or a call path (kind="path").
type Annotation struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Source    string `json:"source"`
	Note      string `json:"note,omitempty"`
	Data      string `json:"data,omitempty"`
	CreatedAt string `json:"created_at"`
}

// AddAnnotation stores an annotation and returns its id.
func (s *Store) AddAnnotation(projectID int64, a Annotation) (int64, error) {
	if a.Source == "" {
		a.Source = "note"
	}
	res, err := s.db.Exec(
		`INSERT INTO annotations (project_id, kind, target, source, note, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, a.Kind, a.Target, a.Source, a.Note, a.Data, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AnnotationsByTarget returns annotations for a specific node/path target.
func (s *Store) AnnotationsByTarget(projectID int64, kind, target string) ([]Annotation, error) {
	return s.scanAnnotations(
		`SELECT id, kind, target, source, note, data, created_at FROM annotations
		 WHERE project_id=? AND kind=? AND target=? ORDER BY id`, projectID, kind, target)
}

// AllAnnotations returns every annotation in a project, newest last.
func (s *Store) AllAnnotations(projectID int64) ([]Annotation, error) {
	return s.scanAnnotations(
		`SELECT id, kind, target, source, note, data, created_at FROM annotations
		 WHERE project_id=? ORDER BY kind, target, id`, projectID)
}

// DeleteAnnotation removes one annotation by id (scoped to the project); reports
// whether a row was deleted.
func (s *Store) DeleteAnnotation(projectID, id int64) (bool, error) {
	res, err := s.db.Exec("DELETE FROM annotations WHERE project_id=? AND id=?", projectID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) scanAnnotations(query string, args ...any) ([]Annotation, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Annotation
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.Kind, &a.Target, &a.Source, &a.Note, &a.Data, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SymInfo holds the displayable text for a symbol, resolved by FQN.
type SymInfo struct {
	Signature string
	Doc       string
}

// SymbolInfoIndex returns FQN → {signature, docstring} for a project's symbols,
// so callers whose results come from elsewhere (e.g. semantic search, backed by
// the vector store) can enrich them in one query rather than per-node lookups.
// Symbols with neither a signature nor a docstring are omitted.
func (s *Store) SymbolInfoIndex(projectID int64) (map[string]SymInfo, error) {
	rows, err := s.db.Query("SELECT fqn, signature, docstring FROM nodes WHERE project_id=? AND fqn != '' AND (signature != '' OR docstring != '')", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]SymInfo)
	for rows.Next() {
		var fqn, sig, doc string
		if err := rows.Scan(&fqn, &sig, &doc); err != nil {
			return nil, err
		}
		out[fqn] = SymInfo{Signature: sig, Doc: doc}
	}
	return out, rows.Err()
}

// BlastRadius returns the transitive callers of symbol — everything affected if
// it changes — up to maxDepth hops, each with its minimum depth. The BFS is
// cycle-safe (a visited map keyed by node id), so recursive call graphs do not
// loop forever. The query symbol's own node(s) are excluded from the result.
func (s *Store) BlastRadius(projectID int64, symbol string, maxDepth int) ([]NodeDepth, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	starts, err := s.startNodeIDs(projectID, symbol)
	if err != nil {
		return nil, err
	}
	visited := make(map[int64]int, len(starts)) // node id -> min depth
	type item struct {
		id    int64
		depth int
	}
	queue := make([]item, 0, len(starts))
	for _, id := range starts {
		visited[id] = 0
		queue = append(queue, item{id: id, depth: 0})
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		callers, err := s.callerIDs(cur.id)
		if err != nil {
			return nil, err
		}
		for _, c := range callers {
			nd := cur.depth + 1
			if prev, seen := visited[c]; seen && prev <= nd {
				continue
			}
			visited[c] = nd
			queue = append(queue, item{id: c, depth: nd})
		}
	}

	out := make([]NodeDepth, 0, len(visited))
	for id, d := range visited {
		if d == 0 {
			continue // skip the query symbol's own node(s)
		}
		n, err := s.GetNode(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, NodeDepth{Node: *n, Depth: d})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		if out[i].Node.FilePath != out[j].Node.FilePath {
			return out[i].Node.FilePath < out[j].Node.FilePath
		}
		return out[i].Node.StartLine < out[j].Node.StartLine
	})
	return out, nil
}

// nodeColsAs returns the node column list qualified with a table alias, in the
// same order scanNode expects.
func nodeColsAs(alias string) string {
	cols := strings.Split(nodeCols, ", ")
	for i := range cols {
		cols[i] = alias + "." + cols[i]
	}
	return strings.Join(cols, ", ")
}

// Callers returns the distinct nodes that call any node named symbol (incoming
// `calls` edges), ordered by location.
func (s *Store) Callers(projectID int64, symbol string) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("src") + " FROM edges e " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"JOIN nodes src ON e.source_id = src.id " +
		"WHERE tgt.project_id = ? AND tgt.symbol = ? AND e.edge_type = ? " +
		"ORDER BY src.file_path, src.start_line"
	return s.queryNodes(q, projectID, symbol, EdgeCalls)
}

// Callees returns the distinct nodes called by any node named symbol (outgoing
// `calls` edges), ordered by location.
func (s *Store) Callees(projectID int64, symbol string) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("tgt") + " FROM edges e " +
		"JOIN nodes src ON e.source_id = src.id " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"WHERE src.project_id = ? AND src.symbol = ? AND e.edge_type = ? " +
		"ORDER BY tgt.file_path, tgt.start_line"
	return s.queryNodes(q, projectID, symbol, EdgeCalls)
}

func (s *Store) calleeIDs(sourceID int64) ([]int64, error) {
	return s.scanIDs("SELECT target_id FROM edges WHERE source_id=? AND edge_type=?", sourceID, EdgeCalls)
}

// Hotspot is a node with its incoming-usage count (hub detection).
type Hotspot struct {
	Node     Node
	InDegree int
}

// Hotspots returns the most-referenced nodes (highest incoming calls/references
// count) — the hubs of the codebase. File nodes are excluded.
func (s *Store) Hotspots(projectID int64, limit int) ([]Hotspot, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT e.target_id, COUNT(*) AS indeg
		FROM edges e JOIN nodes n ON e.target_id = n.id
		WHERE n.project_id = ? AND n.kind != ? AND e.edge_type IN (?, ?)
		GROUP BY e.target_id
		ORDER BY indeg DESC, e.target_id
		LIMIT ?`, projectID, KindFile, EdgeCalls, EdgeReferences, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id    int64
		indeg int
	}
	var raw []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.indeg); err != nil {
			return nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Hotspot, 0, len(raw))
	for _, r := range raw {
		n, err := s.GetNode(r.id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Hotspot{Node: *n, InDegree: r.indeg})
	}
	return out, nil
}

// SearchSymbols returns nodes whose symbol or FQN contains query (case-
// insensitive substring) — a fast, offline name search. File nodes excluded.
func (s *Store) SearchSymbols(projectID int64, query string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 50
	}
	like := "%" + query + "%"
	q := "SELECT " + nodeColsAs("n") + " FROM nodes n " +
		"WHERE n.project_id = ? AND n.kind != ? AND (n.symbol LIKE ? OR n.fqn LIKE ?) " +
		"ORDER BY n.symbol, n.file_path LIMIT ?"
	return s.queryNodes(q, projectID, KindFile, like, like, limit)
}

// SymbolDefCounts returns, per symbol name, how many definition nodes share it
// within a project. A count > 1 means name-based resolution fans calls/references
// out across all of them, inflating in-degrees — used to flag inflated hotspots.
func (s *Store) SymbolDefCounts(projectID int64) (map[string]int, error) {
	rows, err := s.db.Query(
		"SELECT symbol, COUNT(*) FROM nodes WHERE project_id = ? AND kind != ? GROUP BY symbol",
		projectID, KindFile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var sym string
		var n int
		if err := rows.Scan(&sym, &n); err != nil {
			return nil, err
		}
		m[sym] = n
	}
	return m, rows.Err()
}

// Orphans returns function/method nodes with no incoming `calls` edge —
// dead-code candidates. (Heuristic: exported API, entrypoints like main/init,
// and externally-called code may appear here as false positives.)
func (s *Store) Orphans(projectID int64, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 50
	}
	// main and init are invoked automatically by the Go runtime, so a package-level
	// function with either name is never dead — excluding them keeps the dead-code
	// list trustworthy (a method named main/init is not special, hence the kind guard).
	q := "SELECT " + nodeColsAs("n") + ` FROM nodes n
		WHERE n.project_id = ? AND n.kind IN (?, ?)
		AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = n.id AND e.edge_type = ?)
		AND NOT (n.kind = ? AND n.symbol IN ('main', 'init'))
		ORDER BY n.file_path, n.start_line
		LIMIT ?`
	return s.queryNodes(q, projectID, KindFunction, KindMethod, EdgeCalls, KindFunction, limit)
}

// Path returns the shortest call path from one symbol to another (following
// outgoing `calls` edges), or nil if none exists within maxDepth. Cycle-safe.
func (s *Store) Path(projectID int64, from, to string, maxDepth int) ([]Node, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	starts, err := s.startNodeIDs(projectID, from)
	if err != nil {
		return nil, err
	}
	targets, err := s.startNodeIDs(projectID, to)
	if err != nil {
		return nil, err
	}
	if len(starts) == 0 || len(targets) == 0 {
		return nil, nil
	}
	targetSet := make(map[int64]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}

	parent := make(map[int64]int64) // node -> parent (-1 for a start)
	depth := make(map[int64]int)
	var queue []int64
	for _, s0 := range starts {
		parent[s0] = -1
		depth[s0] = 0
		queue = append(queue, s0)
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if targetSet[cur] {
			return s.reconstructPath(cur, parent)
		}
		if depth[cur] >= maxDepth {
			continue
		}
		callees, err := s.calleeIDs(cur)
		if err != nil {
			return nil, err
		}
		for _, c := range callees {
			if _, seen := parent[c]; seen {
				continue
			}
			parent[c] = cur
			depth[c] = depth[cur] + 1
			queue = append(queue, c)
		}
	}
	return nil, nil // no path
}

func (s *Store) reconstructPath(end int64, parent map[int64]int64) ([]Node, error) {
	var ids []int64
	for cur := end; cur != -1; cur = parent[cur] {
		ids = append(ids, cur)
	}
	// reverse (start → end)
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	out := make([]Node, 0, len(ids))
	for _, id := range ids {
		n, err := s.GetNode(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, nil
}

// UpdateNodeVecID links a node to its veclite record id (for semantic search).
func (s *Store) UpdateNodeVecID(id int64, vecID string) error {
	_, err := s.db.Exec("UPDATE nodes SET vec_id=?, updated_at=? WHERE id=?", vecID, now(), id)
	return err
}

// ProjectNodes returns all nodes in a project, ordered by id. Used to build the
// project-wide symbol index for reference resolution.
func (s *Store) ProjectNodes(projectID int64) ([]Node, error) {
	return s.queryNodes("SELECT "+nodeCols+" FROM nodes WHERE project_id=? ORDER BY id", projectID)
}

// WipeProject deletes all nodes (edges cascade) and index state for a project.
// Used by a full reindex.
func (s *Store) WipeProject(projectID int64) error {
	if _, err := s.db.Exec("DELETE FROM nodes WHERE project_id=?", projectID); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM index_state WHERE project_id=?", projectID)
	return err
}
