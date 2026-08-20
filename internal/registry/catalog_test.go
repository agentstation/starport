package registry

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

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
