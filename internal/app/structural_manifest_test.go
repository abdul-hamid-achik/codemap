package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestStructuralManifestMatchesExportWithoutReadingSourceBodies(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Alpha() {}\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	exported, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != StructuralManifestSchemaVersion || StructuralManifestSchemaVersion != 1 || manifest.ExportSchemaVersion != StructuralExportSchemaVersion {
		t.Fatalf("manifest/export schema versions = %d/%d, want current %d/%d",
			manifest.SchemaVersion, manifest.ExportSchemaVersion, StructuralManifestSchemaVersion, StructuralExportSchemaVersion)
	}
	if !manifest.Complete || manifest.TotalRecords != exported.TotalRecords {
		t.Fatalf("manifest completeness/count = %v/%d, export total = %d",
			manifest.Complete, manifest.TotalRecords, exported.TotalRecords)
	}
	if manifest.ProjectKey != exported.ProjectKey || manifest.IndexFingerprint != exported.IndexFingerprint {
		t.Fatalf("manifest identity = %q/%q, export = %q/%q",
			manifest.ProjectKey, manifest.IndexFingerprint, exported.ProjectKey, exported.IndexFingerprint)
	}
	if !manifest.Freshness.Checked || !manifest.Freshness.Fresh || manifest.Freshness.Changed != 0 || manifest.Freshness.New != 0 || manifest.Freshness.Deleted != 0 {
		t.Fatalf("fresh manifest diagnostics = %+v", manifest.Freshness)
	}
	assertStructuralManifestSchema(t, manifest)

	// Removing the only source file proves the manifest does not require a
	// readable source body. Indexed identity stays stable, while drift is
	// reported independently and explicitly.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if stale.IndexFingerprint != manifest.IndexFingerprint || stale.TotalRecords != manifest.TotalRecords || !stale.Complete {
		t.Fatalf("source deletion changed indexed identity: before=%+v after=%+v", manifest, stale)
	}
	if !stale.Freshness.Checked || stale.Freshness.Fresh || stale.Freshness.Deleted != 1 {
		t.Fatalf("deleted source freshness = %+v, want checked stale/deleted=1", stale.Freshness)
	}
	assertStructuralManifestSchema(t, stale)
}

func TestStructuralManifestRejectsIndexedFileReplacedByDirectory(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest, err := svc.StructuralManifest(root)
	if err == nil {
		t.Fatalf("manifest certified indexed directory as fresh: %+v", manifest)
	}
	if !strings.Contains(err.Error(), "structural manifest freshness") || !strings.Contains(err.Error(), "read indexed file sample.go") {
		t.Fatalf("manifest error = %v, want contextual indexed-file read failure", err)
	}
}

func TestStructuralManifestFingerprintTracksIndexedMetadata(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	write := func(doc string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("package sample\n\n// "+doc+"\nfunc Alpha() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("first description")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	before, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}

	write("revised description")
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	after, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.IndexFingerprint == after.IndexFingerprint {
		t.Fatalf("indexed doc metadata change retained fingerprint %q", after.IndexFingerprint)
	}
	if !after.Freshness.Fresh {
		t.Fatalf("reindexed manifest should be fresh: %+v", after.Freshness)
	}
}

func TestStructuralManifestDoesNotCombineOldFingerprintWithNewFreshness(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	const file = "sample.go"
	generationA := []byte("package sample\n\nfunc Alpha() {}\n")
	if err := os.WriteFile(filepath.Join(root, file), generationA, 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	oldExport, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(oldExport.Records) != 1 || oldExport.Records[0].FQN != "sample.Alpha" {
		t.Fatalf("generation-A export = %+v", oldExport.Records)
	}

	pid, _, found, err := svc.project(root)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("indexed project not found")
	}
	writer, err := graph.Open(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	// StructuralManifest has captured generation A's symbols, hashes, and
	// languages when this barrier runs. Commit generation B and make the
	// working tree match it. The old implementation queried staleness here and
	// returned A's fingerprint as fresh against B's index; the captured A hash
	// must instead report one changed file.
	generationB := []byte("package sample\n\n// generation B\nfunc Beta() {}\n")
	hashB := sha256Hex(generationB)
	manifest, err := svc.structuralManifest(root, func() error {
		tx, err := writer.BeginTx(context.Background())
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := graph.DeleteNodesInFileTx(tx, pid, file); err != nil {
			return err
		}
		if _, err := graph.AddNodeTx(tx, &graph.Node{
			ProjectID: pid, FilePath: file, Kind: graph.KindFile, Language: "go",
			StartLine: 1, EndLine: 5, SourceHash: hashB,
		}); err != nil {
			return err
		}
		if _, err := graph.AddNodeTx(tx, &graph.Node{
			ProjectID: pid, FilePath: file, Symbol: "Beta", FQN: "sample.Beta",
			Kind: graph.KindFunction, Language: "go", StartLine: 4, EndLine: 4,
			Signature: "func Beta()", SourceHash: sha256Hex([]byte("func Beta() {}")),
		}); err != nil {
			return err
		}
		if err := graph.SetFileHashTx(tx, pid, file, hashB); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, file), generationB, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.IndexFingerprint != oldExport.IndexFingerprint || manifest.TotalRecords != oldExport.TotalRecords {
		t.Fatalf("generation-A manifest/export parity failed: manifest=%+v export=%+v", manifest, oldExport)
	}
	if manifest.Freshness.Fresh || manifest.Freshness.Changed != 1 || manifest.Freshness.New != 0 || manifest.Freshness.Deleted != 0 {
		t.Fatalf("torn manifest freshness = %+v, want generation-A hash vs generation-B tree", manifest.Freshness)
	}

	liveExport, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	liveManifest, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveExport.Records) != 1 || liveExport.Records[0].FQN != "sample.Beta" || liveExport.IndexFingerprint == oldExport.IndexFingerprint {
		t.Fatalf("generation-B export = %+v", liveExport)
	}
	if liveManifest.IndexFingerprint != liveExport.IndexFingerprint || liveManifest.TotalRecords != liveExport.TotalRecords || !liveManifest.Freshness.Fresh {
		t.Fatalf("generation-B manifest/export parity failed: manifest=%+v export=%+v", liveManifest, liveExport)
	}
}

func TestStructuralManifestNotIndexedHasStableCode(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	_, err = NewService(sess).StructuralManifest(t.TempDir())
	if err == nil {
		t.Fatal("unindexed manifest should fail")
	}
	if got := CodeOf(err); got != CodeMissing {
		t.Fatalf("CodeOf(err) = %q, want %q", got, CodeMissing)
	}
}

func TestStructuralManifestSharedV1Fixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "codemap_structural_manifest_v1.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var rep StructuralManifestReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse shared v1 fixture: %v", err)
	}
	if rep.SchemaVersion != StructuralManifestSchemaVersion || rep.ExportSchemaVersion != StructuralExportSchemaVersion || !rep.Complete || !rep.Freshness.Checked {
		t.Fatalf("unexpected shared v1 fixture: %+v", rep)
	}
	assertStructuralManifestSchema(t, &rep)
}

func assertStructuralManifestSchema(t *testing.T, rep *StructuralManifestReport) {
	t.Helper()
	if err := validateStructuralManifestSchema(t, rep); err != nil {
		t.Fatalf("structural manifest does not validate against v1 schema: %v", err)
	}
}

func validateStructuralManifestSchema(t *testing.T, rep *StructuralManifestReport) error {
	t.Helper()
	documentJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(documentJSON, &document); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join("..", "..", "schemas", "codemap.structural-manifest.v1.schema.json")
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
		t.Fatalf("resolve %s: %v", schemaPath, err)
	}
	return resolved.Validate(document)
}

func TestStructuralManifestSchemaRequiresCanonicalProjectKey(t *testing.T) {
	fixturePath := filepath.Join("testdata", "codemap_structural_manifest_v1.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var valid StructuralManifestReport
	if err := json.Unmarshal(data, &valid); err != nil {
		t.Fatal(err)
	}
	for _, projectKey := range []string{"0123456789a", "ABCDEF012345"} {
		rep := valid
		rep.ProjectKey = projectKey
		if err := validateStructuralManifestSchema(t, &rep); err == nil {
			t.Fatalf("invalid project_key %q validated against structural-manifest v1", projectKey)
		}
	}
}
