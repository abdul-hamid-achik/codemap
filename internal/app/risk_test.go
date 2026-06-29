package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func riskProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	// Risky: 6 callers, no test → untested + high fan-in. Safe: 1 caller, covered.
	a := "package app\n\nfunc Risky() {}\n\nfunc Safe() {}\n\nfunc UsesSafe() { Safe() }\n" +
		"func C1() { Risky() }\nfunc C2() { Risky() }\nfunc C3() { Risky() }\n" +
		"func C4() { Risky() }\nfunc C5() { Risky() }\nfunc C6() { Risky() }\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	test := "package app\n\nimport \"testing\"\n\nfunc TestSafe(t *testing.T) { Safe() }\n"
	if err := os.WriteFile(filepath.Join(proj, "a_test.go"), []byte(test), 0o644); err != nil {
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

func hasFactor(fs []RiskFactor, name string) bool {
	for _, f := range fs {
		if f.Factor == name {
			return true
		}
	}
	return false
}

func TestRiskUntestedHub(t *testing.T) {
	svc, proj := riskProj(t)
	rep, err := svc.Risk(proj, "Risky", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Risky should be found")
	}
	if !hasFactor(rep.Factors, "untested") {
		t.Errorf("an untested symbol should carry the untested factor, got %+v", rep.Factors)
	}
	if !hasFactor(rep.Factors, "high_fan_in") && !hasFactor(rep.Factors, "fan_in") {
		t.Errorf("a 6-caller symbol should carry a fan-in factor, got %+v", rep.Factors)
	}
	if rep.Level != "high" {
		t.Errorf("untested + high fan-in should be high risk, got %s (%.2f)", rep.Level, rep.Score)
	}
}

func TestRiskCoveredLeafIsLow(t *testing.T) {
	svc, proj := riskProj(t)
	rep, err := svc.Risk(proj, "Safe", 3)
	if err != nil {
		t.Fatal(err)
	}
	if hasFactor(rep.Factors, "untested") {
		t.Errorf("Safe is covered by TestSafe — it must not be flagged untested")
	}
	if rep.Level != "low" {
		t.Errorf("a covered, low-fan-in symbol should be low risk, got %s (%.2f)", rep.Level, rep.Score)
	}
}

func TestRiskCombineSaturates(t *testing.T) {
	// Probabilistic-OR never exceeds 1 even with strong, multiple factors.
	got := combineRisk([]RiskFactor{{Severity: 0.9}, {Severity: 0.9}, {Severity: 0.9}})
	if got <= 0.9 || got > 1 {
		t.Errorf("combined risk = %.3f, want (0.9, 1]", got)
	}
	if combineRisk(nil) != 0 {
		t.Errorf("no factors → 0 risk")
	}
}

func TestRiskUnindexed(t *testing.T) {
	isolate(t)
	sess, _ := Open("")
	defer sess.Close()
	rep, err := NewService(sess).Risk(t.TempDir(), "X", 3)
	if err != nil {
		t.Fatalf("unindexed must not error: %v", err)
	}
	if rep.Found {
		t.Errorf("unindexed → found:false, got %+v", rep)
	}
}
