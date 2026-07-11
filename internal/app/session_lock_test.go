package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestEmbeddedIndexReleasesWriterOnEveryReturnPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		canceled bool
	}{
		{name: "success"},
		{name: "index error", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			longLived, err := Open("")
			if err != nil {
				t.Fatal(err)
			}
			defer longLived.Close()
			longLived.SetEmbedder(fakeEmbedder{dims: 4})
			ctx := context.Background()
			if tc.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, indexErr := NewService(longLived).Index(ctx, project, index.Options{}, true)
			if tc.canceled && indexErr == nil {
				t.Fatal("canceled embedded index should fail")
			}
			if !tc.canceled && indexErr != nil {
				t.Fatalf("embedded index: %v", indexErr)
			}
			if longLived.vectors != nil {
				t.Fatal("Index retained its profile-validated writer")
			}

			// The service remains alive, but its completed/failed index no longer
			// excludes an independent owner.
			other, err := Open("")
			if err != nil {
				t.Fatal(err)
			}
			other.SetEmbedder(fakeEmbedder{dims: 4})
			if _, err := other.Vectors(); err != nil {
				_ = other.Close()
				t.Fatalf("independent writer blocked after index return: %v", err)
			}
			if err := other.ReleaseVectors(); err != nil {
				t.Fatal(err)
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}

			// Both read and write paths must remain reusable after cleanup,
			// including after the canceled/error path.
			if _, err := longLived.VectorsReadOnly(); err != nil {
				t.Fatalf("read after index cleanup: %v", err)
			}
			if _, err := NewService(longLived).Index(context.Background(), project, index.Options{}, true); err != nil {
				t.Fatalf("subsequent index after cleanup: %v", err)
			}
			if _, err := longLived.VectorsReadOnly(); err != nil {
				t.Fatalf("read after subsequent index: %v", err)
			}
		})
	}
}
