// Package branchstate manages the per-project pointer file that maps each git
// branch to the fcheap stash holding that branch's code-intelligence index. It's
// a fast local cache over fcheap (the durable content store) — the lookup that
// drives branch-aware index switching without scanning fcheap on every checkout —
// and is rebuildable from fcheap if lost (Rebuild).
package branchstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

const schemaVersion = 1

// BranchEntry records the snapshot that holds a branch's index.
type BranchEntry struct {
	StashID          string `json:"stash_id"`
	BaseSHA          string `json:"base_sha,omitempty"`
	EmbeddingProfile string `json:"embedding_profile,omitempty"`
	NodeCount        int    `json:"node_count,omitempty"`
	VectorCount      int    `json:"vector_count,omitempty"`
	LastSwitchedAt   string `json:"last_switched_at,omitempty"`
}

// State is a repo's branch→snapshot pointer file.
type State struct {
	Schema        int                    `json:"schema"`
	RepoRoot      string                 `json:"repo_root,omitempty"`
	RepoHash      string                 `json:"repo_hash,omitempty"`
	ProjectName   string                 `json:"project_name,omitempty"`
	DefaultBranch string                 `json:"default_branch,omitempty"`
	ActiveBranch  string                 `json:"active_branch,omitempty"`
	Branches      map[string]BranchEntry `json:"branches"`
}

// StatePath is where a repo's pointer file lives — a sibling of the projects
// registry, keyed by the stable repo hash.
func StatePath(repoHash string) string {
	return filepath.Join(config.DataDir(), "branches", repoHash+".json")
}

// Load reads the pointer file at path. A missing file yields an empty (usable)
// State, not an error — the first switch in a repo starts fresh.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Schema: schemaVersion, Branches: map[string]BranchEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Branches == nil {
		s.Branches = map[string]BranchEntry{}
	}
	return &s, nil
}

// Save atomically writes the pointer file at path (temp + rename in the same dir,
// so a concurrent reader never sees a half-written file).
func (s *State) Save(path string) error {
	s.Schema = schemaVersion
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".branchstate-*.tmp")
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

// Lookup returns the entry recorded for a branch, if any.
func (s *State) Lookup(branch string) (BranchEntry, bool) {
	e, ok := s.Branches[branch]
	return e, ok
}

// Record sets/updates a branch's entry, stamping LastSwitchedAt if the caller
// didn't.
func (s *State) Record(branch string, e BranchEntry) {
	if s.Branches == nil {
		s.Branches = map[string]BranchEntry{}
	}
	if e.LastSwitchedAt == "" {
		e.LastSwitchedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.Branches[branch] = e
}

// Rebuild reconstructs a repo's branch→snapshot map from fcheap (the durable
// store), so a lost or stale pointer file is recoverable. It lists the codemap
// index stashes for the repo and reads each one's `branchname:<raw>` tag
// (preferred — the original branch name as the user typed it) or falls back to
// the sanitized `branch:<seg>` tag, keeping the newest stash per branch.
// base_sha and counts aren't in fcheap's list output, so those stay empty on a
// rebuild (a later snapshot fills them in).
func Rebuild(ctx context.Context, repoHash string) (*State, error) {
	stashes, err := snapshot.FcheapList(ctx, []string{"codemap-index", "repo:" + repoHash})
	if err != nil {
		return nil, err
	}
	s := &State{Schema: schemaVersion, RepoHash: repoHash, Branches: map[string]BranchEntry{}}
	for _, st := range stashes {
		// P1-17 (B49): prefer the raw `branchname:` tag so the rebuilt
		// map's keys match the names a fresh BranchSnapshot would record.
		// A snapshot written before this fix only carries the sanitized
		// `branch:` tag, so fall back to that — and prefix `san:` on it
		// to avoid a "feature/x" key colliding with a later raw
		// "feature/x" (impossible today, but it keeps the model sound).
		branch := tagValue(st.Tags, "branchname:")
		if branch == "" {
			if seg := tagValue(st.Tags, "branch:"); seg != "" {
				branch = "san:" + seg
			}
		}
		if branch == "" {
			continue
		}
		if prev, ok := s.Branches[branch]; ok && prev.LastSwitchedAt >= st.CreatedAt {
			continue // keep the newer stash for this branch
		}
		s.Branches[branch] = BranchEntry{StashID: st.ID, LastSwitchedAt: st.CreatedAt}
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
