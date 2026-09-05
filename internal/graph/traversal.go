package graph

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	TraversalOutgoing = "outgoing"
	TraversalIncoming = "incoming"
	TraversalBoth     = "both"
)

var defaultTraversalEdgeTypes = []string{
	EdgeCalls,
	EdgeReferences,
	EdgeImports,
	EdgeImplements,
	EdgeOverrides,
	EdgeDependsOn,
	EdgeTests,
	EdgeStyles, EdgeReads, EdgeWrites, EdgeDocuments,
}

// TraversalOptions bounds a cycle-safe heterogeneous graph walk. Defines is
// intentionally not a default edge because file membership overwhelms the
// architectural relations callers usually want; it remains available when
// explicitly requested.
type TraversalOptions struct {
	Direction string
	EdgeTypes []string
	MaxDepth  int
	MaxNodes  int
}

// TraversalStep records how a node was first reached. Edge direction always
// describes the stored source->target relation; Direction says whether the BFS
// followed that relation forward or backward from its parent.
type TraversalStep struct {
	Node      Node
	Depth     int
	ParentID  int64
	Edge      Edge
	Direction string
}

// TraversalResult is deterministic for a fixed graph and option set.
type TraversalResult struct {
	Start     Node
	Steps     []TraversalStep
	Truncated bool
}

type traversalCandidate struct {
	edge      Edge
	neighbor  int64
	node      Node
	direction string
}

// TraverseFromNode walks calls, references, imports, and other requested edge
// domains without leaking across projects. The start node is returned
// separately and never repeated in Steps, even through cycles.
func (s *Store) TraverseFromNode(projectID, startID int64, opts TraversalOptions) (*TraversalResult, error) {
	opts, err := normalizeTraversalOptions(opts)
	if err != nil {
		return nil, err
	}
	start, err := s.GetNode(startID)
	if err != nil {
		return nil, err
	}
	if start.ProjectID != projectID {
		return &TraversalResult{}, nil
	}
	result := &TraversalResult{Start: *start, Steps: []TraversalStep{}}
	allowed := make(map[string]bool, len(opts.EdgeTypes))
	for _, edgeType := range opts.EdgeTypes {
		allowed[edgeType] = true
	}

	type queueItem struct {
		id    int64
		depth int
		node  Node
	}
	queue := []queueItem{{id: startID, node: *start}}
	visited := map[int64]bool{startID: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth >= opts.MaxDepth {
			continue
		}
		// Bound database work as well as the response. The visited allowance lets
		// a cycle-heavy prefix be skipped without scanning an unbounded hub; the
		// extra row detects whether a deterministic prefix was truncated.
		scanLimit := opts.MaxNodes - len(result.Steps) + len(visited) + 1
		candidates, more, err := s.traversalCandidates(projectID, current.node, opts.Direction, allowed, scanLimit)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if visited[candidate.neighbor] {
				continue
			}
			if len(result.Steps) >= opts.MaxNodes {
				result.Truncated = true
				return result, nil
			}
			node := candidate.node
			if node.ProjectID != projectID {
				continue
			}
			visited[node.ID] = true
			depth := current.depth + 1
			result.Steps = append(result.Steps, TraversalStep{
				Node: node, Depth: depth, ParentID: current.id,
				Edge: candidate.edge, Direction: candidate.direction,
			})
			queue = append(queue, queueItem{id: node.ID, depth: depth, node: node})
		}
		if more {
			// The query deliberately stopped at its work budget. There may be
			// additional unvisited nodes, so report an honest partial traversal
			// instead of doing unbounded work to prove otherwise.
			result.Truncated = true
			return result, nil
		}
	}
	return result, nil
}

func normalizeTraversalOptions(opts TraversalOptions) (TraversalOptions, error) {
	if opts.Direction == "" {
		opts.Direction = TraversalBoth
	}
	if opts.Direction != TraversalOutgoing && opts.Direction != TraversalIncoming && opts.Direction != TraversalBoth {
		return opts, fmt.Errorf("traversal direction must be outgoing, incoming, or both")
	}
	if len(opts.EdgeTypes) == 0 {
		opts.EdgeTypes = append([]string(nil), defaultTraversalEdgeTypes...)
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 2
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 100
	}
	return opts, nil
}

func (s *Store) traversalCandidates(projectID int64, current Node, direction string, allowed map[string]bool, limit int) ([]traversalCandidate, bool, error) {
	if limit < 1 {
		limit = 1
	}
	var out []traversalCandidate
	more := false
	if direction == TraversalOutgoing || direction == TraversalBoth {
		candidates, truncated, err := s.edgesAdjacentTo(projectID, current.ID, true, allowed, limit)
		if err != nil {
			return nil, false, err
		}
		out = append(out, candidates...)
		more = more || truncated
	}
	if direction == TraversalIncoming || direction == TraversalBoth {
		candidates, truncated, err := s.edgesAdjacentTo(projectID, current.ID, false, allowed, limit)
		if err != nil {
			return nil, false, err
		}
		out = append(out, candidates...)
		more = more || truncated
	}

	// Imports are stored as file->file evidence. A traversal starts from an
	// exact symbol, so seed the owner file's import adjacency implicitly without
	// emitting or expanding a defines hop. The import edge itself still costs one
	// depth and its reached node is the imported/importing file.
	if current.Kind != KindFile && allowed[EdgeImports] {
		owner, err := s.fileNodeForPath(projectID, current.FilePath)
		if err != nil {
			return nil, false, err
		}
		if owner != nil {
			importOnly := map[string]bool{EdgeImports: true}
			if direction == TraversalOutgoing || direction == TraversalBoth {
				candidates, truncated, err := s.edgesAdjacentTo(projectID, owner.ID, true, importOnly, limit)
				if err != nil {
					return nil, false, err
				}
				out = append(out, candidates...)
				more = more || truncated
			}
			if direction == TraversalIncoming || direction == TraversalBoth {
				candidates, truncated, err := s.edgesAdjacentTo(projectID, owner.ID, false, importOnly, limit)
				if err != nil {
					return nil, false, err
				}
				out = append(out, candidates...)
				more = more || truncated
			}
		}
	}

	// Node ids are ephemeral and concurrent extraction can allocate them in a
	// different order after reindex. Sort by durable source identity so a bounded
	// traversal keeps the same prefix when the graph facts are unchanged.
	sort.Slice(out, func(i, j int) bool {
		if out[i].edge.EdgeType != out[j].edge.EdgeType {
			return out[i].edge.EdgeType < out[j].edge.EdgeType
		}
		if out[i].node.FilePath != out[j].node.FilePath {
			return out[i].node.FilePath < out[j].node.FilePath
		}
		if out[i].node.StartLine != out[j].node.StartLine {
			return out[i].node.StartLine < out[j].node.StartLine
		}
		if out[i].node.FQN != out[j].node.FQN {
			return out[i].node.FQN < out[j].node.FQN
		}
		if out[i].node.Kind != out[j].node.Kind {
			return out[i].node.Kind < out[j].node.Kind
		}
		if out[i].direction != out[j].direction {
			return out[i].direction < out[j].direction
		}
		if out[i].edge.Provenance != out[j].edge.Provenance {
			return out[i].edge.Provenance < out[j].edge.Provenance
		}
		return out[i].edge.ID < out[j].edge.ID
	})
	if len(out) > limit {
		out = out[:limit]
		more = true
	}
	return out, more, nil
}

func (s *Store) fileNodeForPath(projectID int64, filePath string) (*Node, error) {
	node, err := scanNode(s.db.QueryRow(
		"SELECT "+nodeCols+" FROM nodes WHERE project_id=? AND file_path=? AND kind=? ORDER BY id LIMIT 1",
		projectID, filePath, KindFile,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return node, nil
}

func (s *Store) edgesAdjacentTo(projectID, nodeID int64, outgoing bool, allowed map[string]bool, limit int) ([]traversalCandidate, bool, error) {
	if len(allowed) == 0 {
		return nil, false, nil
	}
	column := "e.source_id"
	neighborJoin := "e.target_id"
	direction := TraversalOutgoing
	if !outgoing {
		column = "e.target_id"
		neighborJoin = "e.source_id"
		direction = TraversalIncoming
	}
	edgeTypes := make([]string, 0, len(allowed))
	for edgeType := range allowed {
		edgeTypes = append(edgeTypes, edgeType)
	}
	sort.Strings(edgeTypes)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(edgeTypes)), ",")
	args := make([]any, 0, len(edgeTypes)+3)
	args = append(args, nodeID, projectID)
	for _, edgeType := range edgeTypes {
		args = append(args, edgeType)
	}
	args = append(args, limit+1)
	rows, err := s.db.Query(
		`SELECT e.id, e.source_id, e.target_id, e.edge_type, e.weight, e.provenance, e.created_at,
			n.id, n.project_id, n.file_path, n.symbol, n.fqn, n.kind, n.language,
			n.start_line, n.end_line, n.signature, n.docstring, n.source_hash, n.vec_id,
			n.created_at, n.updated_at
		 FROM edges e JOIN nodes n ON n.id = `+neighborJoin+`
		 WHERE `+column+` = ? AND n.project_id = ? AND e.edge_type IN (`+placeholders+`)
		 ORDER BY e.edge_type, n.file_path, n.start_line, n.fqn, n.kind, e.provenance, e.id
		 LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]traversalCandidate, 0, limit)
	truncated := false
	for rows.Next() {
		var edge Edge
		var node Node
		if err := rows.Scan(
			&edge.ID, &edge.SourceID, &edge.TargetID, &edge.EdgeType, &edge.Weight, &edge.Provenance, &edge.CreatedAt,
			&node.ID, &node.ProjectID, &node.FilePath, &node.Symbol, &node.FQN, &node.Kind, &node.Language,
			&node.StartLine, &node.EndLine, &node.Signature, &node.Docstring, &node.SourceHash, &node.VecID,
			&node.CreatedAt, &node.UpdatedAt,
		); err != nil {
			return nil, false, err
		}
		if len(out) == limit {
			truncated = true
			break
		}
		neighbor := edge.TargetID
		if !outgoing {
			neighbor = edge.SourceID
		}
		out = append(out, traversalCandidate{edge: edge, neighbor: neighbor, node: node, direction: direction})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}
