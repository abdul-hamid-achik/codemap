package app

import (
	"os"
	"strconv"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// vectorScope is the stable per-project store key (the same RepoHash the
// status report exposes as project_key).
func vectorScope(p *graph.Project) string { return git.RepoHash(p.Path) }

func projectShardExists(scope string) bool {
	return scope != "" && fileExists(config.VecliteProjectPath(scope))
}

// projectVectors returns the writable store a project should use: its own
// shard when one exists, else the legacy shared store (which still holds
// unmigrated records).
func (svc *Service) projectVectors(p *graph.Project) (*vector.Store, error) {
	if projectShardExists(vectorScope(p)) {
		return svc.s.VectorsForProject(vectorScope(p))
	}
	return svc.s.Vectors()
}

func (svc *Service) projectVectorsReadOnly(p *graph.Project) (*vector.Store, error) {
	if projectShardExists(vectorScope(p)) {
		return svc.s.VectorsReadOnlyForProject(vectorScope(p))
	}
	return svc.s.VectorsReadOnly()
}

// reconcileShardVecIDs re-points graph nodes at the records actually in
// the project's shard (by payload node id). It repairs the crash window
// between a migration's file rename and its graph update, and is a no-op
// when pointers already agree.
func (svc *Service) reconcileShardVecIDs(g *graph.Store, p *graph.Project, vec *vector.Store) error {
	owners, err := g.VecIDOwners()
	if err != nil {
		return err
	}
	current := make(map[int64]string, len(owners))
	for vecID, o := range owners {
		if o.Project == p.Name {
			current[o.NodeID] = vecID
		}
	}
	fixes := map[int64]string{}
	vec.ForEachMeta(func(id uint64, meta vector.NodeMeta) bool {
		if meta.Project != p.Name {
			return true
		}
		want := strconv.FormatUint(id, 10)
		if current[meta.NodeID] != want {
			fixes[meta.NodeID] = want
		}
		return true
	})
	if len(fixes) == 0 {
		return nil
	}
	_, err = g.SetNodeVecIDs(p.ID, fixes)
	return err
}

// migrateProjectVectorsToShard moves a project's records out of the legacy
// shared store into its per-project shard, once. Steps are ordered so a
// crash never loses the only copy: copy into a "-migrating" store and sync
// it, rename into place, re-point the graph, and only then delete the
// legacy copies. A crash after the rename leaves complete records in the
// shard with stale graph pointers, which reconcileShardVecIDs repairs on
// the next embedded index. Returns false (without error) when there is
// nothing migratable under the current embedding profile.
func (svc *Service) migrateProjectVectorsToShard(g *graph.Store, p *graph.Project) (bool, error) {
	scope := vectorScope(p)
	if scope == "" || projectShardExists(scope) || !fileExists(config.VeclitePath()) {
		return false, nil
	}
	hasVec, err := g.ProjectHasVecIDs(p.ID)
	if err != nil || !hasVec {
		return false, err
	}
	legacy, err := svc.s.VectorsForMaintenance()
	if err != nil || legacy == nil {
		return false, err
	}
	defer func() { _ = svc.s.ReleaseMaintenanceVectors() }()

	records, err := legacy.IterByProject(p.Name)
	if err != nil {
		return false, err
	}
	owners, err := g.VecIDOwners()
	if err != nil {
		return false, err
	}
	dims := svc.s.Embedder().Profile().Dimensions
	var valid []vector.VecRecord
	for _, r := range records {
		owner, ok := owners[strconv.FormatUint(r.ID, 10)]
		if !ok || owner.NodeID != r.Meta.NodeID || owner.Project != p.Name {
			continue // orphan: GC's job, not migration's
		}
		if len(r.Vector) != dims {
			return false, nil // profile moved: let the legacy guard surface it
		}
		valid = append(valid, r)
	}
	if len(valid) == 0 {
		return false, nil
	}

	tmpScope := scope + "-migrating"
	_ = svc.s.ReleaseProjectVectors(tmpScope)
	_ = os.Remove(config.VecliteProjectPath(tmpScope))
	tmp, err := svc.s.VectorsForProject(tmpScope)
	if err != nil {
		return false, err
	}
	byNode := make(map[int64]string, len(valid))
	for _, r := range valid {
		newID, err := tmp.Insert(r.Vector, r.Content, r.Meta)
		if err != nil {
			_ = svc.s.ReleaseProjectVectors(tmpScope)
			return false, err
		}
		byNode[r.Meta.NodeID] = strconv.FormatUint(newID, 10)
	}
	if err := tmp.Sync(); err != nil {
		_ = svc.s.ReleaseProjectVectors(tmpScope)
		return false, err
	}
	if err := svc.s.ReleaseProjectVectors(tmpScope); err != nil {
		return false, err
	}
	if err := os.Rename(config.VecliteProjectPath(tmpScope), config.VecliteProjectPath(scope)); err != nil {
		return false, err
	}
	if _, err := g.SetNodeVecIDs(p.ID, byNode); err != nil {
		return false, err
	}
	deleted, err := legacy.DeleteByProject(p.Name)
	if err != nil {
		return false, err
	}
	if deleted > 0 {
		if err := legacy.Sync(); err != nil {
			return false, err
		}
	}
	return true, nil
}
