package graph

import "strings"

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
