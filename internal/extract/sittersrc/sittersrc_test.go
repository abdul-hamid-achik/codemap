package sittersrc

import (
	"strings"
	"testing"
)

// TestGrammarSubsetLoadsEveryGrammar parses one file per grammar. Run with the
// release build tags (task test does) it proves the grammar_subset_* tags in
// Taskfile/.goreleaser/stage-binary still embed everything sittersrc needs.
func TestGrammarSubsetLoadsEveryGrammar(t *testing.T) {
	cases := []struct{ lang, path, src, want string }{
		{"typescript", "a.ts", "export function tsFn(): void {}\n", "tsFn"},
		{"typescript", "a.tsx", "export const Tsx = () => <div />;\n", "Tsx"},
		{"javascript", "a.js", "function jsFn() {}\n", "jsFn"},
		{"python", "a.py", "def py_fn():\n    pass\n", "py_fn"},
	}
	for _, c := range cases {
		e, err := New(c.lang)
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.ExtractFile(c.path, []byte(c.src))
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		var names []string
		for _, s := range res.Symbols {
			names = append(names, s.FQN)
		}
		if !strings.Contains(strings.Join(names, ","), c.want) {
			t.Errorf("%s: symbols %v, want %s", c.path, names, c.want)
		}
	}
}
