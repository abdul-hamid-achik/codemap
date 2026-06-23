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
