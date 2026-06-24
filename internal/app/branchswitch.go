package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// Key the snapshot on the BRANCH's tip sha (not HEAD): the hook snapshots the
	// branch it just left while HEAD is already on the new one.
	sha := st.SHA
	if bsha, berr := git.BranchSHA(ctx, root, branch); berr == nil && bsha != "" {
		sha = bsha
	}

	tmp, err := os.MkdirTemp("", "codemap-snap-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	m, err := snapshot.Export(g, vec, p.ID, name, tmp, profile, sha)
	if err != nil {
		return err
	}
	tags := []string{"codemap-index", "repo:" + st.RepoHash, "branch:" + git.SanitizeBranch(branch)}
	stashID, err := snapshot.FcheapSave(ctx, tmp, "codemap", name+"@"+branch, tags, sha)
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
		StashID: stashID, BaseSHA: sha, EmbeddingProfile: profile,
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
	statePath := branchstate.StatePath(st.RepoHash)
	// The post-checkout hook only knows the target branch, so default `from` to the
	// last-active branch recorded in the pointer file.
	if from == "" {
		if bs0, lerr := branchstate.Load(statePath); lerr == nil {
			from = bs0.ActiveBranch
		}
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

const hookMarker = "# codemap-branch-index (auto-switch the code index on checkout)"

// InstallPostCheckoutHook idempotently installs a git post-checkout hook that runs
// `codemap branch-switch` on a branch checkout, so the code index follows the
// working tree automatically. It resolves the repo's hooks dir (worktree/
// core.hooksPath-aware) and appends a guarded block to any existing hook, creating
// a shebang'd hook if none exists. codemapBin is the executable to invoke (default
// "codemap"). Returns the hook path.
func InstallPostCheckoutHook(ctx context.Context, root, codemapBin string) (string, error) {
	st, err := git.Inspect(ctx, root)
	if err != nil {
		return "", err
	}
	if !st.IsRepo {
		return "", fmt.Errorf("%s is not a git repository", root)
	}
	if codemapBin == "" {
		codemapBin = "codemap"
	}
	hooksDir, err := git.HooksDir(ctx, root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(hooksDir, "post-checkout")
	block := hookBlock(codemapBin, st.RepoRoot)

	existing, rerr := os.ReadFile(path)
	switch {
	case errors.Is(rerr, os.ErrNotExist):
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+block), 0o755); err != nil {
			return "", err
		}
	case rerr != nil:
		return "", rerr
	case strings.Contains(string(existing), hookMarker):
		return path, nil // already installed — idempotent
	default:
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		if _, werr := f.WriteString("\n" + block); werr != nil {
			f.Close()
			return "", werr
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		_ = os.Chmod(path, 0o755)
	}
	return path, nil
}

// hookBlock is the guarded shell snippet appended to post-checkout. post-checkout
// receives $1=prev-HEAD $2=new-HEAD $3=flag (1 for a branch checkout, 0 for a file
// checkout), so it only fires on branch switches and never blocks the checkout.
func hookBlock(codemapBin, repoRoot string) string {
	return hookMarker + "\n" +
		`if [ "$3" = "1" ]; then` + "\n" +
		"  " + codemapBin + ` branch-switch --to "$(git rev-parse --abbrev-ref HEAD)" --root ` + shellQuote(repoRoot) + " >/dev/null 2>&1 || true\n" +
		"fi\n"
}

// shellQuote single-quotes s for safe embedding in the POSIX-sh hook.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
