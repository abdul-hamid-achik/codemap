package graph

import (
	"errors"
	"sort"
	"strings"
	"unicode"
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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
	starts, err := s.startNodeIDs(projectID, symbol)
	if err != nil {
		return nil, err
	}
	return s.blastRadiusFromNodes(projectID, starts, maxDepth)
}

// BlastRadiusFromNode is the exact-node variant of BlastRadius. It follows
// incoming call edges from one current graph node rather than unioning every
// definition that shares a symbol name. The node id is deliberately an
// internal traversal key; public callers should resolve a source selector and
// never persist graph ids across reindexes.
func (s *Store) BlastRadiusFromNode(projectID, nodeID int64, maxDepth int) ([]NodeDepth, error) {
	return s.blastRadiusFromNodes(projectID, []int64{nodeID}, maxDepth)
}

func (s *Store) blastRadiusFromNodes(projectID int64, starts []int64, maxDepth int) ([]NodeDepth, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if len(starts) == 0 {
		return nil, nil
	}
	// Refuse a node id from another project. IDs are process-local storage
	// details, so accepting a cross-project id here would silently violate the
	// query's project scope.
	for _, id := range starts {
		n, err := s.GetNode(id)
		if errors.Is(err, ErrNotFound) || (err == nil && n.ProjectID != projectID) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
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
// `calls` edges), ranked by the caller's own in-degree (hubs first) with
// location as a stable tiebreak. When a symbol has more callers than an agent
// will ever see in one bundle (contextListCap et al.), this ordering means the
// truncated view surfaces the callers that matter most — widely-depended-on
// hubs — instead of an arbitrary alphabetical slice.
func (s *Store) Callers(projectID int64, symbol string) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("src") + " FROM edges e " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"JOIN nodes src ON e.source_id = src.id " +
		"WHERE tgt.project_id = ? AND tgt.symbol = ? AND e.edge_type = ? " +
		"ORDER BY (SELECT COUNT(*) FROM edges e2 WHERE e2.target_id = src.id AND e2.edge_type = ?) DESC, " +
		"src.file_path, src.start_line"
	return s.queryNodes(q, projectID, symbol, EdgeCalls, EdgeCalls)
}

// CallersOfNode returns callers of one exact definition node. Unlike Callers,
// it never unions same-named targets. On a name-provenance graph the incoming
// edges may still be heuristic; consumers distinguish that via call_graph.
func (s *Store) CallersOfNode(projectID, nodeID int64) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("src") + " FROM edges e " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"JOIN nodes src ON e.source_id = src.id " +
		"WHERE tgt.project_id = ? AND tgt.id = ? AND e.edge_type = ? " +
		"ORDER BY (SELECT COUNT(*) FROM edges e2 WHERE e2.target_id = src.id AND e2.edge_type = ?) DESC, " +
		"src.file_path, src.start_line"
	return s.queryNodes(q, projectID, nodeID, EdgeCalls, EdgeCalls)
}

// Callees returns the distinct nodes called by any node named symbol (outgoing
// `calls` edges), ranked by the callee's own in-degree (hubs first) with
// location as a stable tiebreak — see Callers for the rationale.
func (s *Store) Callees(projectID int64, symbol string) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("tgt") + " FROM edges e " +
		"JOIN nodes src ON e.source_id = src.id " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"WHERE src.project_id = ? AND src.symbol = ? AND e.edge_type = ? " +
		"ORDER BY (SELECT COUNT(*) FROM edges e3 WHERE e3.target_id = tgt.id AND e3.edge_type = ?) DESC, " +
		"tgt.file_path, tgt.start_line"
	return s.queryNodes(q, projectID, symbol, EdgeCalls, EdgeCalls)
}

// CalleesOfNode returns callees of one exact definition node. It is the
// selector-safe counterpart of the name-unioning Callees query.
func (s *Store) CalleesOfNode(projectID, nodeID int64) ([]Node, error) {
	q := "SELECT DISTINCT " + nodeColsAs("tgt") + " FROM edges e " +
		"JOIN nodes src ON e.source_id = src.id " +
		"JOIN nodes tgt ON e.target_id = tgt.id " +
		"WHERE src.project_id = ? AND src.id = ? AND e.edge_type = ? " +
		"ORDER BY (SELECT COUNT(*) FROM edges e3 WHERE e3.target_id = tgt.id AND e3.edge_type = ?) DESC, " +
		"tgt.file_path, tgt.start_line"
	return s.queryNodes(q, projectID, nodeID, EdgeCalls, EdgeCalls)
}

func (s *Store) calleeIDs(sourceID int64) ([]int64, error) {
	return s.scanIDs("SELECT target_id FROM edges WHERE source_id=? AND edge_type=?", sourceID, EdgeCalls)
}

// CalleeClosure returns the set of node ids reachable from symbol's definition
// node(s) by following call edges FORWARD up to maxDepth hops (the start nodes are
// included). Cycle-safe. Answers "what does this entrypoint's call tree touch" —
// the basis for least-privilege analysis (which secret keys a code path needs).
func (s *Store) CalleeClosure(projectID int64, symbol string, maxDepth int) (map[int64]bool, error) {
	// O21: same default as BlastRadius (3). Without this, maxDepth=0
	// in a caller means "0 hops", which silently returns just the
	// start nodes and produces a misleading "this function is its
	// own closure" answer — the caller (a `secret_impact` or
	// risk call) is then giving a confident wrong answer. The
	// 3-hop default matches BlastRadius, the most-cited caller.
	if maxDepth <= 0 {
		maxDepth = 3
	}
	starts, err := s.startNodeIDs(projectID, symbol)
	if err != nil {
		return nil, err
	}
	reached := make(map[int64]bool, len(starts))
	type item struct {
		id    int64
		depth int
	}
	var queue []item
	for _, id := range starts {
		if !reached[id] {
			reached[id] = true
			queue = append(queue, item{id, 0})
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		callees, err := s.calleeIDs(cur.id)
		if err != nil {
			return nil, err
		}
		for _, c := range callees {
			if !reached[c] {
				reached[c] = true
				queue = append(queue, item{c, cur.depth + 1})
			}
		}
	}
	return reached, nil
}

// NodeAtLine returns the symbol node in file whose [StartLine, EndLine] range
// encloses line, preferring the innermost (smallest range) when symbols nest. The
// file node itself (empty Symbol) is never returned. ok is false when no symbol
// encloses the line. This is the file:line → enclosing-symbol entry point that lets
// sibling tools (which emit file:line) join their results onto the graph.
func (s *Store) NodeAtLine(projectID int64, file string, line int) (Node, bool, error) {
	nodes, err := s.NodesInFile(projectID, file)
	if err != nil {
		return Node{}, false, err
	}
	var best Node
	found := false
	for _, n := range nodes {
		if n.Symbol == "" { // skip the file node
			continue
		}
		if n.StartLine <= line && line <= n.EndLine {
			if !found || (n.EndLine-n.StartLine) < (best.EndLine-best.StartLine) {
				best, found = n, true
			}
		}
	}
	return best, found, nil
}

// NodesAtSource returns definition nodes in file that can match a durable
// source selector. When fqn is present it is the primary identity and the
// recorded start line is only a disambiguator/fallback, so a reindex after
// inserting lines above a declaration still resolves the same symbol. Without
// an FQN, startLine is required. kind is an optional guard in either mode.
func (s *Store) NodesAtSource(projectID int64, file string, startLine int, fqn, kind string) ([]Node, error) {
	nodes, err := s.NodesInFile(projectID, file)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, 1)
	for _, n := range nodes {
		if n.Kind == KindFile || (kind != "" && n.Kind != kind) {
			continue
		}
		if fqn != "" {
			if n.FQN == fqn {
				out = append(out, n)
			}
			continue
		}
		if startLine > 0 && n.StartLine == startLine {
			out = append(out, n)
		}
	}
	// A duplicated FQN can occur in invalid/incomplete code. Preserve exactness
	// by using the old declaration line only as a tie-break; never pick an
	// arbitrary current node.
	if len(out) > 1 && startLine > 0 {
		lineMatches := out[:0]
		for _, n := range out {
			if n.StartLine == startLine {
				lineMatches = append(lineMatches, n)
			}
		}
		if len(lineMatches) > 0 {
			out = lineMatches
		}
	}
	return out, nil
}

// Hotspot is a node with its incoming-usage count (hub detection).
type Hotspot struct {
	Node     Node
	InDegree int
}

// Hotspots returns the most-called nodes (highest incoming `calls` count) — the
// hubs of the call graph. File nodes are excluded. Counts only `calls`, not
// `references` (function values wired as handlers): a hub is something many
// call sites depend on, and counting value references would let a commonly-named
// field shadow the real hubs. `references` feed only `orphans`.
func (s *Store) Hotspots(projectID int64, limit int) ([]Hotspot, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT e.target_id, COUNT(*) AS indeg
		FROM edges e JOIN nodes n ON e.target_id = n.id
		WHERE n.project_id = ? AND n.kind != ? AND e.edge_type = ?
		GROUP BY e.target_id
		ORDER BY indeg DESC, e.target_id
		LIMIT ?`, projectID, KindFile, EdgeCalls, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

// SymbolMatch pairs a SearchSymbols hit with which field satisfied the
// query, so a no-embeddings caller (FindSymbols) can surface why a result
// showed up (name vs docstring).
type SymbolMatch struct {
	Node Node
	// MatchedIn is "symbol", "fqn", or "docstring" — whichever field the
	// query's tokens were found in (see SearchSymbols tiering).
	MatchedIn string
}

// tokenizeSearchQuery splits a search query into terms on whitespace and,
// within each whitespace-delimited word, on camelCase boundaries — query
// side only; indexed symbol/fqn/docstring text is matched via LIKE as
// stored. This is what lets "parse selector" find both ParseSelector and
// parse_selector: whitespace alone already splits it into ["parse",
// "selector"], and each is a case-insensitive substring of either spelling.
// It deliberately does NOT split on `_`/`-`/digits — do_work must stay one
// token so the P1-05 LIKE-escape/back-compat behavior (do_work must not
// match doXwork) is unaffected by tokenization. Empty/whitespace-only
// queries yield zero tokens. Tokens are de-duplicated case-insensitively,
// order preserved.
func tokenizeSearchQuery(query string) []string {
	var words []string
	var cur []rune
	flushWord := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	for _, r := range query {
		if unicode.IsSpace(r) {
			flushWord()
			continue
		}
		cur = append(cur, r)
	}
	flushWord()

	var tokens []string
	for _, w := range words {
		tokens = append(tokens, splitCamelCase(w)...)
	}

	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		lt := strings.ToLower(t)
		if seen[lt] {
			continue
		}
		seen[lt] = true
		out = append(out, t)
	}
	return out
}

// splitCamelCase breaks a single word on case boundaries: a lower/digit
// followed by an upper ("parseSelector" -> "parse","Selector"), and an
// acronym run followed by a new capitalized word ("XMLParser" ->
// "XML","Parser"). Words with no such boundary (all-lower, all-upper, or a
// single leading capital like "Store") pass through unchanged as one token.
func splitCamelCase(w string) []string {
	runes := []rune(w)
	var out []string
	var cur []rune
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			boundary := unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextIsLower)
			if boundary && len(cur) > 0 {
				out = append(out, string(cur))
				cur = nil
			}
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// SearchSymbols is the no-embeddings search floor: it finds nodes by name
// and — additively — by docstring, tokenizing the query so a multi-word
// query like "parse selector" matches a camelCase or snake_case symbol
// without requiring a literal substring match. File nodes excluded.
//
// Matching: every token must LIKE-match at least one of symbol/fqn/docstring
// (AND across tokens, OR across fields) — that's the candidate set. Ranking
// tiers within it:
//  1. symbol or fqn alone contains ALL tokens (name match; MatchedIn "symbol"
//     or "fqn")
//  2. docstring alone contains ALL tokens (MatchedIn "docstring") — ranked
//     below name matches
//  3. legacy back-compat: the whole, untokenized query is a literal
//     substring of symbol or fqn (preserves pre-tokenization behavior for
//     any query shape the tokenizer doesn't improve on)
//
// Within tier 1, exact name match > prefix match > substring, then shortest
// symbol, then alphabetical, then file path — the same secondary ordering
// SearchSymbols has always used (P1-05 / B77), so a single-term query
// behaves exactly as before. P1-05 also fixed the LIKE-metachar escaping
// (B13): `do_work` must not match `doXwork`, so tokens/the raw query are
// always run through likeEscape with ESCAPE '\\'; tokenization does not
// split on `_`, so that guarantee is unaffected by this change.
//
// SQLite's LIKE is case-insensitive for ASCII by default (verified by
// TestSQLiteLikeIsASCIICaseInsensitive); this function's own tiering also
// lower-cases both sides in Go so the ranking never depends on that
// assumption alone.
func (s *Store) SearchSymbols(projectID int64, query string, limit int) ([]SymbolMatch, error) {
	if limit <= 0 {
		limit = 50
	}
	tokens := tokenizeSearchQuery(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	args := []any{projectID, KindFile}
	conds := make([]string, 0, len(tokens))
	for _, t := range tokens {
		like := "%" + likeEscape(t) + "%"
		conds = append(conds, "(n.symbol LIKE ? ESCAPE '\\' OR n.fqn LIKE ? ESCAPE '\\' OR n.docstring LIKE ? ESCAPE '\\')")
		args = append(args, like, like, like)
	}
	rawLike := "%" + likeEscape(query) + "%"
	q := "SELECT " + nodeColsAs("n") + " FROM nodes n WHERE n.project_id = ? AND n.kind != ? AND ((" +
		strings.Join(conds, " AND ") + ") OR n.symbol LIKE ? ESCAPE '\\' OR n.fqn LIKE ? ESCAPE '\\')"
	args = append(args, rawLike, rawLike)

	nodes, err := s.queryNodes(q, args...)
	if err != nil {
		return nil, err
	}

	lowerTokens := make([]string, len(tokens))
	for i, t := range tokens {
		lowerTokens[i] = strings.ToLower(t)
	}
	lowerQuery := strings.ToLower(query)
	allTokensIn := func(hay string) bool {
		if hay == "" {
			return false
		}
		for _, t := range lowerTokens {
			if !strings.Contains(hay, t) {
				return false
			}
		}
		return true
	}

	type scored struct {
		m      SymbolMatch
		tier   int
		exact  bool
		prefix bool
	}
	out := make([]scored, 0, len(nodes))
	for _, n := range nodes {
		lsym := strings.ToLower(n.Symbol)
		lfqn := strings.ToLower(n.FQN)
		ldoc := strings.ToLower(n.Docstring)

		var tier int
		var matchedIn string
		switch {
		case allTokensIn(lsym):
			tier, matchedIn = 0, "symbol"
		case allTokensIn(lfqn):
			tier, matchedIn = 0, "fqn"
		case allTokensIn(ldoc):
			tier, matchedIn = 1, "docstring"
		case strings.Contains(lsym, lowerQuery):
			tier, matchedIn = 2, "symbol"
		case strings.Contains(lfqn, lowerQuery):
			tier, matchedIn = 2, "fqn"
		default:
			// Belt-and-suspenders: the SQL prefilter is intentionally a
			// touch broader than this precise Go-side check; skip rows
			// that don't actually satisfy any tier.
			continue
		}
		out = append(out, scored{
			m:      SymbolMatch{Node: n, MatchedIn: matchedIn},
			tier:   tier,
			exact:  lsym == lowerQuery,
			prefix: strings.HasPrefix(lsym, lowerQuery),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.exact != b.exact {
			return a.exact
		}
		if a.prefix != b.prefix {
			return a.prefix
		}
		if len(a.m.Node.Symbol) != len(b.m.Node.Symbol) {
			return len(a.m.Node.Symbol) < len(b.m.Node.Symbol)
		}
		if a.m.Node.Symbol != b.m.Node.Symbol {
			return a.m.Node.Symbol < b.m.Node.Symbol
		}
		return a.m.Node.FilePath < b.m.Node.FilePath
	})
	if len(out) > limit {
		out = out[:limit]
	}
	result := make([]SymbolMatch, len(out))
	for i, o := range out {
		result[i] = o.m
	}
	return result, nil
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
	defer func() { _ = rows.Close() }()
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

// HasNameInEdges returns which of the given nodes still have at least one
// name-provenance incoming `calls` edge — i.e. whose in-degree may be inflated
// by name-based fan-out. A node whose callers were all resolved by the go/types
// pass (provenance='precise') is absent, so its count is trustworthy. Matches
// Hotspots in counting only `calls` (not value `references`).
func (s *Store) HasNameInEdges(nodeIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return out, nil
	}
	const chunk = 800
	for start := 0; start < len(nodeIDs); start += chunk {
		end := start + chunk
		if end > len(nodeIDs) {
			end = len(nodeIDs)
		}
		batch := nodeIDs[start:end]
		ph := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			ph[i] = "?"
			args[i] = id
		}
		q := "SELECT DISTINCT target_id FROM edges WHERE target_id IN (" + strings.Join(ph, ",") +
			") AND edge_type = '" + EdgeCalls + "' AND provenance = '" + ProvName + "'"
		ids, err := s.scanIDs(q, args...)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
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
	// A node is dead only if nothing calls it AND nothing references it as a value
	// (e.g. `RunE: handler`, `register(callback)`, a function stored in a slice/map)
	// — otherwise framework handlers wired by value would all look like dead code.
	//
	// Also exclude methods that implement well-known stdlib interfaces (error,
	// fmt.Stringer, errors.Unwrap, the JSON/text marshalers): they're invoked via
	// interface dispatch, which a name/types call graph can't see, so they would
	// ALWAYS be false positives. These names are conventionally reserved for those
	// interfaces, so a method with one is effectively never meaningful dead code
	// (every error type has an Error method) — dropping them keeps the candidate
	// list signal-rich on real Go code. Custom-interface methods are still listed
	// (hence "candidates").
	q := "SELECT " + nodeColsAs("n") + ` FROM nodes n
		WHERE n.project_id = ? AND n.kind IN (?, ?)
		AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = n.id AND e.edge_type IN (?, ?))
		AND NOT (n.kind = ? AND n.symbol IN ('main', 'init'))
		AND NOT (n.kind = ? AND n.symbol IN ('Error', 'String', 'Unwrap', 'MarshalJSON', 'UnmarshalJSON', 'MarshalText', 'UnmarshalText'))
		ORDER BY n.file_path, n.start_line
		LIMIT ?`
	return s.queryNodes(q, projectID, KindFunction, KindMethod, EdgeCalls, EdgeReferences, KindFunction, KindMethod, limit)
}

// Path returns the shortest call path from one symbol to another (following
// outgoing `calls` edges), or nil if none exists within maxDepth. A non-positive
// maxDepth is unbounded. Cycle-safe.
func (s *Store) Path(projectID int64, from, to string, maxDepth int) ([]Node, error) {
	starts, err := s.startNodeIDs(projectID, from)
	if err != nil {
		return nil, err
	}
	targets, err := s.startNodeIDs(projectID, to)
	if err != nil {
		return nil, err
	}
	return s.pathFromNodes(projectID, starts, targets, maxDepth)
}

// PathFromNodes returns the shortest path between two exact current nodes.
// Public APIs resolve durable source selectors into these ephemeral ids before
// calling it, so duplicate symbol names cannot change either endpoint.
func (s *Store) PathFromNodes(projectID, fromID, toID int64, maxDepth int) ([]Node, error) {
	return s.pathFromNodes(projectID, []int64{fromID}, []int64{toID}, maxDepth)
}

func (s *Store) pathFromNodes(projectID int64, starts, targets []int64, maxDepth int) ([]Node, error) {
	if len(starts) == 0 || len(targets) == 0 {
		return nil, nil
	}
	for _, id := range append(append([]int64(nil), starts...), targets...) {
		n, err := s.GetNode(id)
		if errors.Is(err, ErrNotFound) || (err == nil && n.ProjectID != projectID) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
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
		if maxDepth > 0 && depth[cur] >= maxDepth {
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

// ProjectNodes returns all nodes in a project, ordered by id. Used to build the
// project-wide symbol index for reference resolution.
func (s *Store) ProjectNodes(projectID int64) ([]Node, error) {
	return s.queryNodes("SELECT "+nodeCols+" FROM nodes WHERE project_id=? ORDER BY id", projectID)
}

// ProjectEdges returns every edge whose source node is in the project, ordered by
// id. Edges carry no project_id of their own (they belong to a project via their
// source/target nodes, both in-project), so this is the project's full edge set —
// used to serialize a project's graph for snapshotting.
func (s *Store) ProjectEdges(projectID int64) ([]Edge, error) {
	rows, err := s.db.Query(
		`SELECT e.id, e.source_id, e.target_id, e.edge_type, e.weight, e.provenance, e.created_at
		 FROM edges e JOIN nodes n ON e.source_id = n.id
		 WHERE n.project_id = ? ORDER BY e.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.EdgeType, &e.Weight, &e.Provenance, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// IndexEntry is one file's recorded incremental-reindex state.
type IndexEntry struct {
	FilePath string `json:"file_path"`
	FileHash string `json:"file_hash"`
}

// ProjectIndexState returns every (file_path, file_hash) recorded for the project,
// ordered by path — the incremental-reindex hashes, for serialization.
func (s *Store) ProjectIndexState(projectID int64) ([]IndexEntry, error) {
	rows, err := s.db.Query("SELECT file_path, file_hash FROM index_state WHERE project_id=? ORDER BY file_path", projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IndexEntry
	for rows.Next() {
		var e IndexEntry
		if err := rows.Scan(&e.FilePath, &e.FileHash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
