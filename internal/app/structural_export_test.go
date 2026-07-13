package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestStructuralExportContractPaginationAndFreshness(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("z.go", "package sample\n\nfunc Zed() {}\n")
	write("a.go", "package sample\n\n// Alpha documents alpha.\nfunc Alpha() {\n\t_ = \"éééééééééééééééé\"\n}\n\nfunc Beta() {}\n")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	first, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 2, MaxContentBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if first.SchemaVersion != StructuralExportSchemaVersion {
		t.Fatalf("schema version = %d, want current version %d", first.SchemaVersion, StructuralExportSchemaVersion)
	}
	if StructuralExportSchemaVersion != 1 {
		t.Fatalf("v1 contract changed in place: version = %d", StructuralExportSchemaVersion)
	}
	if first.Project == "" || first.ProjectKey == "" || first.IndexFingerprint == "" {
		t.Fatalf("missing project identity: %+v", first)
	}
	if first.TotalRecords != 3 || first.ReturnedRecords != 2 || first.Complete || first.NextOffset != 2 {
		t.Fatalf("first page metadata = total:%d returned:%d complete:%v next:%d, want 3/2/false/2",
			first.TotalRecords, first.ReturnedRecords, first.Complete, first.NextOffset)
	}
	if got := first.Records[0].FQN; got != "sample.Alpha" {
		t.Fatalf("first deterministic record = %q, want sample.Alpha", got)
	}
	for i, record := range first.Records {
		if record.SchemaVersion != 1 || record.Project != first.Project || record.ProjectKey != first.ProjectKey || record.IndexFingerprint != first.IndexFingerprint {
			t.Fatalf("record does not carry standalone contract identity: %+v", record)
		}
		if record.Ordinal != i+1 {
			t.Fatalf("first-page ordinal = %d, want %d", record.Ordinal, i+1)
		}
		if record.File == "" || record.StartLine < 1 || record.EndLine < record.StartLine || record.FQN == "" || record.Kind == "" {
			t.Fatalf("incomplete selector fields: %+v", record)
		}
		if record.SourceHash == "" || record.ContentHash == "" || record.ContentOmitted || record.FileStale {
			t.Fatalf("fresh record content metadata = %+v", record)
		}
		if len(record.Content) > 32 || !utf8.ValidString(record.Content) {
			t.Fatalf("content must be bounded and valid UTF-8: bytes=%d valid=%v", len(record.Content), utf8.ValidString(record.Content))
		}
	}
	if !first.Records[0].ContentTruncated {
		t.Fatal("Alpha's body should be truncated by the 32-byte per-record cap")
	}
	assertStructuralExportSchema(t, first)

	second, err := svc.StructuralExport(root, StructuralExportOptions{Offset: first.NextOffset, Limit: 2, MaxContentBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete || second.NextOffset != 0 || second.ReturnedRecords != 1 || second.Records[0].FQN != "sample.Zed" {
		t.Fatalf("second page = %+v", second)
	}
	if second.Records[0].Ordinal != 3 {
		t.Fatalf("second-page ordinal = %d, want 3", second.Records[0].Ordinal)
	}
	if second.IndexFingerprint != first.IndexFingerprint {
		t.Fatalf("index fingerprint changed across pages: %q != %q", second.IndexFingerprint, first.IndexFingerprint)
	}

	// A changed file keeps its structural metadata but never exports a possibly
	// shifted live line range as symbol content.
	write("a.go", "package sample\n\n// inserted after indexing\n\nfunc Alpha() {}\nfunc Beta() {}\n")
	stale, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range stale.Records {
		if record.File != "a.go" {
			continue
		}
		if !record.FileStale || !record.ContentOmitted || record.OmissionReason != "stale_index" || record.Content != "" {
			t.Fatalf("stale record must omit content explicitly: %+v", record)
		}
	}
}

func TestStructuralExportComparesSourceWithCapturedHashGeneration(t *testing.T) {
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
	pid, _, found, err := svc.project(root)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("indexed project not found")
	}
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}

	// Commit generation B after StructuralExport has captured generation A's
	// nodes+hashes, but before it reads the live file. A late FileHash query
	// would accept B's bytes for A's line range; the captured A hash must instead
	// mark the old Alpha record stale.
	generationB := []byte("package sample\n\n// range shifted in generation B\n\nfunc Beta() {}\n")
	hashB := sha256Hex(generationB)
	rep, err := svc.structuralExport(root, StructuralExportOptions{Limit: 10}, func() error {
		tx, err := g.BeginTx(context.Background())
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
			Kind: graph.KindFunction, Language: "go", StartLine: 5, EndLine: 5, SourceHash: hashB,
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
	if len(rep.Records) != 1 || rep.Records[0].FQN != "sample.Alpha" {
		t.Fatalf("exported records = %+v, want captured generation-A Alpha", rep.Records)
	}
	record := rep.Records[0]
	if !record.FileStale || !record.ContentOmitted || record.OmissionReason != "stale_index" || record.Content != "" {
		t.Fatalf("cross-generation source must be omitted: %+v", record)
	}
	if got, err := g.FileHash(pid, file); err != nil || got != hashB {
		t.Fatalf("live hash = %q, err=%v, want committed generation B", got, err)
	}
}

func TestStructuralExportFingerprintChangesWithIndexedMetadata(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	write := func(doc string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("package sample\n\n// "+doc+"\nfunc Alpha() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Alpha returns the first description.")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	before, err := svc.StructuralExport(root, StructuralExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Records) != 1 {
		t.Fatalf("initial records = %d, want 1", len(before.Records))
	}

	// Go declaration source starts at `func`, so a doc-only change preserves
	// source_hash/content_hash while changing exported indexed metadata. The
	// page fingerprint must still change or a consumer may skip re-ingestion.
	write("Alpha returns the revised description.")
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	after, err := svc.StructuralExport(root, StructuralExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Records) != 1 {
		t.Fatalf("reindexed records = %d, want 1", len(after.Records))
	}
	if before.Records[0].SourceHash != after.Records[0].SourceHash || before.Records[0].ContentHash != after.Records[0].ContentHash {
		t.Fatalf("doc-only edit unexpectedly changed source/content hash: before=%+v after=%+v", before.Records[0], after.Records[0])
	}
	if before.Records[0].Docstring == after.Records[0].Docstring {
		t.Fatal("docstring did not change after incremental reindex")
	}
	if before.IndexFingerprint == after.IndexFingerprint {
		t.Fatalf("doc-only indexed metadata change retained fingerprint %q", after.IndexFingerprint)
	}
}

func assertStructuralExportSchema(t *testing.T, rep *StructuralExportReport) {
	t.Helper()
	if err := validateStructuralExportSchema(t, rep); err != nil {
		t.Fatalf("structural export does not validate against v1 schema: %v", err)
	}
}

func validateStructuralExportSchema(t *testing.T, rep *StructuralExportReport) error {
	t.Helper()
	documentJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(documentJSON, &document); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join("..", "..", "schemas", "codemap.structural-export.v1.schema.json")
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

// TestStructuralExportSharedV1Fixture pins the byte-for-byte compatibility
// fixture copied into vecgrep. Producers and consumers validate the same
// envelope instead of maintaining two hand-waved examples of the contract.
func TestStructuralExportSharedV1Fixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "codemap_structural_export_v1.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var rep StructuralExportReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse shared v1 fixture: %v", err)
	}
	if rep.SchemaVersion != StructuralExportSchemaVersion || len(rep.Records) != 1 {
		t.Fatalf("unexpected shared v1 fixture: version=%d records=%d", rep.SchemaVersion, len(rep.Records))
	}
	record := rep.Records[0]
	if record.ContentHash != sha256Hex([]byte(record.Content)) {
		t.Fatalf("shared fixture content hash does not match content")
	}
	fixtureFingerprint := structuralIndexFingerprint(rep.ProjectKey, []graph.Node{{
		FilePath: record.File, StartLine: record.StartLine, EndLine: record.EndLine,
		Symbol: record.Symbol, FQN: record.FQN, Kind: record.Kind, Language: record.Language,
		Signature: record.Signature, Docstring: record.Docstring, SourceHash: record.SourceHash,
	}})
	if rep.IndexFingerprint != fixtureFingerprint || record.IndexFingerprint != fixtureFingerprint {
		t.Fatalf("shared fixture fingerprint = envelope:%q record:%q, want %q",
			rep.IndexFingerprint, record.IndexFingerprint, fixtureFingerprint)
	}
	assertStructuralExportSchema(t, &rep)
}

func TestStructuralExportValidatesBoundsAndPaths(t *testing.T) {
	if _, err := normalizeStructuralExportOptions(StructuralExportOptions{Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("negative offset error = %v", err)
	}
	if _, err := normalizeStructuralExportOptions(StructuralExportOptions{Limit: MaxStructuralExportLimit + 1}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized limit error = %v", err)
	}
	if _, err := normalizeStructuralExportOptions(StructuralExportOptions{MaxContentBytes: MaxStructuralExportContentBytes + 1}); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("oversized content error = %v", err)
	}
	if _, err := safeStructuralPath(t.TempDir(), "../secret.go"); err == nil {
		t.Fatal("path traversal should be rejected")
	}
}

func TestStructuralManifestAndExportCanonicalizeLegacyBackslashPaths(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		storedPath string
		file       string
		fqn        string
		content    []byte
	}
	fixtures := []fixture{
		{storedPath: `pkg\alpha.go`, file: "pkg/alpha.go", fqn: "fixture.Alpha", content: []byte("package fixture\n\nfunc Alpha() {}\n")},
		{storedPath: "pkg/zeta.go", file: "pkg/zeta.go", fqn: "fixture.Zeta", content: []byte("package fixture\n\nfunc Zeta() {}\n")},
	}
	for _, fixture := range fixtures {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(fixture.file)), fixture.content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	initRep, err := svc.Init(root, false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		fileHash := sha256Hex(fixture.content)
		if _, err := g.AddNode(&graph.Node{
			ProjectID: initRep.ProjectID, FilePath: fixture.storedPath, Kind: graph.KindFile,
			Language: "go", StartLine: 1, EndLine: 4, SourceHash: fileHash,
		}); err != nil {
			t.Fatal(err)
		}
		symbol := strings.TrimPrefix(fixture.fqn, "fixture.")
		body := "func " + symbol + "() {}"
		if _, err := g.AddNode(&graph.Node{
			ProjectID: initRep.ProjectID, FilePath: fixture.storedPath, Symbol: symbol,
			FQN: fixture.fqn, Kind: graph.KindFunction, Language: "go",
			StartLine: 3, EndLine: 3, Signature: body, SourceHash: sha256Hex([]byte(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if err := g.SetFileHash(initRep.ProjectID, fixture.storedPath, fileHash); err != nil {
			t.Fatal(err)
		}
	}

	exported, err := svc.StructuralExport(root, StructuralExportOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := svc.StructuralManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.IndexFingerprint != exported.IndexFingerprint || manifest.TotalRecords != exported.TotalRecords {
		t.Fatalf("legacy path identity drift: manifest=%+v export=%+v", manifest, exported)
	}
	if !manifest.Freshness.Fresh {
		t.Fatalf("legacy path produced false staleness: %+v", manifest.Freshness)
	}
	if len(exported.Records) != 2 || exported.Records[0].File != "pkg/alpha.go" || exported.Records[1].File != "pkg/zeta.go" {
		t.Fatalf("portable records = %+v", exported.Records)
	}
	for _, record := range exported.Records {
		if record.ContentOmitted || record.ContentHash == "" {
			t.Fatalf("canonical path did not resolve live source: %+v", record)
		}
	}
}

func TestStructuralExportSchemaRequiresCanonicalProjectKey(t *testing.T) {
	fixturePath := filepath.Join("testdata", "codemap_structural_export_v1.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var valid StructuralExportReport
	if err := json.Unmarshal(data, &valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*StructuralExportReport)
	}{
		{name: "envelope length", mutate: func(rep *StructuralExportReport) { rep.ProjectKey = "0123456789a" }},
		{name: "record lowercase hex", mutate: func(rep *StructuralExportReport) { rep.Records[0].ProjectKey = "ABCDEF012345" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rep := valid
			rep.Records = append([]StructuralSymbolRecord(nil), valid.Records...)
			test.mutate(&rep)
			if err := validateStructuralExportSchema(t, &rep); err == nil {
				t.Fatal("invalid project_key validated against structural-export v1")
			}
		})
	}
}
