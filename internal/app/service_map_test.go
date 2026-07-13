package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestArchitectureMapReturnsSubsystemsBridgesAndEntrypoints(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"cmd/demo/main.go":      "package main\n\nimport \"example.com/map/internal/auth\"\n\nfunc main() { auth.Login() }\n",
		"internal/auth/auth.go": "package auth\n\nimport \"example.com/map/internal/db\"\n\nfunc Login() { db.Query() }\n",
		"internal/db/db.go":     "package db\n\nfunc Query() {}\n",
		"go.mod":                "module example.com/map\n\ngo 1.25\n",
	}
	for name, source := range files {
		path := filepath.Join(proj, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
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

	rep, err := svc.ArchitectureMap(proj, ArchitectureMapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || !rep.Indexed || rep.Project == "" {
		t.Fatalf("map identity = %+v", rep)
	}
	if rep.Strategy != "source_path" || rep.SubsystemsTotal != 3 {
		t.Fatalf("map topology = %+v", rep)
	}
	if len(rep.Bridges) < 2 {
		t.Fatalf("expected cross-subsystem call/import bridges, got %+v", rep.Bridges)
	}
	foundMain := false
	for _, entry := range rep.Entrypoints {
		if entry.Symbol == "main" {
			foundMain = true
			break
		}
	}
	if !foundMain {
		t.Errorf("main entrypoint missing: %+v", rep.Entrypoints)
	}
	if rep.CallGraph != CallGraphName {
		t.Errorf("call_graph = %q, want %q", rep.CallGraph, CallGraphName)
	}
	if rep.Stale {
		t.Error("freshly indexed project reported stale")
	}

	bounded, err := svc.ArchitectureMap(proj, ArchitectureMapOptions{
		TopSubsystems: 1, TopBridges: 1, TopHubs: 1, TopEntrypoints: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.Truncated || !bounded.SubsystemsTruncated || !bounded.BridgesTruncated || !bounded.HubsTruncated || !bounded.EntrypointsTruncated {
		t.Fatalf("component truncation flags = %+v", bounded)
	}
	if bounded.SubsystemsTotal <= len(bounded.Subsystems) || bounded.BridgesTotal <= len(bounded.Bridges) || bounded.HubsTotal <= len(bounded.Hubs) || bounded.EntrypointsTotal <= len(bounded.Entrypoints) {
		t.Fatalf("component totals do not explain truncation: %+v", bounded)
	}
}

func TestArchitectureMapUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	rep, err := NewService(sess).ArchitectureMap(t.TempDir(), ArchitectureMapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.SchemaVersion != 1 || rep.Subsystems == nil || rep.Bridges == nil || rep.CallGraph != CallGraphNone {
		t.Fatalf("unindexed map = %+v", rep)
	}
}

func TestArchitectureMapRejectsUnboundedLimits(t *testing.T) {
	for _, opts := range []ArchitectureMapOptions{
		{TopSubsystems: -1},
		{TopBridges: MaxMapBridges + 1},
		{TopHubs: MaxMapHubs + 1},
		{TopEntrypoints: MaxMapEntrypoints + 1},
	} {
		if _, err := normalizeArchitectureMapOptions(opts); err == nil {
			t.Fatalf("normalizeArchitectureMapOptions(%+v) should fail", opts)
		}
	}
}
