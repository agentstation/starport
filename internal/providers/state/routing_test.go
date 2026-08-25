package state

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// TestRoutingProjectionNeverOutlivesItsCatalogGeneration is the whole reason
// routing is bound to a generation instead of stamped onto the offering
// records. A reachability claim answers "can a request reach this offering in
// the generation the gateway is serving". Carrying a previous generation's
// answer forward, or accepting one computed against a generation the store no
// longer holds, tells an operator that a model works when it may not exist.
func TestRoutingProjectionNeverOutlivesItsCatalogGeneration(t *testing.T) {
	catalog := embeddedCatalog(t)
	offerings, err := catalog.ProviderOfferings(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	require.NotEmpty(t, offerings)
	modelID := offerings[0].ProviderModelID

	store := New()
	require.NoError(t, store.PublishCatalog(
		"generation-1", catalog, catalogAdapterObservations(catalog),
	))

	// Before any planning generation, reachability is unknown rather than a
	// guess in either direction.
	require.Equal(t,
		RoutingStatus{State: RoutingUnknown},
		offeringRouting(t, store, catalogs.ProviderIDOpenAI, modelID),
	)

	// A verdict computed against a generation the store does not hold is
	// discarded instead of overwriting the current projection.
	require.NoError(t, store.PublishRouting("generation-0", []RoutingObservation{{
		ProviderID: catalogs.ProviderIDOpenAI, ProviderModelID: modelID, Routable: true,
	}}))
	require.Equal(t,
		RoutingStatus{State: RoutingUnknown},
		offeringRouting(t, store, catalogs.ProviderIDOpenAI, modelID),
	)

	require.NoError(t, store.PublishRouting("generation-1", []RoutingObservation{{
		ProviderID:      catalogs.ProviderIDOpenAI,
		ProviderModelID: modelID,
		Routable:        false,
		Reason:          ReasonOperationUnsupported,
	}}))
	require.Equal(t,
		RoutingStatus{State: RoutingUnroutable, Reason: ReasonOperationUnsupported},
		offeringRouting(t, store, catalogs.ProviderIDOpenAI, modelID),
	)

	// A new catalog generation invalidates every verdict derived from the old
	// one, even though the offering identity is unchanged.
	require.NoError(t, store.PublishCatalog(
		"generation-2", catalog, catalogAdapterObservations(catalog),
	))
	require.Equal(t,
		RoutingStatus{State: RoutingUnknown},
		offeringRouting(t, store, catalogs.ProviderIDOpenAI, modelID),
	)
}

// TestRoutingIsSeparateFromHealth guards the distinction the projection exists
// to draw. An offering the planner cannot reach stays healthy in circuit terms,
// because no attempt ever fails against it. Collapsing the two would hide
// exactly the offerings this signal is meant to surface.
func TestRoutingIsSeparateFromHealth(t *testing.T) {
	catalog := embeddedCatalog(t)
	offerings, err := catalog.ProviderOfferings(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	require.NotEmpty(t, offerings)
	modelID := offerings[0].ProviderModelID

	store := New()
	require.NoError(t, store.PublishCatalog(
		"generation-1", catalog, catalogAdapterObservations(catalog),
	))
	require.NoError(t, store.PublishRouting("generation-1", []RoutingObservation{{
		ProviderID:      catalogs.ProviderIDOpenAI,
		ProviderModelID: modelID,
		Routable:        false,
		Reason:          ReasonOperationUnsupported,
	}}))

	status := offeringStatus(t, store, catalogs.ProviderIDOpenAI, modelID)
	require.Equal(t, "healthy", string(status.State))
	require.Equal(t, ReasonNone, status.Reason)
	require.Equal(t, RoutingUnroutable, status.Routing.State)
	require.Equal(t, ReasonOperationUnsupported, status.Routing.Reason)
}

// TestRoutingProjectionRejectsUnidentifiedVerdicts keeps an incomplete
// projection from being published as a partial truth.
func TestRoutingProjectionRejectsUnidentifiedVerdicts(t *testing.T) {
	catalog := embeddedCatalog(t)
	store := New()
	require.NoError(t, store.PublishCatalog(
		"generation-1", catalog, catalogAdapterObservations(catalog),
	))

	require.ErrorIs(t, store.PublishRouting("generation-1", []RoutingObservation{{
		ProviderModelID: "gpt-4o", Routable: true,
	}}), ErrRoutingProjectionIncomplete)

	require.ErrorIs(t, store.PublishRouting("generation-1", []RoutingObservation{
		{ProviderID: catalogs.ProviderIDOpenAI, ProviderModelID: "gpt-4o", Routable: true},
		{ProviderID: catalogs.ProviderIDOpenAI, ProviderModelID: "gpt-4o", Routable: false},
	}), ErrRoutingProjectionIncomplete)

	require.ErrorIs(t, store.PublishRouting("", nil), ErrCatalogRequired)
}

func offeringStatus(
	t *testing.T,
	store *Store,
	providerID catalogs.ProviderID,
	providerModelID catalogs.ProviderModelID,
) OfferingStatus {
	t.Helper()
	for _, provider := range store.Snapshot().Providers {
		if provider.ProviderID != providerID {
			continue
		}
		for _, offering := range provider.Offerings {
			if offering.ProviderModelID == providerModelID {
				return offering
			}
		}
	}
	t.Fatalf("no offering status for %s/%s", providerID, providerModelID)
	return OfferingStatus{}
}

func offeringRouting(
	t *testing.T,
	store *Store,
	providerID catalogs.ProviderID,
	providerModelID catalogs.ProviderModelID,
) RoutingStatus {
	t.Helper()
	return offeringStatus(t, store, providerID, providerModelID).Routing
}
