package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/google/jsonschema-go/jsonschema"
)

// taskContextFixture indexes a small Go project: Hub, one explicit caller
// (Entry), and 30 generated callers so impact lists exceed contextListCap and
// the wrapper's *_total disclosure is observable.
func taskContextFixture(t *testing.T) (*Service, string, SymbolSelector) {
	t.Helper()
	isolate(t)
	root := t.TempDir()
	var callers strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&callers, "func C%d() { Hub() }\n", i)
	}
	files := map[string]string{
		"go.mod":   "module example.com/taskctx\n\ngo 1.25\n",
		"main.go":  "package sample\n\n// Hub is load-bearing.\nfunc Hub() {}\n\nfunc Entry() { Hub() }\n",
		"hub30.go": "package sample\n\n" + callers.String(),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep.Enabled = false
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	found, err := svc.FindSymbols(root, "Hub", 5)
	if err != nil || len(found.Hits) == 0 || found.Hits[0].Selector == nil {
		t.Fatalf("fixture lost Hub: err=%v hits=%d", err, len(found.Hits))
	}
	return svc, root, *found.Hits[0].Selector
}

func TestTaskContextValidation(t *testing.T) {
	svc, root, sel := taskContextFixture(t)
	cases := []struct {
		name string
		task string
		opts TaskContextOptions
		hint string
	}{
		{"blank task", "  ", TaskContextOptions{}, "pass a task"},
		{"review mode", "fix the bug", TaskContextOptions{Mode: "review"}, "codemap_review"},
		{"unknown mode", "fix the bug", TaskContextOptions{Mode: "verify"}, "understand, change, or debug"},
		{"selectors with understand", "fix the bug", TaskContextOptions{Selectors: []SymbolSelector{sel}}, "change or debug"},
	}
	for _, tc := range cases {
		_, err := svc.TaskContext(context.Background(), root, tc.task, tc.opts)
		if err == nil {
			t.Fatalf("%s: expected invalid_input error", tc.name)
		}
		if CodeOf(err) != CodeInvalidInput {
			t.Fatalf("%s: code=%s want invalid_input", tc.name, CodeOf(err))
		}
		if !strings.Contains(HintOf(err), tc.hint) {
			t.Fatalf("%s: hint %q missing %q", tc.name, HintOf(err), tc.hint)
		}
	}
}

func TestTaskContextUnderstandMode(t *testing.T) {
	svc, root, _ := taskContextFixture(t)
	rep, err := svc.TaskContext(context.Background(), root, "Hub", TaskContextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != TaskContextSchemaVersion || rep.Mode != TaskModeUnderstand || !rep.Indexed {
		t.Fatalf("identity = %+v", rep)
	}
	if rep.Explore == nil || len(rep.Explore.Seeds) == 0 {
		t.Fatalf("understand must carry explore seeds: %+v", rep.Explore)
	}
	if rep.Targets != nil || rep.Contexts != nil || rep.Impacts != nil || rep.RelatedFiles != nil {
		t.Fatalf("understand must not assemble change/debug sections")
	}
	if !rep.Freshness.Checked || rep.Freshness.Stale {
		t.Fatalf("freshness = %+v (freshly indexed fixture)", rep.Freshness)
	}
	if rep.CallGraph == CallGraphNone {
		t.Fatalf("call_graph should aggregate explore contexts, got %q", rep.CallGraph)
	}

	// Determinism: same inputs + unchanged tree → byte-identical JSON.
	again, err := svc.TaskContext(context.Background(), root, "Hub", TaskContextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(rep)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatalf("understand is not deterministic:\n%s\n%s", a, b)
	}
}

func TestTaskContextChangeModeSelectors(t *testing.T) {
	svc, root, hub := taskContextFixture(t)
	rep, err := svc.TaskContext(context.Background(), root, "make Hub safer", TaskContextOptions{
		Mode: TaskModeChange, Selectors: []SymbolSelector{hub},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Targets) != 1 || !rep.Targets[0].Found || rep.Targets[0].Source != "selector" {
		t.Fatalf("targets = %+v", rep.Targets)
	}
	if rep.Explore != nil {
		t.Fatal("change with selectors must skip explore (the caller already knows where)")
	}
	if rep.Contexts == nil || len(rep.Contexts.Results) != 1 || !rep.Contexts.Results[0].Found {
		t.Fatalf("contexts = %+v", rep.Contexts)
	}
	for _, def := range rep.Contexts.Results[0].Definitions {
		if !def.SourceOmitted || def.Source != "" {
			t.Fatalf("change contexts must be brief: %+v", def)
		}
	}
	for _, c := range rep.Contexts.Results {
		if len(c.Memories) > 0 {
			t.Fatal("task-context disables memory recall for the whole call")
		}
	}
	if len(rep.Impacts) != 1 {
		t.Fatalf("impacts = %+v", rep.Impacts)
	}
	imp := rep.Impacts[0]
	if len(imp.Impact.DirectCallers) != contextListCap || imp.DirectCallersTotal <= contextListCap {
		t.Fatalf("impact cap disclosure broken: shown=%d total=%d",
			len(imp.Impact.DirectCallers), imp.DirectCallersTotal)
	}
	if len(imp.Impact.BlastRadius) > contextListCap || len(imp.Impact.Tests) > contextListCap {
		t.Fatalf("impact lists exceed contextListCap")
	}
	if len(rep.RelatedFiles) != 1 || rep.RelatedFiles[0].File != "main.go" {
		t.Fatalf("related_files = %+v", rep.RelatedFiles)
	}
	rf := rep.RelatedFiles[0]
	if len(rf.Related) > taskRelatedFilesCap || rf.RelatedTotal < len(rf.Related) {
		t.Fatalf("related cap disclosure broken: %+v", rf)
	}
	sawHub30 := false
	for _, r := range rf.Related {
		if r.RelativePath == "hub30.go" && r.Reason == "caller" {
			sawHub30 = true
		}
	}
	if !sawHub30 {
		t.Fatalf("related files missing hub30.go caller evidence: %+v", rf.Related)
	}
	if !rep.Freshness.Checked {
		t.Fatal("freshness must be checked on an indexed project")
	}
}

func TestTaskContextDebugModeExploreTargets(t *testing.T) {
	svc, root, _ := taskContextFixture(t)
	rep, err := svc.TaskContext(context.Background(), root, "Hub", TaskContextOptions{Mode: TaskModeDebug})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Explore == nil || len(rep.Explore.Seeds) == 0 {
		t.Fatalf("debug must carry explore: %+v", rep.Explore)
	}
	if len(rep.Targets) == 0 || rep.Targets[0].Source != "explore" || !rep.Targets[0].Found {
		t.Fatalf("targets must come from explore-joined seeds: %+v", rep.Targets)
	}
	if len(rep.Targets) > DefaultExploreSeeds {
		t.Fatalf("explore-derived targets exceed %d: %d", DefaultExploreSeeds, len(rep.Targets))
	}
	if rep.Contexts == nil {
		t.Fatal("debug must assemble contexts")
	}
	for _, c := range rep.Contexts.Results {
		if len(c.Callers) > MaxExploreEdges || len(c.Callees) > MaxExploreEdges {
			t.Fatalf("debug contexts must be edge-capped at %d: %+v", MaxExploreEdges, c)
		}
	}
	if rep.Impacts != nil || rep.RelatedFiles != nil {
		t.Fatal("impact drill-downs and related files are change-mode sections")
	}
}

func TestTaskContextUnindexedNeverPretendsFresh(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	rep, err := svc.TaskContext(context.Background(), t.TempDir(), "anything", TaskContextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.Freshness.Checked || rep.Freshness.Stale {
		t.Fatalf("unindexed must stay graceful and never assert freshness: %+v", rep)
	}
}

func TestTaskContextPartialErrorCap(t *testing.T) {
	rep := &TaskContextReport{}
	for i := 0; i < taskMaxPartialErrors+5; i++ {
		rep.addPartialError("impact", errors.New("boom"))
	}
	if len(rep.PartialErrors) != taskMaxPartialErrors || rep.PartialErrorsTruncated != 5 {
		t.Fatalf("cap = %d entries + %d truncated, want %d + 5",
			len(rep.PartialErrors), rep.PartialErrorsTruncated, taskMaxPartialErrors)
	}
}

func TestTaskContextReportValidatesAgainstV1Schema(t *testing.T) {
	svc, root, hub := taskContextFixture(t)
	schemaPath := filepath.Join("..", "..", "schemas", "codemap.task-context.v1.schema.json")
	schemaJSON, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(t *testing.T, rep *TaskContextReport) {
		t.Helper()
		got, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(got, &document); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(document); err != nil {
			t.Fatalf("report does not validate against %s: %v\n%s", schemaPath, err, got)
		}
	}

	t.Run("understand", func(t *testing.T) {
		rep, err := svc.TaskContext(context.Background(), root, "Hub", TaskContextOptions{})
		if err != nil {
			t.Fatal(err)
		}
		validate(t, rep)
	})
	t.Run("debug", func(t *testing.T) {
		rep, err := svc.TaskContext(context.Background(), root, "Hub", TaskContextOptions{Mode: TaskModeDebug})
		if err != nil {
			t.Fatal(err)
		}
		validate(t, rep)
	})
	t.Run("change with unresolvable fqn-only selector", func(t *testing.T) {
		// An fqn-only selector (start_line 0) is legitimate input; when it does
		// not resolve it is echoed verbatim and must still validate — the
		// selector's start_line is a fallback, not its identity.
		rep, err := svc.TaskContext(context.Background(), root, "x", TaskContextOptions{
			Mode:      TaskModeChange,
			Selectors: []SymbolSelector{{File: "main.go", FQN: "DoesNotExist"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Targets) != 1 || rep.Targets[0].Found || rep.Targets[0].Selector.StartLine != 0 {
			t.Fatalf("unresolved target echo = %+v", rep.Targets)
		}
		validate(t, rep)
	})
	t.Run("change valid selector", func(t *testing.T) {
		rep, err := svc.TaskContext(context.Background(), root, "make Hub safer", TaskContextOptions{
			Mode: TaskModeChange, Selectors: []SymbolSelector{hub},
		})
		if err != nil {
			t.Fatal(err)
		}
		validate(t, rep)

		// The mode enum is load-bearing: review must never validate.
		got, _ := json.Marshal(rep)
		mutated := map[string]any{}
		if err := json.Unmarshal(got, &mutated); err != nil {
			t.Fatal(err)
		}
		mutated["mode"] = "review"
		if err := resolved.Validate(mutated); err == nil {
			t.Fatal("mode=review must not validate against the v1 schema")
		}
	})
}

func TestTaskContextSelectorCapNote(t *testing.T) {
	svc, root, hub := taskContextFixture(t)
	selectors := make([]SymbolSelector, 0, contextBatchMax+1)
	for i := 0; i < contextBatchMax+1; i++ {
		selectors = append(selectors, hub) // duplicates collapse; pad with an unresolvable variant
		selectors[i].FQN = fmt.Sprintf("Pad%d", i)
		selectors[i].StartLine = 0
	}
	rep, err := svc.TaskContext(context.Background(), root, "x", TaskContextOptions{
		Mode: TaskModeChange, Selectors: selectors,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Targets) != contextBatchMax {
		t.Fatalf("targets = %d, want capped %d", len(rep.Targets), contextBatchMax)
	}
	if !strings.Contains(rep.Note, fmt.Sprintf("analyzed the first %d", contextBatchMax)) {
		t.Fatalf("cap note missing: %q", rep.Note)
	}
}

func TestAttachTaskFreshnessNeverPretendsFresh(t *testing.T) {
	rep := &TaskContextReport{}
	attachTaskFreshness(rep, nil, errors.New("walk failed"))
	if rep.Freshness.Checked || rep.Freshness.Stale {
		t.Fatalf("failed walk must leave freshness unchecked: %+v", rep.Freshness)
	}
	if len(rep.PartialErrors) != 1 || rep.PartialErrors[0].Component != "staleness" {
		t.Fatalf("partial errors = %+v", rep.PartialErrors)
	}
	st := index.Staleness{Changed: 2}
	rep2 := &TaskContextReport{}
	attachTaskFreshness(rep2, &st, nil)
	if !rep2.Freshness.Checked || !rep2.Freshness.Stale || rep2.Freshness.Staleness.Changed != 2 {
		t.Fatalf("freshness = %+v", rep2.Freshness)
	}
	// Unindexed nil,nil stays unchecked with no error entry.
	rep3 := &TaskContextReport{}
	attachTaskFreshness(rep3, nil, nil)
	if rep3.Freshness.Checked || len(rep3.PartialErrors) != 0 {
		t.Fatalf("unindexed freshness = %+v errors=%d", rep3.Freshness, len(rep3.PartialErrors))
	}
}

func TestTaskContextChangeModeDeterministic(t *testing.T) {
	svc, root, hub := taskContextFixture(t)
	opts := TaskContextOptions{Mode: TaskModeChange, Selectors: []SymbolSelector{hub}}
	a, err := svc.TaskContext(context.Background(), root, "make Hub safer", opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.TaskContext(context.Background(), root, "make Hub safer", opts)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("change mode is not deterministic:\n%s\n%s", ja, jb)
	}
}

func TestTaskContextRelatedFilesDedup(t *testing.T) {
	svc, root, hub := taskContextFixture(t)
	entry := hub
	entry.FQN = ""      // force line-based resolution
	entry.StartLine = 6 // Entry's declaration line in the fixture's main.go
	selectors := []SymbolSelector{hub, entry}
	rep, err := svc.TaskContext(context.Background(), root, "x", TaskContextOptions{
		Mode: TaskModeChange, Selectors: selectors,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, rf := range rep.RelatedFiles {
		seen[rf.File]++
	}
	if seen["main.go"] > 1 {
		t.Fatalf("same-file targets must collapse into one related group: %+v", rep.RelatedFiles)
	}
}
