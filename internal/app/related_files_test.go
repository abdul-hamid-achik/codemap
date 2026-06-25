package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// writeProj writes files into a fresh temp project and indexes it (structure only).
func relatedProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"a.go":      "package app\nfunc Helper() {}\nfunc Run() { Helper() }\n",
		"b.go":      "package app\nfunc Other() { Run() }\n",
		"a_test.go": "package app\nimport \"testing\"\nfunc TestRun(t *testing.T) { Run() }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func TestRelatedFiles(t *testing.T) {
	svc, proj := relatedProj(t)
	rep, err := svc.RelatedFiles(proj, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed {
		t.Fatal("project is indexed; Indexed should be true")
	}
	byPath := map[string][]string{} // path -> reasons
	for _, r := range rep.Related {
		byPath[r.RelativePath] = append(byPath[r.RelativePath], r.Reason)
		if r.RelativePath == "a.go" {
			t.Errorf("a.go (self) must not be in its own related set")
		}
	}
	// Run (in a.go) is called by Other (b.go) and TestRun (a_test.go).
	if got := byPath["b.go"]; !contains(got, "caller") {
		t.Errorf("b.go should be a 'caller' of a.go (Other→Run), got %v", got)
	}
	if got := byPath["a_test.go"]; !contains(got, "test") {
		t.Errorf("a_test.go should be a 'test' covering a.go (covers Run), got %v", got)
	}
}

func TestRelatedFilesUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).RelatedFiles(t.TempDir(), "x.go")
	if err != nil {
		t.Fatalf("unindexed project must not error: %v", err)
	}
	if rep.Indexed || len(rep.Related) != 0 {
		t.Errorf("unindexed → {indexed:false, related:[]}, got %+v", rep)
	}
}

func TestSymbolAt(t *testing.T) {
	svc, proj := relatedProj(t)
	// a.go line 3 is `func Run() { Helper() }`.
	rep, err := svc.SymbolAt(proj, "a.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Symbol != "Run" {
		t.Errorf("symbol at a.go:3 = %q, want Run", rep.Symbol)
	}
	if rep.Resolution != "exact" {
		t.Errorf("a.go:3 is Run's def line → resolution=exact, got %q", rep.Resolution)
	}
	// A line with no symbol → resolution none, no error.
	none, err := svc.SymbolAt(proj, "a.go", 999)
	if err != nil {
		t.Fatal(err)
	}
	if none.Resolution != "none" || none.Symbol != "" {
		t.Errorf("a.go:999 → resolution=none, got %+v", none)
	}
}

// TestRelatedFilesContract pins the C1 wire shape: marshaling the Go struct must
// round-trip the committed fixture EXACTLY (a renamed/missing/extra json tag —
// the class of bug that silently broke vecgrep's client — fails this test).
func TestRelatedFilesContract(t *testing.T) {
	assertContract(t, "testdata/contracts/related_files.json", &RelatedFilesReport{})
}

func TestSymbolAtContract(t *testing.T) {
	assertContract(t, "testdata/contracts/symbol_at.json", &SymbolAtReport{})
}

// assertContract unmarshals fixture into v (the typed struct), re-marshals, and
// asserts the normalized JSON is identical — so the struct's tags must match the
// committed contract field-for-field.
func assertContract(t *testing.T, fixture string, v any) {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("fixture doesn't fit the struct: %v", err)
	}
	roundtrip, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(roundtrip, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("contract drift in %s:\n fixture: %s\n struct : %s", fixture, raw, roundtrip)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
