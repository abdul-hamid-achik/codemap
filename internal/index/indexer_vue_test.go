package index

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
)

// vueFixture mirrors internal/extract/vuesrc's test fixture: a <template>
// referencing `count`, a <script setup lang="ts"> block with a function and a
// composable call, and a <style> block. Line numbers below are pinned against
// this exact text.
const vueFixture = `<template>
  <div>{{ count }}</div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const count = ref(0)

function increment() {
  count.value++
}
</script>

<style scoped>
div { color: red; }
</style>
`

// TestIndexVueSymbols proves a .vue file's <script setup> block is indexed
// into the same node/edge model every other language uses, with symbol lines
// mapped back onto the ORIGINAL .vue file (not the extracted fragment).
// Server-gated: skips where typescript-language-server isn't on PATH.
func TestIndexVueSymbols(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "Counter.vue", vueFixture)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("vue", dir, "vue")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "vue", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes == 0 {
		t.Fatal("expected Vue nodes, got 0 (is typescript resolvable in this env?)")
	}
	if res.Languages["vue"] == 0 {
		t.Errorf("expected Languages[vue] > 0, got %v", res.Languages)
	}
	if res.Unsupported["vue"] != 0 {
		t.Errorf("vue should no longer be Unsupported, got %v", res.Unsupported)
	}

	nodes, err := g.FindNodesBySymbol(pid, "increment")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("increment symbol not indexed")
	}
	inc := nodes[0]
	if inc.FilePath != "Counter.vue" {
		t.Errorf("increment FilePath = %q, want Counter.vue (not a synthetic fragment path)", inc.FilePath)
	}
	if inc.StartLine != 10 {
		t.Errorf("increment StartLine = %d, want 10 (original .vue file line, not fragment-relative)", inc.StartLine)
	}
	if inc.Language != "vue" {
		t.Errorf("increment Language = %q, want vue", inc.Language)
	}

	cntNodes, err := g.FindNodesBySymbol(pid, "count")
	if err != nil {
		t.Fatal(err)
	}
	if len(cntNodes) == 0 {
		t.Fatal("count symbol not indexed")
	}
	if cntNodes[0].StartLine != 8 {
		t.Errorf("count StartLine = %d, want 8 (original .vue file line)", cntNodes[0].StartLine)
	}
}

// TestIndexVueOnlyProjectSpawnsServer proves a project made ENTIRELY of .vue
// files (no plain .ts/.js siblings) still spawns typescript-language-server —
// the initial walk sees zero real .ts/.js files, so the ordinary
// DefaultServers loop alone would never spawn it; registerVue must do so
// itself. Server-gated.
func TestIndexVueOnlyProjectSpawnsServer(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "Counter.vue", vueFixture)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("vue-only", dir, "vue")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "vue-only", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "increment"); len(ns) == 0 {
		t.Errorf("expected increment indexed in a vue-only project, res=%+v", res)
	}
}

// TestIndexVueDisabledByNoLSP confirms --no-lsp keeps .vue unindexed
// deterministically (it never spawns a server), same contract as TypeScript.
func TestIndexVueDisabledByNoLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Counter.vue", vueFixture)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("vue", dir, "vue")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "vue", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("NoLSP should index no Vue nodes, got %d", res.Nodes)
	}
	if res.Unsupported["vue"] != 1 {
		t.Errorf("expected 1 unsupported vue file under NoLSP, got %v", res.Unsupported)
	}
}

// TestIndexVueMissingServerReported proves a project with .vue files but no
// resolvable typescript-language-server reports MissingServers["vue"] instead
// of silently producing an empty graph — mirrors
// TestMissingServerReportedNotSilent for TypeScript. Deterministic regardless
// of what's installed locally: it points DefaultServers at a binary that
// cannot resolve on PATH.
func TestIndexVueMissingServerReported(t *testing.T) {
	saved := lspsrc.DefaultServers
	t.Cleanup(func() { lspsrc.DefaultServers = saved })
	lspsrc.DefaultServers = []lspsrc.ServerSpec{{
		Cmd:   "codemap-no-such-language-server",
		Args:  []string{"--stdio"},
		Langs: []lspsrc.LangBinding{{Lang: "typescript", LangID: "typescript"}, {Lang: "javascript", LangID: "javascript"}},
	}}

	dir := t.TempDir()
	writeFile(t, dir, "Counter.vue", vueFixture)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("vue", dir, "vue")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "vue", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("server absent: expected no Vue nodes indexed, got %d", res.Nodes)
	}
	if res.MissingServers["vue"] == "" {
		t.Errorf("server absent: expected MissingServers[vue] to be set, got %v", res.MissingServers)
	}
	if res.Unsupported["vue"] != 1 {
		t.Errorf("expected 1 unsupported vue file, got %v", res.Unsupported)
	}
}

// TestIndexVueJSFallback proves a project with real .js files (which registers
// only the "javascript" binding, not "typescript") still correctly indexes a
// <script setup lang="ts"> block in a sibling .vue file — registerVue must
// bind "typescript" onto the SAME already-spawned server connection rather
// than leaving TS-flavored blocks stuck on the JS delegate. Server-gated.
func TestIndexVueJSFallback(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "plain.js", "export function plain() { return 1; }\n")
	writeFile(t, dir, "Counter.vue", vueFixture)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("mixed", dir, "javascript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "mixed", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["javascript"] == 0 {
		t.Errorf("expected plain.js indexed, got languages %v", res.Languages)
	}
	if ns, _ := g.FindNodesBySymbol(pid, "increment"); len(ns) == 0 {
		t.Errorf("expected increment (TS setup block) indexed alongside a JS-only real project, res=%+v", res)
	}
}
