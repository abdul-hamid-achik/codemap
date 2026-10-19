package app

import (
	"sort"
	"strconv"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
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

// PruneOrphanVectors removes local vector records no graph node claims,
// across the legacy shared store and every per-project shard. It is an
// explicit maintenance operation: opening a veclite store loads its
// records, so nothing on the index/query hot path calls this implicitly.
// With dryRun it only reports.
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
	nodeProject, err := g.NodeProjectNames()
	if err != nil {
		return nil, err
	}
	perProject := map[string]*VectorGCProject{}

	if vec, err := svc.s.VectorsForMaintenance(); err != nil {
		return nil, err
	} else if vec != nil {
		rep.StorePresent = true
		serr := svc.pruneStore(vec, false, dryRun, owners, nodeProject, rep, perProject)
		_ = svc.s.ReleaseMaintenanceVectors()
		if serr != nil {
			return rep, serr
		}
	}

	projects, err := g.ListProjects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		scope := vectorScope(&p)
		if !projectShardExists(scope) {
			continue
		}
		vec, err := svc.s.VectorsForProjectMaintenance(scope)
		if err != nil {
			return rep, err
		}
		if vec == nil {
			continue
		}
		rep.StorePresent = true
		serr := svc.pruneStore(vec, true, dryRun, owners, nodeProject, rep, perProject)
		_ = svc.s.ReleaseProjectMaintenanceVectors(scope)
		if serr != nil {
			return rep, serr
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

// pruneStore scans one store, classifying and (unless dryRun) deleting its
// orphans, accumulating into rep/perProject. Shard records whose payload
// node still belongs to the named project count as valid even when the
// graph back-pointer is stale: that is the migration crash window between
// the shard rename and SetNodeVecIDs, repaired by reconcileShardVecIDs.
func (svc *Service) pruneStore(vec *vector.Store, fromShard, dryRun bool, owners map[string]graph.VecIDOwner, nodeProject map[int64]string, rep *VectorGCReport, perProject map[string]*VectorGCProject) error {
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
		if fromShard && nodeProject[meta.NodeID] == meta.Project && meta.Project != "" {
			return true
		}
		p.Orphans++
		rep.Orphans++
		orphans = append(orphans, orphan{id: id, project: meta.Project})
		return true
	})
	if dryRun {
		return nil
	}
	deleted := 0
	for _, o := range orphans {
		if err := vec.DeleteByID(o.id); err != nil {
			return err
		}
		perProject[o.project].Deleted++
		rep.Deleted++
		deleted++
	}
	if deleted > 0 {
		return vec.Sync()
	}
	return nil
}
