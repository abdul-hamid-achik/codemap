package graph

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenMigrate(t *testing.T) {
	s := openTest(t)
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Errorf("user_version = %d, want %d", v, schemaVersion)
	}
	// Re-opening an existing DB must be a no-op migration.
	if err := s.migrate(); err != nil {
		t.Errorf("re-migrate: %v", err)
	}
}

func TestUpsertProject(t *testing.T) {
	s := openTest(t)
	id1, err := s.UpsertProject("demo", "/tmp/demo", "go")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.UpsertProject("demo", "/tmp/demo2", "go")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("upsert created a new row: %d vs %d", id1, id2)
	}
	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != "/tmp/demo2" {
		t.Errorf("path = %q, want updated /tmp/demo2", p.Path)
	}
	projs, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projs) != 1 {
		t.Errorf("ListProjects len = %d, want 1", len(projs))
	}
}

func TestGetProjectNotFound(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetProjectByName("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddGetNode(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	n := &Node{ProjectID: pid, FilePath: "a.go", Symbol: "Foo", FQN: "pkg.Foo",
		Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 9, SourceHash: "h"}
	id, err := s.AddNode(n)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Symbol != "Foo" || got.FQN != "pkg.Foo" || got.Kind != KindFunction {
		t.Errorf("bad node: %+v", got)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Error("timestamps not stamped")
	}
}

func TestResolveQualifiedName(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(file, sym, fqn string) {
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: fqn,
			Kind: KindFunction, Language: "go", SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("ui/theme.go", "DefaultTheme", "ui.DefaultTheme")
	mk("modes/save.go", "Update", "modes.SaveAsMode.Update")

	cases := []struct {
		in, want string
		ok       bool
	}{
		{"ui.DefaultTheme", "DefaultTheme", true},   // exact fqn (pkg.Sym) — copied from hotspots
		{"modes.SaveAsMode.Update", "Update", true}, // exact fqn (pkg.Type.Method)
		{"SaveAsMode.Update", "Update", true},       // fqn suffix (Type.Method)
		{"NoSuch.Thing", "", false},                 // no match → caller keeps the input
	}
	for _, c := range cases {
		got, ok := s.ResolveQualifiedName(pid, c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ResolveQualifiedName(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestGetNodeNotFound(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetNode(123); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFindNodes(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	add := func(file, sym string, start int) {
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym,
			Kind: KindFunction, Language: "go", StartLine: start, EndLine: start + 1, SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	add("a.go", "Foo", 1)
	add("b.go", "Foo", 3)
	add("a.go", "Bar", 5)

	foos, err := s.FindNodesBySymbol(pid, "Foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(foos) != 2 {
		t.Errorf("Foo count = %d, want 2", len(foos))
	}
	inA, err := s.NodesInFile(pid, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(inA) != 2 {
		t.Errorf("a.go count = %d, want 2", len(inA))
	}
}

func TestAddEdgeForeignKeyAndCascade(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	a, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	b, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "b.go", Symbol: "B", Kind: KindFunction, Language: "go", SourceHash: "h"})

	if _, err := s.AddEdge(a, b, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(a, 99999, EdgeCalls, WeightLSP); err == nil {
		t.Error("expected foreign-key error for edge to missing node")
	}

	if err := s.DeleteNodesInFile(pid, "a.go"); err != nil {
		t.Fatal(err)
	}
	var edges int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Errorf("edges = %d, want 0 after cascade delete", edges)
	}
}

func TestEdgeProvenance(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	a, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	b, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "b.go", Symbol: "B", Kind: KindFunction, Language: "go", SourceHash: "h"})
	c, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "c.go", Symbol: "C", Kind: KindFunction, Language: "go", SourceHash: "h"})

	// AddEdge defaults to 'name'; AddEdgeProv can tag 'precise'.
	if _, err := s.AddEdge(a, b, EdgeCalls, WeightTreeSitter); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdgeProv(a, c, EdgeCalls, WeightLSP, ProvPrecise); err != nil {
		t.Fatal(err)
	}
	prov := func(src, tgt int64) string {
		var p string
		if err := s.db.QueryRow("SELECT provenance FROM edges WHERE source_id=? AND target_id=?", src, tgt).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := prov(a, b); got != ProvName {
		t.Errorf("AddEdge provenance = %q, want %q", got, ProvName)
	}
	if got := prov(a, c); got != ProvPrecise {
		t.Errorf("AddEdgeProv provenance = %q, want %q", got, ProvPrecise)
	}

	// DeleteCallEdgesBySource removes only the named-provenance call edges of the
	// given sources, leaving precise edges (and other sources) intact.
	if err := s.DeleteCallEdgesBySource([]int64{a}, ProvName); err != nil {
		t.Fatal(err)
	}
	var nameLeft, preciseLeft int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM edges WHERE provenance=?", ProvName).Scan(&nameLeft)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM edges WHERE provenance=?", ProvPrecise).Scan(&preciseLeft)
	if nameLeft != 0 {
		t.Errorf("name edges after delete = %d, want 0", nameLeft)
	}
	if preciseLeft != 1 {
		t.Errorf("precise edges after delete = %d, want 1 (untouched)", preciseLeft)
	}
}

func TestMigrateV2ToV3AddsProvenance(t *testing.T) {
	// Simulate a pre-v3 database: an edges table WITHOUT the provenance column,
	// user_version=2 — then Open must add the column and stamp version 3.
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate a v2-shaped edges table and roll the version back to 2.
	for _, stmt := range []string{
		"DROP TABLE edges",
		`CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL, edge_type TEXT NOT NULL, weight REAL NOT NULL DEFAULT 1.0,
			created_at TEXT NOT NULL)`,
		"INSERT INTO edges(source_id,target_id,edge_type,weight,created_at) VALUES(1,2,'calls',1.0,'t')",
		"PRAGMA user_version=2",
	} {
		if _, err := old.db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	if has, _ := old.columnExists("edges", "provenance"); has {
		t.Fatal("precondition failed: v2 edges table should lack provenance")
	}
	_ = old.Close()

	// Re-open: migration must add the column with default 'name' for existing rows.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("re-open/migrate: %v", err)
	}
	defer s.Close()
	has, err := s.columnExists("edges", "provenance")
	if err != nil || !has {
		t.Fatalf("provenance column missing after migrate (has=%v err=%v)", has, err)
	}
	var prov string
	if err := s.db.QueryRow("SELECT provenance FROM edges WHERE source_id=1").Scan(&prov); err != nil {
		t.Fatal(err)
	}
	if prov != ProvName {
		t.Errorf("migrated existing row provenance = %q, want %q", prov, ProvName)
	}
	var v int
	_ = s.db.QueryRow("PRAGMA user_version").Scan(&v)
	if v != schemaVersion {
		t.Errorf("user_version = %d, want %d", v, schemaVersion)
	}
	// Idempotent: re-migrate is a no-op.
	if err := s.migrate(); err != nil {
		t.Errorf("re-migrate after upgrade: %v", err)
	}
}

func TestFileHash(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	h, err := s.FileHash(pid, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if h != "" {
		t.Errorf("initial hash = %q, want empty", h)
	}
	if err := s.SetFileHash(pid, "a.go", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFileHash(pid, "a.go", "def"); err != nil {
		t.Fatal(err)
	}
	if h, _ = s.FileHash(pid, "a.go"); h != "def" {
		t.Errorf("hash = %q, want def", h)
	}
}

func TestCallersCallees(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(file, sym string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: "p." + sym, Kind: KindFunction, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c := mk("a.go", "A"), mk("b.go", "B"), mk("c.go", "C")
	// A->B, C->B (two callers of B); B->C (B calls C)
	for _, e := range [][2]int64{{a, b}, {c, b}, {b, c}} {
		if _, err := s.AddEdge(e[0], e[1], EdgeCalls, WeightLSP); err != nil {
			t.Fatal(err)
		}
	}
	callers, err := s.Callers(pid, "B")
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 2 {
		t.Errorf("callers of B = %d, want 2", len(callers))
	}
	callees, err := s.Callees(pid, "B")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 || callees[0].Symbol != "C" {
		t.Errorf("callees of B = %+v, want [C]", callees)
	}
}

func TestBlastRadius(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(file, sym, kind string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: "p." + sym, Kind: kind, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk("a.go", "A", KindFunction)
	b := mk("b.go", "B", KindFunction)
	c := mk("c.go", "C", KindFunction)
	tn := mk("c_test.go", "TestC", KindTest)
	// A->B, B->C, TestC->B
	for _, e := range [][2]int64{{a, b}, {b, c}, {tn, b}} {
		if _, err := s.AddEdge(e[0], e[1], EdgeCalls, WeightLSP); err != nil {
			t.Fatal(err)
		}
	}

	br, err := s.BlastRadius(pid, "C", 5)
	if err != nil {
		t.Fatal(err)
	}
	depth := map[string]int{}
	for _, nd := range br {
		depth[nd.Node.Symbol] = nd.Depth
	}
	if len(br) != 3 {
		t.Errorf("blast radius size = %d, want 3 (B,A,TestC)", len(br))
	}
	if depth["B"] != 1 || depth["A"] != 2 || depth["TestC"] != 2 {
		t.Errorf("depths = %v, want B:1 A:2 TestC:2", depth)
	}

	// depth limit
	if br1, _ := s.BlastRadius(pid, "C", 1); len(br1) != 1 {
		t.Errorf("depth-1 size = %d, want 1 (only B)", len(br1))
	}
}

func TestBlastRadiusCycleSafe(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym string) int64 {
		id, _ := s.AddNode(&Node{ProjectID: pid, FilePath: sym + ".go", Symbol: sym, FQN: "p." + sym, Kind: KindFunction, Language: "go", SourceHash: "h"})
		return id
	}
	a, b := mk("A"), mk("B")
	// cycle A<->B
	if _, err := s.AddEdge(a, b, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(b, a, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	br, err := s.BlastRadius(pid, "A", 10) // must terminate
	if err != nil {
		t.Fatal(err)
	}
	if len(br) != 1 || br[0].Node.Symbol != "B" {
		t.Errorf("cycle blast radius = %+v, want [B]", br)
	}
}

func TestHotspotsOrphansPath(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym, kind string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: sym + ".go", Symbol: sym, FQN: "p." + sym, Kind: kind, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk("A", KindFunction)
	b := mk("B", KindFunction)
	c := mk("C", KindFunction)
	lonely := mk("Lonely", KindFunction)
	_ = lonely
	// A->B, A->C, B->C   => C in-degree 2 (top hotspot), B in-degree 1
	for _, e := range [][2]int64{{a, b}, {a, c}, {b, c}} {
		if _, err := s.AddEdge(e[0], e[1], EdgeCalls, WeightLSP); err != nil {
			t.Fatal(err)
		}
	}

	// hotspots
	hs, err := s.Hotspots(pid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) == 0 || hs[0].Node.Symbol != "C" || hs[0].InDegree != 2 {
		t.Errorf("top hotspot = %+v, want C with in-degree 2", hs[0])
	}

	// orphans: A and Lonely have no callers; B and C do.
	orph, err := s.Orphans(pid, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range orph {
		got[n.Symbol] = true
	}
	if !got["A"] || !got["Lonely"] {
		t.Errorf("orphans = %v, want A and Lonely", got)
	}
	if got["B"] || got["C"] {
		t.Errorf("orphans should not include called nodes B/C: %v", got)
	}

	// path A -> C exists (A->B->C or A->C); C -> A does not.
	path, err := s.Path(pid, "A", "C", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) == 0 || path[0].Symbol != "A" || path[len(path)-1].Symbol != "C" {
		t.Errorf("path A->C = %+v, want starting A ending C", path)
	}
	if rev, _ := s.Path(pid, "C", "A", 10); len(rev) != 0 {
		t.Errorf("path C->A should be empty, got %+v", rev)
	}
}

// TestHotspotsCountsCallsNotReferences pins the decoupling: a function-value
// `references` edge keeps a node out of orphans (it's wired somewhere) but must
// NOT count toward hotspots — a hub is something many call sites depend on, and
// counting value references would let commonly-named handlers shadow real hubs.
func TestHotspotsCountsCallsNotReferences(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: "f.go", Symbol: sym, FQN: "p." + sym, Kind: KindFunction, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk("a")
	called := mk("called")
	referenced := mk("referenced")
	if _, err := s.AddEdge(a, called, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(a, referenced, EdgeReferences, WeightLSP); err != nil {
		t.Fatal(err)
	}
	hs, err := s.Hotspots(pid, 10)
	if err != nil {
		t.Fatal(err)
	}
	deg := map[string]int{}
	for _, h := range hs {
		deg[h.Node.Symbol] = h.InDegree
	}
	if deg["called"] != 1 {
		t.Errorf("called should be a hotspot with in-degree 1, got %d", deg["called"])
	}
	if _, ok := deg["referenced"]; ok {
		t.Error("a value-referenced node must NOT count toward hotspots (calls only)")
	}
}

// TestOrphansExcludesInterfaceMethods pins that methods implementing well-known
// stdlib interfaces (Error/String/Unwrap/Marshal*) aren't flagged as dead code —
// they're dispatch-invoked, so always false positives — while a plain dead method
// or function still is.
func TestOrphansExcludesInterfaceMethods(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym, kind string) {
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: "f.go", Symbol: sym, FQN: "p.T." + sym, Kind: kind, Language: "go", SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("Error", KindMethod)        // error interface — dispatch-invoked, never an orphan
	mk("String", KindMethod)       // fmt.Stringer
	mk("MarshalJSON", KindMethod)  // json.Marshaler
	mk("doThing", KindMethod)      // a plain dead method — should be flagged
	mk("lonelyFunc", KindFunction) // a plain dead function — should be flagged

	orph, err := s.Orphans(pid, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range orph {
		got[n.Symbol] = true
	}
	for _, m := range []string{"Error", "String", "MarshalJSON"} {
		if got[m] {
			t.Errorf("%s is a stdlib-interface method (dispatch-invoked) — must not be a dead-code candidate", m)
		}
	}
	if !got["doThing"] {
		t.Error("doThing is a plain uncalled method — should be an orphan")
	}
	if !got["lonelyFunc"] {
		t.Error("lonelyFunc is a plain uncalled function — should be an orphan")
	}
}

// TestOrphansExcludesValueReferenced verifies a function reachable only as a
// value (a `references` edge, e.g. a cobra `RunE: handler`) is NOT flagged as
// dead code — while the node that references it, if itself uncalled, still is.
func TestOrphansExcludesValueReferenced(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym, kind string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: "f.go", Symbol: sym, FQN: "p." + sym, Kind: kind, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	file := mk("", KindFile)
	handler := mk("handler", KindFunction) // wired by value, never called
	mk("lonely", KindFunction)             // truly dead
	// The file references handler as a value (e.g. `RunE: handler`) — a
	// references edge, distinct from a calls edge.
	if _, err := s.AddEdge(file, handler, EdgeReferences, WeightLSP); err != nil {
		t.Fatal(err)
	}

	orph, err := s.Orphans(pid, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range orph {
		got[n.Symbol] = true
	}
	if got["handler"] {
		t.Error("handler is referenced as a value (references edge) — must not be an orphan")
	}
	if !got["lonely"] {
		t.Errorf("lonely has no callers/references — should be an orphan, got %v", got)
	}
}

func TestSearchSymbols(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(sym string) {
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: sym + ".go", Symbol: sym, FQN: "p." + sym, Kind: KindFunction, Language: "go", SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("Authenticate")
	mk("authMiddleware")
	mk("Render")
	if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: "x.go", Kind: KindFile, Language: "go", SourceHash: "h"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.SearchSymbols(pid, "auth", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range res {
		got[n.Symbol] = true
	}
	if !got["Authenticate"] || !got["authMiddleware"] {
		t.Errorf("search 'auth' = %v, want Authenticate + authMiddleware (case-insensitive)", got)
	}
	if got["Render"] {
		t.Error("Render should not match 'auth'")
	}
}

func TestAnnotations(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")

	n1, err := s.AddAnnotation(pid, Annotation{Kind: AnnotationNode, Target: "p.Run", Source: "note", Note: "hot path"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAnnotation(pid, Annotation{Kind: AnnotationNode, Target: "p.Run", Source: "postgres", Data: `{"rows":42}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAnnotation(pid, Annotation{Kind: AnnotationPath, Target: "p.Top -> p.Run", Note: "entry chain"}); err != nil {
		t.Fatal(err)
	}

	// by target: both node annotations on p.Run.
	got, err := s.AnnotationsByTarget(pid, AnnotationNode, "p.Run")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("node annotations on p.Run = %d, want 2", len(got))
	}
	if got[1].Source != "postgres" || got[1].Data != `{"rows":42}` {
		t.Errorf("opaque data not round-tripped: %+v", got[1])
	}

	// all: 3 (2 node + 1 path).
	all, _ := s.AllAnnotations(pid)
	if len(all) != 3 {
		t.Errorf("all annotations = %d, want 3", len(all))
	}

	// delete one; the other node annotation remains.
	ok, err := s.DeleteAnnotation(pid, n1)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	got, _ = s.AnnotationsByTarget(pid, AnnotationNode, "p.Run")
	if len(got) != 1 {
		t.Errorf("after delete, node annotations = %d, want 1", len(got))
	}
}

func TestSymbolInfoIndex(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "Run", FQN: "p.Run",
		Kind: KindFunction, Language: "go", Signature: "func Run(x int) error", Docstring: "Run does the thing.", SourceHash: "h"}); err != nil {
		t.Fatal(err)
	}
	// a node with neither signature nor docstring must not appear in the index.
	if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "blank", FQN: "p.blank",
		Kind: KindFunction, Language: "go", SourceHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	idx, err := s.SymbolInfoIndex(pid)
	if err != nil {
		t.Fatal(err)
	}
	if idx["p.Run"].Signature != "func Run(x int) error" || idx["p.Run"].Doc != "Run does the thing." {
		t.Errorf("SymbolInfoIndex[p.Run] = %+v, want signature+doc", idx["p.Run"])
	}
	if _, ok := idx["p.blank"]; ok {
		t.Error("nodes without a signature or docstring should be excluded from the index")
	}
}

func TestStats(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	a, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "a.go", Symbol: "A", Kind: KindFunction, Language: "go", SourceHash: "h"})
	b, _ := s.AddNode(&Node{ProjectID: pid, FilePath: "b.ts", Symbol: "B", Kind: KindClass, Language: "typescript", SourceHash: "h"})
	if _, err := s.AddEdge(a, b, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.Nodes != 2 {
		t.Errorf("nodes = %d, want 2", st.Nodes)
	}
	if st.Edges != 1 {
		t.Errorf("edges = %d, want 1", st.Edges)
	}
	if st.Files != 2 {
		t.Errorf("files = %d, want 2", st.Files)
	}
	if st.Languages["go"] != 1 || st.Languages["typescript"] != 1 {
		t.Errorf("languages = %v", st.Languages)
	}
	if st.Kinds[KindFunction] != 1 || st.Kinds[KindClass] != 1 {
		t.Errorf("kinds = %v", st.Kinds)
	}
}
