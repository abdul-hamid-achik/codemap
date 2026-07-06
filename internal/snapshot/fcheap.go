package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// FcheapBinary is the fcheap executable name/path (overridable for config/tests).
var FcheapBinary = "fcheap"

// FcheapStashDir scopes every fcheap call to a specific stash directory via
// --stash-dir when set (used for test isolation and later for config). Empty
// uses fcheap's default store.
var FcheapStashDir = ""

// FcheapAvailable reports whether the fcheap binary is on PATH, so cache
// operations can no-op cleanly when it isn't installed.
func FcheapAvailable() bool {
	_, err := exec.LookPath(FcheapBinary)
	return err == nil
}

// StashInfo is one stash as reported by `fcheap list --json`. Custom carries the
// stash's `manifest.Custom` map (fcheap v0.27.0+): `source` (the --source base-sha),
// and any `branch`/`embedding_profile` the saver wrote, so a pointer-file rebuild
// can read provenance straight from `list --json` with no per-stash `info` call.
type StashInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Tool      string            `json:"tool"`
	Tags      []string          `json:"tags"`
	CreatedAt string            `json:"created_at"`
	Custom    map[string]string `json:"custom,omitempty"`
}

// fcheapRun executes fcheap with the given args (+ the optional --stash-dir) and
// returns stdout. A missing binary or a non-zero exit (with stderr) is a clear error.
func fcheapRun(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(FcheapBinary)
	if err != nil {
		return nil, fmt.Errorf("fcheap not found on PATH (needed to stash/restore branch index snapshots): %w", err)
	}
	if FcheapStashDir != "" {
		args = append(args, "--stash-dir", FcheapStashDir)
	}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("fcheap %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("fcheap %s: %w", args[0], err)
	}
	return out, nil
}

// FcheapSave stashes the snapshot directory dir into fcheap's content-addressed
// vault and returns the new stash id. sourceSHA is the git commit the snapshot was
// taken at (provenance). The secret scan is skipped — the directory is derived
// index data, not user files.
//
// Each tag is emitted as its own `--tag` flag (fcheap v0.27.0 made `--tag` a
// repeatable StringSliceVar that splits on commas, so a comma-joined value like
// `codemap-index,repo:abc,branchname:feature,x` would be shattered into spurious
// tags — a branch name containing a comma silently corrupted the filter, B57).
// Tag values are percent-escaped for `,` and `%` first, so a raw branch name such
// as `feature,x` round-trips intact through `branchname:feature%2Cx`.
func FcheapSave(ctx context.Context, dir, tool, name string, tags []string, sourceSHA string) (string, error) {
	args := []string{"save", dir, "--tool", tool, "--name", name, "--no-scan", "--json"}
	for _, t := range tags {
		args = append(args, "--tag", escapeTag(t))
	}
	if sourceSHA != "" {
		args = append(args, "--source", sourceSHA)
	}
	out, err := fcheapRun(ctx, args...)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", fmt.Errorf("parse fcheap save output: %w", err)
	}
	if r.ID == "" {
		return "", fmt.Errorf("fcheap save returned no stash id")
	}
	return r.ID, nil
}

// FcheapRestore restores stashID into toDir and returns whether fcheap verified
// the content hashes. A non-restored status is an error.
func FcheapRestore(ctx context.Context, stashID, toDir string) (verified bool, err error) {
	out, err := fcheapRun(ctx, "restore", stashID, "--to", toDir, "--json")
	if err != nil {
		return false, err
	}
	var r struct {
		Status   string `json:"status"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return false, fmt.Errorf("parse fcheap restore output: %w", err)
	}
	if r.Status != "restored" {
		return r.Verified, fmt.Errorf("fcheap restore status %q (not 'restored')", r.Status)
	}
	return r.Verified, nil
}

// FcheapList returns stashes matching ALL of tags. fcheap v0.27.0's `--tag` is a
// repeatable StringSliceVar with AND semantics, so every tag is passed as its own
// `--tag` flag and the server does the AND filter (the prior client-side
// `hasAllTags` fallback is no longer needed). Empty tags lists all. Tag values are
// percent-escaped before sending and unescaped on the returned stashes so callers
// see the raw values (e.g. `branchname:feature,x`) they handed to FcheapSave.
func FcheapList(ctx context.Context, tags []string) ([]StashInfo, error) {
	args := []string{"list", "--json"}
	for _, t := range tags {
		args = append(args, "--tag", escapeTag(t))
	}
	out, err := fcheapRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	var all []StashInfo
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse fcheap list output: %w", err)
	}
	for i := range all {
		for j, t := range all[i].Tags {
			all[i].Tags[j] = unescapeTag(t)
		}
	}
	return all, nil
}

// escapeTag percent-encodes `,` and `%` so fcheap's comma-splitting StringSliceVar
// cannot shatter a single tag value. Branch names (and any other tag value) may
// contain commas; without escaping, `--tag "branchname:feature,x"` is split into
// `branchname:feature` and a spurious `x` tag (B57). The encoding is the minimal
// subset of URL-query escaping needed: `%` -> `%25`, then `,` -> `%2C`. It is a
// no-op for the hex/safe tags codemap already uses (`codemap-cache`, `repo:<hex>`,
// `tree:<hex>`, `branch:<sanitized>`), so existing non-comma stashes are unchanged.
func escapeTag(v string) string {
	v = strings.ReplaceAll(v, "%", "%25")
	return strings.ReplaceAll(v, ",", "%2C")
}

// unescapeTag reverses escapeTag. `%2C` -> `,` is applied before `%25` -> `%` so
// the two encodings (disjoint as substrings) cannot recombine; the round trip is
// exact for any input including literal `%2C`/`%25` sequences.
func unescapeTag(v string) string {
	v = strings.ReplaceAll(v, "%2C", ",")
	return strings.ReplaceAll(v, "%25", "%")
}

// FcheapDrop permanently deletes a stash from fcheap's vault. Requires force=true
// (the fcheap CLI's --force flag) to prevent accidental loss. Returns nil if the
// stash doesn't exist (idempotent).
func FcheapDrop(ctx context.Context, stashID string, force bool) error {
	args := []string{"drop", stashID}
	if force {
		args = append(args, "--force")
	}
	_, err := fcheapRun(ctx, args...)
	return err
}
