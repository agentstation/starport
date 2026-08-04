package app

import (
	"context"
	"sort"
	"testing"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
)

func TestActiveProviderIntersection(t *testing.T) {
	application, err := New(validProductionConfig(t), withBootstrapFactories(explicitTestFactories()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.Equal(t, []string{"openai"}, application.registry.ListProviders())
}

func TestConfiguredProviderMissingCatalogFailsStartup(t *testing.T) {
	runtime, err := runtimecatalog.OpenRuntime(context.Background(), storage.NewMockStore(), "")
	require.NoError(t, err)
	adapters, err := connectors.NewAdapterRegistry(connectors.AdapterDescriptor{
		ProviderID:    "synthetic-provider",
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		Factory: func(value connectors.ProviderConfig) (connectors.Connector, error) {
			return connectors.NewMockConnector(value), nil
		},
		Configured: connectors.APIKeyConfigured,
	})
	require.NoError(t, err)

	_, err = buildRegistrations(
		runtime.ControlPlane(),
		adapters,
		map[catalogs.ProviderID]connectors.ProviderConfig{
			"synthetic-provider": {APIKey: "inference-secret"},
		},
		func(_ string, value connectors.ProviderConfig) (connectors.Connector, error) {
			return connectors.NewMockConnector(value), nil
		},
	)
	require.ErrorIs(t, err, connectors.ErrAdapterProviderMissingCatalog)
}

func TestAuthPlanesAreIsolated(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-acquisition-secret")
	t.Setenv("STARPORT_PROVIDERS_OPENAI_API_KEY", "sk-inference-secret")
	cfg, err := config.NewLoader().WithEnvFiles().Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "sk-inference-secret", cfg.Providers.OpenAI.APIKey)

	runtime, err := runtimecatalog.OpenRuntime(context.Background(), storage.NewMockStore(), "")
	require.NoError(t, err)
	provider, err := runtime.ControlPlane().Current().Catalog().Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	acquisitionSecret, err := provider.APIKeyValue()
	require.NoError(t, err)
	require.Equal(t, "sk-acquisition-secret", acquisitionSecret)
	require.NotEqual(t, cfg.Providers.OpenAI.APIKey, acquisitionSecret)
}

func TestStarmapAcquisitionPublishesRefresh(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-acquisition-secret")
	var capturedSecret string
	runtime, err := runtimecatalog.OpenRuntime(
		context.Background(),
		storage.NewMockStore(),
		"",
		acquisition.WithProviderClientFactory(func(
			provider *catalogs.Provider,
		) (sources.ProviderClient, error) {
			var credentialErr error
			capturedSecret, credentialErr = provider.APIKeyValue()
			if credentialErr != nil {
				return nil, credentialErr
			}
			modelIDs := make([]string, 0, len(provider.Models))
			for modelID, model := range provider.Models {
				if model != nil && model.ModelRef != "" {
					modelIDs = append(modelIDs, modelID)
				}
			}
			sort.Strings(modelIDs)
			models := make([]catalogs.Model, 0, len(modelIDs))
			for _, modelID := range modelIDs {
				models = append(models, *provider.Models[modelID])
			}
			if len(models) > 0 {
				models[0].Name += " observed"
			}
			return staticProviderCatalogClient{models: models}, nil
		}),
	)
	require.NoError(t, err)
	before := runtime.ControlPlane().Current().GenerationID()

	result, err := runtime.Refresh(
		context.Background(),
		pkgsync.WithSources(sources.ProvidersID),
		pkgsync.WithProvider(catalogs.ProviderIDOpenAI),
	)
	require.NoError(t, err)
	require.Equal(t, "sk-acquisition-secret", capturedSecret)
	require.NotEmpty(t, result.GenerationID)
	require.NotEqual(t, before, runtime.ControlPlane().Current().GenerationID())
	require.Equal(t, result.GenerationID, runtime.ControlPlane().Current().GenerationID())
}

type staticProviderCatalogClient struct {
	models []catalogs.Model
}

func (c staticProviderCatalogClient) ListModels(context.Context) ([]catalogs.Model, error) {
	return append([]catalogs.Model(nil), c.models...), nil
}

func (staticProviderCatalogClient) IsAPIKeyRequired() bool { return true }
func (staticProviderCatalogClient) HasAPIKey() bool        { return true }

var _ sources.ProviderClient = staticProviderCatalogClient{}
