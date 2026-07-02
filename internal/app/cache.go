package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/cachestate"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// CacheSave stashes the project's current index (graph + vectors) into fcheap,
// keyed by a tree hash computed from the indexed file set. The tree hash is the
// cache key: two working trees with identical files + content share one fcheap
// entry (content-addressing dedups). Best-effort: a missing fcheap binary, an
// unindexed project, or a non-git dir returns nil (nothing to cache).
func (svc *Service) CacheSave(ctx context.Context, cwd string) (stashID, treeHash string, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return "", "", err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return "", "", err
	}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return "", "", nil // not indexed yet
	}
	if err != nil {
		return "", "", err
	}

	// Compute the tree hash from the index_state.
	treeHash, err = cachestate.TreeHash(g, p.ID)
	if err != nil {
		return "", "", err
	}

	// Carry vectors (and their profile) only if the project has embeddings.
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

	repoHash := git.RepoHash(root)
	tmp, err := os.MkdirTemp("", "codemap-cache-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	m, err := snapshot.Export(g, vec, p.ID, name, tmp, profile, treeHash)
	if err != nil {
		return "", "", err
	}
	if !svc.CacheFcheapAvailable() {
		return "", treeHash, nil // graceful no-op when fcheap is not on PATH
	}

	tags := []string{"codemap-cache", "repo:" + repoHash, "tree:" + treeHash}
	stashID, err = snapshot.FcheapSave(ctx, tmp, "codemap", name+"@"+treeHash[:12], tags, treeHash)
	if err != nil {
		return "", "", err
	}

	// Record in the local pointer file.
	statePath := cachestate.StatePath(repoHash)
	cs, err := cachestate.Load(statePath)
	if err != nil {
		return stashID, treeHash, nil // save succeeded; pointer file is best-effort
	}
	if cs.RepoRoot == "" {
		cs.RepoRoot, cs.RepoHash = root, repoHash
	}
	cs.Record(treeHash, cachestate.CacheEntry{
		StashID:          stashID,
		TreeHash:         treeHash,
		EmbeddingProfile: profile,
		NodeCount:        m.Nodes,
		VectorCount:      m.Vectors,
	})
	_ = cs.Save(statePath) // best-effort
	return stashID, treeHash, nil
}

// CacheRestore searches fcheap for a cached index matching the current working
// tree's hash and restores it (skipping extraction + embedding entirely). Returns
// (restored=true, nil) on a hit, (false, nil) on a miss. The embedding profile
// must match — a mismatch is a miss, not an error.
func (svc *Service) CacheRestore(ctx context.Context, cwd string) (restored bool, stashID string, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return false, "", err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return false, "", err
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return false, "", nil // not indexed yet — nothing to restore into
	}

	// Pin P0-01: the cache key is the working tree's hash (read from disk),
	// not the index_state's hash (read from DB). Pre-fix, TreeHash-from-DB
	// was computed from the last index's recorded hashes, so a working tree
	// edited after the last index still produced a tree hash equal to the
	// last indexed tree hash → every --reindex silently restored a stale
	// cache. WorkingTreeHash walks the disk and uses the same SHA-256
	// per-file hashes the indexer would compute on the same walk, so a
	// hit means "disk matches this snapshot" — not "DB matches itself".
	treeHash, err := cachestate.WorkingTreeHash(root, svc.s.Config.Index.Exclude, svc.s.Config.Index.MaxFileBytes)
	if err != nil {
		return false, "", err
	}

	repoHash := git.RepoHash(root)
	statePath := cachestate.StatePath(repoHash)
	cs, err := cachestate.Load(statePath)
	if err != nil {
		return false, "", nil
	}

	entry, ok := cs.Lookup(treeHash)
	if !ok || entry.StashID == "" {
		return false, "", nil // no local cache entry
	}

	curProfile := ""
	if emb := svc.s.Embedder(); emb != nil {
		curProfile = emb.Profile().String()
	}
	if !profileCompatible(entry.EmbeddingProfile, curProfile) {
		return false, "", nil // profile mismatch → miss, not error
	}

	tmp, err := os.MkdirTemp("", "codemap-cache-restore-")
	if err != nil {
		return false, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	verified, rerr := snapshot.FcheapRestore(ctx, entry.StashID, tmp)
	if rerr != nil || !verified {
		// Dangling/corrupt stash — remove the stale entry and fall through.
		cs.Remove(treeHash)
		_ = cs.Save(statePath)
		return false, "", nil
	}

	var vec *vector.Store
	if entry.VectorCount > 0 {
		if v, verr := svc.s.Vectors(); verr == nil {
			vec = v
		}
	}
	if _, ierr := snapshot.Import(g, vec, p.ID, name, tmp, curProfile); ierr != nil {
		return false, "", nil // bad snapshot → treat as miss
	}
	return true, entry.StashID, nil
}

// CacheListEntry is one cached index in the list output.
type CacheListEntry struct {
	StashID          string `json:"stash_id"`
	TreeHash         string `json:"tree_hash"`
	EmbeddingProfile string `json:"embedding_profile,omitempty"`
	NodeCount        int    `json:"node_count,omitempty"`
	VectorCount      int    `json:"vector_count,omitempty"`
	SavedAt          string `json:"saved_at,omitempty"`
}

// CacheListReport is the list of cached indexes for a repo.
type CacheListReport struct {
	RepoHash string           `json:"repo_hash"`
	Entries  []CacheListEntry `json:"entries"`
}

// CacheList returns the cached indexes for the repo at cwd, from the local
// pointer file. If rebuild is true, it reconstructs from fcheap instead.
func (svc *Service) CacheList(ctx context.Context, cwd string, rebuild bool) (*CacheListReport, error) {
	root, _, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	repoHash := git.RepoHash(root)
	statePath := cachestate.StatePath(repoHash)
	var cs *cachestate.State
	if rebuild {
		cs, err = cachestate.Rebuild(ctx, repoHash)
		if err != nil {
			return nil, err
		}
		// P1-17 (B56): the rebuilt state is the new source of truth
		// (the local pointer file is the thing we just lost) — write
		// it back so a later `cache list` (no --rebuild) reads the
		// recovered entries instead of a stale (or missing) file.
		if cerr := cs.Save(statePath); cerr != nil {
			return nil, cerr
		}
	} else {
		cs, err = cachestate.Load(statePath)
		if err != nil {
			return nil, err
		}
	}

	rep := &CacheListReport{RepoHash: repoHash, Entries: []CacheListEntry{}}
	for _, e := range cs.Entries {
		rep.Entries = append(rep.Entries, CacheListEntry{
			StashID:          e.StashID,
			TreeHash:         e.TreeHash,
			EmbeddingProfile: e.EmbeddingProfile,
			NodeCount:        e.NodeCount,
			VectorCount:      e.VectorCount,
			SavedAt:          e.SavedAt,
		})
	}
	return rep, nil
}

// CacheDrop removes cached indexes from fcheap and the local pointer file. The id
// argument matches by either tree hash or stash id (so the MCP surface, which
// exposes stash_id, and the CLI surface, which exposes --tree, both work). When
// all is true, the id is ignored and every entry for the repo is dropped. If
// fcheap drop fails, the pointer entry is still removed (the stash may already
// be gone). Returns the number of stashes dropped; dropped==0 with err==nil on
// "no match", which is a normal fallthrough (callers can distinguish by comparing
// the input id to entries in CacheList).
func (svc *Service) CacheDrop(ctx context.Context, cwd, id string, all bool) (dropped int, err error) {
	root, _, err := svc.resolveProject(cwd)
	if err != nil {
		return 0, err
	}
	repoHash := git.RepoHash(root)
	statePath := cachestate.StatePath(repoHash)
	cs, err := cachestate.Load(statePath)
	if err != nil {
		return 0, err
	}

	var toDrop []cachestate.CacheEntry
	switch {
	case all:
		for _, e := range cs.Entries {
			toDrop = append(toDrop, e)
		}
	case id != "":
		// Match either identifier — the MCP surface passes the stash_id (what an
		// agent reads out of codemap_cache_list), the CLI passes the tree hash.
		for _, e := range cs.Entries {
			if e.TreeHash == id || e.StashID == id {
				toDrop = append(toDrop, e)
			}
		}
	}

	for _, e := range toDrop {
		if e.StashID != "" {
			if derr := snapshot.FcheapDrop(ctx, e.StashID, true); derr != nil {
				// Best-effort: the stash may already be gone. Still remove the pointer.
				if !strings.Contains(derr.Error(), "not found") {
					err = errors.Join(err, derr)
				}
			}
		}
		cs.Remove(e.TreeHash)
		dropped++
	}
	if dropped > 0 {
		_ = cs.Save(statePath)
	}
	return dropped, err
}

// CacheFcheapAvailable reports whether the fcheap binary is on PATH (a cache
// operation is a no-op when it isn't).
func (svc *Service) CacheFcheapAvailable() bool {
	return snapshot.FcheapAvailable()
}

// CacheReport is the result of an auto-cache or auto-restore operation.
type CacheReport struct {
	Action   string `json:"action"`             // "saved", "restored", "miss", "skipped"
	StashID  string `json:"stash_id,omitempty"` // set on save/restore
	TreeHash string `json:"tree_hash,omitempty"`
	Note     string `json:"note,omitempty"`
}

// MaybeCacheAfterIndex is called after a successful index. If fcheap is on PATH
// and the project is in a git repo, it saves the index to the cache (best-effort:
// never fails the index). Returns a report describing what happened.
func (svc *Service) MaybeCacheAfterIndex(ctx context.Context, cwd string) *CacheReport {
	if !svc.CacheFcheapAvailable() {
		return &CacheReport{Action: "skipped", Note: "fcheap not on PATH"}
	}
	stashID, treeHash, err := svc.CacheSave(ctx, cwd)
	if err != nil {
		return &CacheReport{Action: "skipped", Note: fmt.Sprintf("cache save failed: %v", err)}
	}
	if stashID == "" {
		return &CacheReport{Action: "skipped", Note: "nothing to cache (unindexed or non-git)"}
	}
	return &CacheReport{Action: "saved", StashID: stashID, TreeHash: treeHash}
}

// MaybeRestoreBeforeReindex checks for a matching cache before a costly reindex.
// If a cache hit is found and restored, returns (true, report). If no match or
// fcheap is unavailable, returns (false, report) so the caller proceeds with the
// normal reindex. Never errors — a cache miss is a normal fallthrough.
func (svc *Service) MaybeRestoreBeforeReindex(ctx context.Context, cwd string) (bool, *CacheReport) {
	if !svc.CacheFcheapAvailable() {
		return false, &CacheReport{Action: "skipped", Note: "fcheap not on PATH"}
	}
	restored, stashID, err := svc.CacheRestore(ctx, cwd)
	if err != nil {
		return false, &CacheReport{Action: "miss", Note: fmt.Sprintf("cache restore error: %v", err)}
	}
	if restored {
		// Compute the tree hash for the report (needed by the CLI display).
		g, gerr := svc.s.Graph()
		if gerr != nil {
			return true, &CacheReport{Action: "restored", StashID: stashID}
		}
		root, name, rerr := svc.resolveProject(cwd)
		if rerr != nil {
			return true, &CacheReport{Action: "restored", StashID: stashID}
		}
		_ = root
		p, perr := g.GetProjectByName(name)
		if perr != nil {
			return true, &CacheReport{Action: "restored", StashID: stashID}
		}
		treeHash, _ := cachestate.TreeHash(g, p.ID)
		return true, &CacheReport{Action: "restored", StashID: stashID, TreeHash: treeHash}
	}
	return false, &CacheReport{Action: "miss", Note: "no matching cache entry"}
}

// RestoredCacheHasPrecise reports whether the currently-loaded index for cwd has
// any go/types-resolved (precise-provenance) call edges. Used by the CLI to
// decide whether a cache-restored index satisfies a --precise reindex request.
func (svc *Service) RestoredCacheHasPrecise(cwd string) bool {
	g, err := svc.s.Graph()
	if err != nil {
		return false
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return false
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return false
	}
	n, err := g.CountEdgesByProvenance(p.ID, graph.ProvPrecise)
	if err != nil {
		return false
	}
	return n > 0
}
