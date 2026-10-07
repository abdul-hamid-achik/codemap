package index

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// callPairs lists every calls edge as "srcFile:srcSymbol -> tgtFile:tgtSymbol".
func callPairs(t *testing.T, g *graph.Store) []string {
	t.Helper()
	rows, err := g.DB().Query(`SELECT src.file_path, src.symbol, tgt.file_path, tgt.symbol
		FROM edges e JOIN nodes src ON e.source_id = src.id JOIN nodes tgt ON e.target_id = tgt.id
		WHERE e.edge_type = 'calls'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var sf, ss, tf, ts string
		if err := rows.Scan(&sf, &ss, &tf, &ts); err != nil {
			t.Fatal(err)
		}
		out = append(out, sf+":"+ss+" -> "+tf+":"+ts)
	}
	sort.Strings(out)
	return out
}

func indexDir(t *testing.T, dir string) *graph.Store {
	t.Helper()
	g, v := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	defer func() { _ = ix.Close() }()
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	return g
}

// Two `package main` programs share the FQN main.main. Each one's calls must
// stay on its own node — the old FQN-only source lookup put both programs'
// calls on one main.main, and the same-package filter then used that node's
// directory for both.
func TestSameFQNInTwoPackagesKeepsItsOwnCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cmd/alpha/main.go", "package main\n\nfunc main() {\n\talphaHelper()\n}\n\nfunc alphaHelper() {}\n")
	writeFile(t, dir, "cmd/beta/main.go", "package main\n\nfunc main() {\n\tbetaHelper()\n}\n\nfunc betaHelper() {}\n")

	got := callPairs(t, indexDir(t, dir))
	want := []string{
		"cmd/alpha/main.go:main -> cmd/alpha/main.go:alphaHelper",
		"cmd/beta/main.go:main -> cmd/beta/main.go:betaHelper",
	}
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

// A Go selector call never resolves to a same-named method in another
// language family (a TS class method), only to Go candidates.
func TestNameCallsStayInLanguageFamily(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go/kind.go", "package kind\n\ntype Kind int\n\nfunc (k Kind) String() string { return \"\" }\n\nfunc Describe(k Kind) string {\n\treturn k.String()\n}\n")
	writeFile(t, dir, "web/kind.ts", "export class DataType {\n  String(): string {\n    return \"\";\n  }\n}\n")

	found := false
	for _, p := range callPairs(t, indexDir(t, dir)) {
		switch p {
		case "go/kind.go:Describe -> web/kind.ts:String":
			t.Fatalf("Go call resolved into TypeScript: %s", p)
		case "go/kind.go:Describe -> go/kind.go:String":
			found = true
		}
	}
	if !found {
		t.Fatal("Go call to its own String method missing")
	}
}

// Under --precise the go/types edges also keep each main.main apart, across
// separate modules in a repo with no root go.mod.
func TestPreciseSameFQNAcrossModules(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	writeFile(t, dir, "alpha/go.mod", "module example.com/alpha\n\ngo 1.25\n")
	writeFile(t, dir, "alpha/main.go", "package main\n\nfunc main() {\n\talphaHelper()\n}\n\nfunc alphaHelper() {}\n")
	writeFile(t, dir, "beta/go.mod", "module example.com/beta\n\ngo 1.25\n")
	writeFile(t, dir, "beta/main.go", "package main\n\nfunc main() {\n\tbetaHelper()\n}\n\nfunc betaHelper() {}\n")

	g, v := newStores(t)
	pid, _ := g.UpsertProject("app", dir, "go")
	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	defer func() { _ = ix.Close() }()
	res, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	got := callPairs(t, g)
	want := []string{
		"alpha/main.go:main -> alpha/main.go:alphaHelper",
		"beta/main.go:main -> beta/main.go:betaHelper",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v (precise note: %q)", got, want, res.PreciseNote)
	}
	var precise int
	if err := g.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE edge_type='calls' AND provenance='precise'`).Scan(&precise); err != nil {
		t.Fatal(err)
	}
	if precise != 2 {
		t.Fatalf("precise call edges = %d, want 2 (note: %q)", precise, res.PreciseNote)
	}
}

// callHierarchy item ranges can start at a long JSDoc block; the callee's name
// line must still join the indexed declaration.
func TestLookupPreciseNodeJoinsOnNameLine(t *testing.T) {
	posTo := map[precisePos]int64{{"a.ts", 480}: 7}
	if _, ok := lookupPreciseNode(posTo, "a.ts", 473); ok {
		t.Fatal("a doc-comment start 7 lines up must not join by neighborhood")
	}
	if id, ok := lookupPreciseNode(posTo, "a.ts", 473, 480); !ok || id != 7 {
		t.Fatalf("name line join = %d %v, want 7", id, ok)
	}
	if id, ok := lookupPreciseNode(posTo, "a.ts", 479, 0); !ok || id != 7 {
		t.Fatalf("neighborhood fallback = %d %v, want 7", id, ok)
	}
}

func TestSamePositionWinnersKeepsTheOuterDeclaration(t *testing.T) {
	outer := graph.Node{ID: 1, FilePath: "a.ts", StartLine: 306, EndLine: 319}
	inner := graph.Node{ID: 2, FilePath: "a.ts", StartLine: 306, EndLine: 306}
	twinA := graph.Node{ID: 3, FilePath: "a.ts", StartLine: 400, EndLine: 400}
	twinB := graph.Node{ID: 4, FilePath: "a.ts", StartLine: 400, EndLine: 400}
	got := samePositionWinners(map[precisePos][]graph.Node{
		{"a.ts", 306}: {inner, outer},
		{"a.ts", 400}: {twinA, twinB},
	})
	if got[precisePos{"a.ts", 306}] != 1 {
		t.Fatalf("outer declaration must win line 306: %v", got)
	}
	if _, ok := got[precisePos{"a.ts", 400}]; ok {
		t.Fatalf("two same-width declarations on one line stay ambiguous: %v", got)
	}
}

func TestEnclosingCallablePicksInnermost(t *testing.T) {
	spans := []graph.Node{
		{ID: 1, StartLine: 10, EndLine: 100},
		{ID: 2, StartLine: 20, EndLine: 40},
	}
	if id, ok := enclosingCallable(spans, 25); !ok || id != 2 {
		t.Fatalf("enclosingCallable(25) = %d %v, want 2", id, ok)
	}
	if id, ok := enclosingCallable(spans, 50); !ok || id != 1 {
		t.Fatalf("enclosingCallable(50) = %d %v, want 1", id, ok)
	}
	if _, ok := enclosingCallable(spans, 5); ok {
		t.Fatal("a line outside every span has no owner")
	}
}
