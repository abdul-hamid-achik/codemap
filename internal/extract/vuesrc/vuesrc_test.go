package vuesrc

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
)

// fixture is a small real-world-shaped SFC: a <template> that references
// `count`, a <script setup lang="ts"> block with a function and a composable
// call, and a <style> block — used to pin exact line numbers below, so any
// edit to this constant must be re-counted against the assertions.
const fixture = `<template>
  <div>{{ count }}</div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useCounter } from './useCounter'

const count = ref(0)

function increment() {
  count.value++
}

const { double } = useCounter(count)
</script>

<style scoped>
div { color: red; }
</style>
`

// TestParseScriptBlocksOffsets pins the byte-scanning + line-offset math
// against the fixture above without touching any delegate/LSP: line 5 is
// "<script setup lang="ts">", so the block's content starts at line 5 (the
// byte right after the opening tag, still on that line).
func TestParseScriptBlocksOffsets(t *testing.T) {
	blocks := parseScriptBlocks([]byte(fixture))
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	b := blocks[0]
	if !b.setup {
		t.Error("expected setup=true")
	}
	if b.lang != "ts" {
		t.Errorf("lang = %q, want ts", b.lang)
	}
	if b.startLine != 5 {
		t.Errorf("startLine = %d, want 5", b.startLine)
	}
	if !strings.Contains(string(b.content), "function increment") {
		t.Errorf("content missing expected script text: %q", b.content)
	}
	if strings.Contains(string(b.content), "<template>") || strings.Contains(string(b.content), "<style") {
		t.Errorf("content leaked markup outside the script block: %q", b.content)
	}
}

// TestParseScriptBlocksTwoBlocks covers the combined <script> + <script
// setup> pattern (options-API export default alongside setup bindings), which
// real Vue SFCs use to declare `name`/`inheritAttrs` outside setup.
func TestParseScriptBlocksTwoBlocks(t *testing.T) {
	src := `<script lang="ts">
export default {
  name: 'Widget',
}
</script>
<script setup lang="ts">
function onClick() {}
</script>
`
	blocks := parseScriptBlocks([]byte(src))
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0].setup {
		t.Error("block 0 should not be setup")
	}
	if !blocks[1].setup {
		t.Error("block 1 should be setup")
	}
}

// TestParseScriptBlocksNone covers a template/style-only SFC — a component
// with no script at all is valid Vue and must not be treated as an error.
func TestParseScriptBlocksNone(t *testing.T) {
	src := "<template><div>static</div></template>\n<style>div{color:red}</style>\n"
	if blocks := parseScriptBlocks([]byte(src)); len(blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(blocks))
	}
}

// TestParseScriptBlocksDefaultLangJS covers a bare <script setup> (no lang
// attribute), which Vue's own compiler treats as plain JavaScript.
func TestParseScriptBlocksDefaultLangJS(t *testing.T) {
	src := "<script setup>\nconst x = 1\n</script>\n"
	blocks := parseScriptBlocks([]byte(src))
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].lang != "" {
		t.Errorf("lang = %q, want empty (defaults to JS downstream)", blocks[0].lang)
	}
}

// TestParseScriptBlocksGenericAttribute covers Vue 3.3+ <script setup generic="...">
// where the attribute value itself contains a `>`. The parser must not terminate the
// opening tag at that inner `>` and must capture the full script content.
func TestParseScriptBlocksGenericAttribute(t *testing.T) {
	src := `<template><div>x</div></template>
<script setup lang="ts" generic="T extends Record<string, unknown>">
const x = 1
function handler(v: T) { return v }
</script>
`
	blocks := parseScriptBlocks([]byte(src))
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if !blocks[0].setup {
		t.Error("expected setup=true")
	}
	if blocks[0].lang != "ts" {
		t.Errorf("lang = %q, want ts", blocks[0].lang)
	}
	body := string(blocks[0].content)
	if !strings.Contains(body, "const x = 1") || !strings.Contains(body, "function handler") {
		t.Errorf("content missing expected script text: %q", body)
	}
	if strings.Contains(body, "unknown>") {
		t.Errorf("content leaked the trailing `>` of the generic attribute: %q", body)
	}
}

// stubExtractor is a fake extract.Extractor that returns a canned FileResult
// and records the relPath/content it was called with, so ExtractFile's
// delegation + line-shift logic can be tested without a real language server.
type stubExtractor struct {
	lang       string
	gotRelPath string
	gotSrc     string
	result     *extract.FileResult
	err        error
}

func (s *stubExtractor) Language() string { return s.lang }

func (s *stubExtractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	s.gotRelPath = relPath
	s.gotSrc = string(src)
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// TestExtractFileShiftsLines proves ExtractFile remaps a delegate's
// fragment-relative symbol lines back onto the original .vue file, using the
// fixture's real line numbers: the delegate sees a fragment starting at
// content line 1 == original line 5, and reports "increment" at fragment line
// 7 (0-based line 6 from an LSP-shaped symbol) — 1-based fragment line 7 must
// land on original line 11 ("function increment() {").
func TestExtractFileShiftsLines(t *testing.T) {
	ts := &stubExtractor{
		lang: "typescript",
		result: &extract.FileResult{
			Symbols: []extract.Symbol{
				{Name: "increment", FQN: "increment", Kind: extract.KindFunction, Language: "typescript", StartLine: 7, EndLine: 9},
				{Name: "count", FQN: "count", Kind: extract.KindVariable, Language: "typescript", StartLine: 5, EndLine: 5},
			},
			References: []extract.Reference{
				{From: "increment", To: "count", Kind: extract.RefReferences, Line: 8},
			},
		},
	}
	e := New(ts, nil)
	fr, err := e.ExtractFile("components/Counter.vue", []byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if fr.Language != "vue" {
		t.Errorf("Language = %q, want vue", fr.Language)
	}

	// The delegate must have been called with content confined to the script
	// block (no <template>/<style> markup) and a synthetic sibling path in the
	// SAME directory (so relative imports would still resolve).
	if strings.Contains(ts.gotSrc, "<template>") {
		t.Errorf("delegate received template markup: %q", ts.gotSrc)
	}
	if !strings.HasPrefix(ts.gotRelPath, "components/Counter.vue") || !strings.HasSuffix(ts.gotRelPath, ".ts") {
		t.Errorf("synthetic relPath = %q, want components/Counter.vue-prefixed .ts sibling", ts.gotRelPath)
	}

	byName := map[string]extract.Symbol{}
	for _, s := range fr.Symbols {
		byName[s.Name] = s
	}
	inc, ok := byName["increment"]
	if !ok {
		t.Fatal("increment symbol missing")
	}
	if inc.StartLine != 11 {
		t.Errorf("increment StartLine = %d, want 11 (original file line)", inc.StartLine)
	}
	if inc.EndLine != 13 {
		t.Errorf("increment EndLine = %d, want 13", inc.EndLine)
	}
	if inc.Language != "vue" {
		t.Errorf("increment Language = %q, want vue (not the delegate's typescript)", inc.Language)
	}
	cnt, ok := byName["count"]
	if !ok {
		t.Fatal("count symbol missing")
	}
	if cnt.StartLine != 9 {
		t.Errorf("count StartLine = %d, want 9 (original file line)", cnt.StartLine)
	}
	if len(fr.References) != 1 || fr.References[0].Line != 12 {
		t.Errorf("reference line = %+v, want shifted to 12", fr.References)
	}
}

// TestExtractFileNoScriptBlock proves a template/style-only .vue file yields
// an empty (not erroring) result — it should be treated like an import-only
// TS file, not an unparseable one.
func TestExtractFileNoScriptBlock(t *testing.T) {
	e := New(&stubExtractor{lang: "typescript"}, &stubExtractor{lang: "javascript"})
	src := "<template><div>static</div></template>\n"
	fr, err := e.ExtractFile("Static.vue", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Symbols) != 0 {
		t.Errorf("expected no symbols, got %+v", fr.Symbols)
	}
}

// TestExtractFileNoDelegate proves a .vue file is still handled cleanly (no
// panic, no error) when NEITHER a TS nor a JS extractor was registered —
// e.g. --no-lsp, or typescript-language-server missing. It should behave like
// an unsupported file's worth of content: zero symbols, no crash.
func TestExtractFileNoDelegate(t *testing.T) {
	e := New(nil, nil)
	fr, err := e.ExtractFile("Widget.vue", []byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Symbols) != 0 {
		t.Errorf("expected no symbols with no delegate, got %+v", fr.Symbols)
	}
}

// TestExtractFileTwoBlocksMerge proves ExtractFile correctly merges symbols
// from TWO script blocks (the combined <script> + <script setup> pattern) with
// INDEPENDENT line offsets — the per-block shift is applied to each block's
// delegate result, not a single global offset. The fixture below is:
//
//	line 1: <script lang="ts">        (block 0 content starts at line 1)
//	line 2: export default { name: 'W' }
//	line 3: </script>
//	line 4: <script setup lang="ts">   (block 1 content starts at line 4)
//	line 5: function onClick() {}
//	line 6: </script>
//
// A delegate reporting a symbol at fragment line 2 in block 0 must land on
// original line 2 (shift 0), and the same fragment-line-2 symbol in block 1
// must land on original line 5 (shift 3) — verifying the loop calls
// mergeShifted once per block with that block's own startLine.
func TestExtractFileTwoBlocksMerge(t *testing.T) {
	ts := &stubExtractor{
		lang: "typescript",
		result: &extract.FileResult{
			Symbols: []extract.Symbol{
				{Name: "decl", FQN: "decl", Kind: extract.KindVariable, Language: "typescript", StartLine: 2, EndLine: 2},
			},
		},
	}
	e := New(ts, nil)
	src := `<script lang="ts">
export default { name: 'W' }
</script>
<script setup lang="ts">
function onClick() {}
</script>
`
	fr, err := e.ExtractFile("Widget.vue", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Symbols) != 2 {
		t.Fatalf("symbols = %d, want 2 (one per block)", len(fr.Symbols))
	}
	if fr.Symbols[0].StartLine != 2 {
		t.Errorf("block 0 decl StartLine = %d, want 2", fr.Symbols[0].StartLine)
	}
	if fr.Symbols[1].StartLine != 5 {
		t.Errorf("block 1 decl StartLine = %d, want 5", fr.Symbols[1].StartLine)
	}
	if fr.Symbols[0].Language != "vue" || fr.Symbols[1].Language != "vue" {
		t.Errorf("both symbols must be stamped vue, got %q/%q", fr.Symbols[0].Language, fr.Symbols[1].Language)
	}
}

// TestDelegateForLangSelectionAndFallback covers routing a "ts" block to the
// TypeScript delegate and everything else to JavaScript, plus falling back to
// whichever single delegate is available.
func TestDelegateForLangSelectionAndFallback(t *testing.T) {
	ts := &stubExtractor{lang: "typescript"}
	js := &stubExtractor{lang: "javascript"}

	e := New(ts, js)
	if d, ext := e.delegateFor("ts"); d != ts || ext != ".ts" {
		t.Errorf("lang=ts routed to %v/%s, want ts/.ts", d, ext)
	}
	if d, ext := e.delegateFor(""); d != js || ext != ".js" {
		t.Errorf("lang=(empty) routed to %v/%s, want js/.js", d, ext)
	}
	if d, ext := e.delegateFor("js"); d != js || ext != ".js" {
		t.Errorf("lang=js routed to %v/%s, want js/.js", d, ext)
	}

	tsOnly := New(ts, nil)
	if d, _ := tsOnly.delegateFor(""); d != ts {
		t.Errorf("js block with no JS delegate should fall back to ts, got %v", d)
	}
	jsOnly := New(nil, js)
	if d, _ := jsOnly.delegateFor("ts"); d != js {
		t.Errorf("ts block with no TS delegate should fall back to js, got %v", d)
	}
}

// TestLanguage confirms the extractor identifies as "vue" (what the indexer
// keys extractors by).
func TestLanguage(t *testing.T) {
	if got := New(nil, nil).Language(); got != "vue" {
		t.Errorf("Language() = %q, want vue", got)
	}
}

// TestExtractFileRealTypeScriptServer is an end-to-end check against a real
// typescript-language-server: it proves the delegate the indexer actually uses
// in production (lspsrc.Extractor) can parse a synthetic-path fragment and
// that the resulting symbols land on the correct ORIGINAL .vue line numbers.
// Server-gated: skips where typescript-language-server isn't on PATH.
func TestExtractFileRealTypeScriptServer(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ts, err := lspsrc.New(ctx, "typescript", "typescript", dir, "typescript-language-server", "--stdio")
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	js := ts.Bind("javascript", "javascript")

	e := New(ts, js)
	fr, err := e.ExtractFile("Counter.vue", []byte(fixture))
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]extract.Symbol{}
	for _, s := range fr.Symbols {
		byName[s.Name] = s
	}
	inc, ok := byName["increment"]
	if !ok {
		t.Fatalf("increment symbol not extracted, got %+v", fr.Symbols)
	}
	if inc.Kind != extract.KindFunction {
		t.Errorf("increment kind = %q, want function", inc.Kind)
	}
	if inc.StartLine != 11 {
		t.Errorf("increment StartLine = %d, want 11 (original .vue file line)", inc.StartLine)
	}
	if inc.Language != "vue" {
		t.Errorf("increment Language = %q, want vue", inc.Language)
	}
	cnt, ok := byName["count"]
	if !ok {
		t.Fatalf("count symbol not extracted, got %+v", fr.Symbols)
	}
	if cnt.StartLine != 9 {
		t.Errorf("count StartLine = %d, want 9 (original .vue file line)", cnt.StartLine)
	}
}
