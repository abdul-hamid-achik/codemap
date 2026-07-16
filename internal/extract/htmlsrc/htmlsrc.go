// Package htmlsrc extracts styling references from HTML with a pure-Go
// regex scanner: every static token in a class="…" attribute becomes a
// RefStyles reference to the selector name `.token` (and id="…" to `#token`),
// resolved by the indexer's existing name-resolution pass against the CSS
// backends' selector nodes. The file emits no symbols of its own — references
// hang off the file node (From is the file path, which the resolver keys file
// nodes by), exactly like other file-scope references.
//
// v1 deliberately does not parse <style> blocks or follow <link href>
// stylesheets; the first follow-up is delegating <style> contents to
// csssrc.ScanRules with a line offset. Template placeholders ({{x}}, {%x%},
// <%x%>, ${x}) are dynamic, not static class names — skipped.
package htmlsrc

import (
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the pure-Go HTML backend.
type Extractor struct{}

// New returns an HTML extractor.
func New() *Extractor { return &Extractor{} }

// Language implements extract.Extractor.
func (*Extractor) Language() string { return "html" }

// class="a b" / class='a b' / id="x" — attribute values only; unquoted
// attribute values are legal HTML but vanishingly rare for class/id. The
// leading (?:^|[^-\w]) guard (Go regexp has no lookbehind) keeps hyphenated
// attributes like data-id="…"/data-class="…" from matching — `\b` alone
// treats the `-` as a boundary and reads them as id=/class=.
var classAttrRe = regexp.MustCompile(`(?i)(?:^|[^-\w])(class|id)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// templateMarkers flag dynamic tokens from server/client template dialects.
var templateMarkers = []string{"{{", "{%", "<%", "${", "}}", "%}", "%>"}

// ExtractFile implements extract.Extractor.
func (*Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "html"}
	text := stripHTMLComments(src)
	lineOf := lineOffsets(text)

	seen := map[string]bool{}
	for _, m := range classAttrRe.FindAllStringSubmatchIndex(text, -1) {
		attr := strings.ToLower(text[m[2]:m[3]])
		var value string
		if m[4] >= 0 {
			value = text[m[4]:m[5]]
		} else {
			value = text[m[6]:m[7]]
		}
		line := lineOf(m[2]) // start of the attribute name, not the guard char
		sigil := "."
		if attr == "id" {
			sigil = "#"
		}
		for _, tok := range strings.Fields(value) {
			if isTemplateToken(tok) {
				continue
			}
			target := sigil + tok
			if seen[target] {
				continue
			}
			seen[target] = true
			res.References = append(res.References, extract.Reference{
				From: relPath,
				To:   target,
				Kind: extract.RefStyles,
				Line: line,
				// Qualified: the defining stylesheet is another file; name
				// matching may over-match → candidate weight.
				Qualified: true,
			})
		}
	}
	return res, nil
}

func isTemplateToken(tok string) bool {
	for _, marker := range templateMarkers {
		if strings.Contains(tok, marker) {
			return true
		}
	}
	return false
}

// stripHTMLComments blanks <!-- --> spans (newlines kept so line numbers
// survive). An unterminated comment blanks to EOF — commented-out markup must
// never produce references.
func stripHTMLComments(src []byte) string {
	out := make([]byte, len(src))
	copy(out, src)
	for i := 0; i < len(out); i++ {
		if out[i] != '<' || i+3 >= len(out) || string(out[i:i+4]) != "<!--" {
			continue
		}
		for ; i < len(out); i++ {
			closed := out[i] == '-' && i+2 < len(out) && out[i+1] == '-' && out[i+2] == '>'
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
			if closed {
				out[i+1] = ' '
				out[i+2] = ' '
				i += 2
				break
			}
		}
	}
	return string(out)
}

// lineOffsets returns a function mapping a byte offset to a 1-based line.
func lineOffsets(text string) func(int) int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1
	}
}
