package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

func TestPruneOrphanVectors(t *testing.T) {
	isolate(t)

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	ir, err := svc.Init(proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(ir.Project)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := g.ProjectNodes(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var runNode *graph.Node
	for i := range nodes {
		if nodes[i].Symbol == "Run" {
			runNode = &nodes[i]
		}
	}
	if runNode == nil {
		t.Fatal("Run node not indexed")
	}

	vec, err := sess.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	dims := sess.Embedder().Profile().Dimensions
	mkVec := func() []float32 {
		v := make([]float32, dims)
		v[0] = 1
		return v
	}
	meta := func(project string, nodeID int64) vector.NodeMeta {
		return vector.NodeMeta{NodeID: nodeID, Project: project, File: "main.go", Symbol: "Run", Kind: "function", Language: "go", StartLine: 3, EndLine: 3}
	}
	validID, err := vec.Insert(mkVec(), "func Run() {}", meta(ir.Project, runNode.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vec.Insert(mkVec(), "orphan same project", meta(ir.Project, 999999)); err != nil {
		t.Fatal(err)
	}
	if _, err := vec.Insert(mkVec(), "orphan ghost project", meta("ghost", 42)); err != nil {
		t.Fatal(err)
	}
	tx, err := g.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.UpdateNodeVecIDTx(tx, runNode.ID, strconv.FormatUint(validID, 10)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := vec.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := sess.ReleaseVectors(); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.PruneOrphanVectors(true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.StorePresent || rep.Scanned != 3 || rep.Orphans != 2 || rep.Deleted != 0 {
		t.Fatalf("dry-run report = %+v, want scanned 3, orphans 2, deleted 0", rep)
	}

	rep, err = svc.PruneOrphanVectors(false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (%+v)", rep.Deleted, rep)
	}
	if n, ok := svc.embeddedCount(ir.Project); !ok || n != 1 {
		t.Fatalf("embeddedCount after GC = (%d, %v), want the 1 valid record", n, ok)
	}

	// A second run finds nothing left to do.
	rep, err = svc.PruneOrphanVectors(false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Orphans != 0 || rep.Scanned != 1 {
		t.Fatalf("second GC = %+v, want 1 scanned, 0 orphans", rep)
	}
}
