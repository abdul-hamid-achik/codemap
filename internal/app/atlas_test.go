package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// atlasFixture indexes a small Go project with a README, package docs, a
// license-headed file, tests, markdown docs and mutual recursion (a call cycle).
func atlasFixture(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/atlas\n\ngo 1.25\n",
		"README.md": "# Atlas Demo\n\n[![CI](https://x/y.svg)](https://x/y)\n\n" +
			"A **tiny** demo with [links](https://example.com) and `code`.\nIt spans two lines.\n\n## Install\n\nrun it\n",
		"cmd/demo/main.go": "/* Copyright © 2026 someone */\n\npackage main\n\nimport \"example.com/atlas/internal/auth\"\n\nfunc main() { auth.Login() }\n",
		"internal/auth/auth.go": "// Copyright 2026 Someone. All rights reserved.\n// Licensed under MIT.\n\n" +
			"// Package auth validates credentials.\n//\n// It is the only place that touches sessions.\npackage auth\n\n" +
			"import \"example.com/atlas/internal/db\"\n\n// Login signs a user in.\nfunc Login() { db.Query(); ping() }\n\nfunc ping() { pong() }\n\nfunc pong() { ping() }\n",
		"internal/auth/auth_test.go": "package auth\n\nimport \"testing\"\n\nfunc TestLogin(t *testing.T) { Login() }\n",
		"internal/db/db.go":          "package db\n\n// Query runs a query.\nfunc Query() {}\n",
		"docs/guide.md":              "# Guide\n\nHow to use the demo.\n",
		"testdata/sample/x.go":       "package sample\n\nfunc X() {}\n",
	}
	for name, source := range files {
		p := filepath.Join(proj, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func atlasChild(n *AtlasNode, name string) *AtlasNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestAtlasTreeSummariesRolesAndKeySymbols(t *testing.T) {
	svc, proj := atlasFixture(t)
	rep, err := svc.Atlas(proj, AtlasOptions{Depth: 3, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || !rep.Indexed || rep.Tree == nil || rep.Truncated {
		t.Fatalf("report identity = %+v", rep)
	}
	if rep.CallGraph != CallGraphName {
		t.Errorf("call_graph = %q", rep.CallGraph)
	}
	if rep.Stale {
		t.Error("freshly indexed project reported stale")
	}
	if rep.PartialErrors == nil {
		t.Error("partial_errors must serialize as [] not null")
	}
	if want := "A tiny demo with links and code. It spans two lines."; rep.Summary != want || rep.SummarySource != "README.md" {
		t.Errorf("project summary = %q (%s), want %q", rep.Summary, rep.SummarySource, want)
	}

	root := rep.Tree
	if root.Path != "" || root.Type != "dir" || root.Name == "" {
		t.Fatalf("root = %+v", root)
	}
	// Root closes over the whole project: nothing crosses its boundary.
	if root.Inbound != 0 || root.Outbound != 0 || root.Internal == 0 {
		t.Errorf("root edges in=%d out=%d internal=%d", root.Inbound, root.Outbound, root.Internal)
	}
	if rep.Totals.Files != root.Files || rep.Totals.Symbols != root.Symbols {
		t.Errorf("totals %+v != root %d/%d", rep.Totals, root.Files, root.Symbols)
	}

	auth := atlasChild(atlasChild(root, "internal"), "auth")
	if auth == nil {
		t.Fatalf("internal/auth missing: %+v", root.Children)
	}
	if auth.Summary != "Package auth validates credentials." || auth.SummarySource != "package doc" {
		t.Errorf("auth summary = %q (%s)", auth.Summary, auth.SummarySource)
	}
	if auth.TestFiles != 1 || auth.Tests != 1 || auth.Files != 2 {
		t.Errorf("auth counts = files %d test_files %d tests %d", auth.Files, auth.TestFiles, auth.Tests)
	}
	if !reflect.DeepEqual(auth.Roles, []string{"source", "tests"}) && !reflect.DeepEqual(auth.Roles, []string{"source"}) {
		t.Errorf("auth roles = %v", auth.Roles)
	}
	// auth is called by cmd/demo and calls db: both directions are non-zero and
	// the neighbours name the other subsystems.
	if auth.Inbound == 0 || auth.Outbound == 0 || auth.Internal == 0 {
		t.Errorf("auth edges in=%d out=%d internal=%d", auth.Inbound, auth.Outbound, auth.Internal)
	}
	if auth.TopNeighbors == nil || len(auth.TopNeighbors.Out) == 0 || auth.TopNeighbors.Out[0].Path != "internal/db" {
		t.Errorf("auth out neighbours = %+v", auth.TopNeighbors)
	}
	if len(auth.TopNeighbors.In) == 0 || auth.TopNeighbors.In[0].Path != "cmd/demo" {
		t.Errorf("auth in neighbours = %+v", auth.TopNeighbors)
	}
	if len(auth.KeySymbols) == 0 || auth.KeySymbols[0].Symbol != "Login" || auth.KeySymbols[0].Selector == nil {
		t.Fatalf("auth key symbols = %+v", auth.KeySymbols)
	}
	if auth.KeySymbols[0].Doc != "Login signs a user in." {
		t.Errorf("Login doc = %q", auth.KeySymbols[0].Doc)
	}
	for _, ks := range auth.KeySymbols {
		if strings.Contains(ks.File, "_test.go") || ks.Kind == graph.KindTest {
			t.Errorf("test symbol leaked into key symbols: %+v", ks)
		}
	}

	// File leaves: the license header is skipped and the package doc is used.
	file := atlasChild(auth, "auth.go")
	if file == nil || file.Type != "file" || file.Language != "go" || file.Lines == 0 {
		t.Fatalf("auth.go = %+v", file)
	}
	if file.Summary != "Package auth validates credentials." || file.SummarySource != "package doc" {
		t.Errorf("auth.go summary = %q (%s)", file.Summary, file.SummarySource)
	}
	testFile := atlasChild(auth, "auth_test.go")
	if testFile == nil || !reflect.DeepEqual(testFile.Roles, []string{"tests"}) {
		t.Errorf("auth_test.go roles = %+v", testFile)
	}
	// main.go has only a copyright header: no summary, never the license text.
	mainFile := atlasChild(atlasChild(atlasChild(root, "cmd"), "demo"), "main.go")
	if mainFile == nil || strings.Contains(strings.ToLower(mainFile.Summary), "copyright") {
		t.Errorf("main.go summary leaked license: %+v", mainFile)
	}

	demo := atlasChild(atlasChild(root, "cmd"), "demo")
	if demo.Entrypoints != 1 || !containsString(demo.Roles, "entrypoint") {
		t.Errorf("cmd/demo entrypoints=%d roles=%v", demo.Entrypoints, demo.Roles)
	}
	docs := atlasChild(root, "docs")
	if docs == nil || !containsString(docs.Roles, "docs") {
		t.Fatalf("docs node = %+v", docs)
	}
	if guide := atlasChild(docs, "guide.md"); guide == nil || guide.Summary != "How to use the demo." || guide.SummarySource != "markdown" {
		t.Errorf("guide.md = %+v", guide)
	}
	td := atlasChild(root, "testdata")
	if td == nil || !containsString(td.Roles, "tests") || containsString(td.Roles, "source") {
		t.Errorf("testdata node = %+v", td)
	}
}

func TestAtlasEdgeClassificationConserves(t *testing.T) {
	svc, proj := atlasFixture(t)
	rep, err := svc.Atlas(proj, AtlasOptions{Depth: 4, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	// For every node, every non-defines edge touching it is exactly one of
	// internal / inbound / outbound, so a parent's in+out never exceeds the sum
	// of its children's in+out+...; and siblings' outbound >= edges leaving the parent.
	var walk func(n *AtlasNode)
	walk = func(n *AtlasNode) {
		if len(n.Children) > 0 && n.ChildrenTotal == len(n.Children) {
			childOut, childIn := 0, 0
			for _, c := range n.Children {
				childOut += c.Outbound
				childIn += c.Inbound
			}
			if n.Outbound > childOut || n.Inbound > childIn {
				t.Errorf("%s: parent in/out (%d/%d) exceeds children sum (%d/%d)", n.Path, n.Inbound, n.Outbound, childIn, childOut)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(rep.Tree)
}

func TestAtlasBoundsAndTruncation(t *testing.T) {
	svc, proj := atlasFixture(t)

	shallow, err := svc.Atlas(proj, AtlasOptions{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	internal := atlasChild(shallow.Tree, "internal")
	if internal == nil || len(internal.Children) != 0 || internal.CollapsedDirs != 2 {
		t.Errorf("depth 1 internal = %+v", internal)
	}
	if shallow.NodesEmitted != len(shallow.Tree.Children)+1 {
		t.Errorf("nodes_emitted = %d", shallow.NodesEmitted)
	}

	capped, err := svc.Atlas(proj, AtlasOptions{Depth: 3, Files: true, MaxNodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Truncated || capped.NodesEmitted != 4 || !capped.Tree.ChildrenTrunc || capped.Tree.ChildrenTotal <= len(capped.Tree.Children) {
		t.Errorf("capped = emitted %d truncated %v root %+v", capped.NodesEmitted, capped.Truncated, capped.Tree)
	}
	// The kept children are the largest by symbols.
	for i := 1; i < len(capped.Tree.Children); i++ {
		a, b := capped.Tree.Children[i-1], capped.Tree.Children[i]
		if a.Type == b.Type && a.Symbols < b.Symbols {
			t.Errorf("children not ordered by symbols: %s(%d) before %s(%d)", a.Name, a.Symbols, b.Name, b.Symbols)
		}
	}

	limited, err := svc.Atlas(proj, AtlasOptions{Depth: 3, Files: true, KeySymbols: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(limited.Tree.KeySymbols); n != 1 {
		t.Errorf("key_symbols = %d, want 1", n)
	}
}

func TestAtlasPrefixAndErrors(t *testing.T) {
	svc, proj := atlasFixture(t)
	zoom, err := svc.Atlas(proj, AtlasOptions{Prefix: "internal/auth/", Depth: 1, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	if zoom.Prefix != "internal/auth" || zoom.Tree.Path != "internal/auth" || zoom.Tree.Name != "auth" {
		t.Errorf("zoom = %q %+v", zoom.Prefix, zoom.Tree)
	}
	if zoom.Totals.Files != 2 {
		t.Errorf("zoom totals = %+v", zoom.Totals)
	}
	if zoom.Summary == "" || zoom.SummarySource != "README.md" {
		t.Errorf("project summary should stay the root README: %q (%s)", zoom.Summary, zoom.SummarySource)
	}

	for _, prefix := range []string{"nope", "internal/auth/auth.go"} {
		_, err = svc.Atlas(proj, AtlasOptions{Prefix: prefix})
		if err == nil || CodeOf(err) != "not_found" {
			t.Errorf("prefix %q: err = %v code %q, want not_found", prefix, err, CodeOf(err))
		}
	}
	for _, opts := range []AtlasOptions{{Depth: 9}, {Depth: -1}, {MaxNodes: 20001}, {KeySymbols: 21}, {Prefix: "../x"}} {
		if _, err = svc.Atlas(proj, opts); err == nil || CodeOf(err) != CodeInvalidInput {
			t.Errorf("opts %+v: err = %v code %q, want invalid_input", opts, err, CodeOf(err))
		}
	}
}

func TestAtlasDeterministicAndJSONShape(t *testing.T) {
	svc, proj := atlasFixture(t)
	a, err := svc.Atlas(proj, AtlasOptions{Depth: 4, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Atlas(proj, AtlasOptions{Depth: 4, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("atlas output is not deterministic")
	}
	if strings.Contains(string(ja), ":null") {
		t.Errorf("atlas JSON contains null (empty slices/maps must be [] / {}): %s", ja)
	}
}

func TestAtlasNotIndexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	rep, err := NewService(sess).Atlas(t.TempDir(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Tree != nil || rep.CallGraph != CallGraphNone || rep.PartialErrors == nil {
		t.Errorf("not-indexed report = %+v", rep)
	}
}

// TestAtlasRankSymbolsPenalisesAmbiguousNames pins the key-symbol heuristic on a
// synthetic graph: a name shared by several definitions has its name-based
// in-degree divided by the number of definitions, test callers do not count, and
// the owning type is credited with its methods' callers.
func TestAtlasRankSymbolsPenalisesAmbiguousNames(t *testing.T) {
	file := func(id int64, p string) graph.Node {
		return graph.Node{ID: id, FilePath: p, Symbol: p, FQN: p, Kind: graph.KindFile, Language: "go"}
	}
	fn := func(id int64, p, sym, fqn, kind string) graph.Node {
		return graph.Node{ID: id, FilePath: p, Symbol: sym, FQN: fqn, Kind: kind, Language: "go", StartLine: int(id)}
	}
	nodes := []graph.Node{
		file(1, "a/a.go"), file(2, "b/b.go"), file(3, "c/c.go"), file(4, "c/c_test.go"),
		fn(10, "a/a.go", "Close", "a.Closer.Close", graph.KindMethod),
		fn(11, "b/b.go", "Close", "b.Closer.Close", graph.KindMethod),
		fn(12, "b/b.go", "Run", "b.Run", graph.KindFunction),
		fn(13, "c/c.go", "Caller", "c.Caller", graph.KindFunction),
		fn(14, "c/c_test.go", "TestRun", "c.TestRun", graph.KindTest),
		fn(15, "a/a.go", "Closer", "a.Closer", graph.KindType),
	}
	nodes[4].Docstring = ""
	var edges []graph.Edge
	add := func(src, tgt int64, prov string) {
		edges = append(edges, graph.Edge{ID: int64(len(edges) + 1), SourceID: src, TargetID: tgt, EdgeType: graph.EdgeCalls, Provenance: prov})
	}
	// Close (2 defs) is hit 4x by name-based edges; Run (1 def) twice, plus a test call.
	for i := 0; i < 2; i++ {
		add(13, 10, graph.ProvName)
		add(13, 11, graph.ProvName)
	}
	add(13, 12, graph.ProvName)
	add(13, 12, graph.ProvName)
	for i := 0; i < 5; i++ {
		add(14, 12, graph.ProvName) // test callers must not count
	}
	nodeFile := map[int64]*atlasAgg{}
	files := map[string]*atlasAgg{}
	for _, n := range nodes {
		if nodeFile[n.ID] = files[n.FilePath]; nodeFile[n.ID] == nil {
			files[n.FilePath] = &atlasAgg{path: n.FilePath}
			nodeFile[n.ID] = files[n.FilePath]
		}
	}
	atlasRankSymbols(nodes, edges, nodeFile, 5)

	cands := func(p string) []atlasCand {
		for _, a := range nodeFile {
			if a.path == p && len(a.cands) > 0 {
				return a.cands
			}
		}
		return nil
	}
	run := cands("b/b.go")
	if len(run) == 0 || run[0].node.Symbol != "Run" || run[0].raw != 2 {
		t.Fatalf("b/b.go ranking = %+v", run)
	}
	var closeB atlasCand
	for _, c := range run {
		if c.node.FQN == "b.Closer.Close" {
			closeB = c
		}
	}
	if closeB.shared != 2 || closeB.score >= run[0].score {
		t.Errorf("ambiguous Close should rank below Run: close=%+v run=%+v", closeB, run[0])
	}
	// The owning type is credited with its methods' callers (raw) and a damped score.
	a := cands("a/a.go")
	if len(a) != 2 || a[1].node.Symbol != "Closer" || a[1].raw != 2 || a[1].score <= 0 {
		t.Errorf("a/a.go ranking = %+v", a)
	}
	if c := cands("c/c_test.go"); c != nil {
		t.Errorf("test file produced candidates: %+v", c)
	}
}

func TestAtlasSummaryExtraction(t *testing.T) {
	cases := []struct {
		name, rel, lang, content, want, src string
	}{
		{"markdown skips badges and H1", "a.md", "markdown",
			"# Title\n\n[![b](x)](y)\n![img](z)\n\n> A tagline with [a link](http://x).\n", "A tagline with a link.", "markdown"},
		{"markdown h1 fallback", "a.md", "markdown", "# Only Title\n\n- a\n- b\n", "Only Title", "markdown"},
		{"markdown skips tables, code and html", "a.md", "markdown",
			"# T\n<p align=\"center\"><img src=\"x\"></p>\n\n| a | b |\n|---|---|\n\n```\ncode\n```\n\nReal <b>prose</b> here.\n", "Real prose here.", "markdown"},
		{"python docstring", "m.py", "python", "#!/usr/bin/env python\n\"\"\"Do the thing.\n\nMore detail.\n\"\"\"\nimport os\n", "Do the thing.", "module docstring"},
		{"python license then none", "m.py", "python", "# Copyright 2026 X\nimport os\n", "", ""},
		{"js block comment after license", "a.js", "javascript",
			"#!/usr/bin/env node\n/* Copyright 2026 */\n/**\n * Tiny DOM builder.\n * Every view uses it.\n */\nexport const h = 1\n", "Tiny DOM builder. Every view uses it.", "file comment"},
		{"js line comments", "a.mjs", "javascript", "// Settings store.\n// Under userData.\n\nexport const x = 1\n", "Settings store. Under userData.", "file comment"},
		{"lua comments", "a.lua", "lua", "-- Handy helpers\nlocal M = {}\n", "Handy helpers", "file comment"},
		{"ruby magic comment", "a.rb", "ruby", "# frozen_string_literal: true\n\n# Parses things.\nclass A; end\n", "Parses things.", "file comment"},
		{"yaml comment", "a.yml", "yaml", "# Release workflow.\nname: x\n", "Release workflow.", "file comment"},
		{"css comment", "a.css", "css", "/* Design tokens. */\n:root{}\n", "Design tokens.", "file comment"},
		{"go file doc after license", "a.go", "go", "/* Copyright 2026 */\n\n// Package a does a.\npackage a\n", "Package a does a.", "package doc"},
		{"go license only", "a.go", "go", "// Copyright 2026 X\n// SPDX-License-Identifier: MIT\npackage a\n", "", ""},
		{"go detached file comment", "a.go", "go", "// Helpers for parsing.\n\npackage a\n", "Package a does a.", "package doc"},
	}
	// The last case documents the fallback: a non-license comment above package
	// that is not attached still counts as a file comment.
	cases[len(cases)-1].want, cases[len(cases)-1].src = "Helpers for parsing.", "file comment"
	for _, tc := range cases {
		got := summarizeFileContent(tc.rel, tc.lang, tc.content)
		if got.text != tc.want || got.source != tc.src {
			t.Errorf("%s: got %q (%s), want %q (%s)", tc.name, got.text, got.source, tc.want, tc.src)
		}
	}
}

func TestAtlasSummaryBoundsAndCleaning(t *testing.T) {
	long := strings.Repeat("This is a sentence that keeps going. ", 20)
	got := cleanSummary(long)
	if n := len([]rune(got)); n > atlasSummaryMax || !strings.HasSuffix(got, ".") {
		t.Errorf("long summary len=%d %q", n, got)
	}
	words := cleanSummary(strings.Repeat("word ", 100))
	if n := len([]rune(words)); n > atlasSummaryMax+1 || !strings.HasSuffix(words, "…") {
		t.Errorf("no-boundary summary len=%d %q", n, words)
	}
	if got := cleanSummary("## **Bold** `code` <b>tag</b> [txt](http://u) ![img](x)"); got != "Bold code tag txt" {
		t.Errorf("cleanSummary = %q", got)
	}
	if got := firstSentence("First one. Second one.", 160); got != "First one." {
		t.Errorf("firstSentence = %q", got)
	}
}

func TestAtlasSummarizerSkipsBinaryAndMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bin.go"), []byte("// doc\x00\x01\npackage x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSummarizer(dir)
	if r := s.fileSummary("bin.go", "go"); r.text != "" {
		t.Errorf("binary file summarised: %+v", r)
	}
	if r := s.fileSummary("missing.go", "go"); r.text != "" {
		t.Errorf("missing file summarised: %+v", r)
	}
	if r := s.dirSummary("nodir", []string{"nodir/x.go"}); r.text != "" {
		t.Errorf("missing dir summarised: %+v", r)
	}
}

func TestAtlasSummaryManifestAndWrapperFallbacks(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("web/package.json", `{"name":"web","description":"The dashboard front end.","version":"1.0.0"}`)
	write("py/pyproject.toml", "[project]\nname = \"py\"\ndescription = \"Batch jobs for the warehouse.\"\n")
	write("big/package.json", `{"name":"big","description":"Truncated \"quoted\" manifest.","deps":{`+strings.Repeat(`"x":"1",`, 4000))
	write("cmd/tool/main.go", "// Command tool prints things.\npackage main\n\nfunc main() {}\n")

	s := newSummarizer(dir)
	for rel, want := range map[string]summaryResult{
		"web": {text: "The dashboard front end.", source: "package.json"},
		"py":  {text: "Batch jobs for the warehouse.", source: "pyproject.toml"},
		"big": {text: `Truncated "quoted" manifest.`, source: "package.json"},
	} {
		if got := s.dirSummary(rel, nil); got != want {
			t.Errorf("dirSummary(%s) = %+v, want %+v", rel, got, want)
		}
	}

	tool := &atlasAgg{path: "cmd/tool", name: "tool", isDir: true}
	tool.files = []*atlasAgg{{path: "cmd/tool/main.go", name: "main.go", language: "go"}}
	wrapper := &atlasAgg{path: "cmd", name: "cmd", isDir: true, dirs: []*atlasAgg{tool}}
	got := atlasNodeSummary(s, wrapper)
	if got.text != "Command tool prints things." || got.source != "subdirectory tool/" {
		t.Errorf("wrapper dir summary = %+v, want the single sub-directory's doc", got)
	}
	wrapper.files = []*atlasAgg{{path: "cmd/README.txt", name: "README.txt"}}
	if got := atlasNodeSummary(s, wrapper); got.text != "" {
		t.Errorf("a directory with its own files must not borrow a child's summary: %+v", got)
	}
}
