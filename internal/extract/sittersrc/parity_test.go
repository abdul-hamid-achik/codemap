package sittersrc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/tooling"
)

// TestParityWithLanguageServers diffs this backend against the live language
// servers on a real repository — the acceptance check for any change to the
// emulation. It is opt-in (it needs the servers and minutes of wall time):
//
//	CODEMAP_PARITY_ROOT=~/projects/some-repo \
//	CODEMAP_PARITY_LANG=typescript,javascript,python \  (default: all three)
//	CODEMAP_PARITY_SHOW=40 \                             (diff lines to print)
//	CODEMAP_PARITY_FILES=src/a.py,lib/ \                 (path substrings to keep)
//	go test ./internal/extract/sittersrc -run Parity -v -timeout 30m
//
// Symbols are compared as (kind, fqn, start line, end line) multisets per file.
func TestParityWithLanguageServers(t *testing.T) {
	root := os.Getenv("CODEMAP_PARITY_ROOT")
	if root == "" {
		t.Skip("set CODEMAP_PARITY_ROOT to diff against live language servers")
	}
	root, _ = filepath.Abs(os.ExpandEnv(root))
	want := map[string]bool{"typescript": true, "javascript": true, "python": true}
	if l := os.Getenv("CODEMAP_PARITY_LANG"); l != "" {
		want = map[string]bool{}
		for _, x := range strings.Split(l, ",") {
			want[strings.TrimSpace(x)] = true
		}
	}
	show := 40
	if s, err := strconv.Atoi(os.Getenv("CODEMAP_PARITY_SHOW")); err == nil {
		show = s
	}

	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var only []string
	if f := os.Getenv("CODEMAP_PARITY_FILES"); f != "" {
		only = strings.Split(f, ",")
	}
	byLang := map[string][]string{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if len(only) > 0 && !containsAny(f, only) {
			continue
		}
		if l := extract.LanguageForPath(f); want[l] {
			byLang[l] = append(byLang[l], f)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	servers := map[string]extract.Extractor{}
	spawn := func(cmd, lang string, also ...string) {
		pr := tooling.Probe(ctx, cmd, root)
		if !pr.OK {
			t.Logf("skip %s: %s unavailable", lang, cmd)
			return
		}
		bin := pr.Path
		if bin == "" {
			bin = cmd
		}
		owner, err := lspsrc.New(ctx, lang, lang, root, bin, "--stdio")
		if err != nil {
			t.Logf("skip %s: %v", lang, err)
			return
		}
		t.Cleanup(func() { _ = owner.Close() })
		servers[lang] = owner
		for _, l := range also {
			servers[l] = owner.Bind(l, l)
		}
	}
	if len(byLang["typescript"])+len(byLang["javascript"]) > 0 {
		spawn("typescript-language-server", "typescript", "javascript")
	}
	if len(byLang["python"]) > 0 {
		spawn("pyright-langserver", "python")
	}

	type key struct {
		kind, fqn  string
		start, end int
	}
	var missing, extra []string
	kindStats := map[string][3]int{} // kind → matched, missing, extra
	var lspTotal, sitTotal, files, sitErrs int
	var lspDur, sitDur time.Duration
	for lang, paths := range byLang {
		srv := servers[lang]
		if srv == nil {
			continue
		}
		sit, _ := New(lang)
		for _, rel := range paths {
			src, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				continue
			}
			t0 := time.Now()
			lr, lerr := srv.ExtractFile(rel, src)
			lspDur += time.Since(t0)
			if lerr != nil {
				continue // the LSP path skips such files too
			}
			t1 := time.Now()
			sr, serr := sit.ExtractFile(rel, src)
			sitDur += time.Since(t1)
			if serr != nil {
				sitErrs++
				extra = append(extra, fmt.Sprintf("ERROR %s: %v", rel, serr))
				continue
			}
			files++
			got := map[key]int{}
			for _, s := range sr.Symbols {
				got[key{s.Kind, s.FQN, s.StartLine, s.EndLine}]++
			}
			sitTotal += len(sr.Symbols)
			for _, s := range lr.Symbols {
				lspTotal++
				k := key{s.Kind, s.FQN, s.StartLine, s.EndLine}
				st := kindStats[s.Kind]
				if got[k] > 0 {
					got[k]--
					st[0]++
				} else {
					st[1]++
					missing = append(missing, fmt.Sprintf("%s:%d-%d %s %s", rel, s.StartLine, s.EndLine, s.Kind, s.FQN))
				}
				kindStats[s.Kind] = st
			}
			for k, n := range got {
				for ; n > 0; n-- {
					st := kindStats[k.kind]
					st[2]++
					kindStats[k.kind] = st
					extra = append(extra, fmt.Sprintf("%s:%d-%d %s %s", rel, k.start, k.end, k.kind, k.fqn))
				}
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	matched := lspTotal - len(missing)
	t.Logf("files=%d  lsp symbols=%d  sitter symbols=%d  matched=%d (%.2f%%)  missing=%d  extra=%d  sitter errors=%d",
		files, lspTotal, sitTotal, matched, 100*float64(matched)/float64(max(lspTotal, 1)), len(missing), len(extra), sitErrs)
	t.Logf("time: lsp %v, sitter %v", lspDur.Round(time.Millisecond), sitDur.Round(time.Millisecond))
	var kinds []string
	for k := range kindStats {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		s := kindStats[k]
		t.Logf("  %-9s matched=%-6d missing=%-5d extra=%d", k, s[0], s[1], s[2])
	}
	for i, m := range missing {
		if i >= show {
			t.Logf("  … %d more missing", len(missing)-show)
			break
		}
		t.Logf("  - %s", m)
	}
	for i, x := range extra {
		if i >= show {
			t.Logf("  … %d more extra", len(extra)-show)
			break
		}
		t.Logf("  + %s", x)
	}
	if p := os.Getenv("CODEMAP_PARITY_OUT"); p != "" {
		body := "MISSING\n" + strings.Join(missing, "\n") + "\nEXTRA\n" + strings.Join(extra, "\n") + "\n"
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, strings.TrimSpace(sub)) {
			return true
		}
	}
	return false
}
