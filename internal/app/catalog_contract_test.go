package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	starmap "github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
)

func TestCatalogWideProviderActivation(t *testing.T) {
	application, err := New(validProductionConfig(t), withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.Contains(t, application.registry.ListProviders(), "openai")
	require.Greater(t, len(application.registry.ListProviders()), 1)
}

func TestConfiguredProviderMissingCatalogFailsStartup(t *testing.T) {
	runtime, err := runtimecatalog.OpenRuntime(
		context.Background(),
		storage.NewMockStore(),
		catalogSettings(testCatalogConfig()),
		func(string) (string, bool) { return "", false },
	)
	require.NoError(t, err)
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
	}
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": "inference-secret"},
		credentials.MaterialMetadata{Version: "test"},
	)

	_, err = buildRegistrations(
		runtime.ControlPlane().Current().Catalog(),
		transports,
		authentication,
		map[catalogs.ProviderID]providers.Configuration{
			"synthetic-provider": {
				Connector:        connectors.ProviderConfig{BaseURL: "https://provider.test"},
				CredentialSource: appStaticMaterialSource{material: material},
			},
		},
		func(
			_ string,
			_ []catalogs.EndpointType,
			value connectors.ProviderConfig,
		) (connectors.Connector, error) {
			return connectors.NewMockConnector(value), nil
		},
	)
	require.ErrorIs(t, err, providers.ErrProviderMissingCatalog)
}

func TestUnsupportedCatalogPrimitivesRemainUnavailable(t *testing.T) {
	t.Run("transport", func(t *testing.T) {
		transports, err := connectors.ProductionTransportRegistry()
		require.NoError(t, err)
		_, err = transports.NewProviderConnector(
			"acme",
			[]catalogs.EndpointType{"future-transport"},
			connectors.ProviderConfig{Enabled: true},
		)
		require.ErrorIs(t, err, connectors.ErrTransportUnsupported)
	})

	t.Run("authentication", func(t *testing.T) {
		builder, err := starmap.EmbeddedBuilder()
		require.NoError(t, err)
		catalog, err := builder.Build()
		require.NoError(t, err)
		provider, err := catalog.Provider(catalogs.ProviderIDOpenAI)
		require.NoError(t, err)
		provider.Credentials.Fields = append(provider.Credentials.Fields, catalogs.ProviderCredentialField{
			ID: "region", Kind: catalogs.ProviderCredentialFieldParameter, Required: true,
		})
		var profile catalogs.ProviderCredentialProfile
		for index := range provider.Credentials.Profiles {
			if provider.Credentials.Profiles[index].ID != "api-key" {
				continue
			}
			profile = catalogs.ProviderCredentialProfile{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAWSDefault,
				Fields: []catalogs.ProviderCredentialFieldID{"region"},
				ProtocolOptions: catalogs.ProviderAuthenticationProtocolOptions{
					AWSDefault: &catalogs.ProviderAWSDefaultProtocolOptions{RegionField: "region", Service: "bedrock"},
				},
			}
			provider.Credentials.Profiles[index] = profile
		}
		require.NotEmpty(t, profile.ID)
		catalogBuilder, err := catalogs.NewBuilderFrom(catalog)
		require.NoError(t, err)
		require.NoError(t, catalogBuilder.SetProvider(provider))
		catalog, err = catalogBuilder.Build()
		require.NoError(t, err)
		transports, err := connectors.ProductionTransportRegistry()
		require.NoError(t, err)
		authentication, err := providerauth.ProductionRegistry()
		require.NoError(t, err)
		activations, err := providers.Activate(
			catalog,
			transports,
			authentication,
			map[catalogs.ProviderID]providers.Configuration{
				catalogs.ProviderIDOpenAI: {
					Connector: connectors.ProviderConfig{Enabled: true},
					CredentialSource: appStaticMaterialSource{material: credentials.NewMaterial(
						profile,
						map[catalogs.ProviderCredentialFieldID]string{"region": "test-region"},
						credentials.MaterialMetadata{Version: "test"},
					)},
				},
			},
		)
		require.NoError(t, err)
		for _, activation := range activations {
			require.NotEqual(t, catalogs.ProviderIDOpenAI, activation.ProviderID)
		}
	})
}

type appStaticMaterialSource struct{ material credentials.Material }

func (s appStaticMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return s.material, nil
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

	runtime, err := runtimecatalog.OpenRuntime(
		context.Background(),
		storage.NewMockStore(),
		catalogSettings(testCatalogConfig()),
		func(string) (string, bool) { return "", false },
	)
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
