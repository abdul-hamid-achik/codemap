package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

func unresolvedContextProject(t *testing.T, count, bodyRunes int) (*Service, string, []string, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep = config.VecgrepConfig{Enabled: false}

	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(filepath.Base(proj), proj, "typescript")
	if err != nil {
		t.Fatal(err)
	}

	syms := make([]string, count)
	for i := range count {
		sym := fmt.Sprintf("Symbol%d", i)
		file := fmt.Sprintf("symbol_%02d.ts", i)
		src := fmt.Sprintf("export const %s = %q;", sym, strings.Repeat("é", bodyRunes))
		if err := os.WriteFile(filepath.Join(proj, file), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: file, Symbol: sym, FQN: sym,
			Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1,
			SourceHash: fmt.Sprintf("hash-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
		syms[i] = sym
	}

	// If Context accidentally uses public Callers/Callees, auto-upgrade will find
	// and execute this stub. Direct indexed-graph relations must leave marker absent.
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "lsp-invoked")
	server := filepath.Join(binDir, "typescript-language-server")
	script := "#!/bin/sh\necho invoked >> " + marker + "\nexit 1\n"
	if err := os.WriteFile(server, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return NewService(sess), proj, syms, marker
}

func TestContextBatchUnresolvedUsesNoLanguageServersAndBudgetsSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a small POSIX language-server stub")
	}
	svc, proj, syms, marker := unresolvedContextProject(t, contextBatchMax, 4000)

	rep, err := svc.ContextBatchWithContext(context.Background(), proj, syms, nil, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != contextBatchMax {
		t.Fatalf("results = %d, want %d", len(rep.Results), contextBatchMax)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("context_batch spawned a language server; marker stat error = %v", err)
	}
	for _, result := range rep.Results {
		if result.CallGraph != CallGraphUnresolved || result.Resolution == "" {
			t.Fatalf("%s should stay honestly unresolved, got graph=%q resolution=%q", result.Symbol, result.CallGraph, result.Resolution)
		}
		if len(result.Next) == 0 || result.Next[0].Tool != "codemap_index" {
			t.Fatalf("%s should recommend precise indexing, got next=%+v", result.Symbol, result.Next)
		}
	}
	if rep.SourceBudget.OriginalBytes <= rep.SourceBudget.LimitBytes {
		t.Fatalf("fixture did not exceed budget: %+v", rep.SourceBudget)
	}
	if rep.SourceBudget.IncludedBytes > rep.SourceBudget.LimitBytes {
		t.Fatalf("included source exceeds budget: %+v", rep.SourceBudget)
	}
	if rep.SourceBudget.TruncatedDefinitions == 0 || len(rep.SourceTruncations) == 0 {
		t.Fatalf("expected explicit truncation metadata, got budget=%+v truncations=%+v", rep.SourceBudget, rep.SourceTruncations)
	}
	if !strings.Contains(rep.Note, "source bodies exceeded") {
		t.Fatalf("batch note must disclose source truncation, got %q", rep.Note)
	}
	for _, result := range rep.Results {
		for _, def := range result.Definitions {
			if !utf8.ValidString(def.Source) {
				t.Fatalf("truncated source for %s is not valid UTF-8", result.Symbol)
			}
		}
	}

	// The single-symbol API remains complete; only batch responses apply the
	// aggregate body budget.
	single, err := svc.ContextWithContext(context.Background(), proj, syms[len(syms)-1], 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Definitions) != 1 || len(single.Definitions[0].Source) != rep.SourceTruncations[len(rep.SourceTruncations)-1].OriginalBytes {
		t.Fatalf("single context source should remain complete, got %+v", single.Definitions)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("single context spawned a language server; marker stat error = %v", err)
	}
}

func TestContextWithContextCancelsVecgrepRecall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a deliberately hung POSIX vecgrep stub")
	}
	svc, proj, syms, _ := unresolvedContextProject(t, 1, 10)
	bin := filepath.Join(t.TempDir(), "vecgrep")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The helper returns a Service backed by a live session; set its optional
	// memory sidecar to the deliberately hung executable.
	svc.s.Config.Vecgrep = config.VecgrepConfig{Enabled: true, Bin: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.ContextWithContext(ctx, proj, syms[0], 2, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ContextWithContext error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("caller cancellation took %s; memory recall likely used background context", elapsed)
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := svc.ContextBatchWithContext(cancelled, proj, syms, nil, 2, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("ContextBatchWithContext error = %v, want context canceled", err)
	}

	// A selector-containing batch must still abort on a real cancellation —
	// the new per-item "partial, don't abort" path (design decision #7) is
	// reserved for a selector's own validation failure, not ctx.Err().
	selCancelled, stopSel := context.WithCancel(context.Background())
	stopSel()
	sel := SymbolSelector{File: "symbol_00.ts", StartLine: 1, FQN: syms[0]}
	if _, err := svc.ContextBatchWithContext(selCancelled, proj, nil, []SymbolSelector{sel}, 2, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("ContextBatchWithContext (selector) error = %v, want context canceled", err)
	}
}

func TestContextSurfacesVecgrepExecutionAndJSONFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses small POSIX vecgrep failure stubs")
	}
	svc, proj, syms, _ := unresolvedContextProject(t, 1, 10)
	cases := []struct {
		name   string
		script string
	}{
		{"execution", "#!/bin/sh\nexit 7\n"},
		{"json", "#!/bin/sh\necho not-json\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "vecgrep")
			if err := os.WriteFile(bin, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			svc.s.Config.Vecgrep = config.VecgrepConfig{Enabled: true, Bin: bin}
			rep, err := svc.ContextWithContext(context.Background(), proj, syms[0], 2, false)
			if err != nil {
				t.Fatalf("optional memory failure should preserve context: %v", err)
			}
			found := false
			for _, partial := range rep.PartialErrors {
				if partial.Component == "memory_recall" {
					found = true
					if partial.Error == "" {
						t.Fatal("memory_recall partial error has no message")
					}
				}
			}
			if !found {
				t.Fatalf("%s failure missing from partial_errors: %+v", tc.name, rep.PartialErrors)
			}
			batch, err := svc.ContextBatchWithContext(context.Background(), proj, syms, nil, 2, false)
			if err != nil {
				t.Fatalf("batch should preserve partial context: %v", err)
			}
			if len(batch.PartialErrors) == 0 || batch.PartialErrors[0].Component != "memory_recall" {
				t.Fatalf("batch did not aggregate memory failure: %+v", batch.PartialErrors)
			}
		})
	}
}

func TestContextPartialErrorsAreExplicitAndBounded(t *testing.T) {
	rep := &ContextReport{Symbol: "Target", CallGraph: CallGraphNone}
	longErr := errors.New(strings.Repeat("x", contextErrorMaxRunes+50))
	applyContextRelation(rep, "callers", nil, longErr)
	applyContextReferences(rep, nil, errors.New("references unavailable"))
	applyContextImpact(rep, nil, errors.New("impact unavailable"))

	if len(rep.PartialErrors) != 3 {
		t.Fatalf("partial errors = %+v, want callers + references + impact", rep.PartialErrors)
	}
	if rep.PartialErrors[0].Component != "callers" || rep.PartialErrors[1].Component != "references" || rep.PartialErrors[2].Component != "impact" {
		t.Fatalf("unexpected partial error components: %+v", rep.PartialErrors)
	}
	if got := utf8.RuneCountInString(rep.PartialErrors[0].Error); got > contextErrorMaxRunes {
		t.Fatalf("partial error has %d runes, cap is %d", got, contextErrorMaxRunes)
	}
}

func TestContextReferencesAreCappedWithoutChangingCallGraph(t *testing.T) {
	sites := make([]ReferenceSite, contextListCap+5)
	for i := range sites {
		sites[i] = ReferenceSite{Source: SymbolRef{Symbol: fmt.Sprintf("Wire%d", i)}}
	}
	rep := &ContextReport{Symbol: "Target", CallGraph: CallGraphResolved}
	applyContextReferences(rep, &ReferencesReport{
		References: sites, ReferencesTotal: len(sites) + 2,
		Coverage: ReferenceCoveragePartial, Confidence: ReferenceConfidenceMixed,
		Stale: true, Resolution: "partial reference coverage",
	}, nil)
	if len(rep.References) != contextListCap || rep.ReferencesTotal != contextListCap+7 || rep.ReferencesTruncated != 7 {
		t.Fatalf("context references cap/totals = len:%d total:%d truncated:%d", len(rep.References), rep.ReferencesTotal, rep.ReferencesTruncated)
	}
	if rep.ReferencesCoverage != ReferenceCoveragePartial || !rep.ReferencesStale ||
		rep.ReferencesConfidence != ReferenceConfidenceMixed || rep.ReferencesResolution == "" {
		t.Fatalf("context lost reference honesty: %+v", rep)
	}
	if rep.CallGraph != CallGraphResolved {
		t.Fatalf("references changed independent call_graph to %q", rep.CallGraph)
	}
}
