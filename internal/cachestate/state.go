// Package cachestate manages the per-project pointer file that maps a content
// tree hash (a hash of all indexed file_path + file_content_hash pairs) to the
// fcheap stash holding that exact index snapshot. It's a fast local cache over
// fcheap (the durable content store) — the lookup that lets codemap skip a full
// reindex when the working tree matches a previously-saved index — and is
// rebuildable from fcheap if lost (Rebuild).
package cachestate

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

const schemaName = "cache-v1"

// CacheEntry records the snapshot that holds an index for a specific tree hash.
type CacheEntry struct {
	StashID          string `json:"stash_id"`
	TreeHash         string `json:"tree_hash"`
	EmbeddingProfile string `json:"embedding_profile,omitempty"`
	NodeCount        int    `json:"node_count,omitempty"`
	VectorCount      int    `json:"vector_count,omitempty"`
	SavedAt          string `json:"saved_at,omitempty"`
}

// State is a repo's tree-hash→snapshot pointer file.
type State struct {
	Schema   string                `json:"schema"`
	RepoRoot string                `json:"repo_root,omitempty"`
	RepoHash string                `json:"repo_hash,omitempty"`
	Entries  map[string]CacheEntry `json:"entries"` // keyed by TreeHash
}

// StatePath is where a repo's cache pointer file lives — a sibling of the
// branchstate pointer file, keyed by the stable repo hash.
func StatePath(repoHash string) string {
	return filepath.Join(config.DataDir(), "cache", repoHash+".json")
}

// Load reads the pointer file at path. A missing file yields an empty (usable)
// State, not an error — the first cache operation in a repo starts fresh.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Schema: schemaName, Entries: map[string]CacheEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Entries == nil {
		s.Entries = map[string]CacheEntry{}
	}
	return &s, nil
}

// Save atomically writes the pointer file at path (temp + rename in the same
// dir, so a concurrent reader never sees a half-written file).
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cachestate-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Lookup returns the entry recorded for a tree hash, if any.
func (s *State) Lookup(treeHash string) (CacheEntry, bool) {
	e, ok := s.Entries[treeHash]
	return e, ok
}

// Record sets/updates a tree-hash entry, stamping SavedAt if the caller didn't.
func (s *State) Record(treeHash string, e CacheEntry) {
	if s.Entries == nil {
		s.Entries = map[string]CacheEntry{}
	}
	if e.SavedAt == "" {
		e.SavedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.Entries[treeHash] = e
}

// Remove drops a tree-hash entry (e.g. after its stash was deleted).
func (s *State) Remove(treeHash string) {
	delete(s.Entries, treeHash)
}

// TreeHash computes a deterministic hash of all (file_path, file_content_hash)
// pairs from the project's index_state, sorted by path. This is the cache key:
// two working trees with the same files and content produce the same hash, so
// fcheap's content-addressing dedups identical indexes. A single content change
// produces a different hash → no false cache hit.
func TreeHash(g *graph.Store, projectID int64) (string, error) {
	entries, err := g.ProjectIndexState(projectID)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].FilePath < entries[j].FilePath })
	h := sha1.New()
	for _, e := range entries {
		h.Write([]byte(e.FilePath))
		h.Write([]byte{0})
		h.Write([]byte(e.FileHash))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Rebuild reconstructs a repo's tree-hash→snapshot map from fcheap (the durable
// store), so a lost or stale pointer file is recoverable. It lists the codemap
// index stashes for the repo and reads each one's tree:<hash> tag, keeping the
// newest stash per tree hash.
func Rebuild(ctx context.Context, repoHash string) (*State, error) {
	stashes, err := snapshot.FcheapList(ctx, []string{"codemap-cache", "repo:" + repoHash})
	if err != nil {
		return nil, err
	}
	s := &State{Schema: "cache", RepoHash: repoHash, Entries: map[string]CacheEntry{}}
	for _, st := range stashes {
		treeHash := tagValue(st.Tags, "tree:")
		if treeHash == "" {
			continue
		}
		if prev, ok := s.Entries[treeHash]; ok && prev.SavedAt >= st.CreatedAt {
			continue
		}
		s.Entries[treeHash] = CacheEntry{StashID: st.ID, TreeHash: treeHash, SavedAt: st.CreatedAt}
	}
	return s, nil
}

func tagValue(tags []string, prefix string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimPrefix(t, prefix)
		}
	}
	return ""
}
