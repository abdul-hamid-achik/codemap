package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	type change struct{ toIndex, toRemove []string }
	changes := make(chan change, 32)
	w, err := NewWatcher(dir, WatchConfig{
		Debounce: 80 * time.Millisecond,
		Excluded: func(name string) bool { return name == "node_modules" },
	}, func(toIndex, toRemove []string) {
		changes <- change{toIndex, toRemove}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(60 * time.Millisecond) // let the watch loop start

	contains := func(ss []string, want string) bool {
		for _, s := range ss {
			if s == want {
				return true
			}
		}
		return false
	}
	waitFor := func(desc string, pred func(c change) bool) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case c := <-changes:
				if pred(c) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", desc)
			}
		}
	}
	settle := func() { // let any in-flight flush land, then drain
		time.Sleep(160 * time.Millisecond)
		for {
			select {
			case <-changes:
			default:
				return
			}
		}
	}

	// Create a new source file → reported to index.
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("create b.go", func(c change) bool { return contains(c.toIndex, "b.go") })
	settle()

	// Modify it → reported to index again.
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package m\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("modify b.go", func(c change) bool { return contains(c.toIndex, "b.go") })
	settle()

	// Delete it → reported to remove.
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	waitFor("delete b.go", func(c change) bool { return contains(c.toRemove, "b.go") })
	settle()

	// A file inside an excluded dir is never reported; a sibling source file is.
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "dep.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte("package m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("create c.go (excluded node_modules ignored)", func(c change) bool {
		if contains(c.toIndex, filepath.Join("node_modules", "dep.go")) {
			t.Errorf("a file under an excluded dir must not be reported")
		}
		return contains(c.toIndex, "c.go")
	})
}
