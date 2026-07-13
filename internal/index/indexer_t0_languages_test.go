package index

import (
	"context"
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestRecognitionOnlyLanguagesStayT0 proves the language roadmap does not
// accidentally advertise or implement structural support. Files are recognized
// and counted as unsupported, but no extractor/server is registered and no graph
// nodes are created.
func TestRecognitionOnlyLanguagesStayT0(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"src/main.rs":       "rust",
		"src/App.java":      "java",
		"src/App.kt":        "kotlin",
		"src/App.scala":     "scala",
		"src/native.c":      "c",
		"src/native.cpp":    "cpp",
		"src/kernel.cu":     "cuda",
		"src/App.cs":        "csharp",
		"src/App.vb":        "visualbasic",
		"web/index.php":     "php",
		"lib/main.dart":     "dart",
		"Sources/App.swift": "swift",
		"lib/app.ex":        "elixir",
		"web/App.svelte":    "svelte",
		"web/App.astro":     "astro",
		"web/View.cshtml":   "razor",
		"scripts/build.sh":  "shell",
		"infra/main.hcl":    "hcl",
		"infra/main.tf":     "terraform",
		"db/query.sql":      "sql",
		"config/app.yaml":   "yaml",
	}
	wantUnsupported := make(map[string]int)
	for path, lang := range files {
		writeFile(t, dir, path, "T0 recognition fixture\n")
		wantUnsupported[lang]++
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("polyglot-t0", dir, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "polyglot-t0", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(res.Unsupported, wantUnsupported) {
		t.Errorf("Unsupported = %v, want %v", res.Unsupported, wantUnsupported)
	}
	if res.FilesScanned != 0 || res.FilesIndexed != 0 || res.Nodes != 0 || res.Edges != 0 {
		t.Errorf("T0 files must not be indexed: %+v", res)
	}
	if len(res.Languages) != 0 {
		t.Errorf("Languages = %v, want no indexed languages", res.Languages)
	}
	if len(res.MissingServers) != 0 {
		t.Errorf("MissingServers = %v, want none for T0 recognition", res.MissingServers)
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("project has %d nodes, want 0 for T0-only files", len(nodes))
	}
}
