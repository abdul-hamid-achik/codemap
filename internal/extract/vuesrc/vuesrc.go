// Package vuesrc extracts structural symbols from Vue Single-File Components
// (.vue). An SFC interleaves <template>, <script> (or <script setup>), and
// <style> blocks in one file; the actual code — functions, composables,
// reactive state — lives in the script block(s), written in TypeScript or
// JavaScript. Rather than reimplement a TS/JS parser, vuesrc locates each
// <script>/<script setup> block, slices out its content (dropping the
// surrounding markup), and hands that fragment to the ALREADY-REGISTERED
// TypeScript or JavaScript extractor — the same lspsrc.Extractor instance
// (backed by typescript-language-server) that indexes .ts/.js files elsewhere
// in the project. Symbol positions the delegate reports (fragment-relative,
// 1-based from the top of the fragment) are then shifted by the block's
// offset within the original .vue file, so codemap_source/codemap_symbol_at/
// etc. resolve to the real file:line.
//
// Not covered by this package (by design — see BACKLOG.md "Vue / SFC support"):
//   - <template> expression bindings are not modeled as call/reference edges —
//     only the script block's own top-level declarations are indexed.
//   - <style>/<style scoped> blocks are ignored entirely.
//   - A <script> block with no lang attribute defaults to JavaScript, matching
//     Vue's own SFC compiler default.
package vuesrc

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

var _ extract.Extractor = (*Extractor)(nil)

// scriptBlockRe matches a <script ...>...</script> block, case-insensitive,
// content spanning newlines. Vue SFCs require <template>/<script>/<style> to be
// top-level siblings (never nested in one another), so a non-HTML-aware regex
// scan is enough for the common case; a script-like string deep inside
// <template> markup (e.g. a literal code sample) could false-positive — a known,
// documented limitation rather than a full HTML/SFC parse.
//
// The attribute capture is quote-aware so Vue 3.3+ `generic="T extends Record<string, unknown>"
// does not terminate early at the `>` inside the attribute value.
var scriptBlockRe = regexp.MustCompile(`(?is)<script((?:[^>"']|"[^"]*"|'[^']*')*)>(.*?)</script\s*>`)

var (
	setupAttrRe = regexp.MustCompile(`(?i)(^|\s)setup(\s|=|$)`)
	langAttrRe  = regexp.MustCompile(`(?i)lang\s*=\s*["']([^"']+)["']`)
)

// Extractor extracts symbols from a .vue file's script block(s) by delegating
// to the project's already-registered TypeScript and/or JavaScript extractors.
// Either delegate may be nil (e.g. a project with .vue files but no plain .ts
// files still binds "typescript" for script-setup blocks — see
// Indexer.registerVue — but a caller could construct this with just one);
// ExtractFile falls back to whichever delegate is available.
type Extractor struct {
	ts extract.Extractor // language "typescript", or nil
	js extract.Extractor // language "javascript", or nil
}

// New returns a vue extractor that delegates script-block parsing to ts and js.
// At least one should be non-nil for ExtractFile to produce any symbols; a
// .vue file with neither available still returns an empty (not erroring)
// FileResult, same as one with no script block at all.
func New(ts, js extract.Extractor) *Extractor {
	return &Extractor{ts: ts, js: js}
}

// Language implements extract.Extractor.
func (e *Extractor) Language() string { return "vue" }

// scriptBlock is one <script>/<script setup> block located in a .vue file.
type scriptBlock struct {
	setup     bool
	lang      string // raw lang attribute value ("ts", "js", "" = unspecified)
	content   []byte
	startLine int // 1-based line of content's first byte in the original file
}

// parseScriptBlocks scans src for top-level <script>/<script setup> tags and
// returns each block's content plus its 1-based starting line within src.
func parseScriptBlocks(src []byte) []scriptBlock {
	var blocks []scriptBlock
	for _, m := range scriptBlockRe.FindAllSubmatchIndex(src, -1) {
		// m indices: [0]=matchStart [1]=matchEnd [2]=attrsStart [3]=attrsEnd
		// [4]=contentStart [5]=contentEnd (per regexp/FindSubmatchIndex).
		attrs := string(src[m[2]:m[3]])
		content := src[m[4]:m[5]]
		blocks = append(blocks, scriptBlock{
			setup:     setupAttrRe.MatchString(attrs),
			lang:      langAttr(attrs),
			content:   content,
			startLine: 1 + countNewlines(src[:m[4]]),
		})
	}
	return blocks
}

func langAttr(attrs string) string {
	if m := langAttrRe.FindStringSubmatch(attrs); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

func countNewlines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// delegateFor picks the extractor + synthetic file extension for a script
// block's lang attribute. An unmarked <script>/<script setup> defaults to
// plain JavaScript (matching Vue's own SFC compiler); "ts"/"tsx" route to
// TypeScript. Falls back to whichever delegate IS registered if the preferred
// one isn't (a shared typescript-language-server connection normally has both
// — see Indexer.registerVue).
func (e *Extractor) delegateFor(lang string) (delegate extract.Extractor, ext string) {
	wantTS := strings.Contains(lang, "ts") // covers "ts" and "tsx"
	if wantTS {
		if e.ts != nil {
			return e.ts, ".ts"
		}
		return e.js, ".js"
	}
	if e.js != nil {
		return e.js, ".js"
	}
	return e.ts, ".ts"
}

// ExtractFile implements extract.Extractor. It locates each <script>/<script
// setup> block in src, extracts its content, feeds it to the TS/JS delegate
// under a synthetic sibling path (same directory as relPath, so relative
// imports still resolve), then remaps the returned symbols'/references' line
// numbers back onto the original .vue file. A .vue file with no script block
// (template/style only) is not an error: it yields an empty FileResult, same
// as an import-only TS file.
func (e *Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "vue"}
	for i, b := range parseScriptBlocks(src) {
		delegate, ext := e.delegateFor(b.lang)
		if delegate == nil {
			continue // no TS/JS extractor registered at all — nothing to delegate to
		}
		kind := "script"
		if b.setup {
			kind = "setup"
		}
		synthPath := fmt.Sprintf("%s.__vue_%d_%s%s", relPath, i, kind, ext)
		fr, err := delegate.ExtractFile(synthPath, b.content)
		if err != nil {
			return nil, fmt.Errorf("vue %s block: %w", kind, err)
		}
		mergeShifted(res, fr, b.startLine)
	}
	return res, nil
}

// mergeShifted appends fr's symbols/references/imports into res, shifting
// every line number by offset-1 (fr's lines are 1-based within the extracted
// fragment; offset is that fragment's first line within the original file) and
// stamping Language "vue" so vue-sourced symbols are attributed to the file's
// own language, not the delegate's — matching every other extractor's
// invariant that a symbol's Language equals the language of the file it was
// found in.
func mergeShifted(res *extract.FileResult, fr *extract.FileResult, offset int) {
	shift := offset - 1
	for _, s := range fr.Symbols {
		s.Language = "vue"
		s.StartLine += shift
		s.EndLine += shift
		res.Symbols = append(res.Symbols, s)
	}
	for _, r := range fr.References {
		r.Line += shift
		res.References = append(res.References, r)
	}
	res.Imports = append(res.Imports, fr.Imports...)
}
