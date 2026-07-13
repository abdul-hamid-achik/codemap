package graph

import "testing"

func TestProjectArchitectureAggregatesDirectedSubsystemBridges(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("topology", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}

	aFile := addTopologyNode(t, s, pid, "internal/auth/auth.go", "", KindFile)
	aLogin := addTopologyNode(t, s, pid, "internal/auth/auth.go", "Login", KindFunction)
	aCheck := addTopologyNode(t, s, pid, "internal/auth/check.go", "Check", KindFunction)
	dQuery := addTopologyNode(t, s, pid, "internal/db/query.go", "Query", KindFunction)
	dOpen := addTopologyNode(t, s, pid, "internal/db/open.go", "Open", KindFunction)
	main := addTopologyNode(t, s, pid, "cmd/app/main.go", "main", KindFunction)

	for _, e := range []struct {
		from, to int64
		kind     string
		prov     string
	}{
		{aFile, aLogin, EdgeDefines, ProvName}, // membership noise: excluded
		{aLogin, aCheck, EdgeCalls, ProvPrecise},
		{aLogin, dQuery, EdgeCalls, ProvPrecise},
		{aCheck, dOpen, EdgeCalls, ProvName},
		{main, aLogin, EdgeCalls, ProvPrecise},
		{main, dOpen, EdgeReferences, ProvName},
	} {
		if _, err := s.AddEdgeProv(e.from, e.to, e.kind, 1, e.prov); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := s.ProjectArchitecture(pid, TopologyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Strategy != "source_path" || rep.SubsystemsTotal != 3 || rep.BridgesTotal != 3 {
		t.Fatalf("unexpected topology summary: %+v", rep)
	}

	auth := topologySubsystem(t, rep, "internal/auth")
	if auth.Files != 1 || auth.Symbols != 2 || auth.InternalEdges != 1 || auth.InboundEdges != 1 || auth.OutboundEdges != 2 {
		t.Errorf("auth subsystem = %+v", auth)
	}
	if auth.Languages["go"] != 3 || auth.Kinds[KindFunction] != 2 {
		t.Errorf("auth metadata = %+v", auth)
	}

	bridge := topologyBridge(t, rep, "internal/auth", "internal/db", EdgeCalls)
	if bridge.Count != 2 || bridge.Provenance[ProvPrecise] != 1 || bridge.Provenance[ProvName] != 1 {
		t.Errorf("auth -> db bridge = %+v", bridge)
	}
	if len(bridge.SourceFiles) != 2 || len(bridge.TargetFiles) != 2 {
		t.Errorf("bridge file samples = %+v", bridge)
	}
	if bridge.SourceFilesTotal != 2 || bridge.TargetFilesTotal != 2 || bridge.SourceFilesTruncated || bridge.TargetFilesTruncated {
		t.Errorf("bridge file sample metadata = %+v", bridge)
	}
}

func TestProjectArchitectureDisclosesBridgeFileSampleTruncation(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("bridge-samples", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		from := addTopologyNode(t, s, pid, "a/source"+string(rune('a'+i))+".go", "From"+string(rune('A'+i)), KindFunction)
		to := addTopologyNode(t, s, pid, "b/target"+string(rune('a'+i))+".go", "To"+string(rune('A'+i)), KindFunction)
		if _, err := s.AddEdgeProv(from, to, EdgeCalls, 1, ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := s.ProjectArchitecture(pid, TopologyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bridge := topologyBridge(t, rep, "a", "b", EdgeCalls)
	if len(bridge.SourceFiles) != 5 || bridge.SourceFilesTotal != 7 || !bridge.SourceFilesTruncated ||
		len(bridge.TargetFiles) != 5 || bridge.TargetFilesTotal != 7 || !bridge.TargetFilesTruncated {
		t.Fatalf("bounded bridge file samples are not disclosed: %+v", bridge)
	}
}

func TestProjectArchitectureBoundsAndSortsDeterministically(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("bounded", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	a := addTopologyNode(t, s, pid, "a/a.go", "A", KindFunction)
	b := addTopologyNode(t, s, pid, "b/b.go", "B", KindFunction)
	c := addTopologyNode(t, s, pid, "c/c.go", "C", KindFunction)
	for _, pair := range [][2]int64{{a, b}, {a, b}, {b, c}} {
		if _, err := s.AddEdgeProv(pair[0], pair[1], EdgeCalls, 1, ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := s.ProjectArchitecture(pid, TopologyOptions{MaxSubsystems: 2, MaxBridges: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Truncated || rep.SubsystemsTotal != 3 || rep.BridgesTotal != 2 {
		t.Fatalf("bounds metadata = %+v", rep)
	}
	if len(rep.Subsystems) != 2 || len(rep.Bridges) != 1 {
		t.Fatalf("bounded result = %+v", rep)
	}
	if rep.Bridges[0].From != "a" || rep.Bridges[0].To != "b" || rep.Bridges[0].Count != 2 {
		t.Errorf("bridge ordering = %+v", rep.Bridges)
	}
}

func TestTopologySubsystemName(t *testing.T) {
	for path, want := range map[string]string{
		"main.go":                 "(root)",
		"internal/app/service.go": "internal/app",
		"cmd/codemap/main.go":     "cmd/codemap",
		"pkg/client/http/get.go":  "pkg/client",
		"web/src/app.ts":          "web",
	} {
		if got := topologySubsystemName(path); got != want {
			t.Errorf("topologySubsystemName(%q) = %q, want %q", path, got, want)
		}
	}
}

func addTopologyNode(t *testing.T, s *Store, pid int64, file, symbol, kind string) int64 {
	t.Helper()
	id, err := s.AddNode(&Node{
		ProjectID: pid, FilePath: file, Symbol: symbol, FQN: "topology." + symbol,
		Kind: kind, Language: "go", StartLine: 1, EndLine: 2, SourceHash: file + symbol,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func topologySubsystem(t *testing.T, rep *ProjectTopology, name string) TopologySubsystem {
	t.Helper()
	for _, sub := range rep.Subsystems {
		if sub.Name == name {
			return sub
		}
	}
	t.Fatalf("subsystem %q not found in %+v", name, rep.Subsystems)
	return TopologySubsystem{}
}

func topologyBridge(t *testing.T, rep *ProjectTopology, from, to, kind string) TopologyBridge {
	t.Helper()
	for _, bridge := range rep.Bridges {
		if bridge.From == from && bridge.To == to && bridge.EdgeType == kind {
			return bridge
		}
	}
	t.Fatalf("bridge %s -> %s (%s) not found in %+v", from, to, kind, rep.Bridges)
	return TopologyBridge{}
}
