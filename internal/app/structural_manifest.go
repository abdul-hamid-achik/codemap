package app

import (
	"encoding/hex"
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// StructuralManifestSchemaVersion is the major version of the lightweight
// structural-index identity contract. Removing or changing a required field
// needs a new version; additive optional fields remain compatible within v1.
const StructuralManifestSchemaVersion = 1

// StructuralManifestFreshness reports working-tree drift without including
// any source body. Checked is explicit so a future additive diagnostic can
// represent an unavailable check without pretending that zero means fresh.
type StructuralManifestFreshness struct {
	Checked bool `json:"checked"`
	Fresh   bool `json:"fresh"`
	Changed int  `json:"changed"`
	New     int  `json:"new"`
	Deleted int  `json:"deleted"`
}

// StructuralManifestReport is the single-response identity preflight for the
// paginated codemap.structural-export.v1 stream. IndexFingerprint is computed
// from the same ordered indexed metadata as export-symbols, but the manifest
// streams rows through the digest and never reads or returns source bodies.
// Complete means the complete indexed symbol set contributed to the digest;
// stale working-tree files are reported separately under Freshness.
type StructuralManifestReport struct {
	SchemaVersion       int                         `json:"schema_version"`
	ExportSchemaVersion int                         `json:"export_schema_version"`
	Project             string                      `json:"project"`
	ProjectKey          string                      `json:"project_key"`
	IndexFingerprint    string                      `json:"index_fingerprint"`
	TotalRecords        int                         `json:"total_records"`
	Complete            bool                        `json:"complete"`
	Freshness           StructuralManifestFreshness `json:"freshness"`
}

// StructuralManifest returns a lightweight, versioned preflight for sibling
// processes such as vecgrep. It is intentionally service/CLI-only in v1: peers
// cross one process boundary and never share codemap's database or Go packages.
func (svc *Service) StructuralManifest(cwd string) (*StructuralManifestReport, error) {
	return svc.structuralManifest(cwd, nil)
}

// structuralManifest's barrier is used only by the deterministic concurrency
// test. It runs after the coherent SQLite snapshot has been captured and before
// working-tree freshness is computed; production callers use StructuralManifest.
func (svc *Service) structuralManifest(cwd string, afterSnapshot func() error) (*StructuralManifestReport, error) {
	pid, project, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, coded(CodeMissing, "run: codemap index",
			fmt.Errorf("project %s is not indexed", project))
	}

	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	p, err := g.GetProjectByName(project)
	if err != nil {
		return nil, err
	}
	projectKey := git.RepoHash(p.Path)
	h := newStructuralIndexFingerprint(projectKey)
	rep := &StructuralManifestReport{
		SchemaVersion:       StructuralManifestSchemaVersion,
		ExportSchemaVersion: StructuralExportSchemaVersion,
		Project:             project,
		ProjectKey:          projectKey,
	}
	snapshot, err := g.WalkProjectStructuralIndexSnapshot(pid, func(n graph.Node) error {
		writeStructuralFingerprintNode(h, n)
		rep.TotalRecords++
		return nil
	})
	if err != nil {
		return nil, err
	}
	rep.IndexFingerprint = hex.EncodeToString(h.Sum(nil))
	if afterSnapshot != nil {
		if err := afterSnapshot(); err != nil {
			return nil, fmt.Errorf("structural manifest snapshot barrier: %w", err)
		}
	}

	stale, err := index.New(g, nil, nil, svc.s.Config.Index).StalenessFromSnapshotStrict(
		p.Path, snapshot.FileHashes, snapshot.Languages,
	)
	if err != nil {
		return nil, fmt.Errorf("structural manifest freshness: %w", err)
	}
	rep.Freshness = manifestFreshness(stale)
	rep.Complete = true
	return rep, nil
}

func manifestFreshness(stale index.Staleness) StructuralManifestFreshness {
	return StructuralManifestFreshness{
		Checked: true,
		Fresh:   !stale.Any(),
		Changed: stale.Changed,
		New:     stale.New,
		Deleted: stale.Deleted,
	}
}
