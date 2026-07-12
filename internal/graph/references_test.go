package graph

import "testing"

func TestReferencesAreScopedBoundedDeterministicAndReferenceOnly(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", t.TempDir(), "go")
	otherPID, _ := s.UpsertProject("other", t.TempDir(), "go")
	add := func(projectID int64, file, symbol, kind string, line int) int64 {
		t.Helper()
		id, err := s.AddNode(&Node{
			ProjectID: projectID, FilePath: file, Symbol: symbol, FQN: "p." + symbol,
			Kind: kind, Language: "go", StartLine: line, EndLine: line, SourceHash: file,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	target := add(pid, "target.go", "Handler", KindFunction, 10)
	z := add(pid, "z.go", "ZSetup", KindFunction, 9)
	a := add(pid, "a.go", "ASetup", KindFunction, 3)
	file := add(pid, "cmd.go", "", KindFile, 1)
	callOnly := add(pid, "call.go", "Caller", KindFunction, 4)
	foreign := add(otherPID, "foreign.go", "Foreign", KindFunction, 2)

	for _, source := range []int64{z, a, file} {
		if _, err := s.AddEdge(source, target, EdgeReferences, WeightLSP); err != nil {
			t.Fatal(err)
		}
	}
	// A duplicate row is one enclosing reference site, not a second site.
	if _, err := s.AddEdge(a, target, EdgeReferences, WeightLSP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(callOnly, target, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	// Even a malformed cross-project edge must not leak into a project query.
	if _, err := s.AddEdge(foreign, target, EdgeReferences, WeightLSP); err != nil {
		t.Fatal(err)
	}

	sites, total, err := s.References(pid, "Handler", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(sites) != 2 {
		t.Fatalf("references total=%d sites=%+v, want total 3 capped to 2", total, sites)
	}
	if sites[0].Source.Symbol != "ASetup" || sites[1].Source.Kind != KindFile {
		t.Fatalf("references are not in deterministic file/line order: %+v", sites)
	}
	for _, site := range sites {
		if site.Source.ProjectID != pid || site.Source.Symbol == "Caller" || site.Source.Symbol == "Foreign" {
			t.Fatalf("reference query mixed calls or another project: %+v", site)
		}
	}
	if foreignView, foreignTotal, err := s.ReferencesOfNode(otherPID, target, 10); err != nil || foreignTotal != 0 || len(foreignView) != 0 {
		t.Fatalf("exact query accepted a target outside its project: sites=%+v total=%d err=%v", foreignView, foreignTotal, err)
	}
}

func TestReferencesOfNodePreservesNameFanoutUncertainty(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", t.TempDir(), "go")
	add := func(file, symbol, fqn string) int64 {
		t.Helper()
		id, err := s.AddNode(&Node{
			ProjectID: pid, FilePath: file, Symbol: symbol, FQN: fqn,
			Kind: KindMethod, Language: "go", StartLine: 3, EndLine: 3, SourceHash: file,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	left := add("left.go", "Shared", "p.Left.Shared")
	right := add("right.go", "Shared", "p.Right.Shared")
	fanout := add("a.go", "Wire", "p.Wire")
	rightOnly := add("b.go", "RightWire", "p.RightWire")
	for _, target := range []int64{left, right} {
		if _, err := s.AddEdge(fanout, target, EdgeReferences, WeightTreeSitter); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEdgeProv(rightOnly, right, EdgeReferences, WeightLSP, ProvPrecise); err != nil {
		t.Fatal(err)
	}

	leftSites, total, err := s.ReferencesOfNode(pid, left, 10)
	if err != nil || total != 1 || len(leftSites) != 1 || !leftSites[0].Ambiguous {
		t.Fatalf("exact left references = %+v total=%d err=%v, want one ambiguous fanout", leftSites, total, err)
	}
	rightSites, total, err := s.ReferencesOfNode(pid, right, 10)
	if err != nil || total != 2 || len(rightSites) != 2 {
		t.Fatalf("exact right references = %+v total=%d err=%v", rightSites, total, err)
	}
	merged, total, err := s.References(pid, "Shared", 10)
	if err != nil || total != 2 || len(merged) != 2 {
		t.Fatalf("name-union references = %+v total=%d err=%v", merged, total, err)
	}
}
