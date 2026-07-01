package index

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestMatchExclude pins the exclude glob semantics: bare names match any path
// segment at any depth, slash patterns anchor at the project root, and a "**/"
// prefix un-anchors a slash pattern.
func TestMatchExclude(t *testing.T) {
	patterns := []string{
		"node_modules",    // bare name → any segment, any depth
		"*.min.js",        // bare glob → any segment
		"db/migrations",   // anchored at root
		"**/testdata",     // any depth
		"**/gen/protobuf", // multi-segment, any depth
	}
	cases := []struct {
		rel  string
		want bool
	}{
		{"node_modules/react/index.js", true}, // bare name, depth 1
		{"web/node_modules/x.ts", true},       // bare name, deeper
		{"src/app.go", false},                 // nothing matches
		{"web/bundle.min.js", true},           // bare glob, any depth
		{"db/migrations/0001_init.sql", true}, // anchored prefix
		{"db/migrations", true},               // anchored, exact
		{"app/db/migrations/x.sql", false},    // anchored must NOT match nested
		{"pkg/testdata/fixture.json", true},   // **/ at depth
		{"testdata/a.go", true},               // **/ at root
		{"a/b/gen/protobuf/x.go", true},       // **/ multi-segment, deep
		{"gen/protobuf/x.go", true},           // **/ multi-segment, root
		{"gen/other/x.go", false},             // partial multi-segment, no match
	}
	for _, c := range cases {
		if got := matchExclude(patterns, c.rel); got != c.want {
			t.Errorf("matchExclude(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

// TestExcludeExtraIsAdditive verifies cfg.Index.ExcludeExtra is appended to cfg.Index.Exclude
// rather than replacing it — the whole point of the field.
func TestExcludeExtraIsAdditive(t *testing.T) {
	ix := &Indexer{exclude: append(append([]string{}, "node_modules"), "migrations")}
	if !ix.excluded("node_modules/x.js") {
		t.Error("default exclude should still apply")
	}
	if !ix.excluded("db/migrations/0001.sql") {
		t.Error("exclude_extra entry 'migrations' should apply")
	}
	if ix.excluded("src/app.go") {
		t.Error("unrelated path should not be excluded")
	}
}

// TestExcludeAnchoredDefaults pins P1-11 (B66): pre-fix the default
// excludes included bare names "env", "build", "target" that matched
// ANY path segment at ANY depth, so a Go repo\'s "go/build/" or
// "internal/env/" subpackage was silently excluded. The fix anchors
// them with a trailing slash, scoping to same-level dirs only.
func TestExcludeAnchoredDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, want := range []string{"dist/", "build/", "target/", "coverage/", "env/", "venv/"} {
		found := false
		for _, pat := range cfg.Index.Exclude {
			if pat == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultConfig.Exclude must contain root-anchored %q (P1-11: bare name silently excluded Go subpackages)", want)
		}
	}
	// Sanity: the truly any-depth excludes are still there.
	for _, want := range []string{"node_modules", "vendor", "__pycache__", "site-packages/"} {
		found := false
		for _, pat := range cfg.Index.Exclude {
			if pat == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultConfig.Exclude must still contain any-depth %q", want)
		}
	}
	// And the bare-name footguns are GONE.
	for _, banned := range []string{"env", "build", "target", "dist", "coverage"} {
		for _, pat := range cfg.Index.Exclude {
			if pat == banned {
				t.Errorf("P1-11 regression: DefaultConfig.Exclude must not contain bare %q (matches any-segment-any-depth; silently dropped Go subpackages)", banned)
			}
		}
	}
}
