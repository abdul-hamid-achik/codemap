package git

import (
	"context"
	"errors"
	"fmt"
)

// ValidRef reports whether a user-supplied ref/query is safe to pass as a
// positional argument to an external command. Two rules — both necessary:
//
//  1. Non-empty (catches accidental empty-string passes that some shells fold
//     away silently).
//  2. Does not begin with "-" (option-injection guard). A leading "-" is parsed
//     as an option by argv-parsing commands even when a "--" separator follows
//     later, because the separator only stops pathspec parsing — option
//     parsing happens earlier. The classic exploit in `git diff` is
//     `--output=/path`, which writes the diff to an arbitrary file before
//     `--` is seen.
//
// This is the cheap guard every exec site calls before reaching the argv
// boundary. Defense in depth: git.ChangedFiles and git.BranchSHA also insert
// `--end-of-options` before the ref so even a fat-fingered ref past the guard
// can never reach git's option parser.
func ValidRef(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		return false
	}
	return true
}

// ErrInvalidRef is returned by ResolveRef / ChangedFiles when a ref fails
// ValidRef. Callers can match on this to decide between "user error" (graceful
// note + degraded answer) and a real git failure (hard error).
var ErrInvalidRef = errors.New("invalid git ref: must be non-empty and not start with -")

// EndOfOptions is the git >= 2.24 boundary marker that, when inserted before a
// positional revision, guarantees the parser never reinterprets the value as
// an option even if the upstream ValidRef guard is bypassed. Insert before
// EVERY user-supplied revision / ref positional, never as the last argument
// (it must precede the value it terminates, not follow it).
const EndOfOptions = "--end-of-options"

// ResolveRef verifies a ref is well-formed AND resolves to a commit, returning
// the canonical sha. Used by callers that want to give the agent a graceful
// "since <ref> did not resolve" note on a typo instead of a hard error.
//
// Uses `git rev-parse --verify --quiet --end-of-options <ref>^{commit}` so
// `--end-of-options` is enforced server-side even if ValidRef is bypassed, and
// the `^{commit}` suffix peels annotated tags to commits (avoids accidentally
// resolving a tag object as if it were a usable revision).
func ResolveRef(ctx context.Context, dir, ref string) (string, error) {
	if !ValidRef(ref) {
		return "", ErrInvalidRef
	}
	out, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", EndOfOptions, ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	return out, nil
}
