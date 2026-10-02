package graph

import "sort"

// RankedHotspot is one node with its test-aware, same-name-corrected incoming
// call usage. See RankedHotspots for the exact formula.
type RankedHotspot struct {
	Node Node
	// InDegree is the raw count of incoming `calls` edges from counted sources
	// (test sources are skipped unless HotspotRankOptions.IncludeTests).
	InDegree int
	// NameInDegree / PreciseInDegree split InDegree by edge provenance.
	NameInDegree    int
	PreciseInDegree int
	// SharedDefs is how many definitions in the project share the node's name.
	SharedDefs int
	// EffectiveInDegree is PreciseInDegree + NameInDegree/SharedDefs.
	EffectiveInDegree float64
}

// HotspotRankOptions tunes RankedHotspots.
type HotspotRankOptions struct {
	// IncludeTests keeps test nodes/files both as callers (their calls count
	// toward in-degree) and as ranked targets. Default false: importance ignores
	// tests, so a test mock named Close never outranks product code.
	IncludeTests bool
}

// RankedHotspots returns every node with at least one counted incoming `calls`
// edge, ordered by effective in-degree (most important first).
//
// Effective in-degree: name-based resolution credits a call `x.Close()` to EVERY
// definition named Close, so a name-provenance edge is only 1/N of evidence for
// each of the N definitions sharing that name. Therefore
//
//	effective = precise in-degree + name in-degree / (definitions sharing the name)
//
// Precise-provenance edges (go/types, LSP callHierarchy) name exactly one target
// and count fully. Unless IncludeTests is set, calls whose SOURCE is test code
// (IsTestNode on the caller, or a test path) are not counted, and nodes defined
// in test code are not ranked. The divisor still counts every same-named
// definition (tests included) because name-based fan-out reaches them too.
// Ties break on raw in-degree, then file, line, fqn, kind, symbol, id.
func (s *Store) RankedHotspots(projectID int64, opts HotspotRankOptions) ([]RankedHotspot, error) {
	rows, err := s.db.Query(`
		SELECT e.target_id, e.provenance, src.kind, src.file_path, COUNT(*)
		FROM edges e
		JOIN nodes tgt ON tgt.id = e.target_id
		JOIN nodes src ON src.id = e.source_id
		WHERE tgt.project_id = ? AND tgt.kind != ? AND e.edge_type = ?
		GROUP BY e.target_id, e.provenance, src.kind, src.file_path`,
		projectID, KindFile, EdgeCalls)
	if err != nil {
		return nil, err
	}
	type acc struct{ name, precise int }
	counts := map[int64]*acc{}
	for rows.Next() {
		var id int64
		var prov, kind, file string
		var n int
		if err := rows.Scan(&id, &prov, &kind, &file, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !opts.IncludeTests && (kind == KindTest || IsTestPath(file)) {
			continue
		}
		a := counts[id]
		if a == nil {
			a = &acc{}
			counts[id] = a
		}
		if prov == ProvPrecise {
			a.precise += n
		} else {
			a.name += n
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(counts) == 0 {
		return []RankedHotspot{}, nil
	}

	nodes, err := s.ProjectNodes(projectID)
	if err != nil {
		return nil, err
	}
	defs, err := s.SymbolDefCounts(projectID)
	if err != nil {
		return nil, err
	}
	out := make([]RankedHotspot, 0, len(counts))
	for _, n := range nodes {
		a := counts[n.ID]
		if a == nil {
			continue
		}
		if !opts.IncludeTests && IsTestNode(n) {
			continue
		}
		shared := defs[n.Symbol]
		if shared < 1 {
			shared = 1
		}
		out = append(out, RankedHotspot{
			Node: n, InDegree: a.name + a.precise, NameInDegree: a.name, PreciseInDegree: a.precise,
			SharedDefs: shared, EffectiveInDegree: float64(a.precise) + float64(a.name)/float64(shared),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.EffectiveInDegree != b.EffectiveInDegree {
			return a.EffectiveInDegree > b.EffectiveInDegree
		}
		if a.InDegree != b.InDegree {
			return a.InDegree > b.InDegree
		}
		if a.Node.FilePath != b.Node.FilePath {
			return a.Node.FilePath < b.Node.FilePath
		}
		if a.Node.StartLine != b.Node.StartLine {
			return a.Node.StartLine < b.Node.StartLine
		}
		if a.Node.FQN != b.Node.FQN {
			return a.Node.FQN < b.Node.FQN
		}
		if a.Node.Kind != b.Node.Kind {
			return a.Node.Kind < b.Node.Kind
		}
		if a.Node.Symbol != b.Node.Symbol {
			return a.Node.Symbol < b.Node.Symbol
		}
		return a.Node.ID < b.Node.ID
	})
	return out, nil
}

// WiredTargetCounts returns, per target node, how many `references` edges
// (function/method VALUES stored or passed — cobra RunE: runX, mux.HandleFunc(h))
// point at it from counted sources. Unless includeTests, references whose source
// is test code are skipped. Only nodes with at least one reference appear.
func (s *Store) WiredTargetCounts(projectID int64, includeTests bool) (map[int64]int, error) {
	rows, err := s.db.Query(`
		SELECT e.target_id, src.kind, src.file_path, COUNT(*)
		FROM edges e
		JOIN nodes tgt ON tgt.id = e.target_id
		JOIN nodes src ON src.id = e.source_id
		WHERE tgt.project_id = ? AND e.edge_type = ?
		GROUP BY e.target_id, src.kind, src.file_path`,
		projectID, EdgeReferences)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var kind, file string
		var n int
		if err := rows.Scan(&id, &kind, &file, &n); err != nil {
			return nil, err
		}
		if !includeTests && (kind == KindTest || IsTestPath(file)) {
			continue
		}
		out[id] += n
	}
	return out, rows.Err()
}
