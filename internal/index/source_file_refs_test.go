package index

import (
	"context"
	"sort"
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
