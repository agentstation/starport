package app

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	providerstate "github.com/agentstation/starport/internal/providers/state"
)

// TestProviderRoutingFollowsTheRuntimeAdapterSet reproduces the ordering the
// gateway actually boots in. The provider catalog is projected before any
// adapter exists, and the registry installs the adapter set afterwards by
// replacing it on the control plane directly. Nothing in this package observes
// that call, so a routing projection pushed at a publish site reports every
// offering as adapter_not_ready for the life of the process while requests
// route normally. The verdicts have to follow the snapshot the reader is about
// to see.
func TestProviderRoutingFollowsTheRuntimeAdapterSet(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	catalog := client.Catalog()
	providerID, offering := firstRoutingOffering(t, catalog)
	states := providerstate.New()
	generationID := plane.Current().GenerationID()
	require.NoError(t, states.PublishCatalog(
		generationID, catalog, adapterObservations(t, catalog),
	))
	application := &App{catalog: plane, providerStates: states}

	// The startup publish, which runs before any adapter exists and is the only
	// push the boot sequence performs.
	require.NoError(t, application.publishProviderRouting(plane.Current()))

	// Startup order: the catalog projection exists, the adapter set does not.
	require.Equal(t,
		providerstate.RoutingStatus{
			State:  providerstate.RoutingUnroutable,
			Reason: providerstate.ReasonAdapterNotReady,
		},
		routingFor(t, application, providerID, offering.ProviderModelID),
	)

	// The registry installs the adapters, which is the call this package never
	// sees.
	require.NoError(t, plane.ReplaceAdapters([]runtimecatalog.AdapterAvailability{
		adapterFor(providerID, offering),
	}))

	require.Equal(t,
		providerstate.RoutingStatus{State: providerstate.RoutingRoutable},
		routingFor(t, application, providerID, offering.ProviderModelID),
	)

	// Withdrawing the adapters has to move the verdict back, so the console
	// never keeps calling an offering reachable after its adapter goes away.
	require.NoError(t, plane.ReplaceAdapters(nil))
	require.Equal(t,
		providerstate.RoutingStatus{
			State:  providerstate.RoutingUnroutable,
			Reason: providerstate.ReasonAdapterNotReady,
		},
		routingFor(t, application, providerID, offering.ProviderModelID),
	)
}

func routingFor(
	t *testing.T,
	application *App,
	providerID catalogs.ProviderID,
	providerModelID catalogs.ProviderModelID,
) providerstate.RoutingStatus {
	t.Helper()
	for _, provider := range application.ProviderStates().Providers {
		if provider.ProviderID != providerID {
			continue
		}
		for _, offering := range provider.Offerings {
			if offering.ProviderModelID == providerModelID {
				return offering.Routing
			}
		}
	}
	t.Fatalf("no offering status for %s/%s", providerID, providerModelID)
	return providerstate.RoutingStatus{}
}

// firstRoutingOffering picks a deterministic offering that publishes at least
// one operation, so the adapter can share one with it.
func firstRoutingOffering(
	t *testing.T,
	catalog *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	for _, provider := range catalog.Providers().List() {
		offerings, err := catalog.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if len(offering.Service.Operations) > 0 &&
				offering.Lifecycle != catalogs.OfferingLifecycleRetired &&
				offering.Availability != catalogs.OfferingAvailabilityUnavailable {
				return provider.ID, offering
			}
		}
	}
	t.Fatal("the embedded catalog publishes no routable offering")
	return "", catalogs.ProviderOffering{}
}

func adapterFor(
	providerID catalogs.ProviderID,
	offering catalogs.ProviderOffering,
) runtimecatalog.AdapterAvailability {
	adapter := runtimecatalog.AdapterAvailability{
		ProviderID: providerID,
		Registered: true,
		Operations: offering.Service.Operations,
	}
	for _, endpoint := range offering.Endpoints {
		adapter.EndpointTypes = append(adapter.EndpointTypes, endpoint.Type)
	}
	return adapter
}

func adapterObservations(
	t *testing.T,
	catalog *catalogs.Catalog,
) []providerstate.AdapterObservation {
	t.Helper()
	providerRecords := catalog.Providers().List()
	observations := make([]providerstate.AdapterObservation, 0, len(providerRecords))
	for _, provider := range providerRecords {
		offerings, err := catalog.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		observation := providerstate.AdapterObservation{
			ProviderID: provider.ID, State: providerstate.AdapterReady,
		}
		if len(offerings) == 0 {
			observation.State = providerstate.AdapterNoOfferings
			observation.Reason = providerstate.ReasonNoOfferings
		}
		observations = append(observations, observation)
	}
	return observations
}
