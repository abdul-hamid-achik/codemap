package graph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// UnresolvedRef is one declarative reference (reads/writes/documents/depends_on)
// that matched no node at resolution time. See the unresolved_refs table
// comment in schema.go for why these persist across runs.
type UnresolvedRef struct {
	FilePath string
	ToName   string
	Kind     string
}

// FilesWithUnresolvedRefsByName returns the distinct files that have dangling
// references to any of the given symbol names. The indexer calls this with the
// symbol names of the files it just (re)indexed, so a previously-missing table,
// heading, or sqlc query appearing anywhere in the changed set re-opens exactly
// the dangling files that can now resolve — nothing else is reparsed.
func (s *Store) FilesWithUnresolvedRefsByName(projectID int64, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	const chunk = 500 // same ceiling as DeleteCallEdgesBySourceTx
	seen := map[string]bool{}
	var files []string
	for start := 0; start < len(names); start += chunk {
		end := start + chunk
		if end > len(names) {
			end = len(names)
		}
		batch := names[start:end]
		ph := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		for i, n := range batch {
			ph[i] = "?"
			args = append(args, n)
		}
		args = append(args, projectID)
		if err := collectDistinctFiles(s.db,
			"SELECT DISTINCT file_path FROM unresolved_refs WHERE to_name IN ("+
				strings.Join(ph, ",")+") AND project_id = ?", args, seen, &files); err != nil {
			return nil, fmt.Errorf("unresolved refs by name: %w", err)
		}
	}
	return files, nil
}

// ReplaceUnresolvedRefsTx atomically swaps one file's dangling-reference rows
// for the given set (an empty set clears the file — everything resolved).
func ReplaceUnresolvedRefsTx(tx *sql.Tx, projectID int64, file string, refs []UnresolvedRef) error {
	if _, err := tx.Exec(
		"DELETE FROM unresolved_refs WHERE project_id=? AND file_path=?", projectID, file); err != nil {
		return fmt.Errorf("clear unresolved refs: %w", err)
	}
	for _, r := range refs {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO unresolved_refs (project_id, file_path, to_name, kind) VALUES (?,?,?,?)",
			projectID, file, r.ToName, r.Kind); err != nil {
			return fmt.Errorf("record unresolved ref: %w", err)
		}
	}
	return nil
}

// DanglingDeclarativeInbound returns the declarative references that currently
// target nodes in the given file. The indexer records them as unresolved right
// before pruning the file, so linkers keep a healing trail: when the file (or a
// same-named symbol) returns, the dangling rows re-open the linking files.
// A file node carries no symbol, so its rows are keyed by the file path — the
// same name a re-created file node will re-introduce.
func (s *Store) DanglingDeclarativeInbound(projectID int64, rel string) ([]UnresolvedRef, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT n2.file_path, COALESCE(n1.symbol, n1.file_path), e.edge_type
		FROM edges e
		JOIN nodes n1 ON e.target_id = n1.id
		JOIN nodes n2 ON e.source_id = n2.id
		WHERE n1.project_id = ? AND n1.file_path = ?
		  AND e.edge_type IN ('`+EdgeReads+`','`+EdgeWrites+`','`+EdgeDocuments+`','`+EdgeDependsOn+`')
	`, projectID, rel)
	if err != nil {
		return nil, fmt.Errorf("dangling declarative inbound: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []UnresolvedRef
	for rows.Next() {
		var r UnresolvedRef
		if err := rows.Scan(&r.FilePath, &r.ToName, &r.Kind); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// RecordUnresolvedRefs records dangling references discovered outside a
// transaction (the prune path). INSERT OR IGNORE: a file may already carry a
// matching row from an earlier pass.
func (s *Store) RecordUnresolvedRefs(ctx context.Context, projectID int64, refs []UnresolvedRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range refs {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO unresolved_refs (project_id, file_path, to_name, kind) VALUES (?,?,?,?)",
			projectID, r.FilePath, r.ToName, r.Kind); err != nil {
			return fmt.Errorf("record unresolved ref: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteUnresolvedRefsByFile drops a file's dangling-reference rows (the file
// itself was pruned or its refs fully re-resolved outside a transaction).
func (s *Store) DeleteUnresolvedRefsByFile(projectID int64, file string) error {
	if _, err := s.db.Exec(
		"DELETE FROM unresolved_refs WHERE project_id=? AND file_path=?", projectID, file); err != nil {
		return fmt.Errorf("delete unresolved refs by file: %w", err)
	}
	return nil
}

// ImportSourcesTargeting returns the distinct files whose file→file imports
// edges target any of the given files. Re-indexing a file replaces its nodes,
// which cascade-deletes importers' edges to it; those importers need their
// import edges rewritten even though their own content did not change.
func (s *Store) ImportSourcesTargeting(projectID int64, rels []string) ([]string, error) {
	if len(rels) == 0 {
		return nil, nil
	}
	const chunk = 500
	seen := map[string]bool{}
	var files []string
	for start := 0; start < len(rels); start += chunk {
		end := start + chunk
		if end > len(rels) {
			end = len(rels)
		}
		batch := rels[start:end]
		ph := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		for i, r := range batch {
			ph[i] = "?"
			args = append(args, r)
		}
		args = append(args, projectID)
		if err := collectDistinctFiles(s.db, `
			SELECT DISTINCT n2.file_path FROM edges
			JOIN nodes n1 ON edges.target_id = n1.id
			JOIN nodes n2 ON edges.source_id = n2.id
			WHERE n1.file_path IN (`+strings.Join(ph, ",")+`) AND n1.project_id = ?
			  AND edges.edge_type = '`+EdgeImports+`'
		`, args, seen, &files); err != nil {
			return nil, fmt.Errorf("import sources targeting: %w", err)
		}
	}
	return files, nil
}

// collectDistinctFiles scans one DISTINCT-file-path query into out, deduping
// across chunked calls through seen.
func collectDistinctFiles(db queryer, query string, args []any, seen map[string]bool, out *[]string) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return err
		}
		if !seen[f] {
			seen[f] = true
			*out = append(*out, f)
		}
	}
	return rows.Err()
}

// queryer is the query surface shared by *sql.DB and *sql.Tx.
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}
