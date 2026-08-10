package connectors

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestVertexAIUsesCatalogDerivedBaseURL(t *testing.T) {
	connector, err := NewVertexAIConnector(ProviderConfig{
		BaseURL:  "https://catalog.example/v1",
		APIKey:   "test-key",
		AuthMode: providerauth.ModeStatic,
	})
	require.NoError(t, err)
	require.Equal(t, "https://catalog.example/v1", connector.config.BaseURL)
}
