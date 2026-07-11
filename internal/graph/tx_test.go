package graph

import (
	"context"
	"fmt"
	"testing"
)

// TestSynchronousNormal verifies the DSN sets synchronous=NORMAL (the WAL-safe
// fast mode) rather than the SQLite default FULL. This is a one-line DSN change
// with the biggest write-speed impact.
func TestSynchronousNormal(t *testing.T) {
	s := openTest(t)
	var sync string
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	// SQLite: 0=OFF, 1=NORMAL, 2=FULL, 3=EXTRA. NORMAL (1) is the WAL-safe fast mode.
	if sync != "1" && sync != "NORMAL" {
		t.Errorf("synchronous = %q, want 1 (NORMAL)", sync)
	}
}

// TestWALMode verifies WAL journal mode is active.
func TestWALMode(t *testing.T) {
	s := openTest(t)
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want 'wal'", mode)
	}
}

// TestBeginTxCommit verifies that a transaction commits all writes atomically.
func TestBeginTxCommit(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := AddNodeTx(tx, &Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddEdgeProvTx(tx, id, id, EdgeCalls, WeightLSP, ProvName); err != nil {
		t.Fatal(err)
	}
	if err := SetFileHashTx(tx, pid, "a.go", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := MarkCallGraphResolvedTx(tx, pid, "a.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// All writes should be visible after commit.
	node, err := s.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if node.Symbol != "A" {
		t.Errorf("node symbol = %q, want A", node.Symbol)
	}
	h, _ := s.FileHash(pid, "a.go")
	if h != "abc" {
		t.Errorf("file hash = %q, want abc", h)
	}
	resolved, _ := s.CallGraphResolvedFiles(pid)
	if !resolved["a.go"] {
		t.Error("transaction did not commit call-graph coverage")
	}
}

// TestBeginTxRollback verifies that a rolled-back transaction leaves no writes.
func TestBeginTxRollback(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id, err := AddNodeTx(tx, &Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetFileHashTx(tx, pid, "a.go", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := MarkCallGraphResolvedTx(tx, pid, "a.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()

	// Nothing should be visible after rollback.
	_, err = s.GetNode(id)
	if err == nil {
		t.Error("node should not exist after rollback")
	}
	h, _ := s.FileHash(pid, "a.go")
	if h != "" {
		t.Errorf("file hash = %q, want empty after rollback", h)
	}
	resolved, _ := s.CallGraphResolvedFiles(pid)
	if resolved["a.go"] {
		t.Error("rolled-back call-graph coverage should not be visible")
	}
}

// TestDeleteNodesInFileTx verifies transactional node deletion.
func TestDeleteNodesInFileTx(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	a, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	b, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "B", Kind: KindFunction, Language: "go", SourceHash: "h"})
	_, _ = s.AddNode(&Node{ProjectID: pid, FilePath: "b.go", Symbol: "C", Kind: KindFunction, Language: "go", SourceHash: "h"})
	if _, err := s.AddEdge(a, b, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}

	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteNodesInFileTx(tx, pid, "a.go"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Nodes in a.go should be gone; b.go's node should remain.
	nodes, _ := s.NodesInFile(pid, "a.go")
	if len(nodes) != 0 {
		t.Errorf("a.go nodes after delete = %d, want 0", len(nodes))
	}
	nodes, _ = s.NodesInFile(pid, "b.go")
	if len(nodes) != 1 {
		t.Errorf("b.go nodes = %d, want 1 (unaffected)", len(nodes))
	}
}

func TestWipeProjectTxClearsCallGraphCoverage(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	if err := s.MarkCallGraphResolved(pid, "leaf.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := WipeProjectTx(tx, pid); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Fatalf("transactional wipe retained coverage: %v", resolved)
	}
}

// TestUpdateNodeVecIDTx verifies the transactional vec_id update.
func TestUpdateNodeVecIDTx(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	id, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})

	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateNodeVecIDTx(tx, id, "vec123"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	node, _ := s.GetNode(id)
	if node.VecID != "vec123" {
		t.Errorf("vec_id = %q, want vec123", node.VecID)
	}
}

// TestWipeProjectTx verifies transactional project wipe.
func TestWipeProjectTx(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	_, _ = s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	_ = s.SetFileHash(pid, "a.go", "abc")

	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := WipeProjectTx(tx, pid); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	nodes, _ := s.ProjectNodes(pid)
	if len(nodes) != 0 {
		t.Errorf("nodes after wipe = %d, want 0", len(nodes))
	}
	files, _ := s.IndexedFiles(pid)
	if len(files) != 0 {
		t.Errorf("index_state after wipe = %d, want 0", len(files))
	}
}

// TestAddAnnotationTx verifies transactional annotation insert.
func TestAddAnnotationTx(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")

	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id, err := AddAnnotationTx(tx, pid, Annotation{Kind: AnnotationNode, Target: "p.Run", Note: "test note"})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("annotation id should be non-zero after commit")
	}

	anns, _ := s.AllAnnotations(pid)
	if len(anns) != 1 || anns[0].Note != "test note" {
		t.Errorf("annotation after commit = %+v, want 1 with note 'test note'", anns)
	}
}

// TestPreparedStatements verifies that the hot-path statements are prepared
// after Open and can be used for repeated inserts without error.
func TestPreparedStatements(t *testing.T) {
	s := openTest(t)
	if s.stmtAddNode == nil {
		t.Error("stmtAddNode is nil after Open")
	}
	if s.stmtAddEdge == nil {
		t.Error("stmtAddEdge is nil after Open")
	}
	if s.stmtSetFileHash == nil {
		t.Error("stmtSetFileHash is nil after Open")
	}
	if s.stmtDeleteNodesInFile == nil {
		t.Error("stmtDeleteNodesInFile is nil after Open")
	}
	if s.stmtUpdateVecID == nil {
		t.Error("stmtUpdateVecID is nil after Open")
	}

	pid, _ := s.UpsertProject("p", "/p", "go")

	// Use the prepared AddNode path repeatedly.
	for i := 0; i < 10; i++ {
		_, err := s.AddNode(&Node{
			ProjectID: pid, FilePath: "a.go", Symbol: fmt.Sprintf("S%d", i),
			Kind: KindFunction, Language: "go", SourceHash: "h",
		})
		if err != nil {
			t.Fatalf("prepared AddNode[%d]: %v", i, err)
		}
	}
	nodes, _ := s.ProjectNodes(pid)
	if len(nodes) != 10 {
		t.Errorf("after 10 prepared inserts, nodes = %d, want 10", len(nodes))
	}

	// Prepared DeleteNodesInFile.
	if err := s.DeleteNodesInFile(pid, "a.go"); err != nil {
		t.Fatal(err)
	}
	nodes, _ = s.ProjectNodes(pid)
	if len(nodes) != 0 {
		t.Errorf("after prepared delete, nodes = %d, want 0", len(nodes))
	}

	// Prepared SetFileHash.
	if err := s.SetFileHash(pid, "a.go", "abc"); err != nil {
		t.Fatal(err)
	}
	h, _ := s.FileHash(pid, "a.go")
	if h != "abc" {
		t.Errorf("SetFileHash via prepared stmt: got %q, want abc", h)
	}

	// Prepared UpdateNodeVecID.
	id, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "V", Kind: KindFunction, Language: "go", SourceHash: "h"})
	if err := s.UpdateNodeVecID(id, "vec42"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNode(id)
	if n.VecID != "vec42" {
		t.Errorf("UpdateNodeVecID via prepared stmt: got %q, want vec42", n.VecID)
	}
}
