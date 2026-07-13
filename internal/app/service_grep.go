package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Bounds for Grep, mirroring the MaxSecretKeyNames/MaxSecretKeyNameBytes
// bounding convention (secret_impact.go).
const (
	// MaxGrepPatternBytes rejects an oversized pattern before compiling/scanning
	// anything.
	MaxGrepPatternBytes = 512
	// DefaultGrepTop/MaxGrepTop clamp the result count the same way Semantic
	// clamps topK.
	DefaultGrepTop = 100
	MaxGrepTop     = 1000
	// grepMaxLineRunes bounds MatchedLine so MCP JSON payloads stay bounded, not
	// just terminal columns (cmd/codemap's truncStr is a different package and a
	// narrower cap).
	grepMaxLineRunes = 300
	// maxGrepFileBytes skips a file too large to be worth scanning (e.g. an
	// accidentally-indexed generated/vendored file).
	maxGrepFileBytes = 4 << 20
)

var (
	errGrepEmptyPattern   = errors.New("pattern must not be empty")
	errGrepPatternTooLong = errors.New("pattern exceeds the maximum length")
)

// GrepOptions configures Grep/GrepWithContext.
type GrepOptions struct {
	Regex      bool
	IgnoreCase bool
	Top        int
}

// GrepHit is one pattern match, joined onto its enclosing symbol via the same
// resolution rule as SymbolAt (graph.Store.NodeAtLine): Resolution is "exact"
// when the match's line is the symbol's declaration line, "enclosing" when it
// falls inside the symbol's body, and "none" when the line is outside every
// node's range in that file (e.g. a blank line, a package clause, or a match in
// a file codemap indexes for structure but outside any symbol body). Grep calls
// graph.Store.NodeAtLine directly instead of the public Service.SymbolAt so a
// single already-open project/graph handle serves every hit in the run; the
// observable resolution semantics are identical to SymbolAt.
type GrepHit struct {
	File        string          `json:"file"`
	Line        int             `json:"line"`
	MatchedLine string          `json:"matched_line"`
	Symbol      string          `json:"symbol,omitempty"`
	FQN         string          `json:"fqn,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Resolution  string          `json:"resolution"`
	Selector    *SymbolSelector `json:"selector,omitempty"`
}

// GrepReport is Grep's result. Stale describes file-SET completeness (a file
// added since the last index is not yet in the indexed file set Grep scans),
// never staleness of a matched line's content — every match is read live from
// disk at query time, so it is always current for the file it came from. A file
// deleted from disk since the last index is silently skipped, not reflected in
// Stale.
type GrepReport struct {
	Project    string    `json:"project"`
	Pattern    string    `json:"pattern"`
	Regex      bool      `json:"regex"`
	IgnoreCase bool      `json:"ignore_case"`
	Hits       []GrepHit `json:"hits"`
	Total      int       `json:"total"`
	Truncated  bool      `json:"truncated,omitempty"`
	// FilesScanned and FilesSkipped are mutually exclusive per indexed file
	// (every considered file lands in exactly one of the two, never both):
	// FilesScanned counts a file that was opened and scanned — including a
	// file that hit the scanner's per-line ceiling partway through, since it
	// still contributed whatever hits were found before the error (see
	// scanFileForHits). FilesSkipped counts a file that was never scanned at
	// all (too large, or detected binary).
	FilesScanned int  `json:"files_scanned"`
	FilesSkipped int  `json:"files_skipped,omitempty"`
	Stale        bool `json:"stale,omitempty"`
}

// Grep searches the indexed file set for pattern (literal substring by default,
// RE2 regex with opts.Regex) and resolves each hit to its enclosing symbol. See
// GrepWithContext for the full contract.
func (svc *Service) Grep(cwd, pattern string, opts GrepOptions) (*GrepReport, error) {
	return svc.GrepWithContext(context.Background(), cwd, pattern, opts)
}

// GrepWithContext searches the content of every file in the indexed file set
// (graph.Store.IndexedFiles — the same walk+exclude+extractor-support decision
// the indexer already made, so this deliberately does NOT re-walk the
// filesystem or duplicate exclude-glob logic) for pattern, and resolves each
// hit's enclosing symbol. Reads happen live from disk, not from indexed
// content: a file edited since the last index is searched at its current
// on-disk state.
//
// Scope is deliberately narrower than ripgrep: no context lines, no glob/
// language filters — this is exact text content search over the indexed
// source set, complementing codemap_find (name search) and codemap_semantic
// (meaning search).
func (svc *Service) GrepWithContext(ctx context.Context, cwd, pattern string, opts GrepOptions) (*GrepReport, error) {
	if pattern == "" {
		return nil, coded(CodeOperational, "supply a non-empty pattern", errGrepEmptyPattern)
	}
	if len(pattern) > MaxGrepPatternBytes {
		return nil, coded(CodeOperational, "shorten the pattern", errGrepPatternTooLong)
	}

	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &GrepReport{Project: name, Pattern: pattern, Regex: opts.Regex, IgnoreCase: opts.IgnoreCase, Hits: []GrepHit{}}
	if !found {
		return rep, nil
	}

	var re *regexp.Regexp
	literal := pattern
	if opts.Regex {
		p := pattern
		if opts.IgnoreCase {
			p = "(?i)" + p
		}
		re, err = regexp.Compile(p)
		if err != nil {
			return nil, coded(CodeOperational, "fix the regex syntax", err)
		}
	} else if opts.IgnoreCase {
		literal = strings.ToLower(pattern)
	}

	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}

	if st, _ := svc.Staleness(cwd); st != nil && st.Any() {
		rep.Stale = true
	}

	files, err := g.IndexedFiles(pid)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	top := opts.Top
	if top <= 0 {
		top = DefaultGrepTop
	}
	if top > MaxGrepTop {
		top = MaxGrepTop
	}

	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		abs := filepath.Join(p.Path, rel)
		st, err := os.Stat(abs)
		if err != nil {
			continue // deleted since index — silently skip, disclosed via Stale
		}
		if st.Size() > maxGrepFileBytes {
			rep.FilesSkipped++
			continue
		}

		head, err := os.Open(abs)
		if err != nil {
			continue
		}
		buf := make([]byte, 512)
		n, _ := head.Read(buf)
		_ = head.Close()
		if looksBinary(buf[:n]) {
			rep.FilesSkipped++
			continue
		}

		f, err := os.Open(abs)
		if err != nil {
			continue
		}
		rep.FilesScanned++
		scanFileForHits(f, rel, pid, g, re, literal, opts.IgnoreCase, top, rep)
		_ = f.Close()
	}

	return rep, nil
}

// scanFileForHits scans one already-opened file line by line, appending
// matches to rep.Hits (capped at top) while always incrementing rep.Total so
// Truncated reflects the true match count, not just what fit under the cap.
// bufio.Scanner's default 64 KiB token limit is raised to a 1 MiB per-line
// ceiling — a minified/generated single-line file can otherwise exceed it well
// before the whole-file size cap kicks in.
func scanFileForHits(f *os.File, rel string, pid int64, g *graph.Store, re *regexp.Regexp, literal string, ignoreCase bool, top int, rep *GrepReport) {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		var matched bool
		if re != nil {
			matched = re.MatchString(line)
		} else {
			cmp := line
			if ignoreCase {
				cmp = strings.ToLower(cmp)
			}
			matched = strings.Contains(cmp, literal)
		}
		if !matched {
			continue
		}
		rep.Total++
		if len(rep.Hits) >= top {
			rep.Truncated = true
			continue
		}
		hit := GrepHit{File: rel, Line: lineNo, MatchedLine: truncateLine(line, grepMaxLineRunes), Resolution: "none"}
		if n, ok, _ := g.NodeAtLine(pid, rel, lineNo); ok {
			hit.Symbol, hit.FQN, hit.Kind = n.Symbol, n.FQN, n.Kind
			hit.Selector = selectorForNode(n)
			if n.StartLine == lineNo {
				hit.Resolution = "exact"
			} else {
				hit.Resolution = "enclosing"
			}
		}
		rep.Hits = append(rep.Hits, hit)
	}
	// A scanner.Err() here (bufio.ErrTooLong, or any other scan failure)
	// stops scanning THIS file only; hits already found in it stay in
	// rep.Hits — one pathological file must never abort the whole grep run.
	// The caller already counted this file in FilesScanned before calling
	// in — it was genuinely opened and (partially) scanned, so a scan error
	// must NOT also increment FilesSkipped (that would double-count it and
	// break the "skipped means never scanned" contract the size/binary skip
	// paths rely on). No further accounting is needed on this path.
}

// looksBinary reports whether head (a file's leading bytes) contains a NUL
// byte — the standard git/ripgrep binary-file heuristic.
func looksBinary(head []byte) bool {
	return bytes.IndexByte(head, 0) >= 0
}

// truncateLine rune-truncates s to at most maxRunes runes, appending "…" when
// cut. Rune-safe so a truncation never splits a multi-byte character.
func truncateLine(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
