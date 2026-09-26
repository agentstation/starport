package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestCanonicalAliasResolutionAndRemovalRetainGeneration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	provider, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.SetAdapter(testAdapterAvailability(provider, offering, true)))
	state := client.CurrentCatalogState()
	builder, err := catalogs.NewBuilderFrom(state.Catalog)
	require.NoError(t, err)
	alias := catalogs.CanonicalAlias{ID: "legacy/retired-model", TargetID: offering.DefinitionID, PublisherID: "test-publisher", State: catalogs.CanonicalAliasActive}
	records := append(builder.CanonicalAliasRecords(), alias)
	require.NoError(t, builder.SetCanonicalAliasRecords(records))
	state.Catalog, err = builder.Build()
	require.NoError(t, err)
	state.GenerationID += "-renamed"
	state.Sequence++
	require.NoError(t, plane.Activate(state))
	retained := plane.Current()
	require.True(t, retained.Names(string(alias.ID)))
	route, ok := retained.ResolveRoute(string(alias.ID))
	require.True(t, ok)
	require.Equal(t, offering.DefinitionID, route.DefinitionID)
	for _, operation := range route.Operations {
		resolved, found := retained.ResolveOperation(string(alias.ID), operation)
		require.True(t, found)
		require.Equal(t, offering.DefinitionID, resolved.DefinitionID)
	}
	records[len(records)-1].State = catalogs.CanonicalAliasRemoved
	require.NoError(t, builder.SetCanonicalAliasRecords(records))
	state.Catalog, err = builder.Build()
	require.NoError(t, err)
	state.GenerationID += "-removed"
	state.Sequence++
	require.NoError(t, plane.Activate(state))
	require.False(t, plane.Current().Names(string(alias.ID)))
	_, ok = plane.Current().ResolveRoute(string(alias.ID))
	require.False(t, ok)
	require.True(t, plane.Current().Names(string(offering.DefinitionID)))
	require.True(t, retained.Names(string(alias.ID)))
	retainedRoute, ok := retained.ResolveRoute(string(alias.ID))
	require.True(t, ok)
	require.Equal(t, route.CatalogGenerationID, retainedRoute.CatalogGenerationID)
}

func TestActivationRefusesAliasOfferingNameCollision(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	original := plane.Current()
	var chosen catalogs.ProviderOffering
	for _, provider := range client.Catalog().Providers().List() {
		offerings, err := client.Catalog().ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			id := catalogs.ModelDefinitionID(string(provider.ID) + "/" + string(offering.ProviderModelID))
			if _, _, err := catalogs.ParseModelDefinitionID(id); err != nil {
				continue
			}
			if _, err := client.Catalog().Definition(id); err == nil {
				continue
			}
			chosen = offering
			break
		}
		if chosen.ProviderID != "" {
			break
		}
	}
	require.NotEmpty(t, chosen.ProviderID)
	state := client.CurrentCatalogState()
	builder, err := catalogs.NewBuilderFrom(state.Catalog)
	require.NoError(t, err)
	alias := catalogs.CanonicalAlias{ID: catalogs.ModelDefinitionID(string(chosen.ProviderID) + "/" + string(chosen.ProviderModelID)), TargetID: chosen.DefinitionID, PublisherID: "test-publisher", State: catalogs.CanonicalAliasRemoved}
	require.NoError(t, builder.SetCanonicalAliasRecords(append(builder.CanonicalAliasRecords(), alias)))
	state.Catalog, err = builder.Build()
	require.NoError(t, err)
	state.GenerationID += "-ambiguous"
	state.Sequence++
	require.Error(t, plane.Activate(state))
	require.Same(t, original, plane.Current())
}
