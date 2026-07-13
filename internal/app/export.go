package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/cachestate"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
	"github.com/abdul-hamid-achik/codemap/internal/version"
)

// I30: portable index tarballs. The fcheap-backed cache (cache.go) is same-
// machine only — fcheap's stash vault lives in a local dir. `cache export`/
// `cache import` package the exact same snapshot.Export/Import slice as a
// self-contained tar.gz so a CI job (or a teammate) can hand a finished index
// to the next runner with no shared fcheap store and no re-indexing.

// cacheTarballSchema is bumped when the TARBALL WRAPPER format (this file's
// layout: cache_manifest.json + the snapshot.* files, tar+gzip'd) changes
// incompatibly. It is independent of snapshot.SchemaVersion, which governs
// the graph/vector slice inside — a tarball schema bump would be needed for,
// say, a new compression scheme or manifest shape, not for a graph schema
// change (that's already gated by snapshot.Import itself).
const cacheTarballSchema = 1

// snapshotManifestFile mirrors internal/snapshot's private fileManifest
// const (kept in sync by hand — snapshot.go doesn't export its filename
// consts, and adding cross-package coupling for one string isn't worth it).
const snapshotManifestFile = "snapshot.json"

// cacheManifestFile is the tarball's own wrapper header, sitting alongside
// the snapshot package's snapshot.json inside the archive.
const cacheManifestFile = "cache_manifest.json"

// tarballManifest is cache_manifest.json: the portable-tarball-specific
// header. The graph/vector provenance (project, embedding profile, tree
// hash) already lives in snapshot.json (BaseSHA carries the tree hash — see
// CacheExport, which passes it as Export's baseSHA argument exactly like
// CacheSave does) — this file adds only what the wrapper format itself
// needs: its own schema version and the codemap build that produced it.
type tarballManifest struct {
	SchemaVersion  int    `json:"schema_version"`
	CodemapVersion string `json:"codemap_version"`
	CreatedAt      string `json:"created_at"`
}

// CacheExportReport is the result of `cache export`.
type CacheExportReport struct {
	Path             string `json:"path"`
	Project          string `json:"project"`
	TreeHash         string `json:"tree_hash"`
	EmbeddingProfile string `json:"embedding_profile,omitempty"`
	Nodes            int    `json:"nodes"`
	Edges            int    `json:"edges"`
	Vectors          int    `json:"vectors"`
}

// CacheExport writes the project's current index (graph + vectors, when
// present) to a self-contained tar.gz at outPath — the same snapshot slice
// fcheap-backed caching uses, but portable across machines with no shared
// fcheap store. The tree hash is computed exactly the way CacheSave computes
// it (cachestate.TreeHash off the DB's index_state), so an archive exported
// right after `codemap index` carries the same cache key a same-machine
// fcheap entry would.
func (svc *Service) CacheExport(_ context.Context, cwd, outPath string) (*CacheExportReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, coded(CodeMissing, "run: codemap index", fmt.Errorf("project %q is not indexed yet", name))
	}
	if err != nil {
		return nil, err
	}

	treeHash, err := cachestate.TreeHash(g, p.ID)
	if err != nil {
		return nil, err
	}

	// Carry vectors (and their profile) only if the project has embeddings —
	// mirrors CacheSave/BranchSnapshot exactly.
	var vec *vector.Store
	profile := ""
	if n, _ := svc.embeddedCount(name); n > 0 {
		if v, verr := svc.s.Vectors(); verr == nil {
			vec = v
			if emb := svc.s.Embedder(); emb != nil {
				profile = emb.Profile().String()
			}
		}
	}

	tmp, err := os.MkdirTemp("", "codemap-export-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	m, err := snapshot.Export(g, vec, p.ID, name, tmp, profile, treeHash)
	if err != nil {
		return nil, err
	}

	tm := tarballManifest{
		SchemaVersion:  cacheTarballSchema,
		CodemapVersion: version.Version,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSONFile(filepath.Join(tmp, cacheManifestFile), tm); err != nil {
		return nil, err
	}

	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := tarGzDir(tmp, outPath); err != nil {
		return nil, coded(CodeOperational, "", fmt.Errorf("write tarball %s: %w", outPath, err))
	}

	return &CacheExportReport{
		Path: outPath, Project: name, TreeHash: treeHash, EmbeddingProfile: profile,
		Nodes: m.Nodes, Edges: m.Edges, Vectors: m.Vectors,
	}, nil
}

// CacheImportReport is the result of `cache import`.
type CacheImportReport struct {
	Path             string `json:"path"`
	Project          string `json:"project"`
	ArchiveTreeHash  string `json:"archive_tree_hash"`
	WorkingTreeHash  string `json:"working_tree_hash,omitempty"`
	TreeHashMatched  bool   `json:"tree_hash_matched"`
	EmbeddingProfile string `json:"embedding_profile,omitempty"`
	Nodes            int    `json:"nodes"`
	Edges            int    `json:"edges"`
	Vectors          int    `json:"vectors"`
	Warning          string `json:"warning,omitempty"`
}

// CacheImport restores a tarball produced by CacheExport into the project at
// cwd, registering the project first if it isn't already (the common CI case:
// a fresh checkout with no prior `codemap init`/`index`). It validates, in
// order, BEFORE touching the project:
//
//  1. the tarball's own wrapper schema (cacheTarballSchema) — an unreadable
//     or newer-than-supported archive is refused with a clean CodedError, not
//     a partial/corrupt restore.
//  2. embedding-profile compatibility, EXACTLY the same gate snapshot.Import
//     applies for same-machine restores (empty on either side is compatible;
//     a real mismatch is always refused, force or not — a mismatched local
//     Ollama model must never be allowed to corrupt restored vectors).
//  3. the working tree hash against the archive's recorded tree hash (the
//     same WorkingTreeHash CacheRestore already computes to validate a
//     same-machine cache hit). POLICY: a mismatch is refused by default —
//     the whole point of a portable tarball is "this exact tree", so a silent
//     divergence would answer queries against code that isn't actually
//     checked out. `force` downgrades that refusal to a recorded warning, for
//     the legitimate case of importing a slightly-ahead snapshot on purpose
//     (e.g. pulling a base-branch index to seed a PR branch's cache before an
//     incremental reindex catches it up).
func (svc *Service) CacheImport(_ context.Context, cwd, inPath string, force bool) (*CacheImportReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "codemap-import-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := untarGz(inPath, tmp); err != nil {
		return nil, coded(CodeOperational, "verify the file was produced by `codemap cache export` and isn't truncated", err)
	}

	var tm tarballManifest
	if err := readJSONFile(filepath.Join(tmp, cacheManifestFile), &tm); err != nil {
		return nil, coded(CodeCorrupt, "verify the file was produced by `codemap cache export`", fmt.Errorf("read %s: %w", cacheManifestFile, err))
	}
	if tm.SchemaVersion != cacheTarballSchema {
		return nil, coded(CodeOperational, "re-export with a compatible codemap version", fmt.Errorf("cache tarball schema v%d != supported v%d", tm.SchemaVersion, cacheTarballSchema))
	}

	var sm snapshot.Manifest
	if err := readJSONFile(filepath.Join(tmp, snapshotManifestFile), &sm); err != nil {
		return nil, coded(CodeCorrupt, "verify the file was produced by `codemap cache export`", fmt.Errorf("read %s: %w", snapshotManifestFile, err))
	}

	curProfile := ""
	if emb := svc.s.Embedder(); emb != nil {
		curProfile = emb.Profile().String()
	}
	if !profileCompatible(sm.EmbeddingProfile, curProfile) {
		return nil, coded(CodeOperational,
			"reindex/re-embed with the matching model, or import into a project with no embeddings",
			fmt.Errorf("archive embedding profile %q != current %q — refusing to mix models", sm.EmbeddingProfile, curProfile))
	}

	workingHash, wherr := cachestate.WorkingTreeHash(root, svc.s.Config.Index.Exclude, svc.s.Config.Index.MaxFileBytes)
	matched := wherr == nil && workingHash == sm.BaseSHA
	var warning string
	if !matched {
		if !force {
			return nil, coded(CodeOperational,
				"pass --force to import anyway, or reindex so the archive matches this working tree",
				fmt.Errorf("archive tree hash %s != working tree hash %s", shortHashApp(sm.BaseSHA), shortHashApp(workingHash)))
		}
		warning = fmt.Sprintf("tree hash mismatch (archive %s, working tree %s) — imported anyway (--force)", shortHashApp(sm.BaseSHA), shortHashApp(workingHash))
	}

	pid, err := g.UpsertProject(name, root, detectLanguage(root))
	if err != nil {
		return nil, err
	}
	project, err := g.GetProjectByID(pid)
	if err != nil {
		return nil, err
	}
	name = project.Name

	// Always resolve the vector store here, regardless of whether the
	// ARCHIVE carries vectors (sm.Vectors > 0): snapshot.Import only clears
	// the project's EXISTING vectors when vec != nil (see its
	// vec.DeleteByProject call, gated the same way). Gating vec on
	// sm.Vectors>0 left an already-embedded project's stale vectors in place
	// whenever a vector-less archive (e.g. exported with --no-embed, the
	// typical CI export) was imported over it: WipeProject still replaces
	// every node with a fresh id, so the untouched vectors become orphaned —
	// dangling Meta.NodeID — yet keep answering semantic queries with
	// stale/mismatched results. Passing vec unconditionally lets
	// snapshot.Import's existing wipe-then-restore path clear them even when
	// there are zero rows to re-insert.
	var vec *vector.Store
	if v, verr := svc.s.Vectors(); verr == nil {
		vec = v
	}
	m, ierr := snapshot.Import(g, vec, pid, name, tmp, curProfile)
	if ierr != nil {
		return nil, coded(CodeCorrupt, "the archive may be corrupt — re-export it", ierr)
	}

	return &CacheImportReport{
		Path: inPath, Project: name, ArchiveTreeHash: sm.BaseSHA, WorkingTreeHash: workingHash,
		TreeHashMatched: matched, EmbeddingProfile: sm.EmbeddingProfile,
		Nodes: m.Nodes, Edges: m.Edges, Vectors: m.Vectors, Warning: warning,
	}, nil
}

// shortHashApp is shortHash's app-package twin (the CLI's shortHash lives in
// cmd/codemap, unreachable from here) — first 12 runes of a hash, or the
// whole (possibly empty) string if shorter, safe on malformed/absent input.
func shortHashApp(s string) string {
	r := []rune(s)
	if len(r) <= 12 {
		return s
	}
	return string(r[:12])
}

// --- tar+gzip io (stdlib only, per AGENTS.md — no new deps) ---

// tarGzDir tars+gzips every regular file directly inside srcDir (no
// subdirectories — Export/writeJSONFile only ever write flat files) into a
// single archive at outPath, in sorted filename order so the same snapshot
// content produces a byte-identical tarball.
func tarGzDir(srcDir, outPath string) (err error) {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			return ierr
		}
		data, rerr := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if rerr != nil {
			return rerr
		}
		hdr := &tar.Header{
			Name:    e.Name(),
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// maxUntarGzDecompressedBytes bounds the TOTAL bytes untarGz will write
// across every entry of one archive. It is generous — I30's own committed
// exports run a few MB — but stops a maliciously crafted or corrupt tarball
// (a classic "zip bomb": a tiny gzip stream that decompresses to gigabytes,
// e.g. a multi-GB run of NUL bytes that compresses to a few MB) from
// exhausting local disk before extraction ever fails. Enforced two ways:
// per-entry, against the tar header's declared Size (trivially attacker-
// controlled, but catches the common case outright before a single byte is
// written), and cumulatively, against what untarGz actually writes —
// independent of what any header claims.
// Not a const: tests shrink it temporarily to exercise the cumulative-cap
// path deterministically without writing gigabytes of fixture data.
var maxUntarGzDecompressedBytes int64 = 4 << 30 // 4 GiB

// untarGz extracts inPath into destDir. Every entry is validated to resolve
// INSIDE destDir before anything is written — the classic "tar-slip" path-
// traversal attack packs a "../../etc/cron.d/evil" (or an absolute path) into
// an archive entry name so naive extraction writes outside the intended
// directory. Non-regular entries (symlinks, devices, hardlinks) are skipped
// rather than followed, since a portable index archive never legitimately
// contains one. Total decompressed output is capped at
// maxUntarGzDecompressedBytes (see its doc) against a zip-bomb-style archive.
func untarGz(inPath, destDir string) error {
	f, err := os.Open(inPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", inPath, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a valid gzip archive (corrupt or truncated): %w", inPath, err)
	}
	defer func() { _ = gz.Close() }()

	cleanDest := filepath.Clean(destDir)
	tr := tar.NewReader(gz)
	var totalWritten int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry (archive corrupt or truncated): %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			continue // skip symlinks/hardlinks/devices — never legitimate here
		}
		if hdr.Typeflag == tar.TypeReg && (hdr.Size < 0 || hdr.Size > maxUntarGzDecompressedBytes) {
			return coded(CodeOperational, "the archive may be corrupt or maliciously crafted — re-export it",
				fmt.Errorf("tar entry %q declares an implausible size (%d bytes, cap %d)", hdr.Name, hdr.Size, maxUntarGzDecompressedBytes))
		}
		name := filepath.Clean(hdr.Name)
		if name == "." {
			continue
		}
		if filepath.IsAbs(hdr.Name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("tar entry %q escapes the archive root — refusing to extract (tar-slip)", hdr.Name)
		}
		target := filepath.Join(cleanDest, name)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(filepath.Separator)) {
			return fmt.Errorf("tar entry %q escapes the archive root — refusing to extract (tar-slip)", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		// LimitReader caps THIS copy at (remaining+1) bytes: a copy that reads
		// exactly remaining+1 proves the entry needed more than the budget
		// left, distinguishing "hit the cap" from "happened to end exactly at
		// the boundary".
		remaining := maxUntarGzDecompressedBytes - totalWritten
		n, cerr := io.Copy(out, io.LimitReader(tr, remaining+1)) //nolint:gosec // bounded by maxUntarGzDecompressedBytes, enforced immediately below
		totalWritten += n
		if cerr != nil {
			_ = out.Close()
			return fmt.Errorf("write %s (archive corrupt or truncated): %w", name, cerr)
		}
		if n > remaining {
			_ = out.Close()
			return coded(CodeOperational, "the archive may be corrupt or maliciously crafted — re-export it",
				fmt.Errorf("tarball decompressed past the %d-byte safety cap while writing %q — refusing to extract further (possible zip bomb)", maxUntarGzDecompressedBytes, name))
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
