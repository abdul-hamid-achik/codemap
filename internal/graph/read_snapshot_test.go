package graph

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectStructuralSnapshotDoesNotMixConcurrentIndexGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	pid, err := reader.UpsertProject("snapshot", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AddNode(&Node{
		ProjectID: pid, FilePath: "a.go", Symbol: "GenerationA", FQN: "snapshot.GenerationA",
		Kind: KindFunction, Language: "go", StartLine: 3, EndLine: 5, SourceHash: "source-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := reader.SetFileHash(pid, "a.go", "hash-a"); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	snapshot, err := reader.projectStructuralSnapshot(pid, func() error {
		tx, err := writer.BeginTx(context.Background())
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := WipeProjectTx(tx, pid); err != nil {
			return err
		}
		if _, err := AddNodeTx(tx, &Node{
			ProjectID: pid, FilePath: "a.go", Symbol: "GenerationB", FQN: "snapshot.GenerationB",
			Kind: KindFunction, Language: "go", StartLine: 30, EndLine: 50, SourceHash: "source-b",
		}); err != nil {
			return err
		}
		if err := SetFileHashTx(tx, pid, "a.go", "hash-b"); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Symbol != "GenerationA" {
		t.Fatalf("snapshot nodes = %+v, want generation A", snapshot.Nodes)
	}
	if got := snapshot.FileHashes["a.go"]; got != "hash-a" {
		t.Fatalf("snapshot hash = %q, want generation-A hash", got)
	}

	// Prove the barrier really committed generation B while the read snapshot
	// was open; the A/A result above is therefore snapshot isolation, not a
	// writer that happened to run too late.
	live, err := reader.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].Symbol != "GenerationB" {
		t.Fatalf("live nodes = %+v, want committed generation B", live)
	}
	if got, err := reader.FileHash(pid, "a.go"); err != nil || got != "hash-b" {
		t.Fatalf("live hash = %q, err=%v, want generation-B hash", got, err)
	}
}

func TestWalkProjectStructuralIndexSnapshotDoesNotMixConcurrentGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	pid, err := reader.UpsertProject("streaming-snapshot", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AddNode(&Node{
		ProjectID: pid, FilePath: "a.go", Symbol: "GenerationA", FQN: "snapshot.GenerationA",
		Kind: KindFunction, Language: "go", StartLine: 3, EndLine: 5, SourceHash: "source-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := reader.SetFileHash(pid, "a.go", "hash-a"); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	var symbols []string
	snapshot, err := reader.walkProjectStructuralIndexSnapshot(pid, func(n Node) error {
		symbols = append(symbols, n.Symbol)
		return nil
	}, func() error {
		tx, err := writer.BeginTx(context.Background())
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := WipeProjectTx(tx, pid); err != nil {
			return err
		}
		if _, err := AddNodeTx(tx, &Node{
			ProjectID: pid, FilePath: "a.py", Symbol: "GenerationB", FQN: "snapshot.GenerationB",
			Kind: KindFunction, Language: "python", StartLine: 30, EndLine: 50, SourceHash: "source-b",
		}); err != nil {
			return err
		}
		if err := SetFileHashTx(tx, pid, "a.py", "hash-b"); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(symbols, []string{"GenerationA"}) {
		t.Fatalf("streamed symbols = %v, want generation A", symbols)
	}
	if !reflect.DeepEqual(snapshot.FileHashes, map[string]string{"a.go": "hash-a"}) {
		t.Fatalf("snapshot hashes = %v, want generation A", snapshot.FileHashes)
	}
	if !reflect.DeepEqual(snapshot.Languages, map[string]bool{"go": true}) {
		t.Fatalf("snapshot languages = %v, want generation A", snapshot.Languages)
	}

	live, err := reader.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].Symbol != "GenerationB" {
		t.Fatalf("live nodes = %+v, want committed generation B", live)
	}
	if got, err := reader.FileHash(pid, "a.py"); err != nil || got != "hash-b" {
		t.Fatalf("live hash = %q, err=%v, want generation B", got, err)
	}
}

func TestProjectArchitectureDoesNotMixConcurrentGraphGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	pid, err := reader.UpsertProject("architecture-snapshot", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}

	a, err := reader.AddNode(&Node{
		ProjectID: pid, FilePath: "a/a.go", Symbol: "A", FQN: "a.A",
		Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 2, SourceHash: "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := reader.AddNode(&Node{
		ProjectID: pid, FilePath: "b/b.go", Symbol: "B", FQN: "b.B",
		Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 2, SourceHash: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AddEdgeProv(a, b, EdgeCalls, WeightLSP, ProvPrecise); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	rep, err := reader.projectArchitecture(pid, TopologyOptions{}, func() error {
		tx, err := writer.BeginTx(context.Background())
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := WipeProjectTx(tx, pid); err != nil {
			return err
		}
		xID, err := AddNodeTx(tx, &Node{
			ProjectID: pid, FilePath: "x/x.go", Symbol: "X", FQN: "x.X",
			Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 2, SourceHash: "x",
		})
		if err != nil {
			return err
		}
		yID, err := AddNodeTx(tx, &Node{
			ProjectID: pid, FilePath: "y/y.go", Symbol: "Y", FQN: "y.Y",
			Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 2, SourceHash: "y",
		})
		if err != nil {
			return err
		}
		if _, err := AddEdgeProvTx(tx, xID, yID, EdgeCalls, WeightLSP, ProvName); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}

	if rep.SubsystemsTotal != 2 || rep.BridgesTotal != 1 {
		t.Fatalf("architecture snapshot mixed generations: %+v", rep)
	}
	bridge := topologyBridge(t, rep, "a", "b", EdgeCalls)
	if bridge.Count != 1 || bridge.Provenance[ProvPrecise] != 1 {
		t.Fatalf("generation-A bridge = %+v", bridge)
	}

	live, err := reader.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 || live[0].Symbol != "X" || live[1].Symbol != "Y" {
		t.Fatalf("live nodes = %+v, want committed generation B", live)
	}
}
