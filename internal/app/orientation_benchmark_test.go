package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// BenchmarkOrientationReports measures the bounded response-side surfaces
// added for architecture/agent orientation. It is fully hermetic: the fixture
// is indexed without embeddings or LSP and Explore intentionally exercises
// its offline name-search path. ns/op is the service+JSON latency;
// response-bytes is the compact JSON payload size returned to a CLI/MCP layer.
func BenchmarkOrientationReports(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	b.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	b.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	b.Setenv("CODEMAP_CONFIG", "")
	b.Setenv("XDG_DATA_HOME", "")
	b.Setenv("CODEMAP_VECGREP_ENABLED", "false")

	root := b.TempDir()
	files := map[string]string{
		"go.mod":                     "module example.com/orientationbench\n\ngo 1.25\n",
		"cmd/demo/main.go":           "package main\n\nimport \"example.com/orientationbench/internal/auth\"\n\nfunc main() { auth.Login() }\n",
		"internal/auth/auth.go":      "package auth\n\nimport \"example.com/orientationbench/internal/db\"\n\nfunc Login() { Validate(); db.Query() }\nfunc Validate() {}\n",
		"internal/auth/auth_test.go": "package auth\n\nimport \"testing\"\n\nfunc TestLogin(t *testing.T) { Login() }\n",
		"internal/db/db.go":          "package db\n\nfunc Query() {}\n",
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	sess, err := Open("")
	if err != nil {
		b.Fatal(err)
	}
	defer sess.Close()
	sess.Config.Vecgrep.Enabled = false
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		b.Fatal(err)
	}
	at, err := svc.SymbolAt(root, "internal/auth/auth.go", 5)
	if err != nil {
		b.Fatal(err)
	}
	if at.Selector == nil {
		b.Fatalf("benchmark traversal start did not resolve: %+v", at)
	}
	selector := *at.Selector
	mapCheck, err := svc.ArchitectureMap(root, ArchitectureMapOptions{})
	if err != nil || !mapCheck.Indexed || len(mapCheck.Subsystems) == 0 {
		b.Fatalf("benchmark map fixture is not meaningful: report=%+v err=%v", mapCheck, err)
	}
	exploreCheck, err := svc.Explore(context.Background(), root, "Login", ExploreOptions{})
	if err != nil || !exploreCheck.Indexed || len(exploreCheck.Contexts) == 0 {
		b.Fatalf("benchmark explore fixture is not meaningful: report=%+v err=%v", exploreCheck, err)
	}
	traverseCheck, err := svc.TraverseBySelector(root, selector, TraverseOptions{})
	if err != nil || !traverseCheck.Found || len(traverseCheck.Hops) == 0 {
		b.Fatalf("benchmark traverse fixture is not meaningful: report=%+v err=%v", traverseCheck, err)
	}

	b.Run("map", func(b *testing.B) {
		benchmarkJSONReport(b, func() (any, error) {
			return svc.ArchitectureMap(root, ArchitectureMapOptions{})
		})
	})
	b.Run("explore", func(b *testing.B) {
		benchmarkJSONReport(b, func() (any, error) {
			return svc.Explore(context.Background(), root, "Login", ExploreOptions{})
		})
	})
	b.Run("traverse", func(b *testing.B) {
		benchmarkJSONReport(b, func() (any, error) {
			return svc.TraverseBySelector(root, selector, TraverseOptions{})
		})
	})
}

func benchmarkJSONReport(b *testing.B, call func() (any, error)) {
	b.Helper()
	report, err := call()
	if err != nil {
		b.Fatal(err)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		report, err = call()
		if err != nil {
			b.Fatal(err)
		}
		if _, err = json.Marshal(report); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(payload)), "response-bytes")
}
