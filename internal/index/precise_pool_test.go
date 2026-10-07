package index

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

func TestPreciseServerCount(t *testing.T) {
	cases := []struct{ configured, files, want int }{
		{0, 10_000, 1}, // default: serial
		{1, 5000, 1},
		{3, 5000, 3},
		{8, 2, 2}, // never more processes than files
		{4, 0, 1},
	}
	for _, c := range cases {
		if got := preciseServerCount(c.configured, c.files); got != c.want {
			t.Errorf("preciseServerCount(%d, %d) = %d, want %d", c.configured, c.files, got, c.want)
		}
	}
}

func TestProjectGroupsLargestFirst(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "web/tsconfig.json", "{}")
	writeFile(t, root, "api/package.json", "{}")
	names := []string{"web/a.ts", "web/sub/b.ts", "api/c.js", "scripts/e.js", "web/f.ts", "api/lib/d.js"}
	groups := projectGroups(root, func(i int) string { return names[i] }, []int{0, 1, 2, 3, 4, 5})
	got := make([][]string, len(groups))
	for g, idx := range groups {
		for _, i := range idx {
			got[g] = append(got[g], names[i])
		}
	}
	want := [][]string{{"web/a.ts", "web/sub/b.ts", "web/f.ts"}, {"api/c.js", "api/lib/d.js"}, {"scripts/e.js"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

// Real servers: the same project resolved on one process and on two must
// produce the same precise graph.
func TestPreciseParallelServersMatchSerial(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	dir := t.TempDir()
	tsconfig := `{"compilerOptions":{"strict":true,"module":"commonjs"}}`
	writeFile(t, dir, "pkg-a/tsconfig.json", tsconfig)
	writeFile(t, dir, "pkg-a/util.ts", "export function helper() { return 1; }\nexport function twice() { return helper() + helper(); }\n")
	writeFile(t, dir, "pkg-a/main.ts", "import { twice } from \"./util\";\nexport function run() { return twice(); }\n")
	writeFile(t, dir, "pkg-b/tsconfig.json", tsconfig)
	writeFile(t, dir, "pkg-b/lib.ts", "export function leaf() { return 2; }\nexport function mid() { return leaf(); }\n")
	writeFile(t, dir, "pkg-b/app.ts", "import { mid } from \"./lib\";\nexport function start() { return mid(); }\n")

	run := func(servers int) ([]string, *Result) {
		t.Helper()
		g, v := newStores(t)
		pid, _ := g.UpsertProject("p", dir, "typescript")
		cfg := config.DefaultConfig().Index
		cfg.PreciseServers = servers
		ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
		defer func() { _ = ix.Close() }()
		res, err := ix.IndexProject(context.Background(), pid, "p", dir, Options{Precise: true})
		if err != nil {
			t.Fatal(err)
		}
		return callEdgeSet(t, g, pid), res
	}
	serial, _ := run(1)
	parallel, res := run(2)
	if !strings.Contains(res.PreciseNote, "forked 1 extra language-server process") {
		t.Fatalf("the pass did not fork: note %q", res.PreciseNote)
	}
	if !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("parallel graph differs:\nserial   %v\nparallel %v", serial, parallel)
	}
	precise := 0
	for _, e := range parallel {
		if strings.Contains(e, "|precise|") {
			precise++
		}
	}
	if precise < 4 {
		t.Fatalf("want precise edges from both packages, got %v", parallel)
	}
}

func TestSplitOversizedSpreadsOneBigProject(t *testing.T) {
	one := [][]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}}
	got := splitOversized(one, 3)
	if len(got) != 3 {
		t.Fatalf("a single project must spread over k=3 queues: %v", got)
	}
	n := 0
	for _, g := range got {
		n += len(g)
		if len(g) > 4 {
			t.Fatalf("chunk larger than the fair share: %v", got)
		}
	}
	if n != 10 {
		t.Fatalf("files lost: %v", got)
	}
	balanced := [][]int{{0, 1}, {2, 3}}
	if got := splitOversized(balanced, 2); len(got) != 2 {
		t.Fatalf("fair groups stay whole: %v", got)
	}
}
