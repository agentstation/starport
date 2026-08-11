package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	application, err := New(validProductionConfig(t), withRuntimeFactories(explicitTestFactories()))
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

func TestInferenceCredentialsNeverEnterCatalogState(t *testing.T) {
	testAuthPlanesAreIsolated(t)
}

func TestAuthPlanesAreIsolated(t *testing.T) {
	testAuthPlanesAreIsolated(t)
}

func testAuthPlanesAreIsolated(t *testing.T) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "sk-acquisition-secret")
	cfg, err := config.NewLoader().
		WithEnvironment(map[string]string{"STARPORT_OPENAI_API_KEY": "sk-inference-secret"}).
		WithEnvFiles().
		Load(context.Background())
	require.NoError(t, err)

	runtime, err := runtimecatalog.OpenRuntime(context.Background(), storage.NewMockStore(), "")
	require.NoError(t, err)
	require.NoError(t, cfg.ResolveProviders(
		context.Background(), runtime.ControlPlane().Current().Catalog().Providers(),
	))
	inferenceSecret, found := cfg.Providers[catalogs.ProviderIDOpenAI].Material.Value("api-key")
	require.True(t, found)
	require.Equal(t, "sk-inference-secret", inferenceSecret)
	provider, err := runtime.ControlPlane().Current().Catalog().Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	acquisitionField, found := credentialFieldForEnvironment(provider.Credentials, "OPENAI_API_KEY")
	require.True(t, found)
	require.Equal(t, catalogs.ProviderCredentialFieldSecret, acquisitionField.Kind)
	require.NotContains(t, fmt.Sprintf("%#v", provider), "sk-acquisition-secret")
	require.NotEqual(t, inferenceSecret, "sk-acquisition-secret")
	catalogBytes, err := json.Marshal(runtime.ControlPlane().Current().Catalog())
	require.NoError(t, err)
	require.NotContains(t, string(catalogBytes), inferenceSecret)
	require.NotContains(t, string(catalogBytes), "sk-acquisition-secret")
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
			credentialField, found := credentialFieldForEnvironment(
				provider.Credentials,
				"OPENAI_API_KEY",
			)
			if !found {
				return nil, fmt.Errorf("OPENAI_API_KEY credential field is required")
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
			return staticProviderCatalogClient{
				models: models,
				capture: func(material sources.ProviderCredentialMaterial) error {
					value, exists := material.Value(credentialField.ID)
					if !exists {
						return fmt.Errorf("resolved %s credential is required", credentialField.ID)
					}
					capturedSecret = value
					return nil
				},
			}, nil
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
	models  []catalogs.Model
	capture func(sources.ProviderCredentialMaterial) error
}

func (c staticProviderCatalogClient) ListModels(
	_ context.Context,
	material sources.ProviderCredentialMaterial,
) ([]catalogs.Model, error) {
	if c.capture != nil {
		if err := c.capture(material); err != nil {
			return nil, err
		}
	}
	return append([]catalogs.Model(nil), c.models...), nil
}

var _ sources.ProviderClient = staticProviderCatalogClient{}

func credentialFieldForEnvironment(
	credentials *catalogs.ProviderCredentials,
	environment string,
) (catalogs.ProviderCredentialField, bool) {
	if credentials == nil {
		return catalogs.ProviderCredentialField{}, false
	}
	for _, field := range credentials.Fields {
		for _, name := range field.Environment {
			if name == environment {
				return field, true
			}
		}
	}
	return catalogs.ProviderCredentialField{}, false
}
