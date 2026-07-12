package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestDependenciesGroupsEvidenceAndExposesCoverage(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "target.go", "package app\n\nfunc Target() {}\n")
	mustWrite(t, proj, "wire.go", "package app\n\nfunc Use() { Target() }\n\nvar Hooks = []func(){Target}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Dependencies(proj, "target.go")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || !rep.Found || rep.DependentsTotal != 1 || len(rep.Dependents) != 1 {
		t.Fatalf("dependency report = %+v, want one dependent file", rep)
	}
	dep := rep.Dependents[0]
	if dep.File != "wire.go" || dep.EvidenceTotal != 2 || dep.FileScopedTotal != 2 {
		t.Fatalf("wire.go evidence = %+v, want one call + one function-value reference", dep)
	}
	kinds := map[string]DependencyKindEvidence{}
	for _, kind := range dep.Kinds {
		kinds[kind.Kind] = kind
	}
	for _, kind := range []string{graph.EdgeCalls, graph.EdgeReferences} {
		if kinds[kind].Total != 1 || len(kinds[kind].Samples) != 1 {
			t.Errorf("%s evidence = %+v, want one bounded source→target sample", kind, kinds[kind])
		}
		if sample := kinds[kind].Samples[0]; sample.Source.File != "wire.go" || sample.Target.File != "target.go" || sample.Target.Symbol != "Target" || sample.TargetScope != DependencyTargetFile || sample.Confidence != DependencyConfidenceConfirmed || sample.ConfidenceReason != DependencyReasonSamePackage {
			t.Errorf("%s sample = %+v", kind, sample)
		}
	}
	assertDependencyTotals(t, rep)
	if rep.ConfirmedTotal != 2 || rep.CandidateTotal != 0 || rep.ConfirmedFileScopedTotal != 2 || rep.CandidateFileScopedTotal != 0 {
		t.Fatalf("same-package confidence totals = %+v", rep)
	}
	if rep.CallGraph != CallGraphName || rep.Coverage.Complete {
		t.Fatalf("coverage = %+v call_graph=%q, want explicit incomplete name/reference/import coverage", rep.Coverage, rep.CallGraph)
	}
	if status := dependencyDomainStatus(rep.Coverage, "references"); status != DependencyCoveragePartial {
		t.Errorf("references coverage = %q, want partial", status)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id":`) {
		t.Fatalf("public dependency evidence leaked raw DB ids: %s", raw)
	}
}

func TestDependenciesUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).Dependencies(t.TempDir(), "missing.go")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Found || rep.CallGraph != CallGraphNone || len(rep.Dependents) != 0 {
		t.Fatalf("unindexed dependencies = %+v", rep)
	}
}

func TestDependenciesGoImportIsPackageScopedAndIncremental(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "go.mod", "module example.test/deps\n\ngo 1.25\n")
	mustWrite(t, proj, "a/a.go", "package a\n\ntype Thing struct{}\n")
	mustWrite(t, proj, "b/b.go", "package b\n\nimport \"example.test/deps/a\"\n\nvar _ = a.Thing{}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Dependencies(proj, "a/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if rep.EvidenceTotal != 1 || rep.FileScopedEvidenceTotal != 0 || rep.PackageScopedEvidenceTotal != 1 {
		t.Fatalf("Go import evidence = %+v, want one package-scoped relationship", rep)
	}
	sample := rep.Dependents[0].Kinds[0].Samples[0]
	if sample.TargetScope != DependencyTargetPackage || sample.Source.File != "b/b.go" || sample.Confidence != DependencyConfidenceCandidate || sample.ConfidenceReason != DependencyReasonPackageScope {
		t.Fatalf("Go import sample = %+v, want package-scoped b/b.go→a/a.go", sample)
	}
	assertDependencyTotals(t, rep)
	impact, err := svc.FileImpact(proj, "a/a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if impact.DeleteVerdict != DeleteVerdictUnknown || impact.SafeToDelete || !contains(impact.DependentFiles, "b/b.go") {
		t.Fatalf("package-only import must remain an unknown exact-file verdict while surfacing b/b.go: %+v", impact)
	}

	// Removing the import on an incremental index replaces, rather than appends,
	// the file's import evidence.
	mustWrite(t, proj, "b/b.go", "package b\n\nvar Local = 1\n")
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Dependencies(proj, "a/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if rep.EvidenceTotal != 0 || rep.DependentsTotal != 0 {
		t.Fatalf("removed import left stale dependency evidence: %+v", rep)
	}
}

func TestFileImpactReferenceOnlyEvidenceIsUnsafe(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "target.go", "package app\n\nfunc Target() {}\n")
	mustWrite(t, proj, "wire.go", "package app\n\nvar Hooks = []func(){Target}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	impact, err := svc.FileImpact(proj, "target.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if impact.DeleteVerdict != DeleteVerdictUnsafe || impact.SafeToDelete || !contains(impact.DependentFiles, "wire.go") {
		t.Fatalf("function-value reference should prove target.go unsafe: %+v", impact)
	}
	if impact.DependencyEvidence == nil || impact.DependencyEvidence.FileScopedEvidenceTotal != 1 {
		t.Fatalf("FileImpact did not embed reusable dependency evidence: %+v", impact.DependencyEvidence)
	}
	if impact.DependencyEvidence.ConfirmedFileScopedTotal != 1 || impact.DependencyEvidence.CandidateFileScopedTotal != 0 {
		t.Fatalf("same-package reference confidence = %+v", impact.DependencyEvidence)
	}
	if kinds := dependencyEvidenceKinds(impact.DependencyEvidence, true); len(kinds) != 1 || kinds[0] != graph.EdgeReferences {
		t.Fatalf("unsafe evidence kinds = %v, want references", kinds)
	}
}

func TestDependenciesCapsDependentsAndSamplesWithTotals(t *testing.T) {
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
		Kind: graph.KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantEvidence := 0
	for i := 0; i < dependencyFileCap+5; i++ {
		perFile := 1
		if i == 0 {
			perFile = dependencySampleCap + 4
		}
		for j := 0; j < perFile; j++ {
			symbol := fmt.Sprintf("Use%d_%d", i, j)
			source, addErr := g.AddNode(&graph.Node{
				ProjectID: pid, FilePath: fmt.Sprintf("f%02d.go", i), Symbol: symbol, FQN: "app." + symbol,
				Kind: graph.KindFunction, Language: "go", StartLine: j + 1, EndLine: j + 1, SourceHash: "h",
			})
			if addErr != nil {
				t.Fatal(addErr)
			}
			if _, addErr = g.AddEdge(source, target, graph.EdgeCalls, graph.WeightLSP); addErr != nil {
				t.Fatal(addErr)
			}
			wantEvidence++
		}
	}

	rep, err := NewService(sess).Dependencies(proj, "target.go")
	if err != nil {
		t.Fatal(err)
	}
	if rep.DependentsTotal != dependencyFileCap+5 || len(rep.Dependents) != dependencyFileCap || rep.DependentsTruncated != 5 {
		t.Fatalf("dependent cap metadata = total:%d shown:%d truncated:%d", rep.DependentsTotal, len(rep.Dependents), rep.DependentsTruncated)
	}
	if rep.EvidenceTotal != wantEvidence || rep.SamplesTotal > dependencyGlobalSampleCap || rep.SamplesTruncated != wantEvidence-rep.SamplesTotal {
		t.Fatalf("sample cap metadata = evidence:%d samples:%d truncated:%d, want evidence:%d", rep.EvidenceTotal, rep.SamplesTotal, rep.SamplesTruncated, wantEvidence)
	}
	assertDependencyTotals(t, rep)
	if rep.ConfirmedTotal != wantEvidence || rep.CandidateTotal != 0 {
		t.Fatalf("same-package capped confidence totals = confirmed:%d candidate:%d", rep.ConfirmedTotal, rep.CandidateTotal)
	}
	first := rep.Dependents[0].Kinds[0]
	if first.Total != dependencySampleCap+4 || len(first.Samples) != dependencySampleCap || first.SamplesTruncated != 4 {
		t.Fatalf("per-kind sample cap = %+v", first)
	}
}

func TestDependenciesQualifiedCrossPackageNameFanoutIsCandidate(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "go.mod", "module example.test/confidence\n\ngo 1.25\n")
	mustWrite(t, proj, "app/service.go", "package app\n\ntype Service struct{}\nfunc (Service) Context() {}\n")
	mustWrite(t, proj, "cli/command.go", "package cli\n\ntype Command struct{}\nfunc (Command) Context() {}\nfunc Run(cmd Command) { cmd.Context() }\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Dependencies(proj, "app/service.go")
	if err != nil {
		t.Fatal(err)
	}
	assertDependencyTotals(t, rep)
	if rep.ConfirmedTotal != 0 || rep.CandidateTotal != 1 || rep.ConfirmedFileScopedTotal != 0 || rep.CandidateFileScopedTotal != 1 {
		t.Fatalf("qualified same-name fanout confidence = %+v", rep)
	}
	sample := rep.Dependents[0].Kinds[0].Samples[0]
	if sample.Confidence != DependencyConfidenceCandidate || sample.ConfidenceReason != DependencyReasonNameFanout || sample.Weight != graph.WeightTreeSitter {
		t.Fatalf("qualified same-name fanout sample = %+v", sample)
	}
	impact, err := svc.FileImpact(proj, "app/service.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if impact.DeleteVerdict != DeleteVerdictUnknown || impact.SafeToDelete || impact.BreakingChange {
		t.Fatalf("candidate fanout must not prove the exact file unsafe: %+v", impact)
	}
	if !strings.Contains(impact.Note, "breaking_change withheld") {
		t.Fatalf("candidate-only breaking-change evidence must be explicit: %q", impact.Note)
	}
	if !hasNextAction(impact.Next, "codemap_index", true) {
		t.Fatalf("candidate evidence should recommend a precise reindex: %+v", impact.Next)
	}
}

func TestDependenciesStaleEvidenceIsCandidate(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	mustWrite(t, proj, "target.go", "package app\n\nfunc Target() {}\n")
	mustWrite(t, proj, "wire.go", "package app\n\nfunc Use() { Target() }\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	// The stored same-package edge was confirmed when indexed, but any positive
	// claim from a stale snapshot must be treated as a candidate until refreshed.
	mustWrite(t, proj, "wire.go", "package app\n\nfunc Use() { Target(); Target() }\n")

	rep, err := svc.Dependencies(proj, "target.go")
	if err != nil {
		t.Fatal(err)
	}
	assertDependencyTotals(t, rep)
	if !rep.Stale || rep.ConfirmedTotal != 0 || rep.CandidateTotal != 1 || rep.CandidateFileScopedTotal != 1 {
		t.Fatalf("stale confidence totals = %+v", rep)
	}
	sample := rep.Dependents[0].Kinds[0].Samples[0]
	if sample.Confidence != DependencyConfidenceCandidate || sample.ConfidenceReason != DependencyReasonStale {
		t.Fatalf("stale sample confidence = %+v", sample)
	}
	impact, err := svc.FileImpact(proj, "target.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if impact.DeleteVerdict != DeleteVerdictUnknown || impact.SafeToDelete {
		t.Fatalf("stale evidence must keep deletion unknown: %+v", impact)
	}
	if !hasNextAction(impact.Next, "codemap_index", true) {
		t.Fatalf("stale call evidence should recommend a precise refresh: %+v", impact.Next)
	}
}

func TestDependenciesSortsConfirmedEvidenceBeforeCandidates(t *testing.T) {
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
	add := func(file, symbol string) int64 {
		t.Helper()
		id, addErr := g.AddNode(&graph.Node{ProjectID: pid, FilePath: file, Symbol: symbol, FQN: "app." + symbol, Kind: graph.KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h"})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return id
	}
	target := add("target.go", "Target")
	candidate := add("a/candidate.go", "Candidate")
	confirmed := add("z_confirmed.go", "Confirmed")
	if _, err := g.AddEdge(candidate, target, graph.EdgeCalls, graph.WeightTreeSitter); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(confirmed, target, graph.EdgeCalls, graph.WeightLSP, graph.ProvPrecise); err != nil {
		t.Fatal(err)
	}

	rep, err := NewService(sess).Dependencies(proj, "target.go")
	if err != nil {
		t.Fatal(err)
	}
	assertDependencyTotals(t, rep)
	if len(rep.Dependents) != 2 || rep.Dependents[0].File != "z_confirmed.go" || rep.Dependents[0].ConfirmedTotal != 1 || rep.Dependents[1].File != "a/candidate.go" {
		t.Fatalf("confirmed evidence should sort before candidates: %+v", rep.Dependents)
	}
	if rep.Dependents[0].Kinds[0].Samples[0].ConfidenceReason != DependencyReasonPrecise {
		t.Fatalf("precise sample = %+v", rep.Dependents[0].Kinds[0].Samples[0])
	}
}

func assertDependencyTotals(t *testing.T, rep *FileDependenciesReport) {
	t.Helper()
	if rep.ConfirmedTotal+rep.CandidateTotal != rep.EvidenceTotal {
		t.Errorf("report confidence totals do not partition evidence: confirmed=%d candidate=%d total=%d", rep.ConfirmedTotal, rep.CandidateTotal, rep.EvidenceTotal)
	}
	if rep.ConfirmedFileScopedTotal+rep.CandidateFileScopedTotal != rep.FileScopedEvidenceTotal {
		t.Errorf("file-scoped confidence totals do not partition evidence: confirmed=%d candidate=%d total=%d", rep.ConfirmedFileScopedTotal, rep.CandidateFileScopedTotal, rep.FileScopedEvidenceTotal)
	}
	for _, dependent := range rep.Dependents {
		if dependent.ConfirmedTotal+dependent.CandidateTotal != dependent.EvidenceTotal {
			t.Errorf("dependent %s confidence totals do not partition evidence: %+v", dependent.File, dependent)
		}
		for _, kind := range dependent.Kinds {
			if kind.ConfirmedTotal+kind.CandidateTotal != kind.Total {
				t.Errorf("dependent %s kind %s confidence totals do not partition evidence: %+v", dependent.File, kind.Kind, kind)
			}
		}
	}
}

func hasNextAction(actions []NextAction, tool string, precise bool) bool {
	for _, action := range actions {
		if action.Tool != tool {
			continue
		}
		got, _ := action.Args["precise"].(bool)
		if got == precise {
			return true
		}
	}
	return false
}

func dependencyDomainStatus(coverage FileDependencyCoverage, domain string) string {
	for _, entry := range coverage.Domains {
		if entry.Domain == domain {
			return entry.Status
		}
	}
	return ""
}
