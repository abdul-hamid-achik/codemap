// Package daemon — delegation guard.
//
// Per P0-08: codemap's daemon control socket is global per data dir (one socket
// for all projects), not per-project. Without a project-identity guard, any CLI
// invocation of `codemap index` that finds a running daemon delegates to it
// regardless of which project the daemon serves — yielding a successful
// reindex of the WRONG project and a silent no-op of the user's.
//
// DelegationAllowed is the single source of truth used by both the CLI and the
// MCP `codemap_index` handler. It returns (true, "") when cwd is inside the
// daemon's watched project root (resolved with EvalSymlinks to handle macOS
// /var ↔ /private, GOPATH/worktrees, etc.), and (false, reason) when not. The
// reason is the actionable hint the agent sees.
package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DelegationAllowed reports whether a CLI / MCP `index` request rooted at cwd
// may safely be delegated to a daemon serving the project at info.ProjectRoot.
//
// Returns (true, "") when cwd resolves inside the daemon's project root (symlinks
// resolved on both sides), (false, hint) otherwise — hint is an actionable
// message the caller surfaces to the user/agent.
func DelegationAllowed(cwd string, info *Info) (bool, string) {
	if info == nil {
		return false, ""
	}
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		resolvedCwd = cwd
	}
	resolvedRoot, err := filepath.EvalSymlinks(info.ProjectRoot)
	if err != nil {
		resolvedRoot = info.ProjectRoot
	}
	// filepath.Rel returns "..", "../<sibling>", or "../<x>" when cwd is
	// outside the root. Empty or "." means exact match; any clean path
	// without a leading ".." means cwd is inside root. Anything else is a
	// mismatch and we refuse delegation.
	rel, err := filepath.Rel(resolvedRoot, resolvedCwd)
	if err != nil {
		return false, fmt.Sprintf("a codemap daemon is indexing %s (%s); cannot resolve its project root against your cwd", info.ProjectName, info.ProjectRoot)
	}
	if rel == "" || rel == "." {
		return true, ""
	}
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	if first == ".." {
		return false, fmt.Sprintf(
			"a codemap daemon is indexing %s (%s); stop it with 'codemap daemon stop' or 'cd %s' to delegate, or run against the daemon's project root",
			info.ProjectName, info.ProjectRoot, info.ProjectRoot,
		)
	}
	return true, ""
}
