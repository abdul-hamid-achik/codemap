package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// StructuralExportSchemaVersion is the major version of the portable symbol
// record contract. Additive optional fields are compatible within v1; removing
// or changing a required field needs a new version.
const StructuralExportSchemaVersion = 1

// Structural export is paginated so a peer process never has to accept an
// unbounded JSON response. Content is independently capped per record.
const (
	DefaultStructuralExportLimit        = 1000
	MaxStructuralExportLimit            = 5000
	DefaultStructuralExportContentBytes = 16 * 1024
	MaxStructuralExportContentBytes     = 256 * 1024
)

// StructuralExportOptions controls one deterministic page of symbol records.
// Zero values select the defaults.
type StructuralExportOptions struct {
	Offset          int
	Limit           int
	MaxContentBytes int
}

// StructuralSymbolRecord is a portable, database-independent description of
// one indexed definition. The tuple (project_key,file,start_line,fqn,kind) is
// the join key intended for sibling tools; SQLite ids and vector ids are
// deliberately absent.
type StructuralSymbolRecord struct {
	SchemaVersion int `json:"schema_version"`
	// Ordinal is the one-based position in the complete deterministic export.
	// Consumers use it to validate pagination without reconstructing sort order
	// from signature/docstring fields that may be truncated in this record.
	Ordinal          int    `json:"ordinal"`
	Project          string `json:"project"`
	ProjectKey       string `json:"project_key"`
	IndexFingerprint string `json:"index_fingerprint"`
	File             string `json:"file"`
	StartLine        int    `json:"start_line"`
	EndLine          int    `json:"end_line"`
	Symbol           string `json:"symbol"`
	FQN              string `json:"fqn"`
	Kind             string `json:"kind"`
	Language         string `json:"language"`
	Signature        string `json:"signature"`
	Docstring        string `json:"docstring"`
	SourceHash       string `json:"source_hash"`
	Content          string `json:"content"`
	// ContentHash hashes the complete current line range before Content is
	// capped. It lets a consumer detect changes without depending on the
	// indexer's extractor-specific SourceHash representation.
	ContentHash        string `json:"content_hash,omitempty"`
	SignatureTruncated bool   `json:"signature_truncated,omitempty"`
	DocstringTruncated bool   `json:"docstring_truncated,omitempty"`
	ContentTruncated   bool   `json:"content_truncated,omitempty"`
	ContentOmitted     bool   `json:"content_omitted,omitempty"`
	OmissionReason     string `json:"omission_reason,omitempty"`
	FileStale          bool   `json:"file_stale,omitempty"`
}

// StructuralExportReport is one bounded page of the versioned peer contract.
// Records repeat project/schema metadata so each remains usable if the envelope
// is converted to JSONL later.
type StructuralExportReport struct {
	SchemaVersion    int                      `json:"schema_version"`
	Project          string                   `json:"project"`
	ProjectKey       string                   `json:"project_key"`
	IndexFingerprint string                   `json:"index_fingerprint"`
	Offset           int                      `json:"offset"`
	Limit            int                      `json:"limit"`
	MaxContentBytes  int                      `json:"max_content_bytes"`
	TotalRecords     int                      `json:"total_records"`
	ReturnedRecords  int                      `json:"returned_records"`
	Complete         bool                     `json:"complete"`
	NextOffset       int                      `json:"next_offset,omitempty"`
	Records          []StructuralSymbolRecord `json:"records"`
}

// StructuralExport returns one stable page of indexed symbol definitions for
// a sibling process such as vecgrep. It is intentionally CLI-service only in
// v1: no DB sharing, package import, MCP schema tax, or daemon coupling.
//
// Source is read only for files whose current full-file hash matches the
// indexed hash. A stale/missing/unsafe file still yields its structural record,
// but Content is empty with an explicit omission reason so a consumer never
// embeds a line range that may now point at a different definition.
func (svc *Service) StructuralExport(cwd string, opts StructuralExportOptions) (*StructuralExportReport, error) {
	return svc.structuralExport(cwd, opts, nil)
}

// structuralExport's barrier is used only by the deterministic concurrency
// test. It runs after the SQLite snapshot is captured and before live source is
// read; production callers use StructuralExport.
func (svc *Service) structuralExport(cwd string, opts StructuralExportOptions, afterSnapshot func() error) (*StructuralExportReport, error) {
	opts, err := normalizeStructuralExportOptions(opts)
	if err != nil {
		return nil, err
	}

	pid, project, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &StructuralExportReport{
		SchemaVersion:   StructuralExportSchemaVersion,
		Project:         project,
		ProjectKey:      git.RepoHash(cwd),
		Offset:          opts.Offset,
		Limit:           opts.Limit,
		MaxContentBytes: opts.MaxContentBytes,
		Records:         []StructuralSymbolRecord{},
	}
	if !found {
		rep.Complete = true
		return rep, nil
	}

	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(project)
	if err != nil {
		return nil, err
	}
	rep.ProjectKey = git.RepoHash(p.Path)

	snapshot, err := g.ProjectStructuralSnapshot(pid)
	if err != nil {
		return nil, err
	}
	if afterSnapshot != nil {
		if err := afterSnapshot(); err != nil {
			return nil, fmt.Errorf("structural export snapshot barrier: %w", err)
		}
	}
	nodes := snapshot.Nodes
	symbols := make([]graph.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != graph.KindFile {
			symbols = append(symbols, n)
		}
	}
	sort.Slice(symbols, func(i, j int) bool {
		return structuralNodeLess(symbols[i], symbols[j])
	})
	rep.IndexFingerprint = structuralIndexFingerprint(rep.ProjectKey, symbols)

	rep.TotalRecords = len(symbols)
	start := opts.Offset
	if start > len(symbols) {
		start = len(symbols)
	}
	end := start + opts.Limit
	if end > len(symbols) {
		end = len(symbols)
	}

	files := make(map[string]structuralFileSource)
	for i, n := range symbols[start:end] {
		fsrc, ok := files[n.FilePath]
		if !ok {
			fsrc = loadStructuralFile(snapshot.FileHashes[n.FilePath], p.Path, n.FilePath)
			files[n.FilePath] = fsrc
		}
		rep.Records = append(rep.Records, structuralRecord(rep, n, start+i+1, fsrc, opts.MaxContentBytes))
	}
	rep.ReturnedRecords = len(rep.Records)
	rep.Complete = end >= len(symbols)
	if !rep.Complete {
		rep.NextOffset = end
	}
	return rep, nil
}

func normalizeStructuralExportOptions(opts StructuralExportOptions) (StructuralExportOptions, error) {
	if opts.Offset < 0 {
		return opts, fmt.Errorf("structural export offset must be non-negative")
	}
	if opts.Limit == 0 {
		opts.Limit = DefaultStructuralExportLimit
	}
	if opts.Limit < 0 || opts.Limit > MaxStructuralExportLimit {
		return opts, fmt.Errorf("structural export limit must be between 1 and %d", MaxStructuralExportLimit)
	}
	if opts.MaxContentBytes == 0 {
		opts.MaxContentBytes = DefaultStructuralExportContentBytes
	}
	if opts.MaxContentBytes < 0 || opts.MaxContentBytes > MaxStructuralExportContentBytes {
		return opts, fmt.Errorf("structural export max content bytes must be between 1 and %d", MaxStructuralExportContentBytes)
	}
	return opts, nil
}

func structuralNodeLess(a, b graph.Node) bool {
	aPath := graph.CanonicalStructuralPath(a.FilePath)
	bPath := graph.CanonicalStructuralPath(b.FilePath)
	if aPath != bPath {
		return aPath < bPath
	}
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	if a.EndLine != b.EndLine {
		return a.EndLine < b.EndLine
	}
	if a.FQN != b.FQN {
		return a.FQN < b.FQN
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Symbol != b.Symbol {
		return a.Symbol < b.Symbol
	}
	if a.Language != b.Language {
		return a.Language < b.Language
	}
	if a.Signature != b.Signature {
		return a.Signature < b.Signature
	}
	if a.Docstring != b.Docstring {
		return a.Docstring < b.Docstring
	}
	return a.SourceHash < b.SourceHash
}

func structuralIndexFingerprint(projectKey string, symbols []graph.Node) string {
	h := newStructuralIndexFingerprint(projectKey)
	for _, n := range symbols {
		writeStructuralFingerprintNode(h, n)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newStructuralIndexFingerprint(projectKey string) hash.Hash {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "codemap-structural-export-v%d\x00%s\x00", StructuralExportSchemaVersion, projectKey)
	return h
}

func writeStructuralFingerprintNode(h hash.Hash, n graph.Node) {
	for _, field := range []string{
		graph.CanonicalStructuralPath(n.FilePath),
		fmt.Sprintf("%d", n.StartLine),
		fmt.Sprintf("%d", n.EndLine),
		n.Symbol,
		n.FQN,
		n.Kind,
		n.Language,
		n.Signature,
		n.Docstring,
		n.SourceHash,
	} {
		_, _ = h.Write([]byte(field))
		_, _ = h.Write([]byte{0})
	}
}

type structuralFileSource struct {
	data      []byte
	omission  string
	fileStale bool
}

func loadStructuralFile(indexedHash, root, rel string) structuralFileSource {
	abs, err := safeStructuralPath(root, rel)
	if err != nil {
		if os.IsNotExist(err) {
			return structuralFileSource{omission: "file_missing", fileStale: true}
		}
		return structuralFileSource{omission: "unsafe_path"}
	}
	info, err := structuralExportFileInfo(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return structuralFileSource{omission: "file_missing", fileStale: true}
		}
		return structuralFileSource{omission: "file_unreadable"}
	}
	if !info.Mode().IsRegular() {
		return structuralFileSource{omission: "file_unreadable"}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return structuralFileSource{omission: "file_missing", fileStale: true}
		}
		return structuralFileSource{omission: "file_unreadable"}
	}
	if indexedHash == "" {
		return structuralFileSource{omission: "index_hash_missing", fileStale: true}
	}
	if sha256Hex(data) != indexedHash {
		return structuralFileSource{omission: "stale_index", fileStale: true}
	}
	return structuralFileSource{data: data}
}

// structuralExportFileInfo follows a symlink only for classification. The
// resolved path still comes from safeStructuralPath; this gate solely prevents
// ReadFile from opening a FIFO, socket, device, or directory.
func structuralExportFileInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Stat(path)
	}
	return info, nil
}

func safeStructuralPath(root, rel string) (string, error) {
	rel = filepath.FromSlash(graph.CanonicalStructuralPath(rel))
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute indexed path")
	}
	cleanRel := filepath.Clean(rel)
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("indexed path escapes project")
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	absResolved, err := filepath.EvalSymlinks(filepath.Join(rootResolved, cleanRel))
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(rootResolved, absResolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("indexed path resolves outside project")
	}
	return absResolved, nil
}

func structuralRecord(rep *StructuralExportReport, n graph.Node, ordinal int, src structuralFileSource, maxContentBytes int) StructuralSymbolRecord {
	r := StructuralSymbolRecord{
		SchemaVersion:    rep.SchemaVersion,
		Ordinal:          ordinal,
		Project:          rep.Project,
		ProjectKey:       rep.ProjectKey,
		IndexFingerprint: rep.IndexFingerprint,
		File:             graph.CanonicalStructuralPath(n.FilePath),
		StartLine:        n.StartLine,
		EndLine:          n.EndLine,
		Symbol:           n.Symbol,
		FQN:              n.FQN,
		Kind:             n.Kind,
		Language:         n.Language,
		SourceHash:       n.SourceHash,
		Content:          "",
		FileStale:        src.fileStale,
	}
	r.Signature, r.SignatureTruncated = truncateUTF8Bytes(strings.ToValidUTF8(n.Signature, "\uFFFD"), maxContentBytes)
	r.Docstring, r.DocstringTruncated = truncateUTF8Bytes(strings.ToValidUTF8(n.Docstring, "\uFFFD"), maxContentBytes)
	if src.omission != "" {
		r.ContentOmitted = true
		r.OmissionReason = src.omission
		return r
	}
	content := structuralLineRange(src.data, n.StartLine, n.EndLine)
	content = strings.ToValidUTF8(content, "\uFFFD")
	r.ContentHash = sha256Hex([]byte(content))
	r.Content, r.ContentTruncated = truncateUTF8Bytes(content, maxContentBytes)
	return r
}

func structuralLineRange(data []byte, start, end int) string {
	if start < 1 {
		start = 1
	}
	lines := strings.Split(string(data), "\n")
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

func truncateUTF8Bytes(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end], true
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
