package app

import (
	"strings"
	"testing"
)

// commitResetFixtures adds two same-named methods (C.Reset, D.Reset), commits
// them, then edits C.Reset so the working diff touches exactly one of them.
func commitResetFixtures(t *testing.T) (*Service, string) {
	t.Helper()
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "c.go", "package app\n\ntype C struct{}\n\nfunc (c *C) Reset() {}\n")
	writeRepoFile(t, proj, "d.go", "package app\n\ntype D struct{}\n\nfunc (d *D) Reset() {}\n")
	reviewGit(t, proj, "add", "-A")
	reviewGit(t, proj, "commit", "-m", "add C and D")
	writeRepoFile(t, proj, "c.go", "package app\n\ntype C struct{}\n\nfunc (c *C) Reset() {\n\t_ = 1\n}\n")
	return svc, proj
}

func untestedNames(rep *ReviewReport) []string {
	var out []string
	for _, s := range rep.UntestedSymbols {
		out = append(out, s.Symbol)
	}
	return out
}

// A same-diff test file whose only mention of the name is a comment must not
// weaken --fail-on-untested, and an ambiguous name is never linked by text.
func TestReviewDiffTestCommentDoesNotCover(t *testing.T) {
	svc, proj := commitResetFixtures(t)
	writeRepoFile(t, proj, "x_test.go", "package app\n\nimport \"testing\"\n\n// Reset is exercised elsewhere.\nfunc TestNothing(t *testing.T) {}\n")
	reindex(t, svc, proj)

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(rep.ChangedSymbols, "Reset") {
		t.Fatalf("Reset not among changed symbols: %+v", rep.ChangedSymbols)
	}
	if !hasSymbol(rep.UntestedSymbols, "Reset") {
		t.Fatalf("Reset must stay untested, untested = %v", untestedNames(rep))
	}
	if !rep.ComputeGate().WouldFailOn.Untested {
		t.Error("--fail-on-untested must still trip")
	}
	if rep.Coverage == nil || rep.Coverage.Verdict != CoverageUncovered {
		t.Errorf("coverage = %+v, want uncovered", rep.Coverage)
	}
}

// Even a genuine call in the diff's test file must not link a name shared by
// several definitions: a text scan cannot tell C.Reset from D.Reset.
func TestReviewDiffTestAmbiguousNameNotLinked(t *testing.T) {
	svc, proj := commitResetFixtures(t)
	reindex(t, svc, proj)
	// Written after the index, so only the same-diff text scan can see it (an
	// indexed call would make a name edge to both Reset methods).
	writeRepoFile(t, proj, "x_test.go", "package app\n\nimport \"testing\"\n\nfunc TestReset(t *testing.T) {\n\td := &D{}\n\td.Reset()\n}\n")

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(rep.UntestedSymbols, "Reset") {
		t.Fatalf("Reset must stay untested, untested = %v", untestedNames(rep))
	}
	if rep.Coverage == nil || rep.Coverage.CoveredSymbols != 0 {
		t.Errorf("ambiguous name must not count as covered: %+v", rep.Coverage)
	}
}

// A diff-linked test counts toward the coverage verdict only. untested_symbols,
// the risk untested factor, and --fail-on-untested are unchanged.
func TestReviewDiffTestCountsOnlyTowardCoverage(t *testing.T) {
	svc, proj := reviewRepo(t)
	writeRepoFile(t, proj, "lonely.go", "package app\n\nfunc Lonely() {}\n")
	reindex(t, svc, proj)
	writeRepoFile(t, proj, "lonely_test.go", "package app\n\nimport \"testing\"\n\nfunc TestLonely(t *testing.T) {\n\tLonely()\n}\n")

	rep, err := svc.Review(proj, ReviewOpts{Mode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage == nil || rep.Coverage.Verdict != CoverageCovered {
		t.Fatalf("coverage = %+v, want covered", rep.Coverage)
	}
	if !hasSymbol(rep.UntestedSymbols, "Lonely") {
		t.Errorf("untested_symbols must be unchanged by diff-linked tests: %v", untestedNames(rep))
	}
	g := rep.ComputeGate()
	if !g.WouldFailOn.Untested {
		t.Error("--fail-on-untested must still trip")
	}
	if g.WouldFailOn.Uncovered {
		t.Error("covered verdict must not trip --fail-on-uncovered")
	}
	var hasUntestedFactor bool
	if rep.Risk != nil {
		for _, f := range rep.Risk.Factors {
			if f.Factor == "untested_changes" || f.Factor == "untested" {
				hasUntestedFactor = true
			}
		}
	}
	if !hasUntestedFactor {
		t.Errorf("risk must keep its untested factor: %+v", rep.Risk)
	}
}

func TestStripCommentsAndStrings(t *testing.T) {
	cases := []struct {
		ext, src string
		keep     []string
		drop     []string
	}{
		{".go", "// Reset here\nfunc TestX() { /* Reset */ Foo(\"Reset\", `Reset`, 'R') }\n", []string{"Foo", "TestX"}, []string{"Reset"}},
		{".go", "func TestX() { Reset() } // trailing\n", []string{"Reset"}, []string{"trailing"}},
		{".ts", "// Reset\nit(\"Reset works\", () => { run(`${Reset}`); Real() })\n", []string{"Real", "run"}, []string{"Reset"}},
		{".js", "/* Reset\n spans */ const s = 'it\\'s Reset'; Real()\n", []string{"Real"}, []string{"Reset"}},
		{".py", "# Reset\ndef test_x():\n    \"\"\"Reset docs\"\"\"\n    s = 'Reset'\n    Real()\n", []string{"Real", "test_x"}, []string{"Reset"}},
		{".py", "x = '''\nReset\n'''\nReal()\n", []string{"Real"}, []string{"Reset"}},
	}
	for _, tc := range cases {
		got := string(stripCommentsAndStrings(tc.ext, []byte(tc.src)))
		if len(got) != len(tc.src) || strings.Count(got, "\n") != strings.Count(tc.src, "\n") {
			t.Errorf("%s: length/lines not preserved: %q", tc.ext, got)
		}
		for _, k := range tc.keep {
			if !strings.Contains(got, k) {
				t.Errorf("%s: %q dropped from %q", tc.ext, k, got)
			}
		}
		for _, d := range tc.drop {
			if strings.Contains(got, d) {
				t.Errorf("%s: %q survived in %q", tc.ext, d, got)
			}
		}
	}
}
