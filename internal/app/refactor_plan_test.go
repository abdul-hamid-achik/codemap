package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func refactorPlanProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	// a.go defines Helper (called by Run) and Run. b.go's Other calls Run, so
	// b.go depends on a.go — a move site for a.go's symbols.
	a := "package app\n\nfunc Helper() {}\n\nfunc Run() { Helper() }\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	b := "package app\n\nfunc Other() { Run() }\n"
	if err := os.WriteFile(filepath.Join(proj, "b.go"), []byte(b), 0o644); err != nil {
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

func TestRefactorPlan(t *testing.T) {
	svc, proj := refactorPlanProj(t)
	rep, err := svc.RefactorPlan(context.Background(), proj, "Helper", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Helper should be found")
	}
	if len(rep.Definitions) == 0 {
		t.Error("expected at least one definition site")
	}
	// Run calls Helper → a call site a rename must update.
	if rep.CallSitesTotal < 1 {
		t.Errorf("CallSitesTotal = %d, want >= 1 (Run calls Helper)", rep.CallSitesTotal)
	}
	var foundRun bool
	for _, c := range rep.CallSites {
		if c.Symbol == "Run" {
			foundRun = true
		}
	}
	if !foundRun {
		t.Errorf("call sites should include Run, got %+v", rep.CallSites)
	}
	// b.go depends on a.go (Other calls Run) → a move site for a.go's symbols.
	var foundB bool
	for _, m := range rep.MoveSites {
		if filepath.Base(m) == "b.go" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("move sites should include b.go (depends on a.go), got %v", rep.MoveSites)
	}
	// Blast radius: Run + Other transitively call Helper.
	if rep.BlastRadius < 2 {
		t.Errorf("BlastRadius = %d, want >= 2 (Run, Other)", rep.BlastRadius)
	}
}

func TestRefactorPlanNotFound(t *testing.T) {
	svc, proj := refactorPlanProj(t)
	rep, err := svc.RefactorPlan(context.Background(), proj, "Nope", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found {
		t.Error("Nope should not be found")
	}
}
