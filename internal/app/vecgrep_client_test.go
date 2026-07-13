package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestSemanticBackendVecgrepOwnsZeroHitAndUnavailableStates(t *testing.T) {
	t.Run("zero hits remain a successful vecgrep query", func(t *testing.T) {
		svc, proj := semanticProj(t, fakeVecgrep(t, `[]`))
		svc.s.Config.Semantic.Backend = "vecgrep"
		rep, err := svc.Semantic(context.Background(), proj, "anything", 5)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Mode != "vecgrep" || len(rep.Hits) != 0 {
			t.Fatalf("explicit vecgrep backend = %+v", rep)
		}
		search, err := svc.Search(context.Background(), proj, "TargetFunc", 5)
		if err != nil {
			t.Fatal(err)
		}
		if search.Mode != "vecgrep" || len(search.Hits) != 0 {
			t.Fatalf("Search silently changed owners after a valid zero-hit: %+v", search)
		}
	})

	t.Run("missing explicit backend is an error", func(t *testing.T) {
		svc, proj := semanticProj(t, "")
		svc.s.Config.Semantic.Backend = "vecgrep"
		svc.s.Config.Vecgrep = config.VecgrepConfig{Enabled: true, Bin: "vecgrep-definitely-missing"}
		if _, err := svc.Semantic(context.Background(), proj, "anything", 5); err == nil {
			t.Fatal("explicit vecgrep backend should fail when its binary is unavailable")
		}
		if _, err := svc.Search(context.Background(), proj, "TargetFunc", 5); err == nil {
			t.Fatal("Search should not hide an explicit vecgrep owner failure behind name fallback")
		}
	})
}

func TestSemanticBackendLocalNeverDelegates(t *testing.T) {
	bin := fakeVecgrep(t, `[{"relative_path":"a.go","symbol_name":"TargetFunc","start_line":2,"end_line":2,"language":"go","score":0.91}]`)
	svc, proj := semanticProj(t, bin)
	svc.s.Config.Semantic.Backend = "local"
	rep, err := svc.Semantic(context.Background(), proj, "the target function", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "none" || len(rep.Hits) != 0 {
		t.Fatalf("local backend delegated unexpectedly: %+v", rep)
	}
}

func TestVecgrepSubprocessLimits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses hermetic POSIX helper executables")
	}

	writeHelper := func(t *testing.T, body string) string {
		t.Helper()
		bin := filepath.Join(t.TempDir(), "vecgrep")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}

	t.Run("default timeout bounds a caller without deadline", func(t *testing.T) {
		bin := writeHelper(t, "while :; do :; done\n")
		start := time.Now()
		_, err := runVecgrepJSONWithLimits(context.Background(), bin, t.TempDir(), 75*time.Millisecond, 1024, "search")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("default subprocess timeout took %s", elapsed)
		}
	})

	t.Run("caller deadline wins", func(t *testing.T) {
		bin := writeHelper(t, "while :; do :; done\n")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := runVecgrepJSONWithLimits(ctx, bin, t.TempDir(), 5*time.Second, 1024, "search")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want caller deadline exceeded", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("caller cancellation took %s", elapsed)
		}
	})

	t.Run("stdout is bounded and terminates producer", func(t *testing.T) {
		bin := writeHelper(t, "while :; do printf '012345678901234567890123456789\\n'; done\n")
		start := time.Now()
		_, err := runVecgrepJSONWithLimits(context.Background(), bin, t.TempDir(), 5*time.Second, 1024, "search")
		if err == nil || !strings.Contains(err.Error(), "stdout exceeds 1024 bytes") {
			t.Fatalf("error = %v, want bounded stdout error", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("oversized producer took %s to terminate", elapsed)
		}
	})
}

func TestVecgrepTimeoutPreservesSemanticOwnerPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a hermetic POSIX helper executable")
	}
	bin := filepath.Join(t.TempDir(), "vecgrep")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.VecgrepConfig{Enabled: true, Bin: bin}

	explicitCtx, cancelExplicit := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelExplicit()
	_, available, err := vecgrepSearchCommand(explicitCtx, cfg, t.TempDir(), "query", 5)
	if !available || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("explicit adapter result: available=%v error=%v", available, err)
	}

	fallbackCtx, cancelFallback := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelFallback()
	if hits := vecgrepSearch(fallbackCtx, cfg, t.TempDir(), "query", 5); hits != nil {
		t.Fatalf("optional fallback should swallow adapter timeout, got %+v", hits)
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
	rep, err := svc.Context(proj, "Target", 2, false)
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
	rep, err := svc.Context(proj, "Target", 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Memories) != 0 {
		t.Errorf("expected no memories when vecgrep off, got %d", len(rep.Memories))
	}
}
