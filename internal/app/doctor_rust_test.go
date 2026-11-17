package app

import (
	"context"
	"os/exec"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/tooling"
)

// TestDoctorRustAnalyzerAgreement pins that doctor reports the rust-analyzer
// check (it comes from lspsrc.DefaultServers, so CLI JSON, MCP status, and
// doctor share one source of truth) and that its OK verdict agrees with the
// same asdf-shim-safe probe the indexer uses — not with a naive LookPath,
// which counts a dead shim as installed.
func TestDoctorRustAnalyzerAgreement(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetEmbedder(fakeEmbedder{dims: 8})

	rep := NewService(sess).Doctor(context.Background())
	var check *DoctorCheck
	for i := range rep.Checks {
		if rep.Checks[i].Name == "rust-analyzer (rust)" {
			check = &rep.Checks[i]
		}
	}
	if check == nil {
		t.Fatalf("doctor checks missing rust-analyzer (rust); got %v", checkNames(rep))
	}

	// The authoritative availability signal is the same probe registerLSP uses.
	pr := tooling.Probe(context.Background(), "rust-analyzer", "")
	_, lookErr := exec.LookPath("rust-analyzer")
	if pr.OK && !check.OK {
		t.Errorf("probe says rust-analyzer usable but doctor check is not OK: %+v", check)
	}
	if !pr.OK && check.OK {
		t.Errorf("doctor says OK but probe failed (%v) — dead shim must report as missing", pr.Err)
	}
	if lookErr != nil && check.OK {
		t.Errorf("rust-analyzer not on PATH at all but doctor reports OK: %+v", check)
	}
	if !check.OK && check.Hint == "" {
		t.Error("a failing rust-analyzer check must carry an install hint (rustup component add rust-analyzer)")
	}
}

func checkNames(rep *DoctorReport) []string {
	out := make([]string, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		out = append(out, c.Name)
	}
	return out
}
