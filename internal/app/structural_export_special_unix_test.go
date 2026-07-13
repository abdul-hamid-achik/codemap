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

func TestStructuralExportOmitsIndexedFIFOsWithoutBlocking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		viaLink bool
	}{
		{name: "direct"},
		{name: "symlink", viaLink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root, sourcePath := setupStructuralExportSource(t, tc.viaLink)
			if err := os.Remove(sourcePath); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(sourcePath, 0o600); err != nil {
				t.Skipf("create FIFO: %v", err)
			}

			type outcome struct {
				report *StructuralExportReport
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				report, err := svc.StructuralExport(root, StructuralExportOptions{})
				done <- outcome{report: report, err: err}
			}()
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("structural export with FIFO: %v", got.err)
				}
				if len(got.report.Records) != 1 {
					t.Fatalf("FIFO export records = %+v, want one indexed symbol", got.report.Records)
				}
				record := got.report.Records[0]
				if !record.ContentOmitted || record.OmissionReason != "file_unreadable" || record.Content != "" {
					t.Fatalf("FIFO record = %+v, want file_unreadable omission", record)
				}
				assertStructuralExportSchema(t, got.report)
			case <-time.After(3 * time.Second):
				t.Fatal("structural export blocked while opening FIFO")
			}
		})
	}
}

func TestStructuralExportReadsRegularFileSymlink(t *testing.T) {
	svc, root, _ := setupStructuralExportSource(t, true)
	report, err := svc.StructuralExport(root, StructuralExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Records) != 1 {
		t.Fatalf("regular-file symlink records = %+v, want one indexed symbol", report.Records)
	}
	record := report.Records[0]
	if record.ContentOmitted || record.ContentHash == "" || !strings.Contains(record.Content, "func Alpha() {}") {
		t.Fatalf("regular-file symlink content was not exported: %+v", record)
	}
}

func setupStructuralExportSource(t *testing.T, viaLink bool) (*Service, string, string) {
	t.Helper()
	isolate(t)
	root := t.TempDir()
	indexedPath := filepath.Join(root, "sample.go")
	sourcePath := indexedPath
	if viaLink {
		sourcePath = filepath.Join(root, "sample.source")
	}
	if err := os.WriteFile(sourcePath, []byte("package sample\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if viaLink {
		if err := os.Symlink(sourcePath, indexedPath); err != nil {
			t.Skipf("create source symlink: %v", err)
		}
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
	return svc, root, sourcePath
}
