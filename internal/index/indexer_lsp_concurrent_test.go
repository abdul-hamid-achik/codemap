package index

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestIndexTypeScriptSequentialNoDroppedFiles indexes a multi-file TypeScript
// project through the serial LSP pass and asserts every file's symbols land.
// Concurrent DidOpen+documentSymbol against one stdio language-server connection
// deadlocks on large monorepos, so extraction is capped at one in-flight request;
// this test guards against silently losing symbols when files are closed after
// each extract. Server-gated (skips without typescript-language-server).
func TestIndexTypeScriptSequentialNoDroppedFiles(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	// 10 files with a cross-file import chain — more than the default worker
	// pool (4) — so the concurrent path is genuinely exercised.
	const n = 10
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("mod%d", i)
		want = append(want, "fn"+name)
		var src string
		if i == 0 {
			src = fmt.Sprintf("export function fn%s() { return %d; }\n", name, i)
		} else {
			src = fmt.Sprintf("import { fnmod%d } from \"./mod%d\";\nexport function fn%s() { return fnmod%d(); }\n", i-1, i-1, name, i-1)
		}
		writeFile(t, dir, name+".ts", src)
	}

	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes == 0 {
		t.Fatal("expected TypeScript nodes, got 0 (is typescript resolvable in this env?)")
	}
	// Every file's function must be indexed — a file dropped to the parseWait
	// race under concurrency would leave its symbol absent.
	for _, sym := range want {
		nodes, err := g.FindNodesBySymbol(pid, sym)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) == 0 {
			t.Errorf("symbol %q not indexed — a file was dropped under serial LSP extraction", sym)
		}
	}
	if res.FilesIndexed != n {
		t.Errorf("FilesIndexed = %d, want %d (a file was dropped or skipped)", res.FilesIndexed, n)
	}
}
