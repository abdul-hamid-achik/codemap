package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// preciseFixture: three callers all invoke Run() on the SAME concrete type T1,
// while T2 and T3 also declare a Run(). Name-based resolution fans each caller's
// edge out to all three same-named methods, spuriously inflating T2.Run and
// T3.Run to in-degree 3; precise resolution routes all three to T1.Run only.
const preciseFixture = `package fix

type T1 struct{}
type T2 struct{}
type T3 struct{}

func (T1) Run() {}
func (T2) Run() {}
func (T3) Run() {}

func A() { var t T1; t.Run() }
func B() { var t T1; t.Run() }
func C() { var t T1; t.Run() }
`

// inDegree counts incoming `calls` edges to the node with the given FQN.
func inDegree(t *testing.T, g *graph.Store, pid int64, symbol, fqn string) int {
	t.Helper()
	nodes, err := g.FindNodesBySymbol(pid, symbol)
	if err != nil {
		t.Fatal(err)
	}
	var id int64 = -1
	for _, n := range nodes {
		if n.FQN == fqn {
			id = n.ID
		}
	}
	if id < 0 {
		t.Fatalf("no node with FQN %q (have %d %q nodes)", fqn, len(nodes), symbol)
	}
	var c int
	if err := g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls'", id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPreciseCollapsesNameFanout(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fix\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.go"), []byte(preciseFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, err := g.UpsertProject("fix", dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	// Name-based baseline: every Run method is spuriously credited with all three
	// callers (the over-matching this epic eliminates).
	if _, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T1.Run"); got != 3 {
		t.Errorf("name-based T1.Run in-degree = %d, want 3", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 3 {
		t.Errorf("name-based T2.Run in-degree = %d, want 3 (spurious fan-out baseline)", got)
	}

	// Precise pass: the three calls resolve to T1.Run alone; T2/T3 deflate to 0.
	res, err := ix.IndexProject(context.Background(), pid, "fix", dir, Options{Reindex: true, Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseNote != "" {
		t.Fatalf("precise pass should have run, got note: %q", res.PreciseNote)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatal("PreciseUpgraded should be > 0 (a silent inert run means the join missed)")
	}
	if got := inDegree(t, g, pid, "Run", "fix.T1.Run"); got != 3 {
		t.Errorf("precise T1.Run in-degree = %d, want 3 (real callers preserved)", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T2.Run"); got != 0 {
		t.Errorf("precise T2.Run in-degree = %d, want 0 (spurious fan-out eliminated)", got)
	}
	if got := inDegree(t, g, pid, "Run", "fix.T3.Run"); got != 0 {
		t.Errorf("precise T3.Run in-degree = %d, want 0", got)
	}

	// Double-counting guard: T1.Run must have exactly 3 calls edges total (one per
	// caller), not 6 — i.e. the name edges were deleted before precise re-insert,
	// not left to coexist with the precise 1.0 edges.
	nodes, _ := g.FindNodesBySymbol(pid, "Run")
	var t1 int64 = -1
	for _, n := range nodes {
		if n.FQN == "fix.T1.Run" {
			t1 = n.ID
		}
	}
	var total int
	if err := g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='calls'", t1).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("T1.Run total calls edges = %d, want 3 (no name/precise double-count)", total)
	}
}

func TestPreciseDegradesGracefully(t *testing.T) {
	// A non-module dir (no go.mod): --precise must degrade to name-based with a
	// note, never error, and never wipe the name edges it can't replace.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"),
		[]byte("package x\n\nfunc Helper() {}\nfunc Run() { Helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("x", dir, "go")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "x", dir, Options{Precise: true})
	if err != nil {
		t.Fatalf("precise on a non-module dir should not error, got %v", err)
	}
	if res.PreciseNote == "" {
		t.Error("expected a precise note explaining the degrade")
	}
	// Name-based edge Run->Helper must survive (precise couldn't supersede it).
	if got := inDegree(t, g, pid, "Helper", "x.Helper"); got != 1 {
		t.Errorf("name-based Helper in-degree = %d, want 1 (kept after degrade)", got)
	}
}
