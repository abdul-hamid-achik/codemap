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

// TestBlastRadiusSameFileNameFanoutIsCandidate pins the honesty rule for
// same-file name edges: a name-based call to Close links to EVERY same-named
// method, including ones in the caller's own file, so the edge is confirmed only
// when the target's symbol is unique within its file.
func TestBlastRadiusSameFileNameFanoutIsCandidate(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	mk := func(file, sym, fqn string) int64 {
		id, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: fqn, Kind: KindMethod, Language: "go", SourceHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aClose := mk("pkg/a.go", "Close", "pkg.A.Close")
	bClose := mk("pkg/a.go", "Close", "pkg.B.Close")
	shutdown := mk("pkg/a.go", "Shutdown", "pkg.W.Shutdown")
	// w.a.Close() resolved by name: links to every same-named method.
	for _, tgt := range []int64{aClose, bClose} {
		if _, err := s.AddEdgeProv(shutdown, tgt, EdgeCalls, 1, ProvName); err != nil {
			t.Fatal(err)
		}
	}
	br, err := s.BlastRadiusFromNode(pid, bClose, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(br) != 1 || br[0].Node.Symbol != "Shutdown" {
		t.Fatalf("blast radius = %+v, want only Shutdown", br)
	}
	if br[0].Confirmed {
		t.Fatalf("same-file name fan-out over two Close methods must stay candidate")
	}

	// A precise edge to the same ambiguous target stays confirmed.
	if _, err := s.AddEdgeProv(shutdown, bClose, EdgeCalls, 1, ProvPrecise); err != nil {
		t.Fatal(err)
	}
	br, err = s.BlastRadiusFromNode(pid, bClose, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(br) != 1 || !br[0].Confirmed {
		t.Fatalf("precise edge must stay confirmed: %+v", br)
	}
}
