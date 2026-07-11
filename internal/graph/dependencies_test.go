package graph

import "testing"

func TestInboundFileDependenciesDedupesAndIgnoresCycles(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("deps", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	add := func(file, symbol, kind string) int64 {
		t.Helper()
		id, addErr := s.AddNode(&Node{
			ProjectID: pid, FilePath: file, Symbol: symbol, FQN: symbol,
			Kind: kind, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h",
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return id
	}
	aFile := add("a.go", "", KindFile)
	aFn := add("a.go", "Target", KindFunction)
	bFile := add("b.go", "", KindFile)
	bFn := add("b.go", "Use", KindFunction)
	cFile := add("c.go", "", KindFile)

	// Duplicate logical call rows collapse, and precise wins over name. A reverse
	// edge makes a cycle but cannot expand this direct inbound query.
	for _, provenance := range []string{ProvName, ProvName, ProvPrecise} {
		if _, err := s.AddEdgeProv(bFn, aFn, EdgeCalls, WeightLSP, provenance); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEdge(bFn, aFn, EdgeReferences, WeightTreeSitter); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.AddEdge(cFile, aFile, EdgeImports, WeightLSP); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEdge(aFn, bFn, EdgeCalls, WeightLSP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(aFile, aFn, EdgeReferences, WeightLSP); err != nil {
		t.Fatal(err)
	}
	_ = bFile

	evidence, err := s.InboundFileDependencies(pid, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 3 {
		t.Fatalf("inbound evidence = %+v, want one call, reference, and import", evidence)
	}
	want := map[string]string{
		EdgeCalls:      "b.go",
		EdgeReferences: "b.go",
		EdgeImports:    "c.go",
	}
	for _, edge := range evidence {
		if edge.Source.File != want[edge.EdgeType] || edge.Target.File != "a.go" {
			t.Errorf("unexpected %s evidence: %+v", edge.EdgeType, edge)
		}
		if edge.EdgeType == EdgeCalls && edge.Provenance != ProvPrecise {
			t.Errorf("deduped call provenance = %q, want precise", edge.Provenance)
		}
	}
}
