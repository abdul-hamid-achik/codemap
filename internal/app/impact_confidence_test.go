package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func confidenceBySymbol(nodes []ImpactNode) map[string]string {
	out := map[string]string{}
	for _, n := range nodes {
		out[n.Symbol] = n.Confidence
	}
	return out
}

// reviewRepo call graph: Helper <- Run (same file a.go), Run <- Other (b.go) and
// Run <- TestRun (a_test.go). Name-based indexing, so only same-file edges are
// confirmed.
func TestImpactBucketsAndConfidence(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Impact(proj, "Helper", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Buckets == nil {
		t.Fatal("impact report has no buckets")
	}
	if rep.Buckets.DirectCount != 1 || rep.Buckets.TransitiveCount != 2 {
		t.Fatalf("bucket counts = %d direct / %d transitive, want 1/2 (%+v)", rep.Buckets.DirectCount, rep.Buckets.TransitiveCount, rep.Buckets)
	}
	if len(rep.Buckets.Direct) != 1 || rep.Buckets.Direct[0].Symbol != "Run" {
		t.Fatalf("direct bucket = %+v, want [Run]", rep.Buckets.Direct)
	}
	for _, n := range rep.Buckets.Transitive {
		if n.Depth < 2 {
			t.Errorf("transitive bucket holds depth %d node %s", n.Depth, n.Symbol)
		}
	}
	if len(rep.BlastRadius) != 3 {
		t.Fatalf("flat blast_radius must stay untouched, got %d nodes", len(rep.BlastRadius))
	}
	conf := confidenceBySymbol(rep.BlastRadius)
	if conf["Run"] != ConfidenceConfirmed {
		t.Errorf("Run (same-file edge) confidence = %q, want confirmed", conf["Run"])
	}
	if conf["Other"] != ConfidenceCandidate || conf["TestRun"] != ConfidenceCandidate {
		t.Errorf("cross-file name-based nodes = %v, want candidate", conf)
	}
	// The JSON carries both the buckets and the per-node confidence additively.
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"blast_radius", "buckets", "tests"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("impact JSON missing %q", key)
		}
	}
	if _, ok := doc["filtered"]; ok {
		t.Error("filtered must be absent unless min_confidence was applied")
	}
}

func TestImpactMinConfidenceConfirmedFiltersCandidates(t *testing.T) {
	svc, proj := reviewRepo(t)
	rep, err := svc.Impact(proj, "Helper", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.ApplyMinConfidence("confirmed"); err != nil {
		t.Fatal(err)
	}
	if len(rep.BlastRadius) != 1 || rep.BlastRadius[0].Symbol != "Run" {
		t.Fatalf("confirmed blast radius = %+v, want [Run]", rep.BlastRadius)
	}
	if rep.Buckets.DirectCount != 1 || rep.Buckets.TransitiveCount != 0 {
		t.Errorf("buckets after filter = %+v, want 1 direct / 0 transitive", rep.Buckets)
	}
	if len(rep.Tests) != 0 || len(rep.TestCommands) != 0 {
		t.Errorf("candidate tests must be filtered, got tests=%+v commands=%v", rep.Tests, rep.TestCommands)
	}
	if rep.Filtered == nil || rep.Filtered.Candidate != 2 || rep.MinConfidence != "confirmed" {
		t.Fatalf("filtered = %+v (min=%q), want candidate:2", rep.Filtered, rep.MinConfidence)
	}
	if len(rep.DirectCallers) != 1 || rep.DirectCallers[0].Symbol != "Run" {
		t.Errorf("direct callers = %+v, want [Run]", rep.DirectCallers)
	}

	// Run's only callers are cross-file name-based: everything is a candidate.
	run, err := svc.Impact(proj, "Run", 3)
	if err != nil {
		t.Fatal(err)
	}
	total := len(run.BlastRadius)
	if err := run.ApplyMinConfidence("confirmed"); err != nil {
		t.Fatal(err)
	}
	if len(run.BlastRadius) != 0 || len(run.DirectCallers) != 0 || len(run.Tests) != 0 {
		t.Errorf("Run has no confirmed nodes, got blast=%d callers=%d tests=%d", len(run.BlastRadius), len(run.DirectCallers), len(run.Tests))
	}
	if run.Filtered == nil || run.Filtered.Candidate != total {
		t.Errorf("filtered = %+v, want candidate:%d", run.Filtered, total)
	}
}

func TestImpactMinConfidenceDefaultsAndValidation(t *testing.T) {
	svc, proj := reviewRepo(t)
	for _, min := range []string{"", "candidate"} {
		rep, err := svc.Impact(proj, "Helper", 3)
		if err != nil {
			t.Fatal(err)
		}
		if err := rep.ApplyMinConfidence(min); err != nil {
			t.Fatalf("min_confidence %q: %v", min, err)
		}
		if len(rep.BlastRadius) != 3 || rep.Filtered != nil || rep.MinConfidence != "" {
			t.Errorf("min_confidence %q must leave the report unchanged, got blast=%d filtered=%+v", min, len(rep.BlastRadius), rep.Filtered)
		}
	}
	rep, err := svc.Impact(proj, "Helper", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.ApplyMinConfidence("probable"); err == nil {
		t.Error("an unknown min_confidence must be rejected")
	}
}

// After a precise index the cross-file edges are exact, so they are confirmed.
func TestImpactPreciseEdgesAreConfirmed(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "go.mod", "module example.com/m\n\ngo 1.25\n")
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true, Precise: true}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Impact(proj, "Run", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CallGraph != CallGraphResolved {
		t.Skipf("precise pass unavailable (call_graph=%s): %s", rep.CallGraph, rep.Resolution)
	}
	conf := confidenceBySymbol(rep.BlastRadius)
	if conf["Other"] != ConfidenceConfirmed || conf["TestRun"] != ConfidenceConfirmed {
		t.Fatalf("precise edges must be confirmed, got %v", conf)
	}
	if err := rep.ApplyMinConfidence("confirmed"); err != nil {
		t.Fatal(err)
	}
	if len(rep.BlastRadius) != 2 || rep.Filtered == nil || rep.Filtered.Candidate != 0 {
		t.Errorf("nothing should be filtered on a precise index, got blast=%d filtered=%+v", len(rep.BlastRadius), rep.Filtered)
	}
}

func TestImpactBatchApplyMinConfidence(t *testing.T) {
	svc, proj := reviewRepo(t)
	b, err := svc.ImpactPositions(proj, []FilePosition{{File: "a.go", Line: 5}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ApplyMinConfidence("confirmed"); err != nil {
		t.Fatal(err)
	}
	if len(b.Results) != 1 || b.Results[0].Filtered == nil {
		t.Fatalf("batch result not filtered: %+v", b.Results)
	}
	if err := b.ApplyMinConfidence("nope"); err == nil {
		t.Error("batch must reject an invalid min_confidence")
	}
}

// --- review coverage verdict ---

func writeRepoFile(t *testing.T, proj, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func reindex(t *testing.T, svc *Service, proj string) {
	t.Helper()
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
}

func TestReviewCoverageCovered(t *testing.T) {
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "a.go", "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper() // touched\n}\n")
	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage == nil {
		t.Fatalf("no coverage block: %+v", rep)
	}
	if rep.Coverage.Verdict != CoverageCovered || rep.Coverage.CoveredSymbols != 1 || rep.Coverage.UncoveredSymbols != 0 || rep.Coverage.UnknownSymbols != 0 {
		t.Errorf("coverage = %+v, want covered 1/0/0", rep.Coverage)
	}
	if rep.Gate == nil || rep.Gate.WouldFailOn.Uncovered {
		t.Errorf("covered review must not trip the uncovered gate: %+v", rep.Gate)
	}
}

func TestReviewCoverageUncoveredAndPartial(t *testing.T) {
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "lonely.go", "package app\n\nfunc Lonely() {}\n")
	reindex(t, svc, proj)

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage == nil || rep.Coverage.Verdict != CoverageUncovered || rep.Coverage.UncoveredSymbols != 1 || rep.Coverage.CoveredSymbols != 0 {
		t.Fatalf("coverage = %+v, want uncovered (1 uncovered)", rep.Coverage)
	}
	if !rep.Gate.WouldFailOn.Uncovered {
		t.Error("an uncovered verdict must trip the uncovered gate")
	}

	// Add a covered change next to it: partial, still tripping on the uncovered one.
	writeRepoFile(t, proj, "a.go", "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper() // touched\n}\n")
	rep, err = svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage == nil || rep.Coverage.Verdict != CoveragePartial || rep.Coverage.CoveredSymbols != 1 || rep.Coverage.UncoveredSymbols != 1 {
		t.Fatalf("coverage = %+v, want partial 1 covered / 1 uncovered", rep.Coverage)
	}
	if !rep.Gate.WouldFailOn.Uncovered {
		t.Error("a partial verdict with an uncovered symbol must trip the uncovered gate")
	}
}

// A test file written in the same diff, after the last index, counts toward the
// coverage verdict of the changed symbol it references (work item #1) — but ONLY
// toward that verdict: untested_symbols and --fail-on-untested are unchanged.
func TestReviewDiffTestFileCoversChangedSymbol(t *testing.T) {
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "lonely.go", "package app\n\nfunc Lonely() {}\n")
	reindex(t, svc, proj)
	// The new test is created AFTER the index: it is invisible to the indexed
	// call graph and to the indexed-file heuristic scan.
	writeRepoFile(t, proj, "lonely_test.go", "package app\n\nimport \"testing\"\n\nfunc TestLonely(t *testing.T) {\n\tLonely()\n}\n")

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(rep.ChangedSymbols, "Lonely") {
		t.Fatalf("Lonely not among changed symbols: %+v", rep.ChangedSymbols)
	}
	if !hasSymbol(rep.UntestedSymbols, "Lonely") {
		t.Errorf("a same-diff text link must not clear untested_symbols: %+v", rep.UntestedSymbols)
	}
	if rep.Coverage == nil || rep.Coverage.Verdict != CoverageCovered || rep.Coverage.CoveredSymbols != 1 {
		t.Errorf("coverage = %+v, want covered", rep.Coverage)
	}
	if rep.Gate.WouldFailOn.Uncovered {
		t.Error("covered diff must not trip the uncovered gate")
	}
	if !rep.Gate.WouldFailOn.Untested {
		t.Error("--fail-on-untested must be unchanged by the same-diff link")
	}
}

// A same-diff Go test in a different package directory must not link by name.
func TestReviewDiffTestLinkRequiresSamePackageForGo(t *testing.T) {
	files := []diffTestFile{{path: "other/x_test.go", content: []byte("package other\n\nfunc TestX() { Lonely() }\n")}}
	sym := SymbolRef{Symbol: "Lonely", File: "pkg/lonely.go"}
	if got := diffTestCoverage(files, sym); len(got) != 0 {
		t.Errorf("cross-package test linked by name: %+v", got)
	}
	files[0].path = "pkg/lonely_test.go"
	if got := diffTestCoverage(files, sym); len(got) != 1 {
		t.Errorf("same-package test not linked: %+v", got)
	}
}

func TestComputeReviewCoverageVerdicts(t *testing.T) {
	covered := &ImpactReport{Found: true, CallGraph: CallGraphName, Tests: []ImpactNode{{Symbol: "TestX"}}}
	uncovered := &ImpactReport{Found: true, CallGraph: CallGraphName, Untested: true}
	unknown := &ImpactReport{Found: true, CallGraph: CallGraphUnresolved, Resolution: "unresolved"}
	testSubject := &ImpactReport{Found: true, CallGraph: CallGraphName, Untested: true,
		Locations: []SymbolRef{{Symbol: "TestX", Kind: "test", File: "x_test.go"}}}

	cases := []struct {
		name    string
		imps    []*ImpactReport
		total   int
		verdict string
		c, u, k int
		trips   bool
	}{
		{"all covered", []*ImpactReport{covered, covered}, 2, CoverageCovered, 2, 0, 0, false},
		{"all uncovered", []*ImpactReport{uncovered}, 1, CoverageUncovered, 0, 1, 0, true},
		{"covered and uncovered", []*ImpactReport{covered, uncovered}, 2, CoveragePartial, 1, 1, 0, true},
		{"only unknown never trips", []*ImpactReport{unknown}, 1, CoverageUnknown, 0, 0, 1, false},
		{"covered and unknown is partial but does not trip", []*ImpactReport{covered, unknown}, 2, CoveragePartial, 1, 0, 1, false},
		{"uncovered and unknown", []*ImpactReport{uncovered, unknown}, 2, CoverageUncovered, 0, 1, 1, true},
		{"unanalyzed symbols are unknown", []*ImpactReport{covered}, 3, CoveragePartial, 1, 0, 2, false},
		{"test symbols are not the subject", []*ImpactReport{covered, testSubject}, 2, CoverageCovered, 1, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeReviewCoverage(&ReviewReport{TotalSymbols: tc.total}, tc.imps)
			if got == nil {
				t.Fatal("nil coverage")
			}
			if got.Verdict != tc.verdict || got.CoveredSymbols != tc.c || got.UncoveredSymbols != tc.u || got.UnknownSymbols != tc.k {
				t.Errorf("coverage = %+v, want %s %d/%d/%d", got, tc.verdict, tc.c, tc.u, tc.k)
			}
			if got.UncoveredGateTrips() != tc.trips {
				t.Errorf("UncoveredGateTrips = %v, want %v", got.UncoveredGateTrips(), tc.trips)
			}
		})
	}
	if got := computeReviewCoverage(&ReviewReport{TotalSymbols: 0}, nil); got != nil {
		t.Errorf("zero-symbol diff must have no coverage block, got %+v", got)
	}
	if got := computeReviewCoverage(&ReviewReport{TotalSymbols: 1}, []*ImpactReport{testSubject}); got != nil {
		t.Errorf("test-only diff must have no coverage block, got %+v", got)
	}
	var nilCov *ReviewCoverage
	if nilCov.UncoveredGateTrips() {
		t.Error("nil coverage must not trip")
	}
}

// The structural risk band and the legacy untested gate keep their semantics
// even when the coverage verdict is available.
func TestReviewCoverageDoesNotChangeRiskSemantics(t *testing.T) {
	rep := &ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: true, TotalSymbols: 1, CallGraph: CallGraphUnresolved,
		Risk:     &ReviewRisk{Level: "unknown", Factors: []RiskFactor{}},
		Coverage: &ReviewCoverage{Verdict: CoverageUnknown, UnknownSymbols: 1},
	}
	g := rep.ComputeGate()
	if !g.WouldFailOn.Untested {
		t.Error("--fail-on-untested must keep tripping on an unresolved call graph (back-compat)")
	}
	if g.WouldFailOn.Uncovered {
		t.Error("--fail-on-uncovered must not trip on unknown coverage")
	}
}
