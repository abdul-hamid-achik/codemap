package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestReferencesSeparateValueWiringFromCallsAndExposeCoverage(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "handler.go", "package app\n\nfunc Handler() {}\nfunc Unwired() {}\nfunc Caller() { Handler() }\n")
	mustWrite(t, proj, "hooks.go", `package app

var Hook = Handler

func register(func()) {}
func Setup() { register(Handler) }
`)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.References(proj, "Handler")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.SchemaVersion != 1 || rep.ReferencesTotal != 2 || len(rep.References) != 2 || rep.ReferencesTruncated != 0 {
		t.Fatalf("Handler references = %+v", rep)
	}
	if rep.Coverage != ReferenceCoveragePartial || !strings.Contains(rep.Resolution, "empty result does not prove no wiring") {
		t.Fatalf("reference coverage is not conservative: %+v", rep)
	}
	if rep.Confidence != DependencyConfidenceConfirmed || rep.CallGraph != CallGraphName {
		t.Fatalf("confidence/call graph = %q/%q, want confirmed/name", rep.Confidence, rep.CallGraph)
	}
	got := map[string]ReferenceSite{}
	for _, site := range rep.References {
		got[site.Source.Kind+":"+site.Source.Symbol] = site
		if site.Source.Symbol == "Caller" {
			t.Fatalf("ordinary call leaked into value references: %+v", site)
		}
	}
	if _, ok := got[graph.KindFile+":"]; !ok {
		t.Fatalf("top-level registration did not surface as file scope: %+v", rep.References)
	}
	if _, ok := got[graph.KindFunction+":Setup"]; !ok {
		t.Fatalf("callback argument did not surface its enclosing function: %+v", rep.References)
	}

	empty, err := svc.References(proj, "Unwired")
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Found || empty.ReferencesTotal != 0 || empty.Confidence != ReferenceConfidenceNone ||
		empty.Coverage != ReferenceCoveragePartial || !strings.Contains(empty.Resolution, "does not prove no wiring") ||
		!strings.Contains(empty.Note, "absence is not proof") {
		t.Fatalf("empty Go references looked exhaustive: %+v", empty)
	}
	if err := os.WriteFile(filepath.Join(proj, "hooks.go"), []byte("package app\n// drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.References(proj, "Handler")
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.Confidence != DependencyConfidenceCandidate {
		t.Fatalf("stale confirmed sites were not downgraded: %+v", stale)
	}
	for _, site := range stale.References {
		if site.Confidence != DependencyConfidenceCandidate || site.ConfidenceReason != DependencyReasonStale {
			t.Fatalf("stale site retained stronger confidence: %+v", site)
		}
	}
}

func TestReferencesSelectorKeepsFanoutCandidateAndSurvivesLineShift(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "left.go", `package app

type Left struct{}

func (Left) Shared() {}
`)
	mustWrite(t, proj, "right.go", `package app

type Right struct{}

func (Right) Shared() {}
`)
	mustWrite(t, proj, "wire.go", `package app

func register(func()) {}
func Wire() { register(Left{}.Shared) }
`)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	merged, err := svc.References(proj, "Shared")
	if err != nil {
		t.Fatal(err)
	}
	if merged.DefinitionsTotal != 2 || merged.ReferencesTotal != 1 || merged.Confidence != DependencyConfidenceCandidate {
		t.Fatalf("name-union Shared references = %+v", merged)
	}
	if _, _, err := svc.AnnotateNode(proj, "app.Left.Shared", "test", "left only", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AnnotateNode(proj, "app.Right.Shared", "test", "right only", ""); err != nil {
		t.Fatal(err)
	}

	selector := SymbolSelector{File: "left.go", StartLine: 999, FQN: "app.Left.Shared", Kind: graph.KindMethod}
	exact, err := svc.ReferencesBySelector(proj, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !exact.Found || exact.DefinitionsTotal != 1 || exact.Selector == nil || exact.Selector.FQN != "app.Left.Shared" ||
		exact.ReferencesTotal != 1 || exact.References[0].Confidence != DependencyConfidenceCandidate ||
		exact.References[0].ConfidenceReason != DependencyReasonNameFanout {
		t.Fatalf("exact selector incorrectly upgraded or crossed definitions: %+v", exact)
	}
	if len(exact.Annotations) != 1 || exact.Annotations[0].Note != "left only" {
		t.Fatalf("exact selector leaked same-name annotations: %+v", exact.Annotations)
	}

	// Any working-tree drift downgrades stored sites, even those that were
	// otherwise confirmed by same-package resolution.
	if err := os.WriteFile(filepath.Join(proj, "wire.go"), []byte("package app\n// drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.ReferencesBySelector(proj, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.References[0].Confidence != DependencyConfidenceCandidate ||
		stale.References[0].ConfidenceReason != DependencyReasonStale {
		t.Fatalf("stale reference was not downgraded: %+v", stale)
	}
	ctxRep, err := svc.ContextBySelectorWithContext(context.Background(), proj, selector, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ctxRep.ReferencesStale || ctxRep.ReferencesCoverage != ReferenceCoveragePartial ||
		ctxRep.ReferencesConfidence != DependencyConfidenceCandidate || ctxRep.ReferencesResolution == "" ||
		ctxRep.CallGraph != stale.CallGraph {
		t.Fatalf("context lost reference honesty or changed call-graph semantics: %+v", ctxRep)
	}
}

func TestReferencesAreBoundedAndUnsupportedLanguagesStayUnavailable(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(config.DeriveProjectName(proj), proj, "go")
	if err != nil {
		t.Fatal(err)
	}
	target, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "target.go", Symbol: "Target", FQN: "app.Target",
		Kind: graph.KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "target",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < referenceSiteCap+5; i++ {
		source, addErr := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: fmt.Sprintf("wire/%03d.go", i), Symbol: fmt.Sprintf("Wire%03d", i),
			FQN: fmt.Sprintf("app.Wire%03d", i), Kind: graph.KindFunction, Language: "go",
			StartLine: 1, EndLine: 1, SourceHash: fmt.Sprintf("%d", i),
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		if _, addErr = g.AddEdge(source, target, graph.EdgeReferences, graph.WeightLSP); addErr != nil {
			t.Fatal(addErr)
		}
	}
	typescriptTarget, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "handler.ts", Symbol: "TSHandler", FQN: "TSHandler",
		Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1, SourceHash: "ts",
	})
	if err != nil || typescriptTarget == 0 {
		t.Fatal(err)
	}
	for i := 0; i < referenceDefinitionCap+5; i++ {
		if _, err := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: fmt.Sprintf("defs/%03d.go", i), Symbol: "Shared", FQN: fmt.Sprintf("app.Type%d.Shared", i),
			Kind: graph.KindMethod, Language: "go", StartLine: 1, EndLine: 1, SourceHash: fmt.Sprintf("shared-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(sess)
	rep, err := svc.References(proj, "Target")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.References) != referenceSiteCap || rep.ReferencesTotal != referenceSiteCap+5 || rep.ReferencesTruncated != 5 {
		t.Fatalf("bounded references = len:%d total:%d truncated:%d", len(rep.References), rep.ReferencesTotal, rep.ReferencesTruncated)
	}
	if rep.ReferencesTotal != len(rep.References)+rep.ReferencesTruncated {
		t.Fatalf("total invariant failed: %+v", rep)
	}
	defs, err := svc.References(proj, "Shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs.Definitions) != referenceDefinitionCap || defs.DefinitionsTotal != referenceDefinitionCap+5 || defs.DefinitionsTruncated != 5 {
		t.Fatalf("bounded definitions = len:%d total:%d truncated:%d", len(defs.Definitions), defs.DefinitionsTotal, defs.DefinitionsTruncated)
	}

	unsupported, err := svc.References(proj, "TSHandler")
	if err != nil {
		t.Fatal(err)
	}
	if !unsupported.Found || unsupported.Coverage != ReferenceCoverageUnavailable ||
		!strings.Contains(unsupported.Resolution, "does not prove no wiring") {
		t.Fatalf("unsupported language looked exhaustive: %+v", unsupported)
	}
}

func TestReferencesUniqueCrossPackageSelectorRemainsCandidate(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "a/handler.go", "package a\n\nfunc Handler() {}\n")
	mustWrite(t, proj, "b/wire.go", `package b

import "example.test/a"

func register(func()) {}
func Wire() { register(a.Handler) }
`)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.References(proj, "Handler")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.ReferencesTotal != 1 || len(rep.References) != 1 {
		t.Fatalf("cross-package references = %+v", rep)
	}
	if rep.Confidence != DependencyConfidenceCandidate ||
		rep.References[0].Confidence != DependencyConfidenceCandidate ||
		rep.References[0].ConfidenceReason != DependencyReasonNameFanout {
		t.Fatalf("unique cross-package selector was falsely confirmed: %+v", rep)
	}
}
