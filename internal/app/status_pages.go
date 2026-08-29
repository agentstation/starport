package app

import (
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
)

// catalogStatusPageSource names each routable provider's published status
// page from the current catalog snapshot. The poller calls it fresh each
// pass, so a catalog refresh changes what is polled without a restart.
type catalogStatusPageSource struct {
	catalog *runtimecatalog.ControlPlane
}

func (s catalogStatusPageSource) StatusPages() map[catalogs.ProviderID]string {
	if s.catalog == nil {
		return nil
	}
	snapshot := s.catalog.Current()
	if snapshot == nil {
		return nil
	}
	pages := make(map[catalogs.ProviderID]string)
	for _, route := range snapshot.Routes() {
		if _, exists := pages[route.ProviderID]; exists {
			continue
		}
		provider, err := snapshot.Catalog().Provider(route.ProviderID)
		if err != nil || provider.StatusPageURL == nil {
			continue
		}
		pageURL := strings.TrimSpace(*provider.StatusPageURL)
		if pageURL == "" {
			continue
		}
		pages[route.ProviderID] = pageURL
	}
	return pages
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
