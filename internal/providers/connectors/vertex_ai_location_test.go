package connectors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVertexAIUsesCatalogDerivedBaseURL(t *testing.T) {
	connector, err := NewVertexAIConnector(ProviderConfig{
		BaseURL: "https://catalog.example/v1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://catalog.example/v1", connector.config.BaseURL)
}
