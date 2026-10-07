package graph

import "testing"

// TestBlastRadiusConfirmedPaths pins the per-node confirmation flag: a node is
// confirmed only when a shortest path to it uses precise or same-file edges.
func TestBlastRadiusConfirmedPaths(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(file, sym string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: "p." + sym, Kind: KindFunction, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	target := mk("t.go", "Target")
	sameFile := mk("t.go", "SameFile")     // name edge, same file  -> confirmed
	precise := mk("p.go", "Precise")       // precise edge          -> confirmed
	cand := mk("c.go", "Cand")             // name edge, cross file -> candidate
	viaPrecise := mk("v.go", "ViaPrecise") // precise via Precise   -> confirmed
	viaCand := mk("w.go", "ViaCand")       // precise via Cand      -> candidate (path has a name edge)
	both := mk("b.go", "Both")             // depth 2 via Cand (name) AND via Precise (precise) -> confirmed
	edge := func(src, dst int64, prov string) {
		if _, err := s.AddEdgeProv(src, dst, EdgeCalls, 1, prov); err != nil {
			t.Fatal(err)
		}
	}
	edge(sameFile, target, ProvName)
	edge(precise, target, ProvPrecise)
	edge(cand, target, ProvName)
	edge(viaPrecise, precise, ProvPrecise)
	edge(viaCand, cand, ProvPrecise)
	edge(both, cand, ProvName)
	edge(both, precise, ProvPrecise)

	br, err := s.BlastRadius(pid, "Target", 3)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, nd := range br {
		got[nd.Node.Symbol] = nd.Confirmed
	}
	want := map[string]bool{"SameFile": true, "Precise": true, "Cand": false, "ViaPrecise": true, "ViaCand": false, "Both": true}
	if len(got) != len(want) {
		t.Fatalf("blast radius = %v, want %d nodes", got, len(want))
	}
	for sym, w := range want {
		if got[sym] != w {
			t.Errorf("%s confirmed = %v, want %v", sym, got[sym], w)
		}
	}
}
