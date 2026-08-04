package connectors

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestActiveProviderIntersection(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	adapters, err := ProductionAdapterRegistry()
	require.NoError(t, err)

	active, err := adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		catalogs.ProviderIDOpenAI:      {APIKey: "inference-key"},
		catalogs.ProviderIDMistralAI:   {},
		catalogs.ProviderIDAzureOpenAI: {},
	})
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, catalogs.ProviderIDOpenAI, active[0].ProviderID)
	require.Equal(t, "https://api.openai.com", active[0].Config.BaseURL)
}

func TestConfiguredProviderMissingCatalogFailsStartup(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	adapters, err := NewAdapterRegistry(AdapterDescriptor{
		ProviderID:    "synthetic-provider",
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		Factory: func(config ProviderConfig) (Connector, error) {
			return NewMockConnector(config), nil
		},
		Configured: APIKeyConfigured,
	})
	require.NoError(t, err)

	_, err = adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		"synthetic-provider": {APIKey: "inference-key"},
	})
	require.ErrorIs(t, err, ErrAdapterProviderMissingCatalog)
}

func TestConfiguredProviderWithoutOfferingFailsStartup(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	adapters, err := ProductionAdapterRegistry()
	require.NoError(t, err)

	_, err = adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		catalogs.ProviderIDOllama: {Enabled: true},
	})
	require.ErrorIs(t, err, ErrAdapterProviderMissingOffering)
}

func TestCatalogEndpointBindingsFailClosed(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	adapters, err := ProductionAdapterRegistry()
	require.NoError(t, err)

	_, err = adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		catalogs.ProviderIDGoogleVertex: {APIKey: "inference-token"},
	})
	require.ErrorIs(t, err, ErrAdapterConfigurationInvalid)
	require.ErrorContains(t, err, "endpoint binding")

	active, err := adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		catalogs.ProviderIDGoogleVertex: {
			APIKey: "inference-token",
			EndpointBindings: map[string]string{
				"project":  "tenant-project",
				"location": "us-test1",
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, active, 1)
}

func TestAdapterRegistryDrivesInferenceCredentialValidation(t *testing.T) {
	injected := errors.New("injected credential validation")
	called := false
	adapters, err := NewAdapterRegistry(AdapterDescriptor{
		ProviderID:    "synthetic-provider",
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		Factory: func(config ProviderConfig) (Connector, error) {
			return NewMockConnector(config), nil
		},
		Credential: InferenceCredentialDescriptor{
			Fields: []InferenceCredentialField{{Name: "token", Required: true, Sensitive: true}},
			Validate: func(_ context.Context, key map[string]string, _ map[string]any) error {
				called = key["token"] == "inference-secret"
				return injected
			},
		},
	})
	require.NoError(t, err)

	err = adapters.ValidateCredential(
		context.Background(),
		"synthetic-provider",
		map[string]string{"token": "inference-secret"},
		nil,
	)
	require.ErrorIs(t, err, injected)
	require.True(t, called)
}

func TestAuthPlanesAreIsolated(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "catalog-acquisition-secret")
	client, err := starmap.New()
	require.NoError(t, err)
	adapters, err := ProductionAdapterRegistry()
	require.NoError(t, err)

	active, err := adapters.Activate(client.Catalog(), map[catalogs.ProviderID]ProviderConfig{
		catalogs.ProviderIDOpenAI: {},
	})
	require.NoError(t, err)
	require.Empty(t, active, "Starmap acquisition credentials must not activate inference")

	err = adapters.ValidateCredential(context.Background(), catalogs.ProviderIDOpenAI, nil, nil)
	require.Error(t, err)
}

func TestAdapterRegistryAppliesInferenceAuthentication(t *testing.T) {
	adapters, err := ProductionAdapterRegistry()
	require.NoError(t, err)
	tests := []struct {
		provider catalogs.ProviderID
		header   string
		value    string
	}{
		{catalogs.ProviderIDOpenAI, "Authorization", "Bearer inference-key"},
		{catalogs.ProviderIDAnthropic, "x-api-key", "inference-key"},
		{catalogs.ProviderIDGoogleAIStudio, "x-goog-api-key", "inference-key"},
		{catalogs.ProviderIDGoogleVertex, "Authorization", "Bearer inference-key"},
		{catalogs.ProviderIDAzureOpenAI, "api-key", "inference-key"},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, "https://provider.test/inference", nil)
			require.NoError(t, err)
			require.NoError(t, adapters.ApplyInferenceAuth(test.provider, request, "inference-key"))
			require.Equal(t, test.value, request.Header.Get(test.header))
			require.NotContains(t, request.URL.String(), "inference-key")
		})
	}
}
