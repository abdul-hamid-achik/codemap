package app

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	DefaultMapSubsystems  = 50
	MaxMapSubsystems      = 500
	DefaultMapBridges     = 100
	MaxMapBridges         = 1000
	DefaultMapHubs        = 20
	MaxMapHubs            = 200
	DefaultMapEntrypoints = 10
	MaxMapEntrypoints     = 200
)

// ArchitectureMapOptions bounds the architecture overview. Zero values use
// conservative defaults in the graph layer.
type ArchitectureMapOptions struct {
	TopSubsystems  int
	TopBridges     int
	TopHubs        int
	TopEntrypoints int
}

// ArchitectureMapReport is a deterministic project-level orientation bundle:
// path-shaped subsystems, directed cross-subsystem bridges, hubs, and likely
// entrypoints. It is graph-only and carries the same call-graph/freshness honesty
// signals as the lower-level structural queries.
type ArchitectureMapReport struct {
	SchemaVersion        int                       `json:"schema_version"`
	Project              string                    `json:"project"`
	Indexed              bool                      `json:"indexed"`
	Strategy             string                    `json:"strategy,omitempty"`
	Subsystems           []graph.TopologySubsystem `json:"subsystems"`
	SubsystemsTotal      int                       `json:"subsystems_total"`
	Bridges              []graph.TopologyBridge    `json:"bridges"`
	BridgesTotal         int                       `json:"bridges_total"`
	Hubs                 []HotspotRef              `json:"hubs"`
	HubsTotal            int                       `json:"hubs_total"`
	Entrypoints          []ReadEntry               `json:"entrypoints"`
	EntrypointsTotal     int                       `json:"entrypoints_total"`
	CallGraph            string                    `json:"call_graph"`
	Resolution           string                    `json:"resolution,omitempty"`
	Stale                bool                      `json:"stale,omitempty"`
	Truncated            bool                      `json:"truncated,omitempty"`
	SubsystemsTruncated  bool                      `json:"subsystems_truncated,omitempty"`
	BridgesTruncated     bool                      `json:"bridges_truncated,omitempty"`
	HubsTruncated        bool                      `json:"hubs_truncated,omitempty"`
	EntrypointsTruncated bool                      `json:"entrypoints_truncated,omitempty"`
	PartialErrors        []string                  `json:"partial_errors,omitempty"`
}

// ArchitectureMap returns a bounded, deterministic architecture projection for
// orienting an agent or person before opening individual source files.
func (svc *Service) ArchitectureMap(cwd string, opts ArchitectureMapOptions) (*ArchitectureMapReport, error) {
	opts, err := normalizeArchitectureMapOptions(opts)
	if err != nil {
		return nil, err
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ArchitectureMapReport{
		SchemaVersion: 1, Project: name, Indexed: found,
		Subsystems: []graph.TopologySubsystem{}, Bridges: []graph.TopologyBridge{},
		Hubs: []HotspotRef{}, Entrypoints: []ReadEntry{}, CallGraph: CallGraphNone,
	}
	if !found {
		return rep, nil
	}

	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	topology, err := g.ProjectArchitecture(pid, graph.TopologyOptions{
		MaxSubsystems: opts.TopSubsystems,
		MaxBridges:    opts.TopBridges,
	})
	if err != nil {
		return nil, err
	}
	rep.Strategy = topology.Strategy
	rep.Subsystems = topology.Subsystems
	rep.SubsystemsTotal = topology.SubsystemsTotal
	rep.Bridges = topology.Bridges
	rep.BridgesTotal = topology.BridgesTotal
	rep.SubsystemsTruncated = len(rep.Subsystems) < rep.SubsystemsTotal
	rep.BridgesTruncated = len(rep.Bridges) < rep.BridgesTotal

	hotspots, hErr := svc.Hotspots(cwd, opts.TopHubs)
	if hErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, "hotspots: "+hErr.Error())
	} else {
		rep.Hubs = hotspots.Hotspots
		rep.CallGraph = hotspots.CallGraph
		rep.Resolution = hotspots.Resolution
		if total, countErr := g.HotspotCount(pid); countErr != nil {
			rep.PartialErrors = append(rep.PartialErrors, "hotspot_count: "+countErr.Error())
		} else {
			rep.HubsTotal = total
			rep.HubsTruncated = len(rep.Hubs) < total
		}
	}

	readOrder, rErr := svc.ReadOrder(cwd, ReadOrderOpts{Top: opts.TopEntrypoints, EntrypointsOnly: true})
	if rErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, "read_order: "+rErr.Error())
	} else {
		rep.Entrypoints = append(rep.Entrypoints, readOrder.Entries...)
		rep.EntrypointsTotal = readOrder.totalEntries
		rep.EntrypointsTruncated = readOrder.truncated
		if rep.Resolution == "" {
			rep.Resolution = readOrder.Resolution
		}
	}
	rep.Truncated = rep.SubsystemsTruncated || rep.BridgesTruncated || rep.HubsTruncated || rep.EntrypointsTruncated

	stale, sErr := svc.Staleness(cwd)
	if sErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("staleness: %v", sErr))
	} else if stale != nil {
		rep.Stale = stale.Any()
	}
	return rep, nil
}

func normalizeArchitectureMapOptions(opts ArchitectureMapOptions) (ArchitectureMapOptions, error) {
	limits := []struct {
		name         string
		value        *int
		defaultValue int
		maxValue     int
	}{
		{name: "top subsystems", value: &opts.TopSubsystems, defaultValue: DefaultMapSubsystems, maxValue: MaxMapSubsystems},
		{name: "top bridges", value: &opts.TopBridges, defaultValue: DefaultMapBridges, maxValue: MaxMapBridges},
		{name: "top hubs", value: &opts.TopHubs, defaultValue: DefaultMapHubs, maxValue: MaxMapHubs},
		{name: "top entrypoints", value: &opts.TopEntrypoints, defaultValue: DefaultMapEntrypoints, maxValue: MaxMapEntrypoints},
	}
	for _, limit := range limits {
		if *limit.value == 0 {
			*limit.value = limit.defaultValue
		}
		if *limit.value < 1 || *limit.value > limit.maxValue {
			return opts, fmt.Errorf("map %s must be between 1 and %d", limit.name, limit.maxValue)
		}
	}
	return opts, nil
}
