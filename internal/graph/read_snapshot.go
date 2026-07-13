package graph

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// ProjectStructuralSnapshot is the graph-side input for a structural export.
// Nodes and FileHashes are captured from the same SQLite read transaction, so
// a caller never combines definition ranges from one index generation with
// file hashes from another.
type ProjectStructuralSnapshot struct {
	Nodes      []Node
	FileHashes map[string]string
}

// ProjectStructuralIndexSnapshot is the source-free index metadata captured
// alongside a streamed structural-symbol walk. FileHashes and Languages come
// from the same SQLite read transaction as every node passed to the visitor,
// so freshness cannot accidentally describe a newer index generation than the
// fingerprint built by that visitor.
type ProjectStructuralIndexSnapshot struct {
	FileHashes map[string]string
	Languages  map[string]bool
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// CanonicalStructuralPath projects an indexed path onto the portable path
// representation used by the structural manifest/export contract. New indexes
// normally contain the host OS separator, while restored legacy indexes may
// contain Windows backslashes even when read on another OS. Treat both forms as
// separators so selectors, ordering, and fingerprints do not depend on GOOS.
func CanonicalStructuralPath(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), `\`, "/")
}

// WalkProjectStructuralSymbols visits every non-file node in deterministic
// structural-export order without materializing the project in memory. The
// callback runs while one read transaction is open, so callers must not issue
// another graph query from it (Store intentionally uses one SQLite connection).
//
// This is the lightweight storage seam used by the structural manifest. The
// graph owns row iteration; versioned fingerprints and peer contracts remain
// in internal/app.
func (s *Store) WalkProjectStructuralSymbols(projectID int64, visit func(Node) error) error {
	_, err := s.WalkProjectStructuralIndexSnapshot(projectID, visit)
	return err
}

// WalkProjectStructuralIndexSnapshot visits structural symbols in portable
// export order and returns the file hashes/languages from that exact SQLite
// snapshot. Symbols remain streamed through visit; only O(files) source-free
// metadata is retained for a subsequent working-tree freshness check.
func (s *Store) WalkProjectStructuralIndexSnapshot(projectID int64, visit func(Node) error) (*ProjectStructuralIndexSnapshot, error) {
	return s.walkProjectStructuralIndexSnapshot(projectID, visit, nil)
}

// walkProjectStructuralIndexSnapshot exposes a deterministic test barrier
// after symbol iteration but before index metadata is read. Production callers
// always use WalkProjectStructuralIndexSnapshot.
func (s *Store) walkProjectStructuralIndexSnapshot(projectID int64, visit func(Node) error, afterSymbols func() error) (*ProjectStructuralIndexSnapshot, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin structural symbol walk: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(
		"SELECT "+nodeCols+` FROM nodes
		 WHERE project_id=? AND kind<>?
		 ORDER BY replace(file_path, char(92), '/'),
		          start_line, end_line, fqn, kind, symbol,
		          language, signature, docstring, source_hash, id`,
		projectID, KindFile)
	if err != nil {
		return nil, fmt.Errorf("read structural symbols: %w", err)
	}

	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan structural symbol: %w", err)
		}
		if err := visit(*n); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("visit structural symbol: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate structural symbols: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close structural symbols: %w", err)
	}
	if afterSymbols != nil {
		if err := afterSymbols(); err != nil {
			return nil, fmt.Errorf("structural index snapshot barrier: %w", err)
		}
	}
	hashes, err := projectFileHashesFrom(tx, projectID)
	if err != nil {
		return nil, err
	}
	languages, err := projectLanguagesFrom(tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit structural symbol walk: %w", err)
	}
	return &ProjectStructuralIndexSnapshot{FileHashes: hashes, Languages: languages}, nil
}

// ProjectStructuralSnapshot captures all project nodes and incremental-index
// file hashes in one SQLite snapshot.
func (s *Store) ProjectStructuralSnapshot(projectID int64) (*ProjectStructuralSnapshot, error) {
	return s.projectStructuralSnapshot(projectID, nil)
}

// projectStructuralSnapshot exposes an internal barrier between the two reads
// for deterministic concurrency tests. Production callers always pass nil.
func (s *Store) projectStructuralSnapshot(projectID int64, afterNodes func() error) (*ProjectStructuralSnapshot, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin structural read snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nodes, err := projectNodesFrom(tx, projectID)
	if err != nil {
		return nil, err
	}
	if afterNodes != nil {
		if err := afterNodes(); err != nil {
			return nil, fmt.Errorf("structural read snapshot barrier: %w", err)
		}
	}
	hashes, err := projectFileHashesFrom(tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit structural read snapshot: %w", err)
	}
	return &ProjectStructuralSnapshot{Nodes: nodes, FileHashes: hashes}, nil
}

// projectNodesAndEdgesSnapshot captures the two generations-sensitive inputs
// to ProjectArchitecture in one SQLite snapshot. The barrier is test-only.
func (s *Store) projectNodesAndEdgesSnapshot(projectID int64, afterNodes func() error) ([]Node, []Edge, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin architecture read snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nodes, err := projectNodesFrom(tx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if afterNodes != nil {
		if err := afterNodes(); err != nil {
			return nil, nil, fmt.Errorf("architecture read snapshot barrier: %w", err)
		}
	}
	edges, err := projectEdgesFrom(tx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit architecture read snapshot: %w", err)
	}
	return nodes, edges, nil
}

func projectNodesFrom(q rowsQuerier, projectID int64) ([]Node, error) {
	rows, err := q.Query("SELECT "+nodeCols+" FROM nodes WHERE project_id=? ORDER BY id", projectID)
	if err != nil {
		return nil, fmt.Errorf("read project nodes snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project nodes snapshot: %w", err)
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project nodes snapshot: %w", err)
	}
	return out, nil
}

func projectEdgesFrom(q rowsQuerier, projectID int64) ([]Edge, error) {
	rows, err := q.Query(
		`SELECT e.id, e.source_id, e.target_id, e.edge_type, e.weight, e.provenance, e.created_at
		 FROM edges e JOIN nodes n ON e.source_id = n.id
		 WHERE n.project_id = ? ORDER BY e.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("read project edges snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.EdgeType, &e.Weight, &e.Provenance, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project edges snapshot: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project edges snapshot: %w", err)
	}
	return out, nil
}

func projectFileHashesFrom(q rowsQuerier, projectID int64) (map[string]string, error) {
	rows, err := q.Query("SELECT file_path, file_hash FROM index_state WHERE project_id=?", projectID)
	if err != nil {
		return nil, fmt.Errorf("read project file hashes snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var file, hash string
		if err := rows.Scan(&file, &hash); err != nil {
			return nil, fmt.Errorf("scan project file hashes snapshot: %w", err)
		}
		out[file] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project file hashes snapshot: %w", err)
	}
	return out, nil
}

func projectLanguagesFrom(q rowsQuerier, projectID int64) (map[string]bool, error) {
	rows, err := q.Query("SELECT DISTINCT language FROM nodes WHERE project_id=? AND language<>''", projectID)
	if err != nil {
		return nil, fmt.Errorf("read project languages snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]bool)
	for rows.Next() {
		var language string
		if err := rows.Scan(&language); err != nil {
			return nil, fmt.Errorf("scan project language snapshot: %w", err)
		}
		out[language] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project languages snapshot: %w", err)
	}
	return out, nil
}
