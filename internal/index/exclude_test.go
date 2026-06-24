package index

import "testing"

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

// TestExcludeExtraIsAdditive verifies cfg.ExcludeExtra is appended to cfg.Exclude
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
