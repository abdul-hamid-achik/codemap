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
		if sample := kinds[kind].Samples[0]; sample.Source.File != "wire.go" || sample.Target.File != "target.go" || sample.Target.Symbol != "Target" || sample.TargetScope != DependencyTargetFile {
			t.Errorf("%s sample = %+v", kind, sample)
		}
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
	if sample.TargetScope != DependencyTargetPackage || sample.Source.File != "b/b.go" {
		t.Fatalf("Go import sample = %+v, want package-scoped b/b.go→a/a.go", sample)
	}
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
	first := rep.Dependents[0].Kinds[0]
	if first.Total != dependencySampleCap+4 || len(first.Samples) != dependencySampleCap || first.SamplesTruncated != 4 {
		t.Fatalf("per-kind sample cap = %+v", first)
	}
}

func dependencyDomainStatus(coverage FileDependencyCoverage, domain string) string {
	for _, entry := range coverage.Domains {
		if entry.Domain == domain {
			return entry.Status
		}
	}
	return ""
}
