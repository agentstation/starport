package catalog

import (
	"github.com/agentstation/starmap/acquisition"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
)

// RecoveryTopologyTarget parses current target policy without constructing a connected runtime.
// It starts no source transport, acquisition, or clock monitor.
// Passive collectors preserve the same canonical acquisition capabilities for semantic replay.
func (s Settings) RecoveryTopologyTarget() (TopologyRuntimeTarget, []runtime.Option, error) {
	parsed, err := catalogconfig.Parse(s.catalogValues())
	if err != nil {
		return TopologyRuntimeTarget{}, nil, err
	}
	owner := s.directoryOwner()
	if err := owner.Validate(); err != nil {
		return TopologyRuntimeTarget{}, nil, err
	}
	providers, err := acquisition.NewAcquirer()
	if err != nil {
		return TopologyRuntimeTarget{}, nil, err
	}
	metadata, err := s.metadataCollector()
	if err != nil {
		return TopologyRuntimeTarget{}, nil, err
	}
	options := append(parsed.Options(), runtime.WithDirectoryOwner(owner), runtime.WithAcquirer(providers), runtime.WithSourceAcquirer(metadata))
	return TopologyRuntimeTarget{Directory: parsed.StateDirectory, Owner: owner, SchedulerIdentity: parsed.SchedulerIdentity}, options, nil
}
