package app

import (
	"sort"
	"strconv"

	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// VectorGCProject is one project's orphan accounting in a PruneOrphanVectors
// report.
type VectorGCProject struct {
	Project string `json:"project"`
	Scanned int    `json:"scanned"`
	Orphans int    `json:"orphans"`
	Deleted int    `json:"deleted"`
}

// VectorGCReport summarizes an orphan-vector prune. A record is an orphan
// when no graph node claims it: the payload's node id is not the node whose
// vec_id points back at the record (deleted/replaced nodes, projects whose
// graph was wiped or migrated to the vecgrep backend with pointers cleared).
type VectorGCReport struct {
	SchemaVersion int               `json:"schema_version"`
	DryRun        bool              `json:"dry_run"`
	StorePresent  bool              `json:"store_present"`
	Scanned       int               `json:"scanned"`
	Orphans       int               `json:"orphans"`
	Deleted       int               `json:"deleted"`
	Projects      []VectorGCProject `json:"projects"`
}

// PruneOrphanVectors removes local vector records no graph node claims. It
// is an explicit maintenance operation: opening the shared veclite store
// loads every project's records, so nothing on the index/query hot path
// calls this implicitly. With dryRun it only reports.
func (svc *Service) PruneOrphanVectors(dryRun bool) (*VectorGCReport, error) {
	rep := &VectorGCReport{SchemaVersion: 1, DryRun: dryRun, Projects: []VectorGCProject{}}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	owners, err := g.VecIDOwners()
	if err != nil {
		return nil, err
	}
	vec, err := svc.s.VectorsForMaintenance()
	if err != nil {
		return nil, err
	}
	if vec == nil {
		return rep, nil // no store on disk: nothing to prune
	}
	defer func() { _ = svc.s.ReleaseMaintenanceVectors() }()
	rep.StorePresent = true

	perProject := map[string]*VectorGCProject{}
	type orphan struct {
		id      uint64
		project string
	}
	var orphans []orphan
	vec.ForEachMeta(func(id uint64, meta vector.NodeMeta) bool {
		p := perProject[meta.Project]
		if p == nil {
			p = &VectorGCProject{Project: meta.Project}
			perProject[meta.Project] = p
		}
		p.Scanned++
		rep.Scanned++
		owner, ok := owners[strconv.FormatUint(id, 10)]
		if ok && owner.NodeID == meta.NodeID && owner.Project == meta.Project {
			return true
		}
		p.Orphans++
		rep.Orphans++
		orphans = append(orphans, orphan{id: id, project: meta.Project})
		return true
	})

	if !dryRun {
		for _, o := range orphans {
			if err := vec.DeleteByID(o.id); err != nil {
				return rep, err
			}
			perProject[o.project].Deleted++
			rep.Deleted++
		}
		if rep.Deleted > 0 {
			if err := vec.Sync(); err != nil {
				return rep, err
			}
		}
	}

	names := make([]string, 0, len(perProject))
	for name := range perProject {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rep.Projects = append(rep.Projects, *perProject[name])
	}
	return rep, nil
}
