package graph

import "fmt"

// RecomputeOverrides rebuilds a project's derived `overrides` edges from its
// declared inheritance: for every extends/implements edge C → B, each method
// directly under C (FQN "C.m") gets an overrides edge to the same-named
// method directly under B ("B.m"). Only direct bases are considered and
// constructors ("constructor") are skipped — TypeScript constructors do not
// override. The edges are name-provenance candidates (weight 0.7) and are
// recomputed wholesale, so they follow any change to either side's file.
// Only declared (name-provenance) inheritance is used: Go --precise writes its
// own exact method-level overrides, which are left untouched.
func (s *Store) RecomputeOverrides(projectID int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		DELETE FROM edges
		WHERE edge_type = ? AND provenance = ?
		  AND source_id IN (SELECT id FROM nodes WHERE project_id = ?)`,
		EdgeOverrides, ProvName, projectID); err != nil {
		return 0, fmt.Errorf("clear overrides: %w", err)
	}
	res, err := tx.Exec(`
		INSERT INTO edges(source_id, target_id, edge_type, weight, provenance, created_at)
		SELECT DISTINCT m.id, n.id, ?, ?, ?, ?
		FROM edges h
		JOIN nodes c ON c.id = h.source_id
		JOIN nodes b ON b.id = h.target_id
		JOIN nodes m ON m.project_id = c.project_id AND m.file_path = c.file_path
		            AND m.fqn = c.fqn || '.' || m.symbol
		            AND m.kind IN (?, ?, ?)
		JOIN nodes n ON n.project_id = b.project_id AND n.file_path = b.file_path
		            AND n.fqn = b.fqn || '.' || m.symbol
		            AND n.kind IN (?, ?, ?)
		WHERE h.edge_type IN (?, ?) AND h.provenance = ? AND c.project_id = ? AND m.symbol <> 'constructor' AND m.id <> n.id`,
		EdgeOverrides, WeightTreeSitter, ProvName, now(),
		KindMethod, KindFunction, KindTest,
		KindMethod, KindFunction, KindTest,
		EdgeExtends, EdgeImplements, ProvName, projectID)
	if err != nil {
		return 0, fmt.Errorf("derive overrides: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

// HierarchyEdge is one inheritance-family edge touching a definition: the
// other end, and whether the edge leaves the definition (Outgoing: it extends,
// implements, or overrides Node) or arrives at it (Node is a subtype or an
// overriding method).
type HierarchyEdge struct {
	EdgeType string
	Outgoing bool
	Node     Node
}

// HierarchyOf returns the extends/implements/overrides edges of the
// definition(s) with fqn in file, in both directions (outgoing first).
func (s *Store) HierarchyOf(projectID int64, file, fqn string) ([]HierarchyEdge, error) {
	q := `SELECT e.edge_type, 1, ` + nodeColsAs("o") + `
		FROM nodes n JOIN edges e ON e.source_id = n.id JOIN nodes o ON o.id = e.target_id
		WHERE n.project_id = ? AND n.file_path = ? AND n.fqn = ? AND e.edge_type IN (?, ?, ?)
		UNION ALL
		SELECT e.edge_type, 0, ` + nodeColsAs("o") + `
		FROM nodes n JOIN edges e ON e.target_id = n.id JOIN nodes o ON o.id = e.source_id
		WHERE n.project_id = ? AND n.file_path = ? AND n.fqn = ? AND e.edge_type IN (?, ?, ?)`
	rows, err := s.db.Query(q,
		projectID, file, fqn, EdgeExtends, EdgeImplements, EdgeOverrides,
		projectID, file, fqn, EdgeExtends, EdgeImplements, EdgeOverrides)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HierarchyEdge
	seen := map[string]bool{}
	for rows.Next() {
		var h HierarchyEdge
		var outgoing int
		dest := append([]any{&h.EdgeType, &outgoing}, nodeScanDest(&h.Node)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		h.Outgoing = outgoing == 1
		key := fmt.Sprintf("%s|%v|%d", h.EdgeType, h.Outgoing, h.Node.ID)
		if !seen[key] {
			seen[key] = true
			out = append(out, h)
		}
	}
	return out, rows.Err()
}

// nodeScanDest returns scan destinations for nodeCols, in scanNode's order.
func nodeScanDest(n *Node) []any {
	return []any{&n.ID, &n.ProjectID, &n.FilePath, &n.Symbol, &n.FQN, &n.Kind, &n.Language,
		&n.StartLine, &n.EndLine, &n.Signature, &n.Docstring, &n.SourceHash, &n.VecID,
		&n.CreatedAt, &n.UpdatedAt}
}
