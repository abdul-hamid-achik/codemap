package app

import (
	"context"
	"errors"
	"os"

	"github.com/abdul-hamid-achik/codemap/internal/branchstate"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// BranchSnapshot stashes the project's current index (graph + vectors) into
// fcheap, keyed by the repo + branch + HEAD sha, and records the stash in the
// per-repo pointer file. A non-git dir, a detached HEAD, an empty branch name, or
// an unindexed project are clean no-ops (nothing stable to key a snapshot by).
func (svc *Service) BranchSnapshot(ctx context.Context, root, branch string) error {
	st, err := git.Inspect(ctx, root)
	if err != nil {
		return err
	}
	if !st.IsRepo || st.Detached || branch == "" {
		return nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return err
	}
	_, name, err := svc.resolveProject(root)
	if err != nil {
		return err
	}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return nil // not indexed yet — nothing to snapshot
	}
	if err != nil {
		return err
	}

	// Carry vectors (and their profile) only if the project has any embeddings.
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

	tmp, err := os.MkdirTemp("", "codemap-snap-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	m, err := snapshot.Export(g, vec, p.ID, name, tmp, profile, st.SHA)
	if err != nil {
		return err
	}
	tags := []string{"codemap-index", "repo:" + st.RepoHash, "branch:" + git.SanitizeBranch(branch)}
	stashID, err := snapshot.FcheapSave(ctx, tmp, "codemap", name+"@"+branch, tags, st.SHA)
	if err != nil {
		return err
	}

	statePath := branchstate.StatePath(st.RepoHash)
	bs, err := branchstate.Load(statePath)
	if err != nil {
		return err
	}
	bs.RepoRoot, bs.RepoHash, bs.ProjectName, bs.ActiveBranch = st.RepoRoot, st.RepoHash, name, branch
	bs.Record(branch, branchstate.BranchEntry{
		StashID: stashID, BaseSHA: st.SHA, EmbeddingProfile: profile,
		NodeCount: m.Nodes, VectorCount: m.Vectors,
	})
	return bs.Save(statePath)
}

// BranchSwitch moves the index from branch `from` to branch `to`: it snapshots
// `from`'s index, then restores `to`'s snapshot from fcheap if one exists and is
// still fresh (its base sha is an ancestor of HEAD) and its embedding profile
// matches — otherwise it incrementally (re)indexes the working tree. A non-git dir
// or detached HEAD is a clean no-op (the single flat index is left as-is).
func (svc *Service) BranchSwitch(ctx context.Context, root, from, to string) error {
	st, err := git.Inspect(ctx, root)
	if err != nil {
		return err
	}
	if !st.IsRepo || st.Detached {
		return nil
	}
	// Snapshot the branch we're leaving (best effort — a failed snapshot of `from`
	// must not block loading `to`).
	if from != "" && from != to {
		_ = svc.BranchSnapshot(ctx, root, from)
	}
	if to == "" {
		return nil
	}

	g, err := svc.s.Graph()
	if err != nil {
		return err
	}
	_, name, err := svc.resolveProject(root)
	if err != nil {
		return err
	}
	curProfile := ""
	if emb := svc.s.Embedder(); emb != nil {
		curProfile = emb.Profile().String()
	}

	statePath := branchstate.StatePath(st.RepoHash)
	bs, err := branchstate.Load(statePath)
	if err != nil {
		return err
	}

	restored := false
	entry, ok := bs.Lookup(to)
	if ok && entry.StashID != "" && profileCompatible(entry.EmbeddingProfile, curProfile) {
		fresh := true
		if entry.BaseSHA != "" {
			if anc, aerr := git.IsAncestor(ctx, root, entry.BaseSHA, "HEAD"); aerr == nil {
				fresh = anc // base sha advanced past / unreachable (rebase) → reindex
			}
		}
		if p, perr := g.GetProjectByName(name); fresh && perr == nil {
			restored, err = svc.restoreSnapshot(ctx, g, p.ID, name, entry, curProfile)
			if err != nil {
				return err
			}
		}
	}

	if !restored {
		// Fallback: (re)index the working tree, preserving the current embedding state.
		n, _ := svc.embeddedCount(name)
		if _, err := svc.Index(ctx, root, index.Options{}, n > 0); err != nil {
			return err
		}
	}

	bs.RepoRoot, bs.RepoHash, bs.ProjectName, bs.ActiveBranch = st.RepoRoot, st.RepoHash, name, to
	return bs.Save(statePath)
}

// restoreSnapshot fetches the branch's stash from fcheap into a temp dir and
// imports it into the project. Returns false (without error) if the stash can't be
// restored/verified, so the caller falls back to reindexing.
func (svc *Service) restoreSnapshot(ctx context.Context, g *graph.Store, pid int64, name string, entry branchstate.BranchEntry, curProfile string) (bool, error) {
	tmp, err := os.MkdirTemp("", "codemap-restore-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	verified, rerr := snapshot.FcheapRestore(ctx, entry.StashID, tmp)
	if rerr != nil || !verified {
		return false, nil // dangling/corrupt stash — reindex instead
	}
	var vec *vector.Store
	if entry.VectorCount > 0 {
		if v, verr := svc.s.Vectors(); verr == nil {
			vec = v
		}
	}
	if _, ierr := snapshot.Import(g, vec, pid, name, tmp, curProfile); ierr != nil {
		return false, nil // profile mismatch / bad snapshot — reindex instead
	}
	return true, nil
}

// profileCompatible reports whether a snapshot's embedding profile may be imported
// into the current session. An empty profile on either side (structure-only) is
// treated as compatible.
func profileCompatible(snap, current string) bool {
	return snap == "" || current == "" || snap == current
}
