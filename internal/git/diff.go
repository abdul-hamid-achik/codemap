package git

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// LineRange is an inclusive [Start,End] span of 1-based line numbers in the
// post-image (the new file). A pure deletion contributes no LineRange (there are
// no new lines), though its file is still reported as changed.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Overlaps reports whether this range intersects [start,end] (inclusive). Used to
// decide whether a changed hunk touches a symbol's [StartLine,EndLine] span.
func (r LineRange) Overlaps(start, end int) bool {
	return r.Start <= end && start <= r.End
}

// ChangedFile is one file in a diff with the changed line ranges in its new image.
// Status is the single-letter git status: "A" added, "M" modified, "D" deleted,
// "?" untracked. Hunks is empty for deletes and untracked files (no diffable
// post-image), but the file is still reported so callers can surface it.
type ChangedFile struct {
	Path   string      `json:"path"`
	Status string      `json:"status"`
	Hunks  []LineRange `json:"hunks,omitempty"`
}

// hunkHeader matches a unified-diff hunk header, capturing the new-file start line
// and (optional) line count: `@@ -a,b +c,d @@`. With `git diff -U0` the counts are
// exact (no surrounding context), so the captured range is precisely the changed
// lines in the new file.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ChangedFiles returns the files (and changed line ranges) for one of three diff
// modes, all read-only:
//
//   - "working": everything changed since HEAD — staged AND unstaged tracked
//     edits, plus untracked files. The agent's "what have I done since my last
//     commit" view.
//   - "staged":  only the index (`git diff --cached`).
//   - "since":   everything between <ref> and the working tree (`git diff <ref>`),
//     i.e. committed + uncommitted changes since <ref> — the "what did I do on this
//     branch" view. `since` must be non-empty in this mode.
//
// It shells the system `git` (no CGO, no git library), bounded by ctx. Line ranges
// are 1-based and refer to the new file.
func ChangedFiles(ctx context.Context, dir, mode, since string) ([]ChangedFile, error) {
	var diffArgs []string
	switch mode {
	case "staged":
		diffArgs = []string{"diff", "-U0", "--no-color", "--cached"}
	case "since":
		// `since` is an agent-supplied ref — option-injection guard:
		// reject empty + leading-dash refs (git parses them as options even past
		// `--`), and insert EndOfOptions before the ref so the guard is enforced
		// server-side even if a future caller forgets the cheap check.
		// Trailing `--` separates the revision from pathspecs so a value can't be
		// reinterpreted as a path.
		if !ValidRef(since) {
			return nil, ErrInvalidRef
		}
		diffArgs = []string{"diff", "-U0", "--no-color", EndOfOptions, since, "--"}
	default: // "working"
		// Diff against HEAD so both staged and unstaged tracked edits show. On an
		// unborn branch (no HEAD) there's nothing to diff against, so merge the index
		// diff (staged) with the worktree diff (unstaged) instead.
		if _, err := HeadSHA(ctx, dir); err == nil {
			diffArgs = []string{"diff", "-U0", "--no-color", "HEAD"}
		} else {
			staged, _ := run(ctx, dir, "diff", "-U0", "--no-color", "--cached")
			unstaged, uerr := run(ctx, dir, "diff", "-U0", "--no-color")
			if uerr != nil {
				return nil, uerr
			}
			out := staged + "\n" + unstaged
			files := mergeChangedFiles(parseUnifiedDiff(out))
			return appendUntracked(ctx, dir, files), nil
		}
	}

	out, err := run(ctx, dir, diffArgs...)
	if err != nil {
		return nil, err
	}
	files := mergeChangedFiles(parseUnifiedDiff(out))

	if mode == "working" || mode == "since" {
		files = appendUntracked(ctx, dir, files)
	}
	return files, nil
}

// appendUntracked adds untracked files (which git diff never shows) to the set.
// They carry no hunks (the whole file is new); callers that resolve symbols by line
// range simply find none, which is correct for a not-yet-indexed file.
func appendUntracked(ctx context.Context, dir string, files []ChangedFile) []ChangedFile {
	untracked, err := run(ctx, dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return files
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.Path] = true
	}
	for _, p := range strings.Split(untracked, "\n") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		files = append(files, ChangedFile{Path: p, Status: "?"})
	}
	return files
}

// mergeChangedFiles collapses multiple ChangedFile entries for the same path (e.g.
// a file that is both staged and unstaged on an unborn branch) into one, unioning
// their hunks and keeping the stronger A/D status over a plain M.
func mergeChangedFiles(fs []ChangedFile) []ChangedFile {
	byPath := map[string]int{} // path → index in out
	out := make([]ChangedFile, 0, len(fs))
	for _, f := range fs {
		if i, ok := byPath[f.Path]; ok {
			out[i].Hunks = append(out[i].Hunks, f.Hunks...)
			if out[i].Status == "M" && f.Status != "M" {
				out[i].Status = f.Status
			}
			continue
		}
		byPath[f.Path] = len(out)
		out = append(out, f)
	}
	return out
}

// parseUnifiedDiff turns `git diff -U0` text into per-file changed line ranges. It
// reads the `--- a/<path>` / `+++ b/<path>` headers for the path and status
// (/dev/null on either side marks an add or delete) and each `@@ … +c,d @@` header
// for a new-file range. The `---`/`+++` cases are honored ONLY in a file's header
// section (before its first `@@`): with `git diff -U0` an added line whose content
// begins with `++ ` is emitted as a body line `+++ <content>`, which must not be
// mistaken for a header (it would corrupt the path).
func parseUnifiedDiff(out string) []ChangedFile {
	var files []ChangedFile
	var cur *ChangedFile
	inHunks := false
	flush := func() {
		if cur != nil && cur.Path != "" {
			files = append(files, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			inHunks = false
			// Seed the path from the header; the ---/+++ lines refine it below.
			cur = &ChangedFile{Status: "M", Path: diffGitPath(line)}
		case cur == nil:
			// preamble before the first file header — ignore
		case !inHunks && strings.HasPrefix(line, "--- "):
			// Old-side path: seed cur.Path as a fallback (a delete's +++ is /dev/null,
			// so this is the only place its path appears), and mark adds.
			if strings.HasPrefix(line, "--- /dev/null") {
				cur.Status = "A"
			} else if p := stripDiffPath(strings.TrimPrefix(line, "--- ")); p != "" {
				cur.Path = p
			}
		case !inHunks && strings.HasPrefix(line, "+++ "):
			if strings.HasPrefix(line, "+++ /dev/null") {
				cur.Status = "D" // keep the old-side path captured above
			} else if p := stripDiffPath(strings.TrimPrefix(line, "+++ ")); p != "" {
				cur.Path = p // new-side path is authoritative for adds/modifies
			}
		case strings.HasPrefix(line, "@@"):
			inHunks = true
			if m := hunkHeader.FindStringSubmatch(line); m != nil {
				start, _ := strconv.Atoi(m[1])
				count := 1
				if m[2] != "" {
					count, _ = strconv.Atoi(m[2])
				}
				if count <= 0 || start <= 0 {
					continue // pure deletion (0 new lines) — file already recorded
				}
				cur.Hunks = append(cur.Hunks, LineRange{Start: start, End: start + count - 1})
			}
		}
	}
	flush()
	return files
}

// diffGitPath extracts the new-side path from a `diff --git a/<x> b/<y>` header,
// taking the `b/<y>` side (correct for adds, modifies, and deletes). Splitting on
// the last " b/" handles ordinary paths; pathological paths containing " b/" are
// rare and get refined by the later `+++ b/<path>` line anyway.
func diffGitPath(line string) string {
	s := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(s, " b/"); i >= 0 {
		return stripDiffPath(s[i+1:]) // s[i+1:] == "b/<y>"
	}
	return ""
}

// stripDiffPath cleans a path from a `--- a/<path>` or `+++ b/<path>` header: drop
// any trailing tab-decorated metadata, the C-quoting git adds for paths with
// special characters, and the leading `a/`/`b/` prefix. The unquote MUST precede
// the prefix strip — git puts the prefix INSIDE the quotes (`"b/naïve.txt"`).
func stripDiffPath(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unq, err := strconv.Unquote(s); err == nil {
			s = unq
		}
	}
	// Remove exactly one a//b/ prefix (CutPrefix, not two TrimPrefixes, so a real
	// path like "a/b/x" under the prefix keeps its own leading segment).
	if rest, ok := strings.CutPrefix(s, "a/"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(s, "b/"); ok {
		return rest
	}
	return s
}
