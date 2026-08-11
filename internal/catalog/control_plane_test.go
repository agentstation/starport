package catalog

import (
	"fmt"
	"sync"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/availability"
	"github.com/stretchr/testify/require"
)

func TestRoutableSnapshotGenerationConsistency(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)

	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, true),
	}))

	want := client.CurrentCatalogState()
	snapshot := plane.Current()
	require.Equal(t, want.GenerationID, snapshot.GenerationID())
	require.Equal(t, want.Sequence, snapshot.CatalogSequence())
	require.NotEmpty(t, snapshot.Routes())
	for _, route := range snapshot.Routes() {
		require.Equal(t, want.GenerationID, route.CatalogGenerationID)
	}

	retained := snapshot
	next := want
	next.GenerationID = want.GenerationID + "-next"
	next.Sequence++
	require.NoError(t, plane.Activate(next))
	require.Equal(t, want.GenerationID, retained.GenerationID())
	require.Equal(t, next.GenerationID, plane.Current().GenerationID())
}

func TestCatalogActivationIsAtomic(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)

	plane, err := Open(client)
	require.NoError(t, err)

	base := client.CurrentCatalogState()
	stateA := base
	stateA.GenerationID = "generation-a"
	stateA.Sequence = 101
	stateB := base
	stateB.GenerationID = "generation-b"
	stateB.Sequence = 202

	valid := map[string]uint64{
		stateA.GenerationID: stateA.Sequence,
		stateB.GenerationID: stateB.Sequence,
	}
	require.NoError(t, plane.Activate(stateA))

	const activations = 1_000
	var writers sync.WaitGroup
	errors := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			for index := 0; index < activations; index++ {
				state := stateA
				if (worker+index)%2 == 0 {
					state = stateB
				}
				if err := plane.Activate(state); err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}

	for index := 0; index < activations; index++ {
		snapshot := plane.Current()
		wantSequence, ok := valid[snapshot.GenerationID()]
		require.True(t, ok, fmt.Sprintf("unexpected generation %q", snapshot.GenerationID()))
		require.Equal(t, wantSequence, snapshot.CatalogSequence())
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func TestUnavailableAdapterIsNotAdvertised(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)

	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, false),
	}))
	require.Empty(t, plane.Current().RoutesForProvider(providerID))

	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, true),
	}))
	routes := plane.Current().RoutesForProvider(providerID)
	require.NotEmpty(t, routes)
	resolved, found := plane.Current().ResolveRoute(
		string(providerID) + "/" + string(offering.ProviderModelID),
	)
	require.True(t, found)
	require.Equal(t, offering.Key(), resolved.Key())

	require.NoError(t, plane.ReplaceAdapters(nil))
	require.Empty(t, plane.Current().RoutesForProvider(providerID))
}

func TestNilRoutableSnapshotAccessorsFailClosed(t *testing.T) {
	var snapshot *RoutableSnapshot
	_, err := snapshot.Definition("author/model")
	require.ErrorIs(t, err, ErrCatalogRequired)
	_, err = snapshot.Offering(Route{ProviderID: "provider", ProviderModelID: "model"})
	require.ErrorIs(t, err, ErrCatalogRequired)
}

func TestAvailabilityOwnerPublishesDerivedRoutableView(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, true),
	}))
	require.NotEmpty(t, plane.Current().RoutesForProvider(providerID))

	record := availability.Record{
		Offering: availability.Offering{
			ProviderID:      string(offering.ProviderID),
			ProviderModelID: string(offering.ProviderModelID),
		},
		State: availability.StateOpen,
	}
	require.NoError(t, plane.PublishAvailability(availability.Snapshot{Revision: 1, Records: []availability.Record{record}}))
	routeID := string(providerID) + "/" + string(offering.ProviderModelID)
	_, found := plane.Current().ResolveRoute(routeID)
	require.False(t, found)

	record.State = availability.StateHalfOpen
	require.NoError(t, plane.PublishAvailability(availability.Snapshot{Revision: 2, Records: []availability.Record{record}}))
	_, found = plane.Current().ResolveRoute(routeID)
	require.True(t, found)
}

func firstOffering(t *testing.T, source *catalogs.Catalog) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	require.NotNil(t, source)
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		if err == nil && len(offerings) > 0 {
			return provider.ID, offerings[0]
		}
	}
	t.Fatal("Starmap embedded catalog has no provider offering")
	return "", catalogs.ProviderOffering{}
}

func testAdapterAvailability(
	providerID catalogs.ProviderID,
	offering catalogs.ProviderOffering,
	configured bool,
) AdapterAvailability {
	types := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	return AdapterAvailability{
		ProviderID:    providerID,
		Registered:    true,
		Configured:    configured,
		Operations:    append([]catalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
		BaseURL:       "https://provider.test",
	}
}
