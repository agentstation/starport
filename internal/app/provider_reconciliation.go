package app

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/rs/zerolog/log"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers"
	providerstate "github.com/agentstation/starport/internal/providers/state"
)

// RefreshProviders forces one shared provider credential reconciliation. The
// HTTP admin route added by APR5 uses this application port.
func (a *App) RefreshProviders(ctx context.Context) (providers.ReconcileReport, error) {
	if a == nil || a.providerReconciler == nil {
		return providers.ReconcileReport{}, providers.ErrReconcilerRequired
	}
	return a.providerReconciler.Reconcile(ctx, true)
}

// ProviderStates returns one secret-free provider runtime projection.
//
// The routing verdicts refresh here rather than at each publish. The registry
// replaces the runtime adapter set on its own schedule — when it opens, when a
// connector registers, and when it closes — so no publish site in this package
// observes every change to the routable snapshot. Deriving the verdicts against
// whichever snapshot the control plane currently holds binds the projection to
// the state the caller is about to read instead of to whichever publish
// happened to run last.
func (a *App) ProviderStates() providerstate.Snapshot {
	if a == nil || a.providerStates == nil {
		return providerstate.Snapshot{}
	}
	if err := a.publishProviderRouting(a.currentRoutableSnapshot()); err != nil {
		log.Warn().Err(err).Msg("refresh provider routing projection")
	}
	return a.providerStates.Snapshot()
}

func (a *App) currentRoutableSnapshot() *runtimecatalog.RoutableSnapshot {
	if a == nil || a.catalog == nil {
		return nil
	}
	return a.catalog.Current()
}

func (a *App) publishProviderCatalogState() error {
	if a == nil || a.catalog == nil {
		return ErrCatalogRequired
	}
	if a.providerStates == nil {
		return nil
	}
	snapshot := a.catalog.Current()
	if snapshot == nil || snapshot.Catalog() == nil {
		return ErrCatalogRequired
	}
	assessments, err := providers.Assess(
		snapshot.Catalog(),
		a.transports,
		a.authentication,
		providers.Configurations(a.providerSettings),
	)
	if err != nil {
		return err
	}
	observations := make([]providerstate.AdapterObservation, 0, len(assessments))
	for _, assessment := range assessments {
		observations = append(observations, assessment.Observation)
	}
	if err := a.providerStates.PublishCatalog(
		snapshot.GenerationID(), snapshot.Catalog(), observations,
	); err != nil {
		return err
	}
	return nil
}

// publishProviderRouting carries the route planner's verdicts into the provider
// state projection. An operator otherwise sees an offering the catalog
// advertises and the planner will never reach, with nothing to explain it.
func (a *App) publishProviderRouting(snapshot *runtimecatalog.RoutableSnapshot) error {
	if a == nil {
		return nil
	}
	return publishRouting(a.providerStates, snapshot)
}

func publishRouting(
	states *providerstate.Store,
	snapshot *runtimecatalog.RoutableSnapshot,
) error {
	if states == nil || snapshot == nil {
		return nil
	}
	verdicts := snapshot.OfferingRoutability()
	observations := make([]providerstate.RoutingObservation, 0, len(verdicts))
	for _, verdict := range verdicts {
		observations = append(observations, providerstate.RoutingObservation{
			ProviderID:      verdict.ProviderID,
			ProviderModelID: verdict.ProviderModelID,
			Routable:        verdict.Routable,
			Reason:          routingReason(verdict.Exclusion),
		})
	}
	return states.PublishRouting(snapshot.GenerationID(), observations)
}

// routingReason maps the planner's exclusion vocabulary onto the operator
// reason codes the provider state projection owns.
func routingReason(exclusion runtimecatalog.RouteExclusion) providerstate.ReasonCode {
	switch exclusion {
	case runtimecatalog.RouteExclusionAdapterNotReady:
		return providerstate.ReasonAdapterNotReady
	case runtimecatalog.RouteExclusionCatalogRetired:
		return providerstate.ReasonCatalogRetired
	case runtimecatalog.RouteExclusionCatalogUnavailable:
		return providerstate.ReasonCatalogUnavailable
	case runtimecatalog.RouteExclusionOfferingUnavailable:
		return providerstate.ReasonOfferingUnavailable
	case runtimecatalog.RouteExclusionOperationUnsupported:
		return providerstate.ReasonOperationUnsupported
	default:
		return providerstate.ReasonNone
	}
}

func (a *App) providerReconcileLoop(ctx context.Context) {
	report, err := a.providerReconciler.Reconcile(ctx, false)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn().Err(err).Msg(
				"startup provider credential reconciliation failed; retaining current runtime",
			)
		}
	} else {
		logProviderFailureIDs(
			"startup provider credential reconciliation",
			reconcileFailureIDs(report.Failures),
		)
	}
	interval := a.config.CredentialSources.ReconcileInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := a.providerReconciler.Reconcile(ctx, false)
			if err != nil {
				log.Warn().Err(err).Msg(
					"provider credential reconciliation failed; retaining current runtime",
				)
				continue
			}
			logProviderFailureIDs(
				"provider credential reconciliation",
				reconcileFailureIDs(report.Failures),
			)
		}
	}
}

func (a *App) currentProviderCatalogView() (providers.CatalogView, error) {
	if a == nil || a.catalog == nil {
		return providers.CatalogView{}, ErrCatalogRequired
	}
	snapshot := a.catalog.Current()
	if snapshot == nil || snapshot.Catalog() == nil {
		return providers.CatalogView{}, ErrCatalogRequired
	}
	return providers.CatalogView{
		GenerationID: snapshot.GenerationID(), PayloadChecksum: snapshot.PayloadChecksum(),
		Providers: snapshot.Catalog().Providers().List(),
	}, nil
}

func (a *App) publishProviderRuntime(
	ctx context.Context,
	view providers.CatalogView,
	resolved config.ProvidersConfig,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	current := a.catalog.Current()
	if current == nil || current.GenerationID() != view.GenerationID ||
		current.PayloadChecksum() != view.PayloadChecksum {
		return ErrProviderCatalogChanged
	}
	if a.registry == nil {
		a.config.Providers = config.CloneProvidersConfig(resolved)
		return nil
	}
	registrations, err := buildRegistrations(
		current.Catalog(),
		a.transports,
		a.authentication,
		providers.Configurations(resolved),
		a.newConnector,
	)
	if err != nil {
		return err
	}
	candidate, err := a.registry.Prepare(registrations)
	if err != nil {
		return err
	}
	state := catalogStateFromSnapshot(current)
	if err := a.catalog.ValidateRuntime(state, candidate.Availability()); err != nil {
		return errors.Join(err, candidate.Close())
	}
	snapshot, err := a.catalog.ReplaceRuntime(state, candidate.Availability())
	if err != nil {
		return errors.Join(err, candidate.Close())
	}
	if err := a.registry.Publish(candidate, snapshot); err != nil {
		return errors.Join(err, candidate.Close())
	}
	a.config.Providers = config.CloneProvidersConfig(resolved)
	return nil
}

func providerCatalogView(state starmap.CatalogState) providers.CatalogView {
	return providers.CatalogView{
		GenerationID: state.GenerationID, PayloadChecksum: state.PayloadChecksum,
		Providers: state.Catalog.Providers().List(),
	}
}

func catalogStateFromSnapshot(snapshot *runtimecatalog.RoutableSnapshot) starmap.CatalogState {
	return starmap.CatalogState{
		Catalog: snapshot.Catalog(), GenerationID: snapshot.GenerationID(),
		PayloadChecksum: snapshot.PayloadChecksum(), GeneratedAt: snapshot.GeneratedAt(),
		Sequence: snapshot.CatalogSequence(),
	}
}

func logProviderFailureIDs(message string, providerIDs []catalogs.ProviderID) {
	if len(providerIDs) == 0 {
		return
	}
	values := make([]string, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		values = append(values, string(providerID))
	}
	log.Warn().Strs("providers", values).Int("failures", len(values)).Msg(message)
}

func reconcileFailureIDs(failures []providers.ReconcileFailure) []catalogs.ProviderID {
	providerIDs := make([]catalogs.ProviderID, 0, len(failures))
	for _, failure := range failures {
		providerIDs = append(providerIDs, failure.ProviderID)
	}
	return providerIDs
}

func resolutionFailureIDs(failures []config.ProviderResolutionFailure) []catalogs.ProviderID {
	providerIDs := make([]catalogs.ProviderID, 0, len(failures))
	for _, failure := range failures {
		providerIDs = append(providerIDs, failure.ProviderID)
	}
	return providerIDs
}
