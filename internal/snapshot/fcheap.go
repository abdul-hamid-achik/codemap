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

// StashInfo is one stash as reported by `fcheap list --json`.
type StashInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Tool      string   `json:"tool"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
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
func FcheapSave(ctx context.Context, dir, tool, name string, tags []string, sourceSHA string) (string, error) {
	args := []string{"save", dir, "--tool", tool, "--name", name, "--no-scan", "--json"}
	if len(tags) > 0 {
		args = append(args, "--tag", strings.Join(tags, ","))
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

// FcheapList returns stashes matching ALL of tags. fcheap filters by a single tag
// server-side; any extra tags are AND-matched client-side. Empty tags lists all.
func FcheapList(ctx context.Context, tags []string) ([]StashInfo, error) {
	args := []string{"list", "--json"}
	if len(tags) > 0 {
		args = append(args, "--tag", tags[0])
	}
	out, err := fcheapRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	var all []StashInfo
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse fcheap list output: %w", err)
	}
	if len(tags) <= 1 {
		return all, nil
	}
	var matched []StashInfo
	for _, s := range all {
		if hasAllTags(s.Tags, tags[1:]) {
			matched = append(matched, s)
		}
	}
	return matched, nil
}

func hasAllTags(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, t := range have {
		set[t] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
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
