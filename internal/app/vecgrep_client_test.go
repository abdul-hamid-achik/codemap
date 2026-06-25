package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// fakeVecgrep writes an executable stub that prints jsonOut for any args — standing
// in for `vecgrep search … --format json`.
func fakeVecgrep(t *testing.T, jsonOut string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "vecgrep")
	script := "#!/bin/sh\ncat <<'JSON'\n" + jsonOut + "\nJSON\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func semanticProj(t *testing.T, vecgrepBin string) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\nfunc TargetFunc() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep = config.VecgrepConfig{Enabled: vecgrepBin != "", Bin: vecgrepBin}
	svc := NewService(sess)
	// Structure-only index (withEmbed=false) → no codemap vectors → Mode "none" path.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

// TestSemanticViaVecgrep: a structure-only project (no codemap embeddings) answers
// a semantic query through vecgrep, mapping the hit back onto the graph node.
func TestSemanticViaVecgrep(t *testing.T) {
	bin := fakeVecgrep(t, `[{"relative_path":"a.go","symbol_name":"TargetFunc","start_line":2,"end_line":2,"language":"go","score":0.91}]`)
	svc, proj := semanticProj(t, bin)
	rep, err := svc.Semantic(context.Background(), proj, "the target function", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "vecgrep" {
		t.Fatalf("Mode = %q, want vecgrep (got note: %q)", rep.Mode, rep.Note)
	}
	if len(rep.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(rep.Hits))
	}
	h := rep.Hits[0]
	if h.Symbol != "TargetFunc" || h.Kind != "function" || h.FQN == "" {
		t.Errorf("hit not graph-enriched: %+v (want Symbol=TargetFunc, Kind=function, FQN set)", h)
	}
	if h.Score != 0.91 {
		t.Errorf("score = %v, want 0.91 (vecgrep's)", h.Score)
	}
}

// TestSemanticVecgrepDegrades: with no vecgrep (disabled), a structure-only project
// falls back to the honest "no embeddings" mode — never errors.
func TestSemanticVecgrepDegrades(t *testing.T) {
	svc, proj := semanticProj(t, "") // disabled
	rep, err := svc.Semantic(context.Background(), proj, "anything", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "none" {
		t.Errorf("Mode = %q, want none when vecgrep is disabled", rep.Mode)
	}
	if len(rep.Hits) != 0 {
		t.Errorf("no hits expected, got %d", len(rep.Hits))
	}
}

// TestSemanticVecgrepEmptyResult: vecgrep present but returns nothing → still degrades.
func TestSemanticVecgrepEmptyResult(t *testing.T) {
	svc, proj := semanticProj(t, fakeVecgrep(t, `[]`))
	rep, err := svc.Semantic(context.Background(), proj, "anything", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "none" {
		t.Errorf("Mode = %q, want none when vecgrep returns no hits", rep.Mode)
	}
}

// TestContextRecallsMemories: Context attaches project-scoped agent memories
// recalled from vecgrep (G2).
func TestContextRecallsMemories(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := fakeVecgrep(t, `[{"id":"m1","content":"Target is a hot path; refactor pending","importance":0.8,"tags":["codemap","k"],"score":0.6}]`)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.Config.Vecgrep = config.VecgrepConfig{Enabled: true, Bin: fake}
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Context(proj, "Target", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Target should be found")
	}
	if len(rep.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(rep.Memories))
	}
	if rep.Memories[0].Importance != 0.8 || rep.Memories[0].Content == "" {
		t.Errorf("memory not parsed: %+v", rep.Memories[0])
	}
}

// TestContextNoMemoriesWhenVecgrepOff: Context omits memories when vecgrep is disabled.
func TestContextNoMemoriesWhenVecgrepOff(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"),
		[]byte("package app\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.Config.Vecgrep = config.VecgrepConfig{Enabled: false}
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Context(proj, "Target", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Memories) != 0 {
		t.Errorf("expected no memories when vecgrep off, got %d", len(rep.Memories))
	}
}
