package index

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestMatchExclude pins the exclude glob semantics: a pattern with no slash
// anywhere matches any path segment at any depth; a pattern with a slash
// ANYWHERE (leading, trailing, or embedded) anchors at the project root; and a
// "**/" prefix un-anchors a slash pattern.
func TestMatchExclude(t *testing.T) {
	patterns := []string{
		"node_modules",    // bare name → any segment, any depth
		"*.min.js",        // bare glob → any segment
		"db/migrations",   // anchored at root (embedded slash)
		"**/testdata",     // any depth
		"**/gen/protobuf", // multi-segment, any depth
		"env/",            // anchored at root (trailing-slash-ONLY — the P1-11 footgun pattern)
		"build/",          // anchored at root (trailing-slash-only)
		"/dist",           // anchored at root (leading-slash-only)
		"a/b/",            // anchored multi-segment with a trailing slash
		"./target",        // anchored via a stripped leading "./" marker
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

		// P1-11 pinned failure case: matchExclude(["env/"], "internal/env/e.go")
		// was TRUE pre-fix, because matchExclude trimmed the trailing slash off
		// "env/" BEFORE checking whether the pattern contained a slash, so the
		// anchored pattern silently collapsed to the bare/any-depth form and
		// matched real code under internal/env/. It must be false now.
		{"internal/env/e.go", false},
		{"env/e.go", true}, // root-level env/ is still excluded
		{"env", true},      // root-level env/ itself (no trailing file) matches too

		{"pkg/build/b.go", false}, // same footgun for "build/": pkg/build must survive
		{"build/out.js", true},    // root-level build/ is still excluded

		{"dist/index.html", true},      // "/dist" (leading-slash-only) anchors at root
		{"web/dist/index.html", false}, // ...and must NOT match nested

		{"a/b/c.go", true}, // "a/b/" (trailing slash + embedded slash) anchors the full prefix
		{"x/a/b/c.go", false},

		{"target/release/bin", true}, // "./target" behaves like "target/" (root-anchored)
		{"rust/target/release", false},
	}
	for _, c := range cases {
		if got := matchExclude(patterns, c.rel); got != c.want {
			t.Errorf("matchExclude(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

// TestMatchExcludeDegenerate pins the no-op edge cases: a pattern that
// normalizes to nothing must not panic and must never match anything.
func TestMatchExcludeDegenerate(t *testing.T) {
	patterns := []string{"", "/", "./", "**/"}
	for _, rel := range []string{"a.go", "internal/env/e.go", ""} {
		if matchExclude(patterns, rel) {
			t.Errorf("degenerate patterns must never match, but matched %q", rel)
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

// TestExcludeAnchoredDefaults pins P1-11 (B66) end-to-end against the real
// DefaultConfig() exclude list, testing MATCHING BEHAVIOR (not just which
// strings appear in the slice — the pre-existing version of this test only
// checked that "env/" etc. were present, which gave false confidence: the
// string carried a trailing slash, but matchExclude silently ignored it and
// matched internal/env/e.go anyway).
func TestExcludeAnchoredDefaults(t *testing.T) {
	cfg := config.DefaultConfig()

	// Ambiguous, source-collision-prone names: root-anchored. A same-named
	// real source subpackage must survive; a root-level dir of the same name
	// must still be excluded.
	anchoredCases := []struct {
		rel     string
		want    bool
		comment string
	}{
		{"env/config.go", true, "root-level env/ excluded"},
		{"internal/env/e.go", false, "nested internal/env/ must survive (P1-11)"},
		{"build/out.js", true, "root-level build/ excluded"},
		{"pkg/build/b.go", false, "nested pkg/build/ must survive (collides with go/build)"},
		{"target/release/x", true, "root-level target/ excluded"},
		{"rust-service/target/release/x", false, "nested target/ must survive"},
		{"coverage/index.html", true, "root-level coverage/ excluded"},
		{"internal/coverage/c.go", false, "nested coverage/ must survive (collides with internal/coverage)"},
		{"venv/lib/x.py", true, "root-level venv/ excluded"},
		{"services/api/venv/lib/x.py", false, "nested venv/ must survive"},
		{"dist/bundle.js", true, "root-level dist/ excluded"},
		{"packages/app/dist/bundle.js", false, "nested dist/ must survive (opt-in via exclude_extra)"},
	}
	for _, c := range anchoredCases {
		if got := matchExclude(cfg.Index.Exclude, c.rel); got != c.want {
			t.Errorf("%s: matchExclude(defaults, %q) = %v, want %v", c.comment, c.rel, got, c.want)
		}
	}

	// Genuinely-unambiguous dependency/artifact dirs: any-depth, including
	// nested occurrences (a workspace's per-package node_modules, or a
	// virtualenv's deeply-nested site-packages).
	anyDepthCases := []struct {
		rel     string
		want    bool
		comment string
	}{
		{"node_modules/x/index.js", true, "root node_modules"},
		{"packages/app/node_modules/x/index.js", true, "nested node_modules"},
		{"vendor/mod/x.go", true, "root vendor"},
		{"a/vendor/mod/x.go", true, "nested vendor"},
		{"__pycache__/x.pyc", true, "root __pycache__"},
		{"pkg/__pycache__/x.pyc", true, "nested __pycache__"},
		{"venv/lib/python3.12/site-packages/pkg/y.py", true, "site-packages nested under a venv"},
	}
	for _, c := range anyDepthCases {
		if got := matchExclude(cfg.Index.Exclude, c.rel); got != c.want {
			t.Errorf("%s: matchExclude(defaults, %q) = %v, want %v", c.comment, c.rel, got, c.want)
		}
	}
}

// TestDangerBenchFixturesRootAnchoredExclude mirrors codemap's own repo-root
// codemap.yaml (`index.exclude_extra: [bench/fixtures/]`) so a semantics
// change to matchExclude can never silently stop excluding the vendored
// bench fixture repo (go-git, ~445 files) from codemap's own index.
func TestDangerBenchFixturesRootAnchoredExclude(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Index.ExcludeExtra = append(cfg.Index.ExcludeExtra, "bench/fixtures/")
	exclude := append(append([]string{}, cfg.Index.Exclude...), cfg.Index.ExcludeExtra...)

	if !matchExclude(exclude, "bench/fixtures/fetch.sh") {
		t.Error("bench/fixtures/ (root-level) must still be excluded")
	}
	if !matchExclude(exclude, "bench/fixtures") {
		t.Error("bench/fixtures itself must be excluded")
	}
	if matchExclude(exclude, "other/bench/fixtures/x.go") {
		t.Error("a nested bench/fixtures must NOT be excluded — the pattern is root-anchored")
	}
}

// TestIndexerStalenessWatcherShareExcludeSemantics is a consistency check
// across the three exclude callers (indexer walk, staleness walk, and the
// daemon watcher): all three route through Indexer.excluded → matchExclude,
// so a single fix here fixes all three. This pins that the shared entry
// point (Indexer.Excluded, which watcher.go's WatchConfig.Excluded is wired
// to via internal/daemon) applies the anchored default semantics, not just
// the indexer's own walk.
func TestIndexerStalenessWatcherShareExcludeSemantics(t *testing.T) {
	ix := New(nil, nil, nil, config.DefaultConfig().Index)

	// The indexer's own predicate (used by walk() and staleness.go's
	// WalkDir, both of which call ix.excluded directly).
	if ix.excluded("internal/env/e.go") {
		t.Error("indexer: nested internal/env must not be excluded")
	}
	if !ix.excluded("env/e.go") {
		t.Error("indexer: root-level env/ must be excluded")
	}

	// The public Excluded method is exactly what internal/daemon wires into
	// index.WatchConfig.Excluded for the fsnotify watcher — verify it's the
	// same predicate, not a divergent copy.
	if ix.Excluded("internal/env/e.go") {
		t.Error("watcher predicate (Indexer.Excluded): nested internal/env must not be excluded")
	}
	if !ix.Excluded("env/e.go") {
		t.Error("watcher predicate (Indexer.Excluded): root-level env/ must be excluded")
	}
}
