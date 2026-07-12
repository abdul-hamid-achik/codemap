package graph

import "fmt"

// InboundReference is one enclosing scope that stores or passes a target
// function/method as a value. Source is the enclosing function, method, or file
// node; it is intentionally not described as the exact lexical expression
// because reference edges currently persist scope identity, not use-site lines.
type InboundReference struct {
	Source     Node
	Weight     float64
	Provenance string
	Ambiguous  bool // the same source edge fans out to another same-named target
}

// References returns enclosing scopes with inbound `references` edges to any
// definition named symbol. It never mixes `calls` edges. Both endpoints are
// project-scoped, results are deterministic, and total is computed before the
// bounded result is applied. This is a direct-edge query, not a traversal, so
// cycles cannot amplify or loop.
func (s *Store) References(projectID int64, symbol string, limit int) ([]InboundReference, int, error) {
	return s.references(projectID, "tgt.symbol = ?", symbol, limit)
}

// ReferencesOfNode is the exact-definition counterpart of References. It
// selects one target node while retaining Ambiguous=true when the stored
// name-based reference also fans out to another same-named definition.
func (s *Store) ReferencesOfNode(projectID, nodeID int64, limit int) ([]InboundReference, int, error) {
	return s.references(projectID, "tgt.id = ?", nodeID, limit)
}

func (s *Store) references(projectID int64, targetPredicate string, target any, limit int) ([]InboundReference, int, error) {
	if limit <= 0 {
		limit = 50
	}
	base := ` FROM edges e
		JOIN nodes src ON src.id = e.source_id
		JOIN nodes tgt ON tgt.id = e.target_id
		WHERE src.project_id = ? AND tgt.project_id = ?
		  AND e.edge_type = ? AND ` + targetPredicate

	var total int
	if err := s.db.QueryRow("SELECT COUNT(DISTINCT e.source_id)"+base,
		projectID, projectID, EdgeReferences, target).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count inbound references: %w", err)
	}
	if total == 0 {
		return []InboundReference{}, 0, nil
	}

	// A selector can narrow this query to one target, but a name-based edge may
	// still have been written to several same-named targets. The correlated
	// check preserves that uncertainty instead of upgrading it merely because
	// the consumer selected one of the fanned-out targets.
	q := `SELECT ` + nodeColsAs("src") + `,
		MAX(e.weight),
		CASE WHEN MAX(CASE WHEN e.provenance = ? THEN 1 ELSE 0 END) = 1 THEN ? ELSE ? END,
		MAX(CASE WHEN (
			SELECT COUNT(DISTINCT other.target_id)
			FROM edges other
			JOIN nodes other_tgt ON other_tgt.id = other.target_id
			WHERE other.source_id = e.source_id
			  AND other.edge_type = ?
			  AND other_tgt.project_id = ?
			  AND other_tgt.symbol = tgt.symbol
		) > 1 THEN 1 ELSE 0 END)` + base + `
		GROUP BY src.id
		ORDER BY src.file_path, src.start_line, src.fqn, src.kind, src.id
		LIMIT ?`
	rows, err := s.db.Query(q,
		ProvPrecise, ProvPrecise, ProvName,
		EdgeReferences, projectID,
		projectID, projectID, EdgeReferences, target,
		limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query inbound references: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]InboundReference, 0, min(total, limit))
	for rows.Next() {
		var site InboundReference
		var ambiguous int
		n := &site.Source
		if err := rows.Scan(
			&n.ID, &n.ProjectID, &n.FilePath, &n.Symbol, &n.FQN, &n.Kind, &n.Language,
			&n.StartLine, &n.EndLine, &n.Signature, &n.Docstring, &n.SourceHash, &n.VecID,
			&n.CreatedAt, &n.UpdatedAt,
			&site.Weight, &site.Provenance, &ambiguous,
		); err != nil {
			return nil, 0, fmt.Errorf("scan inbound reference: %w", err)
		}
		site.Ambiguous = ambiguous != 0
		out = append(out, site)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate inbound references: %w", err)
	}
	return out, total, nil
}
