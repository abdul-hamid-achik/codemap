package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

func seedVectorCollection(t *testing.T, dims int) {
	t.Helper()
	seed, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	seed.SetEmbedder(fakeEmbedder{dims: dims})
	seedStore, err := seed.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	v := make([]float32, dims)
	v[0] = 1
	if _, err := seedStore.Insert(v, "seed", vector.NodeMeta{
		NodeID: 1, Project: "p", File: "a.go", Symbol: "A",
	}); err != nil {
		t.Fatal(err)
	}
	if err := seedStore.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestMaintenanceVectorsDoNotBypassProfileGuard pins the long-lived-session
// boundary: cleanup may open an incompatible collection without validation, but
// that wrapper must never satisfy a later normal query/insert open.
func TestMaintenanceVectorsDoNotBypassProfileGuard(t *testing.T) {
	isolate(t)
	seedVectorCollection(t, 4)

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	sess.SetEmbedder(fakeEmbedder{dims: 8}) // incompatible with stored dim=4 profile
	maintenance, err := sess.VectorsForMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	if maintenance == nil {
		t.Fatal("existing collection should open for maintenance")
	}
	if sess.vectors != nil || sess.vectorsMaintenance != maintenance {
		t.Fatal("maintenance wrapper leaked into the profile-validated cache")
	}

	assertIncompatible := func(label string, err error) {
		t.Helper()
		var incompatible *embed.IncompatibleError
		if !errors.As(err, &incompatible) {
			t.Fatalf("%s error = %v, want *embed.IncompatibleError", label, err)
		}
	}
	_, err = sess.Vectors()
	assertIncompatible("Vectors after maintenance", err)
	_, err = sess.VectorsReadOnly()
	assertIncompatible("VectorsReadOnly after maintenance", err)
	if sess.vectors != nil || sess.vectorsRO != nil {
		t.Fatal("failed validated opens must not populate validated caches")
	}

	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if sess.vectorsMaintenance != nil || sess.vecSession != nil {
		t.Fatal("Session.Close retained maintenance/vector-session handles")
	}

	// Closing the maintenance session must release its RW database/lock so a
	// compatible session can immediately reopen the collection normally.
	compatible, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer compatible.Close()
	compatible.SetEmbedder(fakeEmbedder{dims: 4})
	readOnly, err := compatible.VectorsReadOnly()
	if err != nil {
		t.Fatalf("compatible reopen after maintenance close: %v", err)
	}
	if _, err := compatible.VectorsForMaintenance(); err != nil {
		t.Fatalf("upgrade cached read-only handle for maintenance: %v", err)
	}
	if compatible.vectorsRO != nil {
		t.Fatal("maintenance RW upgrade retained a wrapper around the closed RO database")
	}
	revalidated, err := compatible.VectorsReadOnly()
	if err != nil {
		t.Fatalf("revalidate read path after maintenance upgrade: %v", err)
	}
	if revalidated == readOnly {
		t.Fatal("read path reused the invalidated pre-maintenance wrapper")
	}
}

func TestReleaseMaintenanceVectorsKeepsValidatedWriter(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 4})
	validated, err := sess.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := sess.VectorsForMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	if maintenance != validated {
		t.Fatal("maintenance should reuse a pre-existing validated writer")
	}
	if err := sess.ReleaseMaintenanceVectors(); err != nil {
		t.Fatal(err)
	}
	if sess.vectors != validated {
		t.Fatal("maintenance release closed or cleared the validated writer")
	}
	if _, err := validated.Insert([]float32{1, 0, 0, 0}, "still open", vector.NodeMeta{Project: "p"}); err != nil {
		t.Fatalf("validated writer unusable after maintenance release: %v", err)
	}
}

func TestNoEmbedIndexReleasesMaintenanceLock(t *testing.T) {
	for _, tc := range []struct {
		name     string
		canceled bool
	}{
		{name: "success"},
		{name: "index error", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			seedVectorCollection(t, 4)
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			longLived, err := Open("")
			if err != nil {
				t.Fatal(err)
			}
			defer longLived.Close()
			longLived.SetEmbedder(fakeEmbedder{dims: 8}) // maintenance must bypass dim-4 profile
			ctx := context.Background()
			if tc.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, indexErr := NewService(longLived).Index(ctx, project, index.Options{}, false)
			if tc.canceled && indexErr == nil {
				t.Fatal("canceled index should fail")
			}
			if !tc.canceled && indexErr != nil {
				t.Fatalf("no-embed index: %v", indexErr)
			}
			if longLived.vectorsMaintenance != nil {
				t.Fatal("Index retained the cleanup-only vector wrapper")
			}

			// The first Session remains alive. A compatible second session opening
			// now proves the cleanup RW flock was released on this return path.
			other, err := Open("")
			if err != nil {
				t.Fatal(err)
			}
			other.SetEmbedder(fakeEmbedder{dims: 4})
			if _, err := other.VectorsReadOnly(); err != nil {
				_ = other.Close()
				t.Fatalf("second session blocked before long-lived Session.Close: %v", err)
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
