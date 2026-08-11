package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestActivationRejectsUnsupportedAuthenticationPrimitive(t *testing.T) {
	embedded, err := catalogs.NewEmbedded()
	require.NoError(t, err)
	catalog, err := embedded.Build()
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
		provider.Credentials.Profiles[index] = catalogs.ProviderCredentialProfile{
			ID: "api-key", Primitive: catalogs.ProviderAuthenticationAWSDefault,
			Fields: []catalogs.ProviderCredentialFieldID{"region"},
			ProtocolOptions: catalogs.ProviderAuthenticationProtocolOptions{
				AWSDefault: &catalogs.ProviderAWSDefaultProtocolOptions{
					RegionField: "region", Service: "bedrock",
				},
			},
		}
		profile = provider.Credentials.Profiles[index]
	}
	require.NotEmpty(t, profile.ID)

	builder, err := catalogs.NewBuilderFrom(catalog)
	require.NoError(t, err)
	require.NoError(t, builder.SetProvider(provider))
	catalog, err = builder.Build()
	require.NoError(t, err)

	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	_, err = Activate(
		catalog,
		transports,
		authentication,
		map[catalogs.ProviderID]Configuration{
			catalogs.ProviderIDOpenAI: {
				Connector: connectors.ProviderConfig{Enabled: true},
				CredentialSource: activationMaterialSource{material: credentials.NewMaterial(
					profile,
					map[catalogs.ProviderCredentialFieldID]string{"region": "us-east-1"},
					credentials.MaterialMetadata{Version: "test"},
				)},
				Profile: profile,
			},
		},
	)
	require.True(t, errors.Is(err, providerauth.ErrPrimitiveUnsupported), "%v", err)
}

type activationMaterialSource struct{ material credentials.Material }

func (s activationMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return s.material, nil
}
