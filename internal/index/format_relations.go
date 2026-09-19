package index

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"gopkg.in/yaml.v3"
)

// GitHub workflow YAML is source. Keep other hidden directories excluded and
// use the same rule for indexing, freshness checks, and watcher registration.
func skipHiddenDirectory(name string) bool {
	return strings.HasPrefix(name, ".") && name != ".github"
}

// resolveFormatRelations reconciles declarative relationships (sql reads/writes,
// yaml depends_on, markdown documents, sqlc-generated depends_on) into edges.
//
// Full mode (first index, --reindex, or a run with added/deleted files) reparses
// every declarative file and replaces the project's declarative edges wholesale:
// the name-resolution surface itself changed, so any file's edges may differ.
//
// Scoped mode (a modified-only incremental — the steady-state save path) touches
// only (a) declarative files whose nodes were replaced this run (touched) and
// (b) files carrying persisted dangling references whose target name appears
// among the touched files' symbols — the "missing table finally defined" heal.
// Per-file edge replacement leaves every other file's edges and bytes untouched,
// so a one-file save costs that file's work, not the whole project's.
//
// Both modes persist unresolved declarative references (unresolved_refs) so a
// later scoped run can heal exactly the files whose missing target reappeared.
func (ix *Indexer) resolveFormatRelations(ctx context.Context, projectID int64, root string, ni *nodeIndex, full bool, touched []string) error {
	// Scoped reconciliation needs touched files to have something to reconcile
	// (and to name heal candidates); with none, there is nothing to do — and
	// the caller may not even have built ni.
	if !full && len(touched) == 0 {
		return nil
	}
	var scope map[string]bool // nil = every declarative file
	if !full {
		scope = make(map[string]bool)
		touchedSet := make(map[string]bool, len(touched))
		for _, rel := range touched {
			touchedSet[rel] = true
			scope[rel] = true
		}
		heal, err := ix.graph.FilesWithUnresolvedRefsByName(projectID, declarativeTargetNames(ni, touchedSet))
		if err != nil {
			return err
		}
		for _, rel := range heal {
			scope[rel] = true
		}
		if len(scope) == 0 {
			return nil // nothing declarative changed and nothing can heal
		}
	}

	var refs []extract.Reference
	sources := map[string][]byte{}
	var processed []string // declarative files actually re-read + re-extracted
	for _, n := range ni.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.Kind != graph.KindFile || (n.Language != "sql" && n.Language != "yaml" && n.Language != "markdown") {
			continue
		}
		if scope != nil && !scope[n.FilePath] {
			continue
		}
		data, oversized, err := readFileUnderLimit(filepath.Join(root, n.FilePath), ix.cfg.MaxFileBytes)
		if err != nil || oversized {
			continue
		}
		hash, err := ix.graph.FileHash(projectID, n.FilePath)
		if err != nil {
			return err
		}
		if hash != sha256hex(data) {
			continue // drifted mid-run: keep last-good edges + rows for the next pass
		}
		fr, err := ix.extractors[n.Language].ExtractFile(n.FilePath, data)
		if err != nil {
			continue
		}
		refs = append(refs, fr.References...)
		sources[n.FilePath] = data
		processed = append(processed, n.FilePath)
	}
	// sqlc's generated depends_on edges derive from the sqlc.yaml config, the
	// SQL query nodes, and the generated Go methods — any of which a touched
	// sql/yaml/go file may have changed. Full mode always re-derives; scoped
	// mode only when such a file actually changed, reading the (few) sqlc.yaml
	// configs directly since unchanged ones are not in the scoped source set.
	if full || sqlcTriggered(touched) {
		for _, n := range ni.nodes {
			if n.Language != "yaml" || !isSQLCConfig(n.FilePath) {
				continue
			}
			if _, ok := sources[n.FilePath]; !ok {
				data, oversized, err := readFileUnderLimit(filepath.Join(root, n.FilePath), ix.cfg.MaxFileBytes)
				if err != nil || oversized {
					continue
				}
				sources[n.FilePath] = data
			}
		}
		refs = append(refs, sqlcRelations(root, ni, sources, ix.cfg.MaxFileBytes)...)
	}

	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// The rewrite set: every re-processed declarative file (their prior edges
	// must go, even when this extraction produced none) plus the files owning
	// the collected references (sqlc's depends_on sources are generated Go
	// files, which the declarative loop above never visits).
	var unresolved []graph.UnresolvedRef
	fileOfNode := make(map[int64]string, len(ni.nodes))
	for _, n := range ni.nodes {
		fileOfNode[n.ID] = n.FilePath
	}
	rewrite := map[string]bool{}
	for _, f := range processed {
		rewrite[f] = true
	}
	for _, ref := range refs {
		if from, ok := ni.fqnTo[ref.From]; ok {
			rewrite[fileOfNode[from]] = true
		}
	}
	if full {
		if _, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE edge_type IN ('reads','writes','documents','depends_on') AND source_id IN (SELECT id FROM nodes WHERE project_id=?)`, projectID); err != nil {
			return err
		}
	} else {
		for f := range rewrite {
			if _, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE edge_type IN ('reads','writes','documents','depends_on') AND source_id IN (SELECT id FROM nodes WHERE project_id=? AND file_path=?)`, projectID, f); err != nil {
				return err
			}
		}
	}
	if _, err = resolveEdgesTx(tx, refs, ni, &unresolved); err != nil {
		return err
	}
	rowsByFile := map[string][]graph.UnresolvedRef{}
	for _, r := range unresolved {
		rowsByFile[r.FilePath] = append(rowsByFile[r.FilePath], r)
	}
	for f := range rewrite {
		if err := graph.ReplaceUnresolvedRefsTx(tx, projectID, f, rowsByFile[f]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// declarativeTargetNames returns every name a dangling reference could match
// that is defined by the given files: node symbols (SQL tables/views), node
// FQNs (markdown sections), and the file paths themselves (file-keyed documents
// links — buildNodeIndex keys file nodes by path). The (re)appearance of any of
// these names is what can resolve another file's persisted dangling reference.
func declarativeTargetNames(ni *nodeIndex, files map[string]bool) []string {
	if len(files) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, n := range ni.nodes {
		if !files[n.FilePath] {
			continue
		}
		if n.Symbol != "" {
			names[n.Symbol] = true
		}
		if n.FQN != "" {
			names[n.FQN] = true
		}
		if n.Kind == graph.KindFile {
			names[n.FilePath] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out
}

// sqlcTriggered reports whether any touched file can change the sqlc
// config→query→generated-method mapping (the config is yaml, queries are sql,
// methods live in generated Go).
func sqlcTriggered(touched []string) bool {
	for _, rel := range touched {
		switch extract.LanguageForPath(rel) {
		case "sql", "yaml", "go":
			return true
		}
	}
	return false
}

func isSQLCConfig(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return base == "sqlc.yaml" || base == "sqlc.yml"
}

// sqlc's config scopes the SQL inputs and generated Go directory. A matching
// method name alone is insufficient: require the generated-file marker and
// the exact query annotation in that generated source too.
func sqlcRelations(root string, ni *nodeIndex, sources map[string][]byte, maxBytes int) []extract.Reference {
	var refs []extract.Reference
	for file, data := range sources {
		if !isSQLCConfig(file) {
			continue
		}
		var cfg struct {
			SQL []struct {
				Queries any `yaml:"queries"`
				Gen     struct {
					Go struct {
						Out string `yaml:"out"`
					} `yaml:"go"`
				} `yaml:"gen"`
			} `yaml:"sql"`
		}
		if yaml.Unmarshal(data, &cfg) != nil {
			continue
		}
		for _, entry := range cfg.SQL {
			if entry.Gen.Go.Out == "" {
				continue
			}
			out := normalizeSlashPath(joinSlash(parentDir(file), entry.Gen.Go.Out))
			if out == "" && entry.Gen.Go.Out != "." {
				continue
			}
			var inputs []string
			switch q := entry.Queries.(type) {
			case string:
				inputs = append(inputs, q)
			case []any:
				for _, v := range q {
					if s, ok := v.(string); ok {
						inputs = append(inputs, s)
					}
				}
			}
			queryNodes := map[string][]graph.Node{}
			for _, n := range ni.nodes {
				if n.Language != "sql" || n.Kind != graph.KindQuery {
					continue
				}
				for _, input := range inputs {
					pattern := normalizeSlashPath(joinSlash(parentDir(file), input))
					if pattern == "" {
						continue
					}
					match, _ := filepath.Match(pattern, n.FilePath)
					if match || strings.HasPrefix(n.FilePath, pattern+"/") {
						queryNodes[n.Symbol] = append(queryNodes[n.Symbol], n)
						break
					}
				}
			}
			generated := map[string]string{}
			for _, n := range ni.nodes {
				if n.Language != "go" || (n.Kind != graph.KindMethod && n.Kind != graph.KindFunction) || parentDir(n.FilePath) != out {
					continue
				}
				qs := queryNodes[n.Symbol]
				if len(qs) != 1 {
					continue
				}
				body, ok := generated[n.FilePath]
				if !ok {
					b, oversized, err := readFileUnderLimit(filepath.Join(root, n.FilePath), maxBytes)
					if err != nil || oversized {
						continue
					}
					body = string(b)
					generated[n.FilePath] = body
				}
				if !strings.Contains(body, "Code generated by sqlc. DO NOT EDIT.") || !strings.Contains(body, "-- name: "+n.Symbol+" :") {
					continue
				}
				refs = append(refs, extract.Reference{From: n.FQN, ToFQN: qs[0].FQN, Kind: graph.EdgeDependsOn, Line: n.StartLine})
			}
		}
	}
	return refs
}
