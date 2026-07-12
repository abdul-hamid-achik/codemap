package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Default/max per-file and per-directory row caps for `codemap coverage` —
// picked to match the order of magnitude of codemap_context_batch's 25-symbol
// cap and codemap_dependencies' sample caps, scaled up because this list is
// single-field-per-row and cheap to serialize.
const (
	DefaultCoverageTop = 200
	MaxCoverageTop     = 2000
)

// CoverageFile is one indexed file's precise call-graph coverage plus whether
// its on-disk content has drifted since the last index.
type CoverageFile struct {
	File       string `json:"file"`
	Language   string `json:"language"`
	Covered    bool   `json:"covered"`
	Resolver   string `json:"resolver,omitempty"`
	ResolvedAt string `json:"resolved_at,omitempty"`
	Stale      bool   `json:"stale"`
}

// CoverageLangRollup is one language's aggregate coverage counts.
type CoverageLangRollup struct {
	Files   int `json:"files"`
	Covered int `json:"covered"`
	Stale   int `json:"stale"`
}

// CoverageDirRollup is one immediate parent directory's aggregate coverage
// counts (filepath.Dir(file), "." for project-root files).
type CoverageDirRollup struct {
	Dir     string `json:"dir"`
	Files   int    `json:"files"`
	Covered int    `json:"covered"`
	Stale   int    `json:"stale"`
}

// CoverageReport is the `codemap coverage` / `codemap_coverage` report:
// rollups by language and by directory (worst-covered first) are always
// computed over the FULL indexed file set — filters only narrow the optional
// per-file list — plus that bounded per-file list itself when requested.
//
// This is a genuinely new, lower-level, more granular honesty signal, not a
// replacement for the per-query `call_graph` enum (see callGraphEnum in
// service_core.go). That enum classifies the WORST file among the definitions
// one query actually touched; coverage answers the standing question "which
// files/packages are covered RIGHT NOW, project-wide, before I even ask a
// symbol question" — so a caller can calibrate trust per package instead of
// being penalized by an unrelated file elsewhere in the project's call_graph.
type CoverageReport struct {
	Project        string                        `json:"project"`
	TotalFiles     int                           `json:"total_files"`
	CoveredFiles   int                           `json:"covered_files"`
	StaleFiles     int                           `json:"stale_files"`
	ByLanguage     map[string]CoverageLangRollup `json:"by_language"`
	ByDirectory    []CoverageDirRollup           `json:"by_directory"`
	ByDirTruncated bool                          `json:"by_directory_truncated,omitempty"`
	Files          []CoverageFile                `json:"files,omitempty"`
	FilesTotal     int                           `json:"files_total,omitempty"`
	FilesTruncated bool                          `json:"files_truncated,omitempty"`
	Note           string                        `json:"note,omitempty"`
}

// CoverageOptions narrows only the optional per-file list. Rollups and totals
// always run over the full file set (see CoverageReport's doc comment and the
// "rollups must run over the unfiltered set" gotcha in the feature plan).
type CoverageOptions struct {
	PathPrefix    string // project-relative file path prefix filter
	Language      string
	OnlyUncovered bool
	Detail        bool // force the per-file list even with no filter set
	Top           int  // caps by_directory rows and (when included) files rows; default DefaultCoverageTop, max MaxCoverageTop
}

// coverageSha256hex duplicates internal/index's unexported sha256hex
// byte-for-byte rather than exporting it across an index->app coupling for
// three lines. Must stay identical to index.Indexer.Staleness' own hashing so
// `stale` here means exactly what `codemap status`'s staleness means, just
// scoped to one file instead of the whole project.
func coverageSha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func coverageRatio(d CoverageDirRollup) float64 {
	if d.Files == 0 {
		return 0
	}
	return float64(d.Covered) / float64(d.Files)
}

// Coverage reports per-file precise call-graph coverage: rollups by language
// and by directory (worst-covered first, so an agent sees which packages to
// distrust most), plus an optional bounded per-file list. `stale` is computed
// at query time by re-hashing each file on disk and comparing against
// index_state's stored hash — the same technique (and the same query-time
// cost) as index.Indexer.Staleness, but scoped per file rather than
// aggregated project-wide: a file can be `covered` (its call_graph_coverage
// row still exists — nothing has re-extracted it yet) while also `stale` (its
// on-disk content has drifted since that row was written). Do not infer
// staleness from an old resolved_at — only the hash comparison is
// authoritative; call_graph_coverage rows are binary (present or cleared by
// ClearCallGraphResolvedTx), never a persisted tri-state.
//
// Coverage follows Hotspots/Orphans' "unregistered project" convention
// (service_query.go): it does not itself special-case a CodedError for "not
// indexed" — that gate lives one layer up (CLI's requireIndexed, MCP's
// Server.notIndexed). An unregistered project gets a zero-valued report.
func (svc *Service) Coverage(cwd string, opts CoverageOptions) (*CoverageReport, error) {
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &CoverageReport{
		Project:     name,
		ByLanguage:  map[string]CoverageLangRollup{},
		ByDirectory: []CoverageDirRollup{},
	}
	if !found {
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}

	rows, err := g.ProjectFileCoverage(pid)
	if err != nil {
		return nil, err
	}

	top := opts.Top
	if top <= 0 {
		top = DefaultCoverageTop
	}
	if top > MaxCoverageTop {
		top = MaxCoverageTop
	}

	wantDetail := opts.Detail || opts.PathPrefix != "" || opts.Language != "" || opts.OnlyUncovered

	byDir := make(map[string]*CoverageDirRollup)
	var filtered []CoverageFile

	for _, r := range rows {
		covered := r.Resolver != ""
		stale := false
		// An unreadable file (deleted since index, permission error, …) is
		// conservatively NOT flagged stale — same "be conservative" precedent
		// as index.Indexer.Staleness.
		if content, rerr := os.ReadFile(filepath.Join(p.Path, r.FilePath)); rerr == nil {
			stale = coverageSha256hex(content) != r.IndexHash
		}

		rep.TotalFiles++
		if covered {
			rep.CoveredFiles++
		}
		if stale {
			rep.StaleFiles++
		}

		lr := rep.ByLanguage[r.Language]
		lr.Files++
		if covered {
			lr.Covered++
		}
		if stale {
			lr.Stale++
		}
		rep.ByLanguage[r.Language] = lr

		dir := filepath.Dir(r.FilePath)
		if dir == "" {
			dir = "."
		}
		dr, ok := byDir[dir]
		if !ok {
			dr = &CoverageDirRollup{Dir: dir}
			byDir[dir] = dr
		}
		dr.Files++
		if covered {
			dr.Covered++
		}
		if stale {
			dr.Stale++
		}

		if !wantDetail {
			continue
		}
		if opts.PathPrefix != "" && !strings.HasPrefix(r.FilePath, opts.PathPrefix) {
			continue
		}
		if opts.Language != "" && r.Language != opts.Language {
			continue
		}
		if opts.OnlyUncovered && covered {
			continue
		}
		filtered = append(filtered, CoverageFile{
			File: r.FilePath, Language: r.Language, Covered: covered,
			Resolver: r.Resolver, ResolvedAt: r.ResolvedAt, Stale: stale,
		})
	}

	dirList := make([]CoverageDirRollup, 0, len(byDir))
	for _, dr := range byDir {
		dirList = append(dirList, *dr)
	}
	// Worst-covered first: ascending covered/files ratio, then descending file
	// count, then lexical dir — so an agent immediately sees which packages to
	// distrust most, with the biggest ones broken ties in favor of visibility.
	sort.Slice(dirList, func(i, j int) bool {
		a, b := dirList[i], dirList[j]
		ra, rb := coverageRatio(a), coverageRatio(b)
		if ra != rb {
			return ra < rb
		}
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Dir < b.Dir
	})
	if len(dirList) > top {
		dirList = dirList[:top]
		rep.ByDirTruncated = true
	}
	rep.ByDirectory = dirList

	if wantDetail {
		rep.FilesTotal = len(filtered)
		if len(filtered) > top {
			filtered = filtered[:top]
			rep.FilesTruncated = true
		}
		rep.Files = filtered
	}

	return rep, nil
}
