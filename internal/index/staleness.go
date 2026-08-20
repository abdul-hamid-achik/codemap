package index

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Staleness summarizes how the working tree has drifted from the index since the
// last run: files whose content changed, recognized source files of an
// already-indexed language that aren't indexed yet (new), and indexed files no
// longer on disk (deleted). It is computed WITHOUT language servers — it hashes
// the files already recorded in index_state and recognizes new ones by extension
// — so it's cheap enough to report from `status` and lets an agent know its
// answers may be behind the code before it trusts them.
type Staleness struct {
	Changed int `json:"changed"`
	New     int `json:"new"`
	Deleted int `json:"deleted"`
}

// Any reports whether the index is behind the working tree in any way.
func (s Staleness) Any() bool { return s.Changed+s.New+s.Deleted > 0 }

// Staleness compares the project's index_state to the working tree at root.
// indexedLangs restricts "new" detection to languages already present in the
// index, so a recognized-but-unindexed language can't show false drift. It needs
// no registered extractors (unlike walk), so it never spawns a language server.
func (ix *Indexer) Staleness(projectID int64, root string, indexedLangs map[string]bool) (Staleness, error) {
	indexed, err := ix.graph.IndexedFiles(projectID)
	if err != nil {
		return Staleness{}, err
	}
	fileHashes := make(map[string]string, len(indexed))
	for _, rel := range indexed {
		fileHashes[rel], _ = ix.graph.FileHash(projectID, rel)
	}
	return ix.StalenessFromSnapshot(root, fileHashes, indexedLangs)
}

// StalenessFromSnapshot compares the working tree against source-free index
// metadata already captured by the caller. StructuralManifest uses this with
// file hashes, languages, and its fingerprint drawn from one SQLite read
// transaction, preventing a concurrent reindex from producing a torn
// old-fingerprint/new-freshness response.
func (ix *Indexer) StalenessFromSnapshot(root string, fileHashes map[string]string, indexedLangs map[string]bool) (Staleness, error) {
	return ix.stalenessFromSnapshot(root, fileHashes, indexedLangs, false)
}

// StalenessFromSnapshotStrict is the manifest-facing variant of
// StalenessFromSnapshot. It refuses to certify freshness when an indexed file
// cannot be read for a reason other than non-existence, or when the working
// tree cannot be walked completely. Ordinary status remains conservative and
// best-effort through StalenessFromSnapshot.
func (ix *Indexer) StalenessFromSnapshotStrict(root string, fileHashes map[string]string, indexedLangs map[string]bool) (Staleness, error) {
	return ix.stalenessFromSnapshot(root, fileHashes, indexedLangs, true)
}

func (ix *Indexer) stalenessFromSnapshot(root string, fileHashes map[string]string, indexedLangs map[string]bool, strict bool) (Staleness, error) {
	var st Staleness
	inIndex := make(map[string]bool, len(fileHashes))
	for rel, prev := range fileHashes {
		contractRel := graph.CanonicalStructuralPath(rel)
		inIndex[contractRel] = true
		path := filepath.Join(root, filepath.FromSlash(contractRel))
		info, rerr := stalenessIndexedFileInfo(path)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				st.Deleted++
				continue
			}
			if strict {
				return Staleness{}, fmt.Errorf("read indexed file %s: %w", contractRel, rerr)
			}
			continue // ordinary status is conservative for other read failures
		}
		if !info.Mode().IsRegular() {
			if strict {
				return Staleness{}, fmt.Errorf("read indexed file %s: non-regular file mode %s", contractRel, info.Mode())
			}
			continue
		}
		currentHash, rerr := hashFile(path)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				st.Deleted++
				continue
			}
			if strict {
				return Staleness{}, fmt.Errorf("read indexed file %s: %w", contractRel, rerr)
			}
			continue // ordinary status is conservative for other read failures
		}
		if prev != "" && currentHash != prev {
			st.Changed++
		}
	}
	// New: a recognized source file of an already-indexed language that isn't in
	// the index yet. Mirrors walk's dir/exclude rules but recognizes by extension
	// only (no extractor needed, so no server spawn).
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if strict {
				return err
			}
			return nil
		}
		name := d.Name()
		// P1-07: pass the project-relative path (not the bare base name)
		// to ix.excluded so slash-anchored patterns (e.g. "db/migrations")
		// match what the indexer's walk skipped. Pre-fix a base name
		// never matched a slash-anchored pattern, so files under
		// excluded dirs were counted as "new" forever — perpetual
		// false drift that erodes the freshness signal.
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			if strict {
				return fmt.Errorf("relative path for %s: %w", path, relErr)
			}
			rel = path
		}
		if d.IsDir() {
			if path != root && (ix.excluded(rel) || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if ix.excluded(rel) {
			return nil
		}
		lang := extract.LanguageForPath(path)
		if lang == "" || !indexedLangs[lang] {
			return nil
		}

		if !inIndex[graph.CanonicalStructuralPath(rel)] {
			st.New++
		}
		return nil
	})
	if walkErr != nil && strict {
		return Staleness{}, fmt.Errorf("walk working tree %s: %w", root, walkErr)
	}
	return st, nil
}

// stalenessIndexedFileInfo follows a symlink only far enough to classify its
// target. Callers must check Mode().IsRegular before ReadFile so FIFOs, sockets,
// devices, and directories cannot block or masquerade as readable source.
func stalenessIndexedFileInfo(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Stat(path)
	}
	return info, nil
}
