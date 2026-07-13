//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestStalenessSurfacesDoNotBlockOnIndexedFIFO(t *testing.T) {
	t.Run("ordinary status", func(t *testing.T) {
		svc, root := setupIndexedFIFO(t)
		type outcome struct {
			st  *index.Staleness
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			st, err := svc.Staleness(root)
			done <- outcome{st: st, err: err}
		}()
		select {
		case got := <-done:
			if got.err != nil || got.st == nil || got.st.Any() {
				t.Fatalf("ordinary FIFO staleness = %+v err=%v, want conservative zero result", got.st, got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ordinary staleness blocked while opening FIFO")
		}
	})

	t.Run("strict manifest", func(t *testing.T) {
		svc, root := setupIndexedFIFO(t)
		type outcome struct {
			report *StructuralManifestReport
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			report, err := svc.StructuralManifest(root)
			done <- outcome{report: report, err: err}
		}()
		select {
		case got := <-done:
			if got.err == nil {
				t.Fatalf("manifest certified FIFO as fresh: %+v", got.report)
			}
			if !strings.Contains(got.err.Error(), "read indexed file sample.go") || !strings.Contains(got.err.Error(), "non-regular") {
				t.Fatalf("manifest FIFO error = %v, want contextual non-regular-file failure", got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("structural manifest blocked while opening FIFO")
		}
	})
}

func setupIndexedFIFO(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("create FIFO: %v", err)
	}
	return svc, root
}
