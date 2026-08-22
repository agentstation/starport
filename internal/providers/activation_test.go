package providers

import (
	"testing"

	starmap "github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
	providerstate "github.com/agentstation/starport/internal/providers/state"
)

func TestCatalogProviderRegistersWithoutOperatorMaterial(t *testing.T) {
	embedded, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	catalog, err := embedded.Build()
	require.NoError(t, err)
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)

	activations, err := Activate(catalog, transports, authentication, nil)
	require.NoError(t, err)
	require.NotEmpty(t, activations)
	require.Contains(t, activationProviderIDs(activations), catalogs.ProviderIDOpenAI)
	for _, activation := range activations {
		require.Nil(t, activation.Configuration.CredentialSource)
	}
}

func TestNoAuthProviderRegistersWithoutMaterial(t *testing.T) {
	embedded, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	catalog, err := embedded.Build()
	require.NoError(t, err)
	provider, err := catalog.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	provider.Credentials.Profiles = []catalogs.ProviderCredentialProfile{{
		ID: "public", Primitive: catalogs.ProviderAuthenticationNone,
	}}
	provider.Credentials.Inference = catalogs.ProviderCredentialPlane{
		Alternatives: []catalogs.ProviderCredentialProfileID{"public"},
	}
	provider.Credentials.CatalogAcquisition = catalogs.ProviderCredentialPlane{
		Alternatives: []catalogs.ProviderCredentialProfileID{"public"},
	}
	builder, err := catalogs.NewBuilderFrom(catalog)
	require.NoError(t, err)
	require.NoError(t, builder.SetProvider(provider))
	catalog, err = builder.Build()
	require.NoError(t, err)
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)

	activations, err := Activate(catalog, transports, authentication, nil)
	require.NoError(t, err)
	var found bool
	for _, activation := range activations {
		if activation.ProviderID != catalogs.ProviderIDOpenAI {
			continue
		}
		found = true
		require.False(t, activation.RequiresAuth)
		require.False(t, activation.Anonymous.Empty())
	}
	require.True(t, found)
}

func TestActivationSkipsUnsupportedAuthenticationPrimitive(t *testing.T) {
	embedded, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	catalog, err := embedded.Build()
	require.NoError(t, err)
	provider, err := catalog.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)

	provider.Credentials.Fields = append(provider.Credentials.Fields, catalogs.ProviderCredentialField{
		ID: "region", Kind: catalogs.ProviderCredentialFieldParameter, Required: true,
	})
	for index := range provider.Credentials.Profiles {
		if provider.Credentials.Profiles[index].ID != "api-key" {
			continue
		}
		provider.Credentials.Profiles[index] = catalogs.ProviderCredentialProfile{
			ID: "api-key", Primitive: catalogs.ProviderAuthenticationAWSDefault,
			Fields: []catalogs.ProviderCredentialFieldID{"region"},
			ProtocolOptions: catalogs.ProviderAuthenticationProtocolOptions{
				AWSDefault: &catalogs.ProviderAWSDefaultProtocolOptions{
					RegionField: "region", Service: "bedrock",
				},
			},
		}
	}

	builder, err := catalogs.NewBuilderFrom(catalog)
	require.NoError(t, err)
	require.NoError(t, builder.SetProvider(provider))
	catalog, err = builder.Build()
	require.NoError(t, err)

	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	activations, err := Activate(
		catalog,
		transports,
		authentication,
		nil,
	)
	require.NoError(t, err)
	require.NotContains(t, activationProviderIDs(activations), catalogs.ProviderIDOpenAI)
	assessments, err := Assess(catalog, transports, authentication, nil)
	require.NoError(t, err)
	for _, assessment := range assessments {
		if assessment.Observation.ProviderID != catalogs.ProviderIDOpenAI {
			continue
		}
		require.Nil(t, assessment.Activation)
		require.Equal(
			t,
			providerstate.AdapterUnsupportedAuthentication,
			assessment.Observation.State,
		)
		return
	}
	t.Fatal("OpenAI assessment was not projected")
}

func activationProviderIDs(activations []Activation) []catalogs.ProviderID {
	result := make([]catalogs.ProviderID, len(activations))
	for index, activation := range activations {
		result[index] = activation.ProviderID
	}
	return result
}
