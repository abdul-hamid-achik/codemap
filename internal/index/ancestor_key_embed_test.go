package index

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// TestAncestorKeyEmbedSkips pins the declarative-key embed dedup: a YAML key's
// source spans its whole subtree, so ancestors duplicate their leaves' text and
// structural names ("responses", "200") outrank real code on lexical matches
// (observed: OpenAPI YAML flooding the top-10 of a "durability" query). Only
// the leaf-most key per span is embedded.
func TestAncestorKeyEmbedSkips(t *testing.T) {
	syms := []extract.Symbol{
		{Name: "Commit", Kind: extract.KindFunction, StartLine: 10, EndLine: 20},
		{Name: "openapi", Kind: extract.KindKey, StartLine: 1, EndLine: 30},      // root: contains everything
		{Name: "info", Kind: extract.KindKey, StartLine: 3, EndLine: 5},          // ancestor
		{Name: "title", Kind: extract.KindKey, StartLine: 4, EndLine: 4},         // leaf
		{Name: "responses", Kind: extract.KindKey, StartLine: 12, EndLine: 18},   // ancestor
		{Name: "'200'", Kind: extract.KindKey, StartLine: 13, EndLine: 17},       // ancestor (nested)
		{Name: "description", Kind: extract.KindKey, StartLine: 14, EndLine: 14}, // leaf
		{Name: "twinA", Kind: extract.KindKey, StartLine: 25, EndLine: 26},       // equal twins: keep first
		{Name: "twinB", Kind: extract.KindKey, StartLine: 25, EndLine: 26},
	}
	skip := ancestorKeyEmbedSkips(syms)
	wantSkip := map[string]bool{
		"openapi":     true,
		"info":        true,
		"responses":   true,
		"'200'":       true,
		"twinA":       false,
		"twinB":       true,
		"Commit":      false, // never a key
		"title":       false, // leaf
		"description": false, // leaf
	}
	// Symbols with identical names (twinA/twinB distinguished by order).
	names := []string{"Commit", "openapi", "info", "title", "responses", "'200'", "description", "twinA", "twinB"}
	for i, name := range names {
		if skip[i] != wantSkip[name] {
			t.Errorf("skip[%d] (%s) = %v, want %v", i, name, skip[i], wantSkip[name])
		}
	}
}
