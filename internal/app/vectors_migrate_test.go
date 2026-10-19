package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// An embedded index migrates a project's records out of the legacy shared
// store into its per-project shard (once): valid records are copied, the
// graph is re-pointed at the shard ids, the legacy copies are deleted, and
// later runs open only the shard.
func TestEmbeddedIndexMigratesLegacyVectorsToShard(t *testing.T) {
	isolate(t)

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 4})
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
	legacy, err := sess.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	meta := vector.NodeMeta{NodeID: runNode.ID, Project: ir.Project, File: "main.go", Symbol: "Run", Kind: "function", Language: "go", StartLine: 3, EndLine: 3}
	legacyID, err := legacy.Insert([]float32{1, 0, 0, 0}, "func Run() { Helper() }", meta)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := g.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.UpdateNodeVecIDTx(tx, runNode.ID, strconv.FormatUint(legacyID, 10)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := sess.ReleaseVectors(); err != nil {
		t.Fatal(err)
	}
	scope := vectorScope(p)
	if projectShardExists(scope) {
		t.Fatal("shard should not exist before migration")
	}

	// Embedded reindex after a touch: migration runs, then embedding lands
	// in the shard.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// Run it.\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Index(context.Background(), proj, index.Options{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Embedded {
		t.Fatalf("expected embedded index, warning=%q", rep.Warning)
	}
	if !projectShardExists(scope) {
		t.Fatal("shard was not created by the embedded index")
	}
	if _, err := os.Stat(config.VecliteProjectPath(scope)); err != nil {
		t.Fatal(err)
	}

	// Graph pointers name records that live in the shard; the legacy store
	// no longer carries this project.
	shard, err := sess.VectorsForProjectMaintenance(scope)
	if err != nil || shard == nil {
		t.Fatalf("shard maintenance = (%v, %v)", shard, err)
	}
	shardRecs, err := shard.IterByProject(ir.Project)
	if err != nil || len(shardRecs) == 0 {
		t.Fatalf("shard records = (%d, %v)", len(shardRecs), err)
	}
	owners, err := g.VecIDOwners()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range shardRecs {
		owner, ok := owners[strconv.FormatUint(r.ID, 10)]
		if !ok || owner.NodeID != r.Meta.NodeID {
			t.Errorf("shard record %d (node %d) not claimed by graph owners", r.ID, r.Meta.NodeID)
		}
	}
	_ = sess.ReleaseProjectMaintenanceVectors(scope)

	legacyAfter, err := sess.VectorsForMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	if legacyAfter != nil {
		left, err := legacyAfter.IterByProject(ir.Project)
		if err != nil || len(left) != 0 {
			t.Fatalf("legacy records after migration = (%d, %v), want 0", len(left), err)
		}
		_ = sess.ReleaseMaintenanceVectors()
	}

	// Semantic search reads through the shard, not the legacy store.
	srep, err := svc.Semantic(context.Background(), proj, "Run", 5)
	if err != nil {
		t.Fatal(err)
	}
	foundRun := false
	for _, h := range srep.Hits {
		if h.Symbol == "Run" {
			foundRun = true
		}
	}
	if !foundRun {
		t.Fatalf("semantic over shard found no Run hit: %+v", srep.Hits)
	}
}
