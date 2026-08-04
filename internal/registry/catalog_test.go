package registry

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestRegistryProviderMetadataUsesRoutableSnapshot(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerID, _ := firstCatalogOffering(t, client.Catalog())
	provider := string(providerID)
	connector := connectors.NewMockConnector(connectors.ProviderConfig{})
	registry := NewEmptyWithCatalog(plane)
	require.NoError(t, registry.Register(provider, connector))
	require.NoError(t, registry.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	metadata := registry.GetProviderMetadata()
	require.Len(t, metadata, 1)
	require.Equal(t, provider, metadata[0].ID)
	require.NotEmpty(t, metadata[0].Name)
	require.True(t, metadata[0].RequiresAuth)

	require.NoError(t, plane.RemoveAdapter(providerID))
	require.Empty(t, registry.GetProviderMetadata())
}

func TestRegistryDoesNotInventProviderMetadataWithoutCatalog(t *testing.T) {
	require.Empty(t, NewEmpty().GetProviderMetadata())
}

func firstCatalogOffering(
	t *testing.T,
	source *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	offerings, err := source.ProviderOfferings(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	if len(offerings) > 0 {
		return catalogs.ProviderIDOpenAI, offerings[0]
	}
	t.Fatal("Starmap embedded catalog has no OpenAI offering")
	return "", catalogs.ProviderOffering{}
}
