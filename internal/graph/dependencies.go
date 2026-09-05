package graph

import "sort"

// FileDependencyNode is the location half of one dependency edge. IDs stay
// inside the store: app-facing dependency evidence can expose useful
// source→target locations without leaking database implementation details.
type FileDependencyNode struct {
	File      string
	Symbol    string
	FQN       string
	Kind      string
	Language  string
	StartLine int
}

// FileDependencyEdge is one distinct inbound structural relationship from a
// node in another file to a node in the target file. EdgeType is currently one
// of calls, references, imports, or styles. Duplicate edge rows are collapsed by
// logical source→target relationship, preferring precise provenance.
type FileDependencyEdge struct {
	EdgeType   string
	Source     FileDependencyNode
	Target     FileDependencyNode
	Weight     float64
	Provenance string
}

// InboundFileDependencies returns the direct call/reference/import evidence
// entering targetFile from other files in the same indexed project. This is a
// bounded-domain query, not a traversal, so cycles cannot amplify or loop. The
// caller groups/caps the distinct relationships for its public response.
func (s *Store) InboundFileDependencies(projectID int64, targetFile string) ([]FileDependencyEdge, error) {
	rows, err := s.db.Query(`
		SELECT e.source_id, e.target_id, e.edge_type, e.weight, e.provenance,
		       src.file_path, src.symbol, src.fqn, src.kind, src.language, src.start_line,
		       tgt.file_path, tgt.symbol, tgt.fqn, tgt.kind, tgt.language, tgt.start_line
		FROM edges e
		JOIN nodes src ON src.id = e.source_id
		JOIN nodes tgt ON tgt.id = e.target_id
		WHERE src.project_id = ? AND tgt.project_id = ?
		  AND tgt.file_path = ? AND src.file_path != tgt.file_path
		  AND e.edge_type IN (?, ?, ?, ?, ?, ?, ?, ?)
		ORDER BY src.file_path, e.edge_type, src.start_line, tgt.start_line`,
		projectID, projectID, targetFile, EdgeCalls, EdgeReferences, EdgeImports, EdgeStyles, EdgeReads, EdgeWrites, EdgeDocuments, EdgeDependsOn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type edgeKey struct {
		sourceID int64
		targetID int64
		edgeType string
	}
	best := map[edgeKey]FileDependencyEdge{}
	for rows.Next() {
		var sourceID, targetID int64
		var edge FileDependencyEdge
		if err := rows.Scan(
			&sourceID, &targetID, &edge.EdgeType, &edge.Weight, &edge.Provenance,
			&edge.Source.File, &edge.Source.Symbol, &edge.Source.FQN, &edge.Source.Kind, &edge.Source.Language, &edge.Source.StartLine,
			&edge.Target.File, &edge.Target.Symbol, &edge.Target.FQN, &edge.Target.Kind, &edge.Target.Language, &edge.Target.StartLine,
		); err != nil {
			return nil, err
		}
		key := edgeKey{sourceID: sourceID, targetID: targetID, edgeType: edge.EdgeType}
		if current, ok := best[key]; !ok || strongerDependencyEdge(edge, current) {
			best[key] = edge
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]FileDependencyEdge, 0, len(best))
	for _, edge := range best {
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source.File != b.Source.File {
			return a.Source.File < b.Source.File
		}
		if a.EdgeType != b.EdgeType {
			return a.EdgeType < b.EdgeType
		}
		if a.Source.StartLine != b.Source.StartLine {
			return a.Source.StartLine < b.Source.StartLine
		}
		if a.Target.StartLine != b.Target.StartLine {
			return a.Target.StartLine < b.Target.StartLine
		}
		if a.Source.FQN != b.Source.FQN {
			return a.Source.FQN < b.Source.FQN
		}
		return a.Target.FQN < b.Target.FQN
	})
	return out, nil
}

func strongerDependencyEdge(candidate, current FileDependencyEdge) bool {
	if candidate.Provenance == ProvPrecise && current.Provenance != ProvPrecise {
		return true
	}
	if candidate.Provenance != ProvPrecise && current.Provenance == ProvPrecise {
		return false
	}
	return candidate.Weight > current.Weight
}
