package extract

import "testing"

// TestLanguageForPath pins extension → language id, including the markup
// languages that are recognized-but-unsupported (so they're reported as
// "planned" rather than silently ignored) and truly unknown extensions ("").
func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"pkg/a.go":   "go",
		"b.ts":       "typescript",
		"c.tsx":      "typescript",
		"d.js":       "javascript",
		"e.jsx":      "javascript",
		"f.mjs":      "javascript",
		"g.py":       "python",
		"h.lua":      "lua",
		"i.rb":       "ruby",
		"App.vue":    "vue",
		"index.html": "html",
		"x.htm":      "html",
		"style.css":  "css",
		"README.md":  "",
		"data.json":  "",
		"noext":      "",
	}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}
