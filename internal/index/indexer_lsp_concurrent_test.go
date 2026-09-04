package index

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
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

// degradedExtractor stands in for an lspsrc.Extractor whose parse-wait breaker
// tripped mid-run. noteDegradedServers matches on the behavioral interface, not
// the concrete type, so the indexer package can assert the reporting contract
// without spawning a real language server.
type degradedExtractor struct {
	lang   string
	down   bool
	binary string
}

func (d degradedExtractor) Language() string { return d.lang }
func (d degradedExtractor) ExtractFile(string, []byte) (*extract.FileResult, error) {
	return &extract.FileResult{}, nil
}
func (d degradedExtractor) Degraded() (bool, string) { return d.down, d.binary }

// A server that went quiet must reach the caller as ONE issue naming every
// language it served — never one per language, and never as a missing server
// (the binary was found and did index files, so calling its languages
// unsupported would contradict the nodes already in the graph).
func TestNoteDegradedServersDedupesByBinary(t *testing.T) {
	res := &Result{}
	noteDegradedServers(res, map[string]extract.Extractor{
		"typescript": degradedExtractor{lang: "typescript", down: true, binary: "typescript-language-server"},
		"javascript": degradedExtractor{lang: "javascript", down: true, binary: "typescript-language-server"},
		"python":     degradedExtractor{lang: "python", down: false, binary: "pyright-langserver"},
		"go":         stubHealthyExtractor{},
	})
	if len(res.ServerIssues) != 1 {
		t.Fatalf("ServerIssues = %d, want 1 (deduped by binary)", len(res.ServerIssues))
	}
	iss := res.ServerIssues[0]
	if iss.Code != tooling.CodeStoppedResponding || iss.Binary != "typescript-language-server" {
		t.Fatalf("issue = %+v, want code %q for typescript-language-server", iss, tooling.CodeStoppedResponding)
	}
	if len(iss.Languages) != 2 || iss.Languages[0] != "javascript" || iss.Languages[1] != "typescript" {
		t.Fatalf("Languages = %v, want [javascript typescript] (sorted union)", iss.Languages)
	}
	if len(res.MissingServers) != 0 {
		t.Fatalf("MissingServers = %v, want empty — the binary was present and did index files", res.MissingServers)
	}
}

func TestNoteDegradedServersSilentWhenHealthy(t *testing.T) {
	res := &Result{}
	noteDegradedServers(res, map[string]extract.Extractor{
		"typescript": degradedExtractor{lang: "typescript", down: false, binary: "typescript-language-server"},
		"go":         stubHealthyExtractor{},
	})
	if len(res.ServerIssues) != 0 {
		t.Fatalf("ServerIssues = %v, want none", res.ServerIssues)
	}
}

// stubHealthyExtractor has no Degraded method at all — the common case (every
// cheap pure-Go backend), which must be skipped rather than panic.
type stubHealthyExtractor struct{}

func (stubHealthyExtractor) Language() string { return "go" }
func (stubHealthyExtractor) ExtractFile(string, []byte) (*extract.FileResult, error) {
	return &extract.FileResult{}, nil
}
