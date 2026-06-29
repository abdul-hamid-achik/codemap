package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func contextBatchProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	// Shared() calls both A and B, so it's a common caller of the {A,B} batch.
	src := "package app\n\nfunc A() {}\n\nfunc B() {}\n\nfunc Shared() {\n\tA()\n\tB()\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func TestContextBatchSharedCaller(t *testing.T) {
	svc, proj := contextBatchProj(t)
	rep, err := svc.ContextBatch(proj, []string{"A", "B", "DoesNotExist"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requested != 3 || len(rep.Results) != 3 {
		t.Fatalf("expected 3 requested + 3 results, got requested=%d results=%d", rep.Requested, len(rep.Results))
	}
	if !contains(rep.NotFound, "DoesNotExist") {
		t.Errorf("DoesNotExist should be in not_found, got %v", rep.NotFound)
	}
	// Shared() calls both A and B → it is a common caller.
	if !hasSymbol(rep.CommonCallers, "Shared") {
		t.Errorf("Shared should be a common caller of {A,B}, got %+v", rep.CommonCallers)
	}
	if rep.CombinedBlastRadius <= 0 {
		t.Errorf("combined blast radius should be > 0 for two called symbols, got %d", rep.CombinedBlastRadius)
	}
}

func TestContextBatchDedupAndUnindexed(t *testing.T) {
	svc, proj := contextBatchProj(t)
	// Duplicate symbols collapse to one result.
	rep, err := svc.ContextBatch(proj, []string{"A", "A", ""}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Errorf("duplicate + blank symbols should dedup to 1 result, got %d", len(rep.Results))
	}

	isolate(t)
	sess, _ := Open("")
	defer sess.Close()
	un, err := NewService(sess).ContextBatch(t.TempDir(), []string{"X"}, 3)
	if err != nil {
		t.Fatalf("unindexed must not error: %v", err)
	}
	if un.Indexed {
		t.Errorf("unindexed → indexed:false, got %+v", un)
	}
}

func TestContextBatchCapsAt25(t *testing.T) {
	svc, proj := contextBatchProj(t)
	syms := make([]string, 30) // mostly nonexistent — cheap, they land in not_found
	for i := range syms {
		syms[i] = fmt.Sprintf("Sym%d", i)
	}
	rep, err := svc.ContextBatch(proj, syms, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) > contextBatchMax {
		t.Errorf("batch should cap results at %d, got %d", contextBatchMax, len(rep.Results))
	}
	if !strings.Contains(rep.Note, "analyzed the first") {
		t.Errorf("a >25 batch should note the elision, got %q", rep.Note)
	}
}
