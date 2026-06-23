// Package graph is codemap's structural code graph: a pure-Go SQLite store of
// code nodes (files, functions, types, …) and the edges between them (calls,
// imports, implements, …), plus traversal queries over that graph.
package graph

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Node is a code entity in the graph.
type Node struct {
	ID         int64
	ProjectID  int64
	FilePath   string
	Symbol     string
	FQN        string
	Kind       string
	Language   string
	StartLine  int
	EndLine    int
	Signature  string
	Docstring  string
	SourceHash string
	VecID      string
	CreatedAt  string
	UpdatedAt  string
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	ID         int64
	SourceID   int64
	TargetID   int64
	EdgeType   string
	Weight     float64
	Provenance string
	CreatedAt  string
}

// Project is a registered project whose code is in the graph.
type Project struct {
	ID        int64
	Name      string
	Path      string
	Language  string
	CreatedAt string
	UpdatedAt string
}

// Stats summarizes the graph for a project (or all projects when ProjectID 0).
type Stats struct {
	Nodes     int
	Edges     int
	Files     int
	Languages map[string]int
	Kinds     map[string]int
}

// Store is a handle to the SQLite graph database.
type Store struct {
	db *sql.DB
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Open opens (creating if needed) the graph database at path and runs
// migrations. busy_timeout, foreign_keys, and WAL are enabled on every
// connection via DSN pragmas.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open graph db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB for advanced queries (e.g. traversal).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v >= schemaVersion {
		return nil
	}
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// v2 -> v3: add edges.provenance. schemaSQL's CREATE TABLE IF NOT EXISTS is a
	// no-op for a pre-existing edges table, so the column must be added by ALTER —
	// then its index can be created (it can't go in schemaSQL, which runs before
	// the column exists on an upgraded table).
	if v < 3 {
		if err := s.addColumnIfMissing("edges", "provenance", "TEXT NOT NULL DEFAULT 'name'"); err != nil {
			return err
		}
		if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_edges_source_prov ON edges(source_id, edge_type, provenance)"); err != nil {
			return fmt.Errorf("create provenance index: %w", err)
		}
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

// addColumnIfMissing adds a column to a table if it isn't already present. It is
// idempotent and race-safe across the multiple processes that may open the same
// DB (CLAUDE.md's multi-MCP model): if another process wins the race and adds the
// column first, SQLite's "duplicate column name" error is treated as success
// rather than relying on a TOCTOU table_info check.
func (s *Store) addColumnIfMissing(table, col, decl string) error {
	has, err := s.columnExists(table, col)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, decl))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return nil // another process added it concurrently — success
	}
	if err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, col, err)
	}
	return nil
}

func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		// table_info columns: cid, name, type, notnull, dflt_value, pk
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ---- projects ----

// UpsertProject inserts or updates a project by name and returns its id.
func (s *Store) UpsertProject(name, path, language string) (int64, error) {
	ts := now()
	_, err := s.db.Exec(`
		INSERT INTO projects(name, path, language, created_at, updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			path=excluded.path, language=excluded.language, updated_at=excluded.updated_at`,
		name, path, language, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("upsert project: %w", err)
	}
	var id int64
	if err := s.db.QueryRow("SELECT id FROM projects WHERE name=?", name).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// GetProjectByName returns the project with the given name.
func (s *Store) GetProjectByName(name string) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow(
		"SELECT id, name, path, language, created_at, updated_at FROM projects WHERE name=?", name).
		Scan(&p.ID, &p.Name, &p.Path, &p.Language, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListProjects returns all registered projects ordered by name.
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query("SELECT id, name, path, language, created_at, updated_at FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Language, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- nodes ----

const nodeCols = "id, project_id, file_path, symbol, fqn, kind, language, start_line, end_line, signature, docstring, source_hash, vec_id, created_at, updated_at"

func scanNode(sc interface{ Scan(...any) error }) (*Node, error) {
	n := &Node{}
	err := sc.Scan(&n.ID, &n.ProjectID, &n.FilePath, &n.Symbol, &n.FQN, &n.Kind, &n.Language,
		&n.StartLine, &n.EndLine, &n.Signature, &n.Docstring, &n.SourceHash, &n.VecID,
		&n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// AddNode inserts a node and returns its id. CreatedAt/UpdatedAt are stamped if
// empty.
func (s *Store) AddNode(n *Node) (int64, error) {
	if n.CreatedAt == "" {
		n.CreatedAt = now()
	}
	n.UpdatedAt = now()
	res, err := s.db.Exec(`
		INSERT INTO nodes(project_id, file_path, symbol, fqn, kind, language, start_line, end_line,
			signature, docstring, source_hash, vec_id, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ProjectID, n.FilePath, n.Symbol, n.FQN, n.Kind, n.Language, n.StartLine, n.EndLine,
		n.Signature, n.Docstring, n.SourceHash, n.VecID, n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("add node: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	n.ID = id
	return id, nil
}

// GetNode returns the node with the given id.
func (s *Store) GetNode(id int64) (*Node, error) {
	n, err := scanNode(s.db.QueryRow("SELECT "+nodeCols+" FROM nodes WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

// FindNodesBySymbol returns nodes whose symbol matches exactly within a project.
func (s *Store) FindNodesBySymbol(projectID int64, symbol string) ([]Node, error) {
	return s.queryNodes("SELECT "+nodeCols+" FROM nodes WHERE project_id=? AND symbol=? ORDER BY file_path, start_line", projectID, symbol)
}

// NodeExistsByName reports whether any node in the project has the given symbol
// name or fully-qualified name. Annotations surface by matching either, so this
// answers "would an annotation on this target ever surface?" — used to warn on
// annotations pinned to a name nothing is indexed under.
func (s *Store) NodeExistsByName(projectID int64, name string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM nodes WHERE project_id=? AND (symbol=? OR fqn=?) LIMIT 1",
		projectID, name, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// NodesInFile returns all nodes for a file within a project.
func (s *Store) NodesInFile(projectID int64, file string) ([]Node, error) {
	return s.queryNodes("SELECT "+nodeCols+" FROM nodes WHERE project_id=? AND file_path=? ORDER BY start_line", projectID, file)
}

// DeleteNodesInFile removes all nodes for a file (edges cascade). Used for
// incremental reindex of a changed file.
func (s *Store) DeleteNodesInFile(projectID int64, file string) error {
	_, err := s.db.Exec("DELETE FROM nodes WHERE project_id=? AND file_path=?", projectID, file)
	return err
}

func (s *Store) queryNodes(query string, args ...any) ([]Node, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// ---- edges ----

// AddEdge inserts a directed edge between two existing nodes.
func (s *Store) AddEdge(sourceID, targetID int64, edgeType string, weight float64) (int64, error) {
	return s.AddEdgeProv(sourceID, targetID, edgeType, weight, ProvName)
}

// AddEdgeProv inserts an edge tagged with its provenance ('name' for fast
// name-based resolution, 'precise' for the go/types pass). AddEdge defaults to
// 'name' so existing callers (the name-based passes) need no change.
func (s *Store) AddEdgeProv(sourceID, targetID int64, edgeType string, weight float64, provenance string) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO edges(source_id, target_id, edge_type, weight, provenance, created_at) VALUES(?,?,?,?,?,?)",
		sourceID, targetID, edgeType, weight, provenance, now())
	if err != nil {
		return 0, fmt.Errorf("add edge: %w", err)
	}
	return res.LastInsertId()
}

// DeleteCallEdgesBySource removes the calls/references edges of the given source
// nodes that have the given provenance. The go/types pass uses it to drop the
// name-based ('name') call edges of cleanly type-checked source nodes before
// inserting their precise replacements — so precise supersedes name without
// double-counting. defines edges (structural, from file nodes) are never touched.
func (s *Store) DeleteCallEdgesBySource(sourceIDs []int64, provenance string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	const chunk = 500 // stay well under SQLite's variable limit
	for start := 0; start < len(sourceIDs); start += chunk {
		end := start + chunk
		if end > len(sourceIDs) {
			end = len(sourceIDs)
		}
		batch := sourceIDs[start:end]
		ph := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		for i, id := range batch {
			ph[i] = "?"
			args = append(args, id)
		}
		args = append(args, provenance)
		q := "DELETE FROM edges WHERE source_id IN (" + strings.Join(ph, ",") +
			") AND edge_type IN ('" + EdgeCalls + "','" + EdgeReferences + "') AND provenance = ?"
		if _, err := s.db.Exec(q, args...); err != nil {
			return fmt.Errorf("delete call edges by source: %w", err)
		}
	}
	return nil
}

// ---- index state ----

// SetFileHash records the indexed hash for a file (incremental reindex).
func (s *Store) SetFileHash(projectID int64, file, hash string) error {
	_, err := s.db.Exec(`
		INSERT INTO index_state(project_id, file_path, file_hash, indexed_at)
		VALUES(?,?,?,?)
		ON CONFLICT(project_id, file_path) DO UPDATE SET file_hash=excluded.file_hash, indexed_at=excluded.indexed_at`,
		projectID, file, hash, now())
	return err
}

// FileHash returns the previously indexed hash for a file, or "" if unknown.
func (s *Store) FileHash(projectID int64, file string) (string, error) {
	var h string
	err := s.db.QueryRow("SELECT file_hash FROM index_state WHERE project_id=? AND file_path=?", projectID, file).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// ---- stats ----

// Stats returns counts for a project (projectID 0 = all projects).
func (s *Store) Stats(projectID int64) (Stats, error) {
	st := Stats{Languages: map[string]int{}, Kinds: map[string]int{}}
	where, args := projectFilter(projectID)

	if err := s.db.QueryRow("SELECT COUNT(*) FROM nodes"+where, args...).Scan(&st.Nodes); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("SELECT COUNT(DISTINCT file_path) FROM nodes"+where, args...).Scan(&st.Files); err != nil {
		return st, err
	}
	if err := s.countEdges(projectID, &st); err != nil {
		return st, err
	}
	if err := s.countBy("language", where, args, st.Languages); err != nil {
		return st, err
	}
	if err := s.countBy("kind", where, args, st.Kinds); err != nil {
		return st, err
	}
	return st, nil
}

func projectFilter(projectID int64) (string, []any) {
	if projectID == 0 {
		return "", nil
	}
	return " WHERE project_id=?", []any{projectID}
}

func (s *Store) countEdges(projectID int64, st *Stats) error {
	if projectID == 0 {
		return s.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&st.Edges)
	}
	// edges whose source node belongs to the project
	return s.db.QueryRow(
		"SELECT COUNT(*) FROM edges e JOIN nodes n ON e.source_id=n.id WHERE n.project_id=?",
		projectID).Scan(&st.Edges)
}

// CountEdgesByProvenance counts the project's edges with the given provenance
// (e.g. ProvPrecise) — used to report whether an index is name-based or precise.
func (s *Store) CountEdgesByProvenance(projectID int64, provenance string) (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM edges e JOIN nodes n ON e.source_id=n.id WHERE n.project_id=? AND e.provenance=?",
		projectID, provenance).Scan(&n)
	return n, err
}

func (s *Store) countBy(column, where string, args []any, dst map[string]int) error {
	rows, err := s.db.Query("SELECT "+column+", COUNT(*) FROM nodes"+where+" GROUP BY "+column, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var c int
		if err := rows.Scan(&k, &c); err != nil {
			return err
		}
		dst[k] = c
	}
	return rows.Err()
}
