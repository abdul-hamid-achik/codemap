package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/tsscan"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// scanFake is a hermetic TS stand-in for lspsrc.Extractor that runs the REAL
// tsscan enrichment (like lspsrc.ExtractFile does) over a tiny line-based
// symbol finder, so the full pipeline — tsscan call candidates → import
// binding → file-scoped edge resolution → precise supersede — is exercised
// without a language server. It also implements extract.CallResolver with
// canned callHierarchy answers.
type scanFake struct {
	edges map[string][]extract.CallEdge
}

var fnDeclRe = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?(function|class)\s+([A-Za-z_$][\w$]*)`)

func (f *scanFake) Language() string { return "typescript" }

// ExtractFile finds top-level `function`/`class` declarations; a declaration
// ends at the first following line that is exactly "}" (or on its own line when
// it closes on the same line).
func (f *scanFake) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "typescript"}
	ls := strings.Split(string(src), "\n")
	for i, l := range ls {
		m := fnDeclRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		kind := extract.KindFunction
		if m[1] == "class" {
			kind = extract.KindClass
		}
		end := i
		if !strings.HasSuffix(strings.TrimSpace(l), "}") {
			for j := i + 1; j < len(ls); j++ {
				if ls[j] == "}" {
					end = j
					break
				}
			}
		}
		res.Symbols = append(res.Symbols, extract.Symbol{
			Name: m[2], FQN: m[2], Kind: kind, Language: "typescript",
			StartLine: i + 1, EndLine: end + 1,
		})
	}
	tsscan.Enrich(res, relPath, src)
	return res, nil
}

func (f *scanFake) CallEdges(_ context.Context, relPath string) ([]extract.CallEdge, error) {
	return f.edges[relPath], nil
}

// callEdgeSet returns every `calls` edge as "fromFile:fromFQN>toFile:toFQN|prov|weight".
func callEdgeSet(t *testing.T, g *graph.Store, pid int64) []string {
	t.Helper()
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]graph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	name := func(n graph.Node) string {
		if n.Kind == graph.KindFile {
			return n.FilePath
		}
		return n.FilePath + ":" + n.FQN
	}
	var out []string
	for _, e := range edges {
		if e.EdgeType != graph.EdgeCalls {
			continue
		}
		out = append(out, fmt.Sprintf("%s>%s|%s|%.1f", name(byID[e.SourceID]), name(byID[e.TargetID]), e.Provenance, e.Weight))
	}
	sort.Strings(out)
	return out
}

func indexScanFake(t *testing.T, dir string, fake *scanFake, opts Options) (*graph.Store, int64) {
	t.Helper()
	g, _ := newStores(t)
	pid, err := g.UpsertProject("ts-calls", dir, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(fake)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "ts-calls", dir, opts); err != nil {
		t.Fatal(err)
	}
	return g, pid
}

// TestTSCallCandidatesResolveThroughImports proves candidates land on the right
// node across files: relative, aliased (@/), workspace-package and default
// imports each bind to the symbol in the RESOLVED file — never to a same-named
// symbol elsewhere — and bare package / node: imports yield no edge.
func TestTSCallCandidatesResolveThroughImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/util.ts", "export function helper() {}\nexport function other() {}\n")
	// Same names elsewhere: must never receive an edge from app.ts / pages/p.ts.
	writeFile(t, dir, "decoy/util.ts", "export function helper() {}\nexport function other() {}\n")
	writeFile(t, dir, "lib/widget.ts", "function internalName() {}\nexport default internalName;\n")
	writeFile(t, dir, "packages/core/package.json", `{"name":"@acme/core","main":"src/index.ts"}`)
	writeFile(t, dir, "packages/core/src/index.ts", "export function coreFn() {}\n")
	writeFile(t, dir, "app.ts", strings.Join([]string{
		`import { helper as h } from "./lib/util";`,
		`import Widget from "./lib/widget";`,
		`import * as u from "./lib/util";`,
		`import { coreFn } from "@acme/core";`,
		`import { readFile } from "node:fs";`,
		`import React from "react";`,
		`export function run() {`,
		`  h();`,
		`  Widget();`,
		`  u.other();`,
		`  coreFn();`,
		`  readFile();`,
		`  React.useState();`,
		`  local();`,
		`}`,
		`function local() {}`,
		``,
	}, "\n"))
	writeFile(t, dir, "pages/p.ts", strings.Join([]string{
		`import { helper } from "@/lib/util";`,
		`export function page() {`,
		`  helper();`,
		`}`,
		``,
	}, "\n"))

	g, pid := indexScanFake(t, dir, &scanFake{}, Options{})
	got := callEdgeSet(t, g, pid)
	want := []string{
		"app.ts:run>app.ts:local|name|0.7",
		"app.ts:run>lib/util.ts:helper|name|0.7",
		"app.ts:run>lib/util.ts:other|name|0.7",
		"app.ts:run>lib/widget.ts:internalName|name|0.7",
		"app.ts:run>packages/core/src/index.ts:coreFn|name|0.7",
		"pages/p.ts:page>lib/util.ts:helper|name|0.7",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("call edges =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// local() is called inside its own file, so it is no longer an orphan; the
	// uncalled decoys still are.
	orphans, err := g.Orphans(pid, 100)
	if err != nil {
		t.Fatal(err)
	}
	orphan := map[string]bool{}
	for _, n := range orphans {
		orphan[n.FilePath+":"+n.Symbol] = true
	}
	if orphan["app.ts:local"] {
		t.Errorf("local is called in its own file and must not be an orphan: %v", orphan)
	}
	if !orphan["decoy/util.ts:helper"] {
		t.Errorf("decoy helper has no caller and should remain an orphan: %v", orphan)
	}
}

// TestTSCallCandidatesSamenameAcrossFilesStayScoped proves the caller is bound
// by (file, FQN): two files that both define `run` and call a local `helper`
// produce one edge each within their own file, not a cross-file mix.
func TestTSCallCandidatesSamenameAcrossFilesStayScoped(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.ts", "b.ts"} {
		writeFile(t, dir, f, "function helper() {}\nexport function run() {\n  helper();\n}\n")
	}
	g, pid := indexScanFake(t, dir, &scanFake{}, Options{})
	got := strings.Join(callEdgeSet(t, g, pid), "\n")
	want := strings.Join([]string{
		"a.ts:run>a.ts:helper|name|0.7",
		"b.ts:run>b.ts:helper|name|0.7",
	}, "\n")
	if got != want {
		t.Errorf("call edges =\n%s\nwant\n%s", got, want)
	}
}

// TestPreciseSupersedesTSCallCandidates proves the --precise pass replaces a
// file's name candidates with the exact callHierarchy edges, and that an EMPTY
// precise answer leaves the candidates in place.
func TestPreciseSupersedesTSCallCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.ts", "export function b() {}\n")
	writeFile(t, dir, "c.ts", "export function c() {}\n")
	writeFile(t, dir, "a.ts", strings.Join([]string{
		`import { b } from "./b";`,
		`import { c } from "./c";`,
		`export function f() {`,
		`  b();`,
		`  c();`,
		`}`,
		`b();`, // module-level call: sourced from the file node
		``,
	}, "\n"))
	writeFile(t, dir, "d.ts", strings.Join([]string{
		`import { b } from "./b";`,
		`export function g() {`,
		`  b();`,
		`}`,
		``,
	}, "\n"))

	// Name candidates only: f>b, f>c, g>b.
	g0, pid0 := indexScanFake(t, dir, &scanFake{}, Options{})
	before := callEdgeSet(t, g0, pid0)
	wantBefore := []string{
		"a.ts:f>b.ts:b|name|0.7",
		"a.ts:f>c.ts:c|name|0.7",
		"a.ts>b.ts:b|name|0.7",
		"d.ts:g>b.ts:b|name|0.7",
	}
	if strings.Join(before, "\n") != strings.Join(wantBefore, "\n") {
		t.Fatalf("pre-precise edges =\n%s\nwant\n%s", strings.Join(before, "\n"), strings.Join(wantBefore, "\n"))
	}

	// Precise: a.ts reports ONLY f→b (the c() candidate was a false positive);
	// d.ts reports nothing (empty answer).
	fake := &scanFake{edges: map[string][]extract.CallEdge{
		"a.ts": {{FromFQN: "f", FromFile: "a.ts", FromLine: 3, ToFile: "b.ts", ToLine: 1}},
	}}
	g, pid := indexScanFake(t, dir, fake, Options{Precise: true})
	got := callEdgeSet(t, g, pid)
	want := []string{
		"a.ts:f>b.ts:b|precise|1.0", // exact edge; f's name candidates are gone
		"a.ts>b.ts:b|name|0.7",      // module-level candidate: callHierarchy cannot re-emit it, so it stays
		"d.ts:g>b.ts:b|name|0.7",    // empty precise answer must not erase the candidate
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("post-precise edges =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestDefaultImportBindsToDefaultExportName proves a default import resolves to
// the symbol the imported file actually exports as default, not the local alias.
func TestDefaultImportBindsToDefaultExportName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.ts", "export default function realName() {}\n")
	writeFile(t, dir, "y.ts", "export default function other() {}\n")
	writeFile(t, dir, "use.ts", "import alias from \"./x\";\nexport function go() {\n  alias();\n}\n")
	g, pid := indexScanFake(t, dir, &scanFake{}, Options{})
	got := strings.Join(callEdgeSet(t, g, pid), "\n")
	if want := "use.ts:go>x.ts:realName|name|0.7"; got != want {
		t.Errorf("call edges = %q, want %q", got, want)
	}
}

// TestResolveImportFileRejectsPackages pins that bare package specifiers and
// node: builtins resolve to no project file (so no edge is ever written).
func TestResolveImportFileRejectsPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.ts"), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := newImportIndex(root, []fileTask{{abs: filepath.Join(root, "x.ts"), rel: "x.ts", lang: "typescript"}})
	for _, spec := range []string{"react", "node:fs", "@types/node", "lodash/fp"} {
		if got := resolveImportFile("typescript", "app.ts", spec, idx); got != "" {
			t.Errorf("resolveImportFile(%q) = %q, want no project file", spec, got)
		}
	}
	if got := resolveImportFile("typescript", "app.ts", "./x", idx); got != "x.ts" {
		t.Errorf("resolveImportFile(./x) = %q, want x.ts", got)
	}
}
