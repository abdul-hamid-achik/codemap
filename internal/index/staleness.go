package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
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
	var st Staleness
	indexed, err := ix.graph.IndexedFiles(projectID)
	if err != nil {
		return st, err
	}
	inIndex := make(map[string]bool, len(indexed))
	for _, rel := range indexed {
		inIndex[rel] = true
		content, rerr := os.ReadFile(filepath.Join(root, rel))
		if rerr != nil {
			if os.IsNotExist(rerr) {
				st.Deleted++
			}
			continue // unreadable for another reason: be conservative, don't flag
		}
		prev, herr := ix.graph.FileHash(projectID, rel)
		if herr == nil && prev != "" && sha256hex(content) != prev {
			st.Changed++
		}
	}
	// New: a recognized source file of an already-indexed language that isn't in
	// the index yet. Mirrors walk's dir/exclude rules but recognizes by extension
	// only (no extractor needed, so no server spawn).
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (ix.excluded(name) || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if ix.excluded(name) {
			return nil
		}
		lang := extract.LanguageForPath(path)
		if lang == "" || !indexedLangs[lang] {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if !inIndex[rel] {
			st.New++
		}
		return nil
	})
	return st, nil
}
