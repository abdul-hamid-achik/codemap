package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestScanLiteralUsages pins the critique's blocking fix: a comment mention and a
// different-but-substring key must NOT be counted; only a real string literal is.
func TestScanLiteralUsages(t *testing.T) {
	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.go", "package app\nimport \"os\"\n"+
		"func Read() string { return os.Getenv(\"STRIPE_KEY\") }\n"+ // line 3: real string literal
		"// STRIPE_KEY rotates quarterly — a comment, not a usage\n"+ // line 4: comment → excluded
		"func Other() string { return \"STRIPE_KEY_BACKUP\" }\n") // line 5: different key (word boundary)
	must("b.py", "os.environ[\"STRIPE_KEY\"]   # reads it\n"+ // line 1: code → hit
		"# STRIPE_KEY mentioned only in a comment\n") // line 2: comment → excluded

	sites, err := scanLiteralUsages(dir, []string{"a.go", "b.py"}, "STRIPE_KEY")
	if err != nil {
		t.Fatal(err)
	}
	// Expect: a.go:3 (string), b.py:1 (code). NOT a.go:4/5, NOT b.py:2.
	got := map[string]string{}
	for _, s := range sites {
		got[s.File+":"+itoa(s.Line)] = s.Confidence
	}
	if len(got) != 2 {
		t.Fatalf("got %d sites, want 2: %v", len(got), got)
	}
	if got["a.go:3"] != "string" {
		t.Errorf("a.go:3 should be a string-literal hit, got %v", got)
	}
	if got["b.py:1"] != "code" {
		t.Errorf("b.py:1 should be a code hit, got %v", got)
	}
	for _, bad := range []string{"a.go:4", "a.go:5", "b.py:2"} {
		if _, ok := got[bad]; ok {
			t.Errorf("%s must NOT be a hit (comment / different key)", bad)
		}
	}
}

func TestScanLiteralUsagesForKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keys.go"), []byte("package keys\nconst a = \"STRIPE_KEY\"\nconst b = \"DATABASE_URL\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	byKey, err := scanLiteralUsagesForKeys(context.Background(), dir, []string{"keys.go"}, []string{"STRIPE_KEY", "DATABASE_URL"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"STRIPE_KEY", "DATABASE_URL"} {
		if len(byKey[key]) != 1 {
			t.Fatalf("%s sites = %v, want one", key, byKey[key])
		}
	}
}

func TestSecretKeyNameLimits(t *testing.T) {
	var svc Service // validation must happen before any session/store access

	oversized := strings.Repeat("K", MaxSecretKeyNameBytes+1)
	if _, err := svc.SecretImpactWithContext(context.Background(), ".", []string{oversized}, 3); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized key error = %v, want byte-limit error", err)
	}

	tooMany := make([]string, MaxSecretKeyNames+1)
	for i := range tooMany {
		tooMany[i] = "KEY_" + itoa(i+1)
	}
	if _, err := svc.RequiredKeysWithContext(context.Background(), ".", "Entry", tooMany, 5); err == nil || !strings.Contains(err.Error(), "too many unique") {
		t.Fatalf("count-cap error = %v, want unique-key limit error", err)
	}

	// The cap applies after normalization and deduplication.
	withDuplicates := append(append([]string(nil), tooMany[:MaxSecretKeyNames]...), " KEY_1 ", "KEY_1")
	got, err := validateSecretKeyNames(withDuplicates)
	if err != nil {
		t.Fatalf("duplicates should not consume the unique-key budget: %v", err)
	}
	if len(got) != MaxSecretKeyNames {
		t.Fatalf("normalized keys = %d, want %d", len(got), MaxSecretKeyNames)
	}

	invalidUTF8 := string([]byte{0xff})
	if _, err := validateSecretKeyNames([]string{invalidUTF8}); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 error = %v, want validation error", err)
	}
	if _, err := compileSecretKeyPatterns([]string{invalidUTF8}); err == nil {
		t.Fatal("invalid pattern input must return an error, not panic")
	}
}

func TestSecretQueriesHonorCancellation(t *testing.T) {
	svc, proj := secretProj(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.SecretImpactWithContext(ctx, proj, []string{"STRIPE_KEY"}, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("SecretImpactWithContext error = %v, want context.Canceled", err)
	}
	if _, err := svc.RequiredKeysWithContext(ctx, proj, "Caller", []string{"STRIPE_KEY"}, 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequiredKeysWithContext error = %v, want context.Canceled", err)
	}
	if _, err := svc.SecretImpactWithInventory(ctx, proj, nil, 3, "demo", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("inventory cancellation error = %v, want context.Canceled", err)
	}
}

func secretProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"a.go":      "package app\nimport \"os\"\nfunc ReadKey() string { return os.Getenv(\"STRIPE_KEY\") }\nfunc Caller() string { return ReadKey() }\n",
		"a_test.go": "package app\nimport \"testing\"\nfunc TestReadKey(t *testing.T) { ReadKey() }\n",
		// a hardcoded value that must never appear in the report:
		"config.go": "package app\nvar apiKey = \"SECRET_VALUE_ABCDEF\"\nfunc unused() string { return apiKey }\n",
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

func TestSecretImpact(t *testing.T) {
	svc, proj := secretProj(t)
	rep, err := svc.SecretImpact(proj, []string{"STRIPE_KEY"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || len(rep.Keys) != 1 {
		t.Fatalf("want 1 key analyzed, got indexed=%v keys=%d", rep.Indexed, len(rep.Keys))
	}
	k := rep.Keys[0]
	if len(k.UsedBy) != 1 || k.UsedBy[0].Symbol != "ReadKey" {
		t.Errorf("used_by = %+v, want [ReadKey]", k.UsedBy)
	}
	if k.BlastRadius < 1 {
		t.Errorf("blast radius = %d, want ≥1 (Caller + TestReadKey reach ReadKey)", k.BlastRadius)
	}
	if k.CoveringTests < 1 || k.Untested {
		t.Errorf("ReadKey is reached by TestReadKey → covered, got tests=%d untested=%v", k.CoveringTests, k.Untested)
	}
}

func TestSecretImpactPrecisionRequiresPerFileCoverage(t *testing.T) {
	svc, proj := secretProj(t)
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	_, name, err := svc.resolveProject(proj)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(p.ID, "a.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SecretImpact(proj, []string{"STRIPE_KEY"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Precise {
		t.Fatal("coverage for one reader file must not make the whole project blast radius precise")
	}

	// Cover the remaining callable files, including a leaf with no call edges.
	for _, file := range []string{"a_test.go", "config.go"} {
		if err := g.MarkCallGraphResolved(p.ID, file, "go/types"); err != nil {
			t.Fatal(err)
		}
	}
	rep, err = svc.SecretImpact(proj, []string{"STRIPE_KEY"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Precise {
		t.Fatalf("all callable files are covered; expected precise secret impact, got note %q", rep.Note)
	}
}

func TestSecretImpactWithVaultInventory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a small POSIX tvault stub")
	}
	svc, proj := secretProj(t)
	bin := t.TempDir()
	stub := filepath.Join(bin, "tvault")
	script := `#!/bin/sh
case " $* " in
  *" -p demo list --json --prefix STRIPE_ "*) printf '["STRIPE_KEY","STRIPE_KEY"]' ;;
  *) exit 17 ;;
esac
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rep, err := svc.SecretImpactWithInventory(context.Background(), proj, nil, 3, "demo", "STRIPE_")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Keys) != 1 || rep.Keys[0].Key != "STRIPE_KEY" {
		t.Fatalf("vault inventory should be honored and deduplicated, got %+v", rep)
	}
	required, err := svc.RequiredKeysWithInventory(context.Background(), proj, "Caller", nil, 5, "demo", "STRIPE_")
	if err != nil {
		t.Fatal(err)
	}
	if len(required.RequiredKeys) != 1 || required.RequiredKeys[0] != "STRIPE_KEY" {
		t.Fatalf("required-keys should honor the same vault inventory, got %+v", required)
	}
}

func TestVaultInventoryHonorsUniqueKeyCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a small POSIX tvault stub")
	}
	keys := make([]string, MaxSecretKeyNames+1)
	for i := range keys {
		keys[i] = "VAULT_KEY_" + itoa(i+1)
	}
	payload, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "tvault")
	script := "#!/bin/sh\nprintf '%s' '" + string(payload) + "'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err = keyNamesWithInventory(context.Background(), nil, "demo", "")
	if err == nil || !strings.Contains(err.Error(), "too many unique") {
		t.Fatalf("oversized inventory error = %v, want unique-key limit error", err)
	}
}

func TestSecretImpactOrphanKey(t *testing.T) {
	svc, proj := secretProj(t)
	rep, err := svc.SecretImpact(proj, []string{"NEVER_USED_KEY"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.OrphanKeys) != 1 || rep.OrphanKeys[0] != "NEVER_USED_KEY" {
		t.Errorf("a key with no usages should be an orphan, got %+v", rep)
	}
}

// TestSecretImpactNoValueLeak is the security invariant: the report carries key
// NAMES + symbols + file:line, never a secret VALUE or any scanned line content —
// so a hardcoded value in a scanned file can't leak into the output.
func TestSecretImpactNoValueLeak(t *testing.T) {
	svc, proj := secretProj(t)
	rep, err := svc.SecretImpact(proj, []string{"STRIPE_KEY"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "SECRET_VALUE_ABCDEF") {
		t.Errorf("the report leaked a hardcoded value from a scanned file:\n%s", out)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestRequiredKeys proves least-privilege scoping: only keys read within the
// entrypoint's transitive call tree are required; a key read elsewhere is excluded.
func TestRequiredKeys(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	src := "package app\nimport \"os\"\n" +
		"func Reader() string { return os.Getenv(\"STRIPE_KEY\") }\n" +
		"func Middle() string { return Reader() }\n" +
		"func Entry() string { return Middle() }\n" +
		"func Unrelated() string { return os.Getenv(\"DATABASE_URL\") }\n"
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte(src), 0o644); err != nil {
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
	rep, err := svc.RequiredKeys(proj, "Entry", []string{"STRIPE_KEY", "DATABASE_URL"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Entry should be found")
	}
	// Entry→Middle→Reader reads STRIPE_KEY; DATABASE_URL is read only by Unrelated.
	if len(rep.RequiredKeys) != 1 || rep.RequiredKeys[0] != "STRIPE_KEY" {
		t.Errorf("required_keys = %v, want [STRIPE_KEY] (DATABASE_URL is outside Entry's call tree)", rep.RequiredKeys)
	}
}
