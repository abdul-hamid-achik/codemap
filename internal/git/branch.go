// Package git provides minimal, read-only git inspection via the system `git`
// binary (no CGO, no git library) — current branch, HEAD sha, repo root, and the
// stable identifiers used to key per-branch code-intelligence index snapshots.
// Everything here is read-only; it never mutates a repository.
package git

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// run executes `git -C dir <args...>` and returns trimmed stdout. The context
// bounds a hung git (e.g. an index.lock contention) so it can't freeze a caller.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoRoot returns the absolute toplevel of the work tree containing dir, or an
// error if dir isn't inside a git repository.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "rev-parse", "--show-toplevel")
}

// CurrentBranch returns the checked-out branch name, or "" when HEAD is detached.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	b, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if b == "HEAD" { // detached HEAD reports the literal "HEAD"
		return "", nil
	}
	return b, nil
}

// HeadSHA returns the full commit sha at HEAD (empty on an unborn branch).
func HeadSHA(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "rev-parse", "HEAD")
}

// IsAncestor reports whether ancestorSHA is an ancestor of (or equal to) ref — so
// ref's history reaches ancestorSHA. Used to tell whether a branch snapshot taken
// at ancestorSHA is still fresh for the current HEAD (a rebase/amend makes the old
// sha unreachable → not an ancestor → reindex). A definitive non-ancestor is a
// false result, not an error; only an unexpected git failure errors.
func IsAncestor(ctx context.Context, dir, ancestorSHA, ref string) (bool, error) {
	err := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", ancestorSHA, ref).Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// IsDetached reports whether HEAD points directly at a commit rather than a branch.
func IsDetached(ctx context.Context, dir string) (bool, error) {
	b, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false, err
	}
	return b == "HEAD", nil
}

// Status is a read-only snapshot of a directory's git state, used to key and
// describe per-branch index snapshots.
type Status struct {
	IsRepo   bool   `json:"is_repo"`
	RepoRoot string `json:"repo_root,omitempty"`
	RepoHash string `json:"repo_hash,omitempty"` // stable id of the repo (sha1[:12] of the resolved root)
	Branch   string `json:"branch,omitempty"`    // "" when detached
	SHA      string `json:"sha,omitempty"`       // HEAD commit (empty on an unborn branch)
	Detached bool   `json:"detached"`
	Key      string `json:"key,omitempty"` // SanitizeBranch(Branch) — the per-branch index key (empty when detached)
}

// Inspect gathers a directory's git Status with a few read-only git calls. A
// non-repo (or git absent) returns {IsRepo:false} with no error, so callers can
// degrade to the single flat index.
func Inspect(ctx context.Context, dir string) (Status, error) {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return Status{IsRepo: false}, nil // not a git repo (or git missing) — not an error here
	}
	st := Status{IsRepo: true, RepoRoot: root, RepoHash: RepoHash(root)}
	branch, err := CurrentBranch(ctx, dir)
	if err != nil {
		return st, err
	}
	st.Branch = branch
	st.Detached = branch == ""
	if !st.Detached {
		st.Key = SanitizeBranch(branch)
	}
	st.SHA, _ = HeadSHA(ctx, dir) // best-effort: empty on an unborn branch is fine
	return st, nil
}

// branchUnsafe matches characters that can't appear in a filesystem path segment.
var branchUnsafe = regexp.MustCompile(`[/\\:\s]+`)

// SanitizeBranch turns a branch name into a stable, collision-free path segment:
// unsafe characters (/, \, :, whitespace) collapse to "-", leading dots are
// stripped, the readable prefix is capped, and a short hash of the RAW name is
// always appended so distinct branches that sanitize to the same prefix (e.g.
// "feature/x" and "feature-x") never collide.
func SanitizeBranch(name string) string {
	s := branchUnsafe.ReplaceAllString(name, "-")
	s = strings.TrimLeft(s, ".")
	const maxLen = 60
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = "branch"
	}
	h := sha1.Sum([]byte(name))
	return s + "-" + hex.EncodeToString(h[:])[:8]
}

// RepoHash is a stable identifier for a repository: the first 12 hex of the sha1
// of its absolute, symlink-resolved root. Used to scope index snapshots to a repo
// regardless of where it's checked out or how it's reached (symlinks, worktrees).
func RepoHash(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	h := sha1.Sum([]byte(abs))
	return hex.EncodeToString(h[:])[:12]
}
