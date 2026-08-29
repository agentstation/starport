package app

import (
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
)

// catalogHealthAPISource names each routable provider's declared health API
// from the current catalog snapshot: the URL, the wire convention it
// speaks, and the components that serve this gateway's endpoints. The
// poller calls it fresh each pass, so a catalog refresh changes what is
// polled without a restart. A provider that declares no health API is not
// polled — the catalog owns status sources, and a guessed poll asserts
// evidence about a page the catalog never named.
type catalogHealthAPISource struct {
	catalog *runtimecatalog.ControlPlane
}

func (s catalogHealthAPISource) HealthAPIs() map[catalogs.ProviderID]statuspage.Target {
	if s.catalog == nil {
		return nil
	}
	snapshot := s.catalog.Current()
	if snapshot == nil {
		return nil
	}
	targets := make(map[catalogs.ProviderID]statuspage.Target)
	for _, route := range snapshot.Routes() {
		if _, exists := targets[route.ProviderID]; exists {
			continue
		}
		provider, err := snapshot.Catalog().Provider(route.ProviderID)
		if err != nil || provider.Inference == nil || provider.Inference.HealthAPIURL == nil {
			continue
		}
		apiURL := strings.TrimSpace(*provider.Inference.HealthAPIURL)
		if apiURL == "" {
			continue
		}
		targets[route.ProviderID] = statuspage.Target{
			URL:        apiURL,
			Kind:       provider.Inference.HealthAPIKind,
			Components: provider.Inference.HealthComponents,
		}
	}
	return targets
}

// providerIncidentPublisher carries status-page observations into the
// state-owned projection, mirroring providerAvailabilityPublisher, so the
// state package depends on no status-page reader.
type providerIncidentPublisher struct {
	states *providerstate.Store
}

func (p providerIncidentPublisher) PublishIncidents(observations []statuspage.Observation) {
	if p.states == nil {
		return
	}
	projected := make([]providerstate.IncidentObservation, 0, len(observations))
	for _, observation := range observations {
		projected = append(projected, providerstate.IncidentObservation{
			ProviderID:  observation.ProviderID,
			Indicator:   string(observation.Indicator),
			Description: observation.Description,
			CheckedAt:   observation.CheckedAt,
		})
	}
	p.states.PublishIncidents(projected)
}
