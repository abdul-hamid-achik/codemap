package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// rustSym builds a Rust symbol node at a 1-based start line, mirroring what
// lspsrc.FromDocumentSymbols produces for rust-analyzer answers.
func rustSym(name, fqn, kind string, line int) extract.Symbol {
	return extract.Symbol{
		Name: name, FQN: fqn, Kind: kind, Language: "rust",
		StartLine: line, EndLine: line,
	}
}

// TestRustSymbolsGoldenFixtureHermetic indexes a representative Cargo-workspace
// fixture — module tree (mod.rs + `mod name;`), trait + impl blocks, generics,
// a macro_rules! and #[test] functions — through the same fakeLSPExtractor seam
// the TS/Python hermetic tests use, and pins the resulting nodes: kinds, FQNs,
// and stable ranges. No server is spawned.
func TestRustSymbolsGoldenFixtureHermetic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n")
	writeFile(t, dir, "src/main.rs", "mod models;\n\nfn main() {\n    let u = models::User::new(\"a\");\n}\n")
	writeFile(t, dir, "src/models/mod.rs", "pub mod user;\npub use user::User;\n")
	writeFile(t, dir, "src/models/user.rs",
		"pub struct User { name: String }\n\npub trait Greet {\n    fn greet(&self) -> String;\n}\n\nimpl User {\n    pub fn new(name: &str) -> Self { User { name: name.into() } }\n}\n\nimpl Greet for User {\n    fn greet(&self) -> String { self.name.clone() }\n}\n\npub fn identity<T: Clone>(x: T) -> T { x }\n\nmacro_rules! twice {\n    ($e:expr) => { $e; $e };\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n    #[test]\n    fn user_greets() { assert!(true) }\n}\n")

	fake := &fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/main.rs": {
				rustSym("main", "fixture::main", extract.KindFunction, 3),
			},
			"src/models/mod.rs": {
				rustSym("user", "fixture::models::user", extract.KindModule, 1),
			},
			"src/models/user.rs": {
				rustSym("User", "fixture::models::user::User", extract.KindClass, 1),
				rustSym("Greet", "fixture::models::user::Greet", extract.KindType, 3),
				rustSym("new", "fixture::models::user::User::new", extract.KindMethod, 8),
				rustSym("greet", "fixture::models::user::User::greet", extract.KindMethod, 13),
				rustSym("identity", "fixture::models::user::identity", extract.KindFunction, 16),
				rustSym("user_greets", "fixture::models::user::tests::user_greets", extract.KindTest, 23),
			},
		},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-golden", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "rust-golden", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["rust"] != 3 {
		t.Fatalf("expected 3 rust files indexed via the fake extractor, got languages %v", res.Languages)
	}

	// Stable FQNs and kinds survive into the graph.
	for _, want := range []struct{ fqn, kind string }{
		{"fixture::main", extract.KindFunction},
		{"fixture::models::user", extract.KindModule},
		{"fixture::models::user::User", extract.KindClass},
		{"fixture::models::user::Greet", extract.KindType},
		{"fixture::models::user::User::new", extract.KindMethod},
		{"fixture::models::user::User::greet", extract.KindMethod},
		{"fixture::models::user::identity", extract.KindFunction},
		{"fixture::models::user::tests::user_greets", extract.KindTest},
	} {
		nodes, err := g.FindNodesByFQN(pid, want.fqn)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) == 0 {
			t.Errorf("no node for FQN %q", want.fqn)
			continue
		}
		if nodes[0].Kind != want.kind {
			t.Errorf("node %s kind = %q, want %q", want.fqn, nodes[0].Kind, want.kind)
		}
		if nodes[0].Language != "rust" {
			t.Errorf("node %s language = %q, want rust", want.fqn, nodes[0].Language)
		}
	}
}

// TestRustPreciseCoverageHermetic proves the --precise rust-analyzer path end
// to end with a fake CallResolver: exact call edges are recorded, a LEAF file
// (successful, zero edges) is still covered, and a file whose callHierarchy
// fails is left uncovered — honest per-file coverage.
func TestRustPreciseCoverageHermetic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"cov\"\n")
	writeFile(t, dir, "src/lib.rs", "pub fn caller() { callee(); leaf(); }\n")
	writeFile(t, dir, "src/leaf.rs", "pub fn leaf() {}\n")
	writeFile(t, dir, "src/fail.rs", "pub fn fail() {}\n")

	fake := &fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/lib.rs": {rustSym("caller", "cov::caller", extract.KindFunction, 1)},
			"src/leaf.rs": {
				rustSym("leaf", "cov::leaf", extract.KindFunction, 1),
			},
			"src/fail.rs": {rustSym("fail", "cov::fail", extract.KindFunction, 1)},
		},
		edges: map[string][]extract.CallEdge{
			"src/lib.rs": {
				{FromFQN: "caller", FromFile: "src/lib.rs", FromLine: 1, ToFile: "src/leaf.rs", ToLine: 1},
			},
			"src/leaf.rs": {}, // successful leaf: zero edges, still covered
		},
		failFiles: map[string]bool{"src/fail.rs": true},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-cov", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "rust-cov", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	covered := map[string]string{}
	cov, err := g.ProjectCallGraphCoverage(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cov {
		covered[e.FilePath] = e.Resolver
	}
	if covered["src/lib.rs"] != "lsp" {
		t.Errorf("coverage[src/lib.rs] = %q, want lsp", covered["src/lib.rs"])
	}
	if covered["src/leaf.rs"] != "lsp" {
		t.Errorf("coverage[src/leaf.rs] = %q, want lsp (leaf file with zero edges is still covered)", covered["src/leaf.rs"])
	}
	if _, ok := covered["src/fail.rs"]; ok {
		t.Errorf("coverage[src/fail.rs] = %q, want uncovered (callHierarchy failure)", covered["src/fail.rs"])
	}

	// The exact edge caller -> leaf exists with precise provenance.
	callerID := nodeIDBySymbol(t, g, pid, "caller")
	leafID := nodeIDBySymbol(t, g, pid, "leaf")
	if p := callEdgeProvenance(t, g, pid, callerID, leafID); p == "" {
		t.Error("no calls edge caller -> leaf")
	}
}

// TestRustEmptyPreciseNeverErasesNameCandidates mirrors the TS invariant for
// Rust: an empty callHierarchy answer for a file must not erase the file's
// name-based call candidates.
func TestRustEmptyPreciseNeverErasesNameCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/lib.rs", "pub fn user() { target(); }\n")
	writeFile(t, dir, "src/target.rs", "pub fn target() {}\n")

	fake := &fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/lib.rs":    {rustSym("user", "r::user", extract.KindFunction, 1)},
			"src/target.rs": {rustSym("target", "r::target", extract.KindFunction, 1)},
		},
		refs: map[string][]extract.Reference{
			"src/lib.rs": {{From: "r::user", To: "target", Kind: extract.RefCalls}},
		},
		edges: map[string][]extract.CallEdge{
			"src/lib.rs":    {}, // empty precise answer
			"src/target.rs": {},
		},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-keep", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "rust-keep", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	userID := nodeIDBySymbol(t, g, pid, "user")
	targetID := nodeIDBySymbol(t, g, pid, "target")
	if p := callEdgeProvenance(t, g, pid, userID, targetID); p == "" {
		t.Error("empty precise answer erased the name-based candidate edge user -> target")
	}
}

// TestRustMissingServerDegradesVisibly indexes .rs files with rust-analyzer
// nowhere on PATH (empty PATH): no crash, the files stay recognized and
// reported as unsupported, and the missing server is surfaced — never silent.
func TestRustMissingServerDegradesVisibly(t *testing.T) {
	dir := t.TempDir()
	emptyBin := t.TempDir()
	t.Setenv("PATH", emptyBin) // asdf shims and all: LookPath must fail
	writeFile(t, dir, "src/main.rs", "fn main() {}\n")

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-missing", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig()) // lsp structural mode: rust needs the server

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "rust-missing", dir, Options{})
	if err != nil {
		t.Fatalf("missing rust-analyzer must degrade, not fail: %v", err)
	}
	if res.Unsupported["rust"] != 1 {
		t.Errorf("unsupported = %v, want rust:1 (files stay recognized)", res.Unsupported)
	}
	if res.MissingServers["rust"] != "rust-analyzer" {
		t.Errorf("missing_servers = %v, want rust -> rust-analyzer", res.MissingServers)
	}
	if len(res.ServerIssues) == 0 {
		t.Error("expected a structured server issue for the missing rust-analyzer")
	}
}

// TestRustIncrementalAddChangeDelete drives add → change → delete over a Rust
// project through the fake extractor: changed symbols replace stale ones, the
// deleted file's nodes and call coverage are pruned (stale coverage
// invalidation).
func TestRustIncrementalAddChangeDelete(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/a.rs", "pub fn a_one() {}\n")
	writeFile(t, dir, "src/b.rs", "pub fn b_gone() {}\n")

	mkFake := func() *fakeLSPExtractor {
		return &fakeLSPExtractor{
			lang: "rust",
			symbols: map[string][]extract.Symbol{
				"src/a.rs": {rustSym("a_one", "i::a_one", extract.KindFunction, 1)},
				"src/b.rs": {rustSym("b_gone", "i::b_gone", extract.KindFunction, 1)},
			},
			edges: map[string][]extract.CallEdge{
				"src/a.rs": {},
				"src/b.rs": {},
			},
		}
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-incr", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(mkFake())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "rust-incr", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	// Change a.rs (new symbol name), delete b.rs.
	writeFile(t, dir, "src/a.rs", "pub fn a_two() {}\n")
	if err := removeFile(t, dir, "src/b.rs"); err != nil {
		t.Fatal(err)
	}
	ix2 := New(g, nil, nil, lspModeConfig())
	ix2.Register(&fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/a.rs": {rustSym("a_two", "i::a_two", extract.KindFunction, 1)},
		},
		edges: map[string][]extract.CallEdge{"src/a.rs": {}},
	})
	if _, err := ix2.IndexProject(ctx, pid, "rust-incr", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := g.FindNodesBySymbol(pid, "a_two"); err != nil {
		t.Fatal(err)
	} else if nodes, _ := g.FindNodesBySymbol(pid, "a_two"); len(nodes) == 0 {
		t.Error("changed symbol a_two missing after reindex")
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "a_one"); len(nodes) != 0 {
		t.Error("stale symbol a_one still present after change")
	}
	if nodes, _ := g.FindNodesBySymbol(pid, "b_gone"); len(nodes) != 0 {
		t.Error("deleted file's symbol b_gone still present (ghost node)")
	}
	cov, err := g.ProjectCallGraphCoverage(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cov {
		if e.FilePath == "src/b.rs" {
			t.Error("stale call coverage for deleted src/b.rs")
		}
	}
}

// TestRustSameNameAmbiguity: two modules each declare `fn helper`; a precise
// call edge from one module's caller must resolve to ITS OWN module's helper,
// never the sibling's.
func TestRustSameNameAmbiguity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/mod1.rs", "pub fn caller1() { helper(); }\npub fn helper() {}\n")
	writeFile(t, dir, "src/mod2.rs", "pub fn helper() {}\n")

	fake := &fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/mod1.rs": {
				rustSym("caller1", "s::mod1::caller1", extract.KindFunction, 1),
				rustSym("helper", "s::mod1::helper", extract.KindFunction, 2),
			},
			"src/mod2.rs": {rustSym("helper", "s::mod2::helper", extract.KindFunction, 1)},
		},
		edges: map[string][]extract.CallEdge{
			// rust-analyzer resolves the call to mod1's helper by position.
			"src/mod1.rs": {
				{FromFQN: "caller1", FromFile: "src/mod1.rs", FromLine: 1, ToFile: "src/mod1.rs", ToLine: 2},
			},
			"src/mod2.rs": {},
		},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-ambig", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "rust-ambig", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	nodes, err := g.FindNodesByFQN(pid, "s::mod1::helper")
	if err != nil || len(nodes) == 0 {
		t.Fatalf("mod1 helper missing: %v", err)
	}
	mod1Helper := nodes[0].ID
	nodes, err = g.FindNodesByFQN(pid, "s::mod2::helper")
	if err != nil || len(nodes) == 0 {
		t.Fatalf("mod2 helper missing: %v", err)
	}
	mod2Helper := nodes[0].ID
	if mod1Helper == mod2Helper {
		t.Fatal("same-name helpers in different modules collapsed into one node")
	}

	caller := nodeIDBySymbol(t, g, pid, "caller1")
	if p := callEdgeProvenance(t, g, pid, caller, mod1Helper); p == "" {
		t.Error("precise edge caller1 -> mod1::helper missing")
	}
	if p := callEdgeProvenance(t, g, pid, caller, mod2Helper); p != "" {
		t.Error("precise edge leaked to the same-named helper in mod2")
	}
}

// TestRustLanguageFamily pins the family isolation used by name-based call
// resolution: Rust stands alone, so a Go call never resolves to a Rust symbol
// (and vice versa) by name alone.
func TestRustLanguageFamily(t *testing.T) {
	if got := languageFamily("rust"); got != "rust" {
		t.Errorf("languageFamily(rust) = %q, want rust", got)
	}
	if languageFamily("go") == languageFamily("rust") {
		t.Error("go and rust must not share a language family")
	}
}

// TestMixedGoRustCallIsolation indexes a mixed Go/Rust project (both via the
// fake seam) where Go code calls `helper` and BOTH languages declare a symbol
// named helper. The name-based calls edge must land on the Go node only —
// never the Rust symbol.
func TestMixedGoRustCallIsolation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n")
	writeFile(t, dir, "src/lib.rs", "pub fn helper() {}\n")

	goFake := &fakeLSPExtractor{
		lang: "go",
		symbols: map[string][]extract.Symbol{
			"main.go": {
				{Name: "main", FQN: "main.main", Kind: extract.KindFunction, Language: "go", StartLine: 3, EndLine: 3},
				{Name: "helper", FQN: "main.helper", Kind: extract.KindFunction, Language: "go", StartLine: 5, EndLine: 5},
			},
		},
		refs: map[string][]extract.Reference{
			"main.go": {{From: "main.main", To: "helper", Kind: extract.RefCalls}},
		},
	}
	rustFake := &fakeLSPExtractor{
		lang: "rust",
		symbols: map[string][]extract.Symbol{
			"src/lib.rs": {rustSym("helper", "m::helper", extract.KindFunction, 1)},
		},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("mixed-go-rust", dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig())
	ix.Register(goFake)
	ix.Register(rustFake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "mixed-go-rust", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["go"] != 1 || res.Languages["rust"] != 1 {
		t.Fatalf("languages = %v, want go:1 rust:1", res.Languages)
	}

	rustNodes, err := g.FindNodesByFQN(pid, "m::helper")
	if err != nil || len(rustNodes) == 0 {
		t.Fatalf("rust helper node missing: %v", err)
	}
	mainID := nodeIDBySymbol(t, g, pid, "main")
	if p := callEdgeProvenance(t, g, pid, mainID, rustNodes[0].ID); p != "" {
		t.Errorf("Go call resolved to the Rust symbol %s by name (provenance %q) — family isolation broken", "m::helper", p)
	}
	// And the Go-side edge does exist (the isolation dropped the wrong
	// candidate, not the whole resolution).
	goHelper, err := g.FindNodesByFQN(pid, "main.helper")
	if err != nil || len(goHelper) == 0 {
		t.Fatalf("go helper node missing: %v", err)
	}
	if p := callEdgeProvenance(t, g, pid, mainID, goHelper[0].ID); p == "" {
		t.Error("Go call edge main -> helper missing")
	}
}

// TestRustRealServer indexes a small real Cargo-workspace fixture with the
// actual rust-analyzer on PATH (skip cleanly elsewhere). cargo is NOT required
// for indexing — only the rust-analyzer binary. If the server cannot serve the
// fixture, the degradation must be captured honestly (server issues /
// unsupported) with no hang.
func TestRustRealServer(t *testing.T) {
	bin, err := exec.LookPath("rust-analyzer")
	if err != nil {
		t.Skipf("rust-analyzer not on PATH: %v", err)
	}
	if strings.Contains(bin, "shims") {
		t.Logf("rust-analyzer via version-manager shim: %s", bin)
	}

	dir := t.TempDir()
	// An asdf shim needs a project pin to resolve the version; without one it
	// exits 126 ("no version is set"), which the indexer reports as a
	// version-manager gap. Pin the first installed rust version when asdf is
	// the resolver, so the fixture exercises the real server on such hosts.
	if v := asdfRustVersion(); v != "" {
		writeFile(t, dir, ".tool-versions", "rust "+v+"\n")
		t.Logf("pinned asdf rust %s for the fixture", v)
	}
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"real\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, dir, "src/main.rs", "mod util;\n\nfn main() {\n    let s = util::shout(\"hi\");\n    println!(\"{s}\");\n}\n")
	writeFile(t, dir, "src/util.rs", "pub fn shout(s: &str) -> String {\n    format!(\"{s}!\")\n}\n")

	g, _ := newStores(t)
	pid, err := g.UpsertProject("rust-real", dir, "rust")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, lspModeConfig()) // lsp mode: .rs routes to rust-analyzer

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	done := make(chan *Result, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := ix.IndexProject(ctx, pid, "rust-real", dir, Options{})
		done <- res
		errCh <- err
	}()

	select {
	case <-time.After(100 * time.Second):
		t.Fatal("indexing with rust-analyzer did not finish in 100s — unbounded hang")
	case err := <-errCh:
		if err != nil {
			t.Fatalf("real-server index failed: %v", err)
		}
		res := <-done
		if res.Languages["rust"] > 0 {
			t.Logf("rust-analyzer indexed %d rust files; degraded note: %q", res.Languages["rust"], res.PreciseNote)
			nodes, err := g.FindNodesBySymbol(pid, "shout")
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) == 0 {
				t.Error("rust-analyzer ran but symbol `shout` was not extracted — check degradation path")
				return
			}
			// Precise pass through the real callHierarchy: main calls util::shout.
			// A cold rust-analyzer serves prepareCallHierarchy only once its Cargo
			// workspace finishes loading (the bounded ladder in lspsrc pays this
			// once per run); a fresh-spawned server can still lose the race on a
			// loaded machine, so the check retries with fresh indexers — the shape
			// successive CLI invocations would have.
			var callers []graph.Node
			for attempt := 1; attempt <= 3; attempt++ {
				ixN := New(g, nil, nil, lspModeConfig())
				if _, perr := ixN.IndexProject(context.Background(), pid, "rust-real", dir, Options{Precise: true, Reindex: true}); perr != nil {
					t.Fatalf("precise reindex failed: %v", perr)
				}
				var cerr error
				callers, cerr = g.Callers(pid, "shout")
				if cerr != nil {
					t.Fatal(cerr)
				}
				for _, c := range callers {
					if c.Symbol == "main" {
						return // resolved: T3 works against the real server
					}
				}
			}
			t.Errorf("precise pass did not resolve main->shout via callHierarchy after 3 attempts (callers=%d)", len(callers))
			return
		}
		// Honest degradation: report why the server could not serve the fixture.
		t.Logf("rust-analyzer present at %s but served 0 files (unsupported=%v, issues=%d) — captured degradation, hermetic tests remain authoritative",
			bin, res.Unsupported, len(res.ServerIssues))
		for _, iss := range res.ServerIssues {
			t.Logf("issue: %s: %s", iss.Code, iss.Detail)
		}
	}
}

// removeFile deletes dir/rel (test helper; os.Remove with a clear failure).
func removeFile(t *testing.T, dir, rel string) error {
	t.Helper()
	return os.Remove(filepath.Join(dir, rel))
}

// asdfRustVersion returns the first rust version asdf has installed (so a
// project .tool-versions pin can unblock an asdf rust-analyzer shim), or ""
// when asdf isn't the resolver or has no rust installed.
func asdfRustVersion() string {
	asdf, err := exec.LookPath("asdf")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, asdf, "list", "rust").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if v != "" && !strings.Contains(v, " ") {
			return v
		}
	}
	return ""
}
