package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestCacheExportImportRoundTrip mirrors internal/snapshot's TestRoundTrip
// approach at the app-service layer: index a tiny project, export it to a
// portable tarball, wipe the project (simulating a fresh runner with no
// index), import the tarball, and prove a graph query answers identically —
// the whole point of I30 (a CI job hands its finished index to the next job).
func TestCacheExportImportRoundTrip(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()
	ctx := context.Background()

	writeGoFile(t, filepath.Join(root, "b.go"),
		"package main\n\nfunc Helper() {}\n\nfunc Caller() { Helper() }\n")
	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	out := filepath.Join(t.TempDir(), "index.tar.gz")
	exp, err := svc.CacheExport(ctx, root, out)
	if err != nil {
		t.Fatalf("CacheExport: %v", err)
	}
	if exp.Nodes == 0 {
		t.Fatal("export reported 0 nodes")
	}
	if exp.TreeHash == "" {
		t.Fatal("export reported an empty tree hash")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("export tarball missing on disk: %v", err)
	}

	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	_, name, err := svc.resolveProject(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		t.Fatal(err)
	}

	callersBefore, err := g.Callers(p.ID, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if len(callersBefore) == 0 {
		t.Fatal("expected Caller to call Helper before wipe (test setup broken)")
	}

	// Simulate the "next runner has no index" scenario: wipe the project clean.
	if err := g.WipeProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if st, err := g.Stats(p.ID); err != nil || st.Nodes != 0 {
		t.Fatalf("wipe left nodes=%d err=%v, want 0 nodes", st.Nodes, err)
	}

	imp, err := svc.CacheImport(ctx, root, out, false)
	if err != nil {
		t.Fatalf("CacheImport: %v", err)
	}
	if !imp.TreeHashMatched {
		t.Error("expected the tree hash to match after an unmodified export/import cycle")
	}
	if imp.Nodes != exp.Nodes {
		t.Errorf("imported nodes = %d, want %d (from export)", imp.Nodes, exp.Nodes)
	}

	// The graph answers the same query identically post-import.
	callersAfter, err := g.Callers(p.ID, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	foundCaller := false
	for _, n := range callersAfter {
		if n.Symbol == "Caller" {
			foundCaller = true
		}
	}
	if !foundCaller {
		t.Errorf("after import, Helper's callers should include Caller, got %+v", callersAfter)
	}
}

// TestCacheImportProfileMismatchRefused pins the "never silently mix models"
// rule from AGENTS.md: a portable archive built with one embedding profile
// must be refused against a session configured with a different one, and
// --force must NOT override it (force only relaxes the tree-hash check).
func TestCacheImportProfileMismatchRefused(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()
	ctx := context.Background()

	svc.s.SetEmbedder(fakeEmbedder{dims: 4})
	if _, err := svc.Index(ctx, root, index.Options{}, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	out := filepath.Join(t.TempDir(), "index.tar.gz")
	if _, err := svc.CacheExport(ctx, root, out); err != nil {
		t.Fatalf("CacheExport: %v", err)
	}

	// A different local model — simulates a teammate/CI runner with a
	// different Ollama model pulled.
	svc.s.SetEmbedder(fakeEmbedder{dims: 8})

	if _, err := svc.CacheImport(ctx, root, out, false); err == nil {
		t.Fatal("CacheImport with a mismatched embedding profile should be refused")
	} else if !strings.Contains(err.Error(), "refusing to mix models") {
		t.Errorf("error = %v, want a 'refusing to mix models' message", err)
	}
	if _, err := svc.CacheImport(ctx, root, out, true); err == nil {
		t.Fatal("CacheImport --force must still refuse a profile mismatch (force only relaxes the tree-hash gate)")
	}
}

// TestCacheImportTreeHashMismatch pins the tree-hash-mismatch policy this
// implementation chose: refuse by default (a portable archive promises "this
// exact tree"; silently importing a divergent one would answer queries
// against code that isn't checked out), and let --force downgrade the
// refusal to a recorded warning for the deliberate case (e.g. seeding a PR
// branch's cache from its base branch ahead of an incremental catch-up).
func TestCacheImportTreeHashMismatch(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	out := filepath.Join(t.TempDir(), "index.tar.gz")
	if _, err := svc.CacheExport(ctx, root, out); err != nil {
		t.Fatalf("CacheExport: %v", err)
	}

	// Drift the working tree without reindexing.
	writeGoFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() { _ = 1 /* drift */ }\n")

	if _, err := svc.CacheImport(ctx, root, out, false); err == nil {
		t.Fatal("CacheImport should refuse a tree-hash mismatch without --force")
	} else if code := CodeOf(err); code != CodeOperational {
		t.Errorf("CodeOf(mismatch error) = %q, want %q", code, CodeOperational)
	}

	rep, err := svc.CacheImport(ctx, root, out, true)
	if err != nil {
		t.Fatalf("CacheImport with --force: %v", err)
	}
	if rep.TreeHashMatched {
		t.Error("expected TreeHashMatched=false on a drifted tree")
	}
	if rep.Warning == "" {
		t.Error("expected a warning noting the forced tree-hash mismatch")
	}
}

// TestCacheImportClearsOrphanedVectorsWhenArchiveHasNone pins the fix for
// CacheImport leaving a project's PRE-EXISTING vectors untouched when the
// imported archive itself carries zero vectors (e.g. exported via
// --no-embed — the typical CI export). Before the fix, CacheImport only
// opened/passed the vector store into snapshot.Import when sm.Vectors > 0;
// with vec==nil, snapshot.Import's WipeProject still replaced every node
// with a fresh id, but the OLD vectors were never cleared — they survived
// with dangling Meta.NodeID and kept answering semantic queries with stale
// results after the import. This reproduces the cross-machine scenario from
// the finding: an already-embedded project imports a vector-less archive
// whose tree hash happens to match (e.g. a --no-embed CI export of the same
// commit).
func TestCacheImportClearsOrphanedVectorsWhenArchiveHasNone(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()
	ctx := context.Background()

	svc.s.SetEmbedder(fakeEmbedder{dims: 4})
	if _, err := svc.Index(ctx, root, index.Options{}, true); err != nil {
		t.Fatalf("Index (embedded): %v", err)
	}
	_, rootName, err := svc.resolveProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := svc.embeddedCount(rootName); !ok || n == 0 {
		t.Fatalf("fixture must produce embedded vectors before import, embeddedCount=%d ok=%v", n, ok)
	}

	// A "donor" directory with byte-identical content to root, indexed
	// WITHOUT embeddings, so exporting it produces a vector-less archive
	// whose tree hash matches root's current working tree — the same
	// tarball a --no-embed CI export of this exact commit would produce.
	donor := t.TempDir()
	writeGoFile(t, filepath.Join(donor, "main.go"), "package main\n\nfunc main() {}\n")
	if _, err := svc.Index(ctx, donor, index.Options{}, false); err != nil {
		t.Fatalf("Index (donor, no-embed): %v", err)
	}
	out := filepath.Join(t.TempDir(), "no-vectors.tar.gz")
	exp, err := svc.CacheExport(ctx, donor, out)
	if err != nil {
		t.Fatalf("CacheExport (donor): %v", err)
	}
	if exp.Vectors != 0 {
		t.Fatalf("fixture must export zero vectors, got %d", exp.Vectors)
	}

	imp, err := svc.CacheImport(ctx, root, out, false)
	if err != nil {
		t.Fatalf("CacheImport: %v", err)
	}
	if !imp.TreeHashMatched {
		t.Fatalf("fixture must produce a matching tree hash (donor content == root content), got %+v", imp)
	}
	if imp.Vectors != 0 {
		t.Errorf("imported report Vectors = %d, want 0 (archive carried none)", imp.Vectors)
	}

	if n, ok := svc.embeddedCount(rootName); !ok || n != 0 {
		t.Errorf("embeddedCount(%q) after importing a vector-less archive = %d, want 0 (stale vectors must be cleared, not orphaned)", rootName, n)
	}
}

// TestCacheExportNotIndexed verifies exporting an unindexed project fails
// with the stable index_missing code (rather than silently writing an empty
// tarball).
func TestCacheExportNotIndexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)

	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := svc.CacheExport(context.Background(), root, out); err == nil {
		t.Fatal("CacheExport of an unindexed project should fail")
	} else if code := CodeOf(err); code != CodeMissing {
		t.Errorf("CodeOf(err) = %q, want %q", code, CodeMissing)
	}
}

// TestCacheImportCorruptArchive verifies a non-tarball input fails with a
// clean CodedError instead of a panic or a partial/garbage restore.
func TestCacheImportCorruptArchive(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(bad, []byte("this is not a gzip file"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CacheImport(context.Background(), root, bad, false)
	if err == nil {
		t.Fatal("CacheImport of a corrupt archive should fail")
	}
	if code := CodeOf(err); code != CodeOperational {
		t.Errorf("CodeOf(err) = %q, want %q", code, CodeOperational)
	}
}

// TestCacheImportTruncatedArchive verifies a truncated (mid-write) tarball
// fails cleanly rather than importing a partial snapshot.
func TestCacheImportTruncatedArchive(t *testing.T) {
	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	out := filepath.Join(t.TempDir(), "index.tar.gz")
	if _, err := svc.CacheExport(ctx, root, out); err != nil {
		t.Fatalf("CacheExport: %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 10 {
		t.Fatal("exported tarball unexpectedly tiny")
	}
	truncated := filepath.Join(t.TempDir(), "truncated.tar.gz")
	if err := os.WriteFile(truncated, b[:len(b)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CacheImport(ctx, root, truncated, false); err == nil {
		t.Fatal("CacheImport of a truncated archive should fail")
	}
}

// --- untarGz unit tests (tar-slip / path-traversal safety) ---

// writeTarGz packs files (name -> content) into a tar.gz at path, with NO
// path sanitization — the point is to hand untarGz a hostile archive exactly
// as a hand-crafted or corrupted one would arrive.
func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUntarGzRejectsTarSlipRelative(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	// A single hostile entry is enough — untarGz must catch it whether it's
	// the first or last entry read, so this doesn't depend on tar order.
	writeTarGz(t, archive, map[string]string{
		"../../../tmp/codemap-tar-slip-test-evil.txt": "pwned",
	})
	dest := t.TempDir()
	err := untarGz(archive, dest)
	if err == nil {
		t.Fatal("untarGz should reject a tar entry that escapes the destination directory")
	}
	if !strings.Contains(err.Error(), "escapes the archive root") {
		t.Errorf("error = %v, want a tar-slip rejection message", err)
	}
	// The destination directory itself must stay exactly as untarGz left it —
	// no entries written outside it (that's the property under test; we
	// don't assert on a specific outside path since the resolved location
	// depends on how deep t.TempDir() nests).
	entries, rerr := os.ReadDir(dest)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("destination dir has %d entries after a rejected archive, want 0: %v", len(entries), entries)
	}
}

func TestUntarGzRejectsAbsolutePath(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil-abs.tar.gz")
	evilTarget := filepath.Join(t.TempDir(), "evil.txt")
	writeTarGz(t, archive, map[string]string{
		evilTarget: "pwned",
	})
	dest := t.TempDir()
	if err := untarGz(archive, dest); err == nil {
		t.Fatal("untarGz should reject an absolute-path tar entry")
	} else if !strings.Contains(err.Error(), "escapes the archive root") {
		t.Errorf("error = %v, want a tar-slip rejection message", err)
	}
	if _, statErr := os.Stat(evilTarget); statErr == nil {
		t.Fatal("tar-slip entry with an absolute path was actually written to disk")
	}
}

func TestUntarGzRejectsCorruptGzip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "not-gzip.tar.gz")
	if err := os.WriteFile(archive, []byte("plain text, not a gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := untarGz(archive, t.TempDir()); err == nil {
		t.Fatal("untarGz should reject a non-gzip file")
	}
}

func TestUntarGzRejectsTruncatedTar(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "valid.tar.gz")
	writeTarGz(t, archive, map[string]string{"a.txt": strings.Repeat("hello world ", 200)})
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(t.TempDir(), "truncated.tar.gz")
	if err := os.WriteFile(truncated, b[:len(b)-20], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := untarGz(truncated, t.TempDir()); err == nil {
		t.Fatal("untarGz should reject a truncated tar stream")
	}
}

func TestUntarGzExtractsValidArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "valid.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"snapshot.json": `{"schema_version":1}`,
		"nodes.jsonl":   `{"file_path":"a.go"}`,
	})
	dest := t.TempDir()
	if err := untarGz(archive, dest); err != nil {
		t.Fatalf("untarGz of a well-formed archive should succeed: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "schema_version") {
		t.Errorf("extracted snapshot.json content = %q, want schema_version", b)
	}
}

// TestUntarGzRejectsImplausibleDeclaredSize pins the fix for untarGz's
// unbounded gzip decompression (a "zip bomb": a tiny gzip stream whose tar
// header declares a multi-gigabyte entry — e.g. a run of NUL bytes that
// compresses ~1000:1 — can fill the local disk before extraction ever
// fails). The declared size alone must be rejected before a single content
// byte is read, so the fixture here deliberately never supplies (or needs)
// the declared bytes: it writes a lying tar header directly (bypassing
// writeTarGz/tar.Writer's own "missed writing N bytes" accounting, which
// would refuse to let a legitimate writer build an inconsistent archive)
// and finalizes the gzip stream immediately after.
func TestUntarGzRejectsImplausibleDeclaredSize(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "lying-size.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "snapshot.json", Mode: 0o644, Size: maxUntarGzDecompressedBytes + 1}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write lying header: %v", err)
	}
	// Deliberately no tw.Write/tw.Close (Close would refuse an entry short of
	// its declared Size) — finalize the gzip stream directly so the archive
	// is still a valid, readable gzip file carrying just the one lying header.
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := untarGz(archive, dest); err == nil {
		t.Fatal("untarGz should reject a tar entry whose declared size exceeds the decompression cap")
	} else if !strings.Contains(err.Error(), "implausible size") {
		t.Errorf("error = %v, want an implausible-size rejection", err)
	}
	entries, rerr := os.ReadDir(dest)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("destination dir has %d entries after a rejected archive, want 0: %v", len(entries), entries)
	}
}

// TestUntarGzCapsCumulativeDecompressedBytes pins untarGz's second line of
// defense: even when every individual entry's declared size is well under
// the cap, the TOTAL bytes actually written across all entries must still be
// bounded (independent of what any one header claims). The cap is shrunk to
// a tiny value for the duration of the test so this is fast and
// deterministic without gigabyte-scale fixtures.
func TestUntarGzCapsCumulativeDecompressedBytes(t *testing.T) {
	orig := maxUntarGzDecompressedBytes
	maxUntarGzDecompressedBytes = 1024
	defer func() { maxUntarGzDecompressedBytes = orig }()

	archive := filepath.Join(t.TempDir(), "cumulative.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"a.jsonl": strings.Repeat("a", 700),
		"b.jsonl": strings.Repeat("b", 700), // neither alone exceeds the cap, but 700+700 > 1024 together
	})
	dest := t.TempDir()
	if err := untarGz(archive, dest); err == nil {
		t.Fatal("untarGz should refuse once cumulative decompressed bytes exceed the cap")
	} else if !strings.Contains(err.Error(), "safety cap") {
		t.Errorf("error = %v, want a safety-cap rejection", err)
	}
}

func TestTarGzDirRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jsonl"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := tarGzDir(src, out); err != nil {
		t.Fatalf("tarGzDir: %v", err)
	}
	dest := t.TempDir()
	if err := untarGz(out, dest); err != nil {
		t.Fatalf("untarGz of tarGzDir's own output should succeed: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "line1\nline2\n" {
		t.Errorf("round-tripped a.jsonl = %q, want %q", b, "line1\nline2\n")
	}
}
