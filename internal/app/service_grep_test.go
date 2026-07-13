package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// grepFixtureSrc has:
//   - a package-clause comment literal (packageScopeNeedleXyz) that lands
//     outside every symbol's [StartLine,EndLine] range → Resolution:"none".
//   - a doc-comment literal (docNeedleCommentXyz) above Helper, also outside
//     Helper's range (StartLine is the "func" keyword line, not the doc
//     comment) → grep is not comment-aware, so it still matches.
//   - a distinctive literal inside Helper's body (needle-marker-xyz) that
//     resolves to the enclosing function.
//   - a differently-cased literal in a package-level var for --ignore-case.
const grepFixtureSrc = `package app // packageScopeNeedleXyz

// docNeedleCommentXyz sits in a doc comment above Helper
func Helper() {
	x := "needle-marker-xyz"
	_ = x
}

func Wrapper() {
	Helper()
}

var CaseNeedle = "CASE-NEEDLE-XYZ"
`

func TestServiceGrepResolvesHitsToEnclosingSymbol(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"), []byte(grepFixtureSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	t.Run("literal match resolves to the enclosing function", func(t *testing.T) {
		rep, err := svc.Grep(proj, "needle-marker-xyz", GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Hits) != 1 {
			t.Fatalf("Hits = %+v, want exactly 1", rep.Hits)
		}
		h := rep.Hits[0]
		if h.Symbol != "Helper" || h.Kind != "function" || h.Resolution != "enclosing" {
			t.Fatalf("hit = %+v, want Helper/function/enclosing", h)
		}
		if h.Selector == nil || h.Selector.FQN == "" {
			t.Fatalf("hit selector = %+v, want a populated selector", h.Selector)
		}
		// The selector must round-trip through the same selector-based entry
		// points other tools use to re-resolve a grep hit onto the graph.
		if _, err := svc.SourceBySelector(proj, *h.Selector, false); err != nil {
			t.Errorf("SourceBySelector(hit.Selector) failed: %v", err)
		}
		if _, err := svc.CallersBySelector(proj, *h.Selector); err != nil {
			t.Errorf("CallersBySelector(hit.Selector) failed: %v", err)
		}
	})

	t.Run("regex opts in to patterns a literal search would miss", func(t *testing.T) {
		literalRep, err := svc.Grep(proj, "needle-[a-z]+-xyz", GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if literalRep.Total != 0 {
			t.Fatalf("literal search for a regex-only pattern should find nothing, got total=%d", literalRep.Total)
		}
		regexRep, err := svc.Grep(proj, "needle-[a-z]+-xyz", GrepOptions{Regex: true})
		if err != nil {
			t.Fatal(err)
		}
		if regexRep.Total != 1 || len(regexRep.Hits) != 1 || regexRep.Hits[0].Symbol != "Helper" {
			t.Fatalf("regex search = %+v, want one hit resolving to Helper", regexRep)
		}
	})

	t.Run("ignore-case matches a differently-cased needle", func(t *testing.T) {
		rep, err := svc.Grep(proj, "case-needle-xyz", GrepOptions{IgnoreCase: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Hits) != 1 || rep.Hits[0].Symbol != "CaseNeedle" || rep.Hits[0].Kind != "variable" {
			t.Fatalf("ignore-case search = %+v, want one hit on CaseNeedle/variable", rep.Hits)
		}
		// Without ignore-case the lowercase query must not match the
		// uppercase literal.
		exact, err := svc.Grep(proj, "case-needle-xyz", GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if exact.Total != 0 {
			t.Fatalf("case-sensitive search should not match, got total=%d", exact.Total)
		}
	})

	t.Run("invalid regex is a coded operational error", func(t *testing.T) {
		_, err := svc.Grep(proj, "(unterminated[", GrepOptions{Regex: true})
		if err == nil {
			t.Fatal("expected an error for invalid regex syntax")
		}
		if CodeOf(err) != CodeOperational {
			t.Errorf("CodeOf(err) = %q, want %q", CodeOf(err), CodeOperational)
		}
	})

	t.Run("a match outside every symbol's range resolves to none", func(t *testing.T) {
		rep, err := svc.Grep(proj, "packageScopeNeedleXyz", GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Hits) != 1 {
			t.Fatalf("Hits = %+v, want exactly 1", rep.Hits)
		}
		h := rep.Hits[0]
		if h.Resolution != "none" || h.Symbol != "" || h.Selector != nil {
			t.Fatalf("hit = %+v, want Resolution:none, empty Symbol, nil Selector", h)
		}
	})

	t.Run("grep is not comment-aware — a doc-comment literal still matches", func(t *testing.T) {
		rep, err := svc.Grep(proj, "docNeedleCommentXyz", GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Total != 1 {
			t.Fatalf("Total = %d, want 1 (comment literals are not excluded)", rep.Total)
		}
	})
}

func TestServiceGrepTopClamping(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	for i, name := range []string{"a.go", "b.go", "c.go"} {
		src := "package app\n\nfunc F" + string(rune('A'+i)) + "() {\n\t_ = \"clampme-marker-xyz\"\n}\n"
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Grep(proj, "clampme-marker-xyz", GrepOptions{Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(rep.Hits))
	}
	if rep.Total != 3 {
		t.Fatalf("Total = %d, want 3 (the true match count, not just what fit under top)", rep.Total)
	}
	if !rep.Truncated {
		t.Error("Truncated should be true when Total > len(Hits)")
	}
}

func TestServiceGrepSkipsFileDeletedSinceIndex(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	kept := filepath.Join(proj, "kept.go")
	gone := filepath.Join(proj, "gone.go")
	if err := os.WriteFile(kept, []byte("package app\n\nfunc Kept() {\n\t_ = \"deleteme-marker-xyz\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gone, []byte("package app\n\nfunc Gone() {\n\t_ = \"deleteme-marker-xyz\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Grep(proj, "deleteme-marker-xyz", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 || rep.Hits[0].Symbol != "Kept" {
		t.Fatalf("Hits = %+v, want exactly the surviving file's hit", rep.Hits)
	}
	if rep.FilesScanned != 1 {
		t.Fatalf("FilesScanned = %d, want 1 (the deleted file is silently skipped, not an error)", rep.FilesScanned)
	}
}

// TestServiceGrepScanAbortedFileCountsOnlyAsScanned pins the fix for
// scanFileForHits double-counting a file whose scan aborts mid-way (a single
// line exceeding the 1 MiB bufio.Scanner ceiling, despite the whole file
// being well under the 4 MiB size cap): before the fix, GrepWithContext
// incremented FilesScanned unconditionally, then scanFileForHits ALSO
// incremented FilesSkipped on the scanner error — counting one file in both
// fields, breaking the "skipped means never scanned" contract the size/
// binary skip paths rely on. Hits found before the oversized line must
// still survive.
func TestServiceGrepScanAbortedFileCountsOnlyAsScanned(t *testing.T) {
	isolate(t)
	proj := t.TempDir()

	// A single line well over the 1 MiB (1<<20) scanner ceiling, in a file
	// well under the 4 MiB (maxGrepFileBytes) whole-file cap — deterministically
	// triggers bufio.ErrTooLong partway through the scan.
	oversized := strings.Repeat("x", 1_200_000)
	src := "package app\n\nfunc Kept() {\n\t_ = \"scanabort-marker-xyz\"\n}\n\nfunc Oversized() {\n\t_ = \"" + oversized + "\"\n}\n"
	if err := os.WriteFile(filepath.Join(proj, "big.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Grep(proj, "scanabort-marker-xyz", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 || rep.Hits[0].Line != 4 {
		t.Fatalf("Hits = %+v, want the hit found before the oversized line to survive", rep.Hits)
	}
	if rep.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (the file was genuinely opened and partially scanned)", rep.FilesScanned)
	}
	if rep.FilesSkipped != 0 {
		t.Errorf("FilesSkipped = %d, want 0 (a scan-aborted file must not ALSO count as skipped)", rep.FilesSkipped)
	}
}

func TestServiceGrepNotIndexedProjectReturnsEmptyNoError(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	rep, err := svc.Grep(proj, "anything", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hits == nil || len(rep.Hits) != 0 {
		t.Fatalf("Hits = %+v, want an empty (non-nil) slice on a not-indexed project", rep.Hits)
	}
}

func TestServiceGrepEmptyAndOversizedPattern(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	if _, err := svc.Grep(proj, "", GrepOptions{}); err == nil || CodeOf(err) != CodeOperational {
		t.Errorf("empty pattern: err=%v, code=%q, want a CodeOperational error", err, CodeOf(err))
	}
	huge := make([]byte, MaxGrepPatternBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	if _, err := svc.Grep(proj, string(huge), GrepOptions{}); err == nil || CodeOf(err) != CodeOperational {
		t.Errorf("oversized pattern: err=%v, code=%q, want a CodeOperational error", err, CodeOf(err))
	}
}

func TestLooksBinary(t *testing.T) {
	if looksBinary([]byte("package main\n\nfunc main() {}\n")) {
		t.Error("ordinary Go source must not be flagged as binary")
	}
	if !looksBinary([]byte("PNG\x00\x01\x02")) {
		t.Error("a head containing a NUL byte must be flagged as binary")
	}
	if looksBinary(nil) {
		t.Error("an empty head must not be flagged as binary")
	}
}

func TestTruncateLine(t *testing.T) {
	if got := truncateLine("short", 300); got != "short" {
		t.Errorf("truncateLine(short) = %q, want unchanged", got)
	}
	if got := truncateLine("abcdef", 3); got != "abc…" {
		t.Errorf("truncateLine(abcdef, 3) = %q, want %q", got, "abc…")
	}
	// Rune-safe: truncating must not split a multi-byte character.
	if got := truncateLine("日本語abc", 3); got != "日本語…" {
		t.Errorf("truncateLine(multibyte) = %q, want a rune-safe cut", got)
	}
}
