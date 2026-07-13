package index

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

type duplicateFQNCallResolver struct{}

func (duplicateFQNCallResolver) Language() string { return "typescript" }

func (duplicateFQNCallResolver) ExtractFile(path string, src []byte) (*extract.FileResult, error) {
	name := "run"
	if filepath.Base(path) == "target-a.ts" || filepath.Base(path) == "target-b.ts" {
		name = "target"
	}
	return &extract.FileResult{
		Path: path, Language: "typescript",
		Symbols: []extract.Symbol{{
			Name: name, FQN: name, Kind: extract.KindFunction,
			Language: "typescript", StartLine: 1, EndLine: 1, Source: string(src),
		}},
	}, nil
}

func (duplicateFQNCallResolver) CallEdges(_ context.Context, path string) ([]extract.CallEdge, error) {
	switch path {
	case "a.ts":
		return []extract.CallEdge{{
			FromFQN: "run", FromFile: "a.ts", FromLine: 1,
			ToFile: "target-a.ts", ToLine: 1,
		}}, nil
	case "b.ts":
		return []extract.CallEdge{{
			FromFQN: "run", FromFile: "b.ts", FromLine: 1,
			ToFile: "target-b.ts", ToLine: 1,
		}}, nil
	default:
		return nil, nil
	}
}

func TestLSPPreciseEdgesJoinBothEndsByPosition(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.ts", "export function run() {}\n")
	writeFile(t, dir, "b.ts", "export function run() {}\n")
	writeFile(t, dir, "target-a.ts", "export function target() {}\n")
	writeFile(t, dir, "target-b.ts", "export function target() {}\n")

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("duplicates", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(duplicateFQNCallResolver{})
	if _, err := ix.IndexProject(context.Background(), pid, "duplicates", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	rows, err := g.DB().Query(`
SELECT source.file_path, target.file_path
FROM edges e
JOIN nodes source ON source.id = e.source_id
JOIN nodes target ON target.id = e.target_id
WHERE source.project_id = ? AND e.edge_type = ? AND e.provenance = ?
ORDER BY source.file_path, target.file_path`, pid, graph.EdgeCalls, graph.ProvPrecise)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatal(err)
		}
		got = append(got, pair)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"a.ts", "target-a.ts"}, {"b.ts", "target-b.ts"}}
	if len(got) != len(want) {
		t.Fatalf("precise edges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("precise edges = %v, want %v", got, want)
		}
	}
}
