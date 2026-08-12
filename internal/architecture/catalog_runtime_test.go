package architecture

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestStarportProductionHasNoProviderRoster(t *testing.T) {
	for _, removed := range []string{
		"../providers/connectors/adapter_registry.go",
		"../providers/connectors/adapter_descriptors.go",
	} {
		_, err := os.Stat(removed)
		require.ErrorIs(t, err, os.ErrNotExist, "%s must remain removed", removed)
	}

	for _, path := range []string{
		"../providers/activation.go",
		"../providers/connectors/transport_registry.go",
		"../app/providers.go",
		"../registry/registry.go",
	} {
		source, err := os.ReadFile(filepath.Clean(path))
		require.NoError(t, err)
		text := string(source)
		for _, providerFact := range []string{
			"ProviderIDOpenAI", "ProviderIDAnthropic", "ProviderIDGoogleVertex",
			"ProviderIDGroq", "ProviderIDMistralAI", "ProviderIDAzureOpenAI",
		} {
			require.NotContainsf(t, text, providerFact, "%s contains provider membership", path)
		}
		require.NotContainsf(t, text, "switch provider", "%s selects behavior by provider ID", path)
	}

	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := transports.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		connectors.ProviderConfig{BaseURL: "https://provider.example"},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	require.Equal(t, "acme", connector.Name())
}

func TestTransportAuthenticationRegistriesUsePrimitives(t *testing.T) {
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	require.ElementsMatch(t, []catalogs.EndpointType{
		catalogs.EndpointTypeOpenAI,
		catalogs.EndpointTypeAnthropic,
		catalogs.EndpointTypeGoogle,
		catalogs.EndpointTypeGoogleCloud,
		catalogs.EndpointTypeOllama,
	}, transports.EndpointTypes())
	require.True(t, transports.Supports(
		catalogs.EndpointTypeOpenAI,
		catalogs.ProviderOperationChatCompletions,
	))

	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	for _, primitive := range []catalogs.ProviderAuthenticationPrimitive{
		catalogs.ProviderAuthenticationNone,
		catalogs.ProviderAuthenticationAPIKey,
		catalogs.ProviderAuthenticationBearerToken,
		catalogs.ProviderAuthenticationGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault,
	} {
		require.Truef(t, authentication.Supports(primitive), "primitive %s is missing", primitive)
	}
	require.False(t, authentication.Supports(catalogs.ProviderAuthenticationAWSDefault))

	request, err := http.NewRequest(http.MethodPost, "https://provider.example", strings.NewReader("{}"))
	require.NoError(t, err)
	material := credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{
			ID: "aws", Primitive: catalogs.ProviderAuthenticationAWSDefault,
		},
		nil,
		credentials.MaterialMetadata{Version: "test"},
	)
	err = authentication.Apply(material, request)
	require.True(t, errors.Is(err, providerauth.ErrPrimitiveUnsupported))
}
