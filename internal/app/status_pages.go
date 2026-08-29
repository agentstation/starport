package app

import (
	"context"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/rs/zerolog/log"

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
// state package depends on no status-page reader. The transitions the
// projection reports back go to the durable repository: the live verdict
// stays in memory where routing reads it, and only the record of changes
// touches relational storage — on the poller's goroutine, never a
// request's.
type providerIncidentPublisher struct {
	states      *providerstate.Store
	transitions providerstate.TransitionRepository
}

// incidentRecordTimeout bounds one durable write of observed transitions.
const incidentRecordTimeout = 10 * time.Second

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
	transitions := p.states.PublishIncidents(projected)
	if len(transitions) == 0 || p.transitions == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), incidentRecordTimeout)
	defer cancel()
	if err := p.transitions.Record(ctx, transitions); err != nil {
		// The projection already holds the live verdict; losing one durable
		// record is worth a log line, not a failed poll pass.
		log.Warn().Err(err).Msg("record incident transitions failed")
	}
}

// ProviderIncidentLog answers one provider's published incident log. The
// second return reports whether the catalog knows the provider at all, so
// the HTTP surface can separate "unknown provider" from "no log".
func (a *App) ProviderIncidentLog(ctx context.Context, providerID catalogs.ProviderID) (statuspage.History, bool) {
	if a == nil || a.catalog == nil {
		return statuspage.History{Availability: statuspage.HistoryUnreachable}, false
	}
	snapshot := a.catalog.Current()
	if snapshot == nil {
		return statuspage.History{Availability: statuspage.HistoryUnreachable}, false
	}
	if _, err := snapshot.Catalog().Provider(providerID); err != nil {
		return statuspage.History{}, false
	}
	return a.incidentHistory.History(ctx, providerID), true
}

// ProviderIncidentTransitions answers the durable record of indicator
// changes this gateway observed for one provider, newest first.
func (a *App) ProviderIncidentTransitions(ctx context.Context, providerID catalogs.ProviderID) ([]providerstate.IncidentTransition, error) {
	if a == nil || a.incidentTransitions == nil {
		return nil, nil
	}
	return a.incidentTransitions.Transitions(ctx, providerID)
}
