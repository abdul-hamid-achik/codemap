package sittersrc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
)

// The navtree fixtures pin the emulation against what the real servers
// returned (typescript-language-server 5.1.3 / TypeScript 5.9.3, pyright
// 1.1.408) for the constructs that needed special handling. Regenerate the
// .golden files from the live servers after a deliberate change:
//
//	CODEMAP_REGEN_GOLDEN=1 go test ./internal/extract/sittersrc -run Golden
const navtreeDir = "testdata/navtree"

func goldenLines(res *extract.FileResult) string {
	lines := make([]string, 0, len(res.Symbols))
	for _, s := range res.Symbols {
		lines = append(lines, fmt.Sprintf("%-8s %s %d-%d", s.Kind, s.FQN, s.StartLine, s.EndLine))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func navtreeFixtures(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(navtreeDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".golden") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestGoldenNavtreeMatchesLanguageServers(t *testing.T) {
	if os.Getenv("CODEMAP_REGEN_GOLDEN") != "" {
		regenerateGolden(t)
	}
	for _, name := range navtreeFixtures(t) {
		src, err := os.ReadFile(filepath.Join(navtreeDir, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(navtreeDir, name+".golden"))
		if err != nil {
			t.Fatalf("%s: missing golden (regenerate with CODEMAP_REGEN_GOLDEN=1): %v", name, err)
		}
		e, err := New(extract.LanguageForPath(name))
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.ExtractFile(name, src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := goldenLines(res); got != string(want) {
			t.Errorf("%s: symbols differ from the language server's\n--- got\n%s--- want\n%s", name, got, want)
		}
	}
}

func regenerateGolden(t *testing.T) {
	root, _ := filepath.Abs(navtreeDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	servers := map[string]extract.Extractor{}
	for _, s := range []struct{ cmd, lang string }{
		{"typescript-language-server", "typescript"},
		{"pyright-langserver", "python"},
	} {
		pr := tooling.Probe(ctx, s.cmd, root)
		if !pr.OK {
			t.Fatalf("regenerating golden needs %s on PATH", s.cmd)
		}
		bin := pr.Path
		if bin == "" {
			bin = s.cmd
		}
		srv, err := lspsrc.New(ctx, s.lang, s.lang, root, bin, "--stdio")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = srv.Close() }()
		servers[s.lang] = srv
		if s.lang == "typescript" {
			servers["javascript"] = srv.Bind("javascript", "javascript")
		}
	}
	for _, name := range navtreeFixtures(t) {
		src, err := os.ReadFile(filepath.Join(navtreeDir, name))
		if err != nil {
			t.Fatal(err)
		}
		res, err := servers[extract.LanguageForPath(name)].ExtractFile(name, src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(navtreeDir, name+".golden"), []byte(goldenLines(res)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
