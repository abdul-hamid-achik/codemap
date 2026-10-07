package graph

import (
	"database/sql"
	"sort"
	"strings"
	"unicode"
)

// lexicalStopwords are question scaffolding and English function words. A
// natural-language query ("how does signup work") carries its intent in one or
// two content words; matching the scaffolding would rank doesNothing or
// workQueue above the signup code.
var lexicalStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "into": true,
	"that": true, "this": true, "these": true, "those": true, "are": true, "was": true,
	"were": true, "been": true, "being": true, "has": true, "have": true, "had": true,
	"does": true, "did": true, "doing": true, "can": true, "could": true, "should": true,
	"would": true, "will": true, "how": true, "what": true, "where": true, "which": true,
	"who": true, "why": true, "when": true, "there": true, "here": true, "about": true,
	"work": true, "works": true, "working": true, "happen": true, "happens": true,
	"handled": true, "implemented": true, "code": true, "find": true, "show": true,
	"all": true, "any": true, "some": true, "its": true, "our": true, "your": true,
	"you": true, "not": true, "use": true, "used": true, "uses": true,
}

// lexicalTerms reduces a query to its content words: split on anything that
// is not a letter or digit, lower-cased, de-duplicated, with stopwords and
// words shorter than three runes dropped (the trigram tokenizer cannot match
// them). Identifiers survive whole, so "signupUser" stays one substring term.
func lexicalTerms(query string) []string {
	words := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		lw := strings.ToLower(w)
		if len([]rune(lw)) < 3 || lexicalStopwords[lw] || seen[lw] {
			continue
		}
		seen[lw] = true
		out = append(out, lw)
	}
	return out
}

// SyncLexical reconciles nodes_fts with the nodes table in one transaction:
// ids whose node is gone (or was flagged by the UPDATE safety-net trigger) are
// removed, then every node above the highest still-indexed id — plus flagged
// ones — is added. That is the complete diff because node ids are
// AUTOINCREMENT and node text is never updated in place (see schema.go). A
// read-only no-op when the index is current. The indexer calls it after every
// run that changed the graph; LexicalSearch calls it when nodes are pending.
func (s *Store) SyncLexical() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	remove, err := queryIDs(tx, `SELECT i.id FROM nodes_fts_ids i
		WHERE NOT EXISTS (SELECT 1 FROM nodes n WHERE n.id = i.id)
		   OR i.id IN (SELECT id FROM nodes_fts_dirty)`)
	if err != nil {
		return err
	}
	if len(remove) > 0 {
		del, err := tx.Prepare("DELETE FROM nodes_fts WHERE rowid = ?")
		if err != nil {
			return err
		}
		defer func() { _ = del.Close() }()
		delID, err := tx.Prepare("DELETE FROM nodes_fts_ids WHERE id = ?")
		if err != nil {
			return err
		}
		defer func() { _ = delID.Close() }()
		for _, id := range remove {
			if _, err := del.Exec(id); err != nil {
				return err
			}
			if _, err := delID.Exec(id); err != nil {
				return err
			}
		}
	}

	type row struct {
		id                          int64
		symbol, fqn, path, doc, sig sql.NullString
	}
	rows, err := tx.Query(`SELECT n.id, n.symbol, n.fqn, n.file_path, n.docstring, n.signature FROM nodes n
		WHERE (n.id > (SELECT COALESCE(MAX(id), 0) FROM nodes_fts_ids) OR n.id IN (SELECT id FROM nodes_fts_dirty))
		  AND NOT EXISTS (SELECT 1 FROM nodes_fts_ids i WHERE i.id = n.id)`)
	if err != nil {
		return err
	}
	var add []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.symbol, &r.fqn, &r.path, &r.doc, &r.sig); err != nil {
			_ = rows.Close()
			return err
		}
		add = append(add, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(remove) == 0 && len(add) == 0 {
		var dirty bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes_fts_dirty)").Scan(&dirty); err != nil || !dirty {
			return err // current: commit nothing, never take the write lock
		}
	}
	if len(add) > 0 {
		ins, err := tx.Prepare("INSERT INTO nodes_fts(rowid, symbol, fqn, file_path, docstring, signature) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer func() { _ = ins.Close() }()
		insID, err := tx.Prepare("INSERT INTO nodes_fts_ids(id) VALUES (?)")
		if err != nil {
			return err
		}
		defer func() { _ = insID.Close() }()
		for _, r := range add {
			if _, err := ins.Exec(r.id, r.symbol, r.fqn, r.path, r.doc, r.sig); err != nil {
				return err
			}
			if _, err := insID.Exec(r.id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec("DELETE FROM nodes_fts_dirty"); err != nil {
		return err
	}
	return tx.Commit()
}

// lexicalPending reports, cheaply (two primary-key probes), whether nodes were
// added or flagged since the last SyncLexical. Deletions are not checked: the
// search JOIN already hides gone nodes until the next index-time sync.
func (s *Store) lexicalPending() (bool, error) {
	var pending bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM nodes WHERE id > (SELECT COALESCE(MAX(id), 0) FROM nodes_fts_ids))
		OR EXISTS(SELECT 1 FROM nodes_fts_dirty)`).Scan(&pending)
	return pending, err
}

func queryIDs(tx *sql.Tx, q string) ([]int64, error) {
	rows, err := tx.Query(q)
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

// LexicalSearch is the BM25 floor for intent queries when no embeddings
// exist: it ORs the query's content words over nodes_fts (symbol, fqn, path,
// docstring, signature — weighted in that order) and ranks definitions that
// match more distinct words first, production code before test code (the same
// default as the importance rankings), BM25 last. File nodes are excluded, like
// SearchSymbols. Unlike SearchSymbols it never requires every word to match,
// so question-shaped queries still find their subject.
func (s *Store) LexicalSearch(projectID int64, query string, limit int) ([]SymbolMatch, error) {
	if limit <= 0 {
		limit = 50
	}
	terms := lexicalTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	// Best-effort: if another process holds the write lock past the busy
	// timeout, search the slightly stale index. The JOIN on nodes below still
	// drops deleted definitions; only very recent additions are missed.
	if pending, err := s.lexicalPending(); err == nil && pending {
		_ = s.SyncLexical()
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"` // terms are letters/digits only — nothing to escape
	}
	// Over-fetch so the coverage re-rank below can promote a node matching
	// several words from beyond the first page of pure-BM25 order.
	fetch := limit * 5
	if fetch > 500 {
		fetch = 500
	}
	q := "SELECT " + nodeColsAs("n") + " FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.rowid" +
		" WHERE nodes_fts MATCH ? AND n.project_id = ? AND n.kind != ?" +
		" ORDER BY bm25(nodes_fts, 10.0, 6.0, 3.0, 2.0, 1.0) ASC, n.id ASC LIMIT ?"
	nodes, err := s.queryNodes(q, strings.Join(quoted, " OR "), projectID, KindFile, fetch)
	if err != nil {
		return nil, err
	}

	type scored struct {
		m       SymbolMatch
		covered int
		test    bool
	}
	out := make([]scored, 0, len(nodes))
	for _, n := range nodes {
		fields := [...]struct{ name, text string }{
			{"symbol", strings.ToLower(n.Symbol)},
			{"fqn", strings.ToLower(n.FQN)},
			{"path", strings.ToLower(n.FilePath)},
			{"docstring", strings.ToLower(n.Docstring)},
			{"signature", strings.ToLower(n.Signature)},
		}
		covered, best := 0, len(fields)
		for _, t := range terms {
			for fi, f := range fields {
				if strings.Contains(f.text, t) {
					covered++
					if fi < best {
						best = fi
					}
					break
				}
			}
		}
		if covered == 0 {
			continue
		}
		out = append(out, scored{m: SymbolMatch{Node: n, MatchedIn: fields[best].name}, covered: covered, test: IsTestNode(n)})
	}
	// Stable: ties keep BM25 order from SQL.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].covered != out[j].covered {
			return out[i].covered > out[j].covered
		}
		return !out[i].test && out[j].test
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

// LexicalTerms exposes the lexical floor's query tokenization (content words:
// lower-cased, stopwords and short words dropped) so other layers filter with
// exactly the words LexicalSearch would match.
func LexicalTerms(query string) []string { return lexicalTerms(query) }
