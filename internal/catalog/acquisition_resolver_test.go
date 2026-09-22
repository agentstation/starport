package catalog

import (
	"reflect"
	"testing"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/stretchr/testify/require"
)

// TestAcquisitionResolverReadsOnlyDeploymentLookup proves catalog acquisition
// reads the deployment alone.
//
// The resolver holds exactly one field, the deployment lookup, so it can reach
// no keyring, no account store, and no BYOK record. The structural check states
// that, and the behavior checks prove what the one field supplies: a derived
// gateway name, a conventional ambient name, and a refusal when the deployment
// supplies neither.
func TestAcquisitionResolverReadsOnlyDeploymentLookup(t *testing.T) {
	resolverType := reflect.TypeOf(AcquisitionResolver{})
	require.Equal(t, 1, resolverType.NumField(),
		"the acquisition resolver must hold the deployment lookup alone")
	require.Equal(t, "lookup", resolverType.Field(0).Name)
	require.Equal(t, reflect.TypeOf(DeploymentLookup(nil)), resolverType.Field(0).Type)

	provider := acquisitionTestProvider(true)
	tests := []struct {
		name      string
		values    map[string]string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "derived gateway name resolves",
			values:    map[string]string{"STARPORT_TESTPROVIDER_API_KEY": "derived"},
			wantValue: "derived",
		},
		{
			name:      "conventional ambient name resolves",
			values:    map[string]string{"TESTPROVIDER_API_KEY": "ambient"},
			wantValue: "ambient",
		},
		{
			name: "the derived gateway name wins",
			values: map[string]string{
				"STARPORT_TESTPROVIDER_API_KEY": "derived",
				"TESTPROVIDER_API_KEY":          "ambient",
			},
			wantValue: "derived",
		},
		{
			name:    "an empty value resolves nothing",
			values:  map[string]string{"TESTPROVIDER_API_KEY": "   "},
			wantErr: true,
		},
		{
			name:    "a deployment that supplies nothing makes the provider ineligible",
			values:  nil,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewAcquisitionResolver(func(name string) (string, bool) {
				value, found := test.values[name]
				return value, found
			})

			material, err := resolver.ResolveCatalog(t.Context(), provider)
			if test.wantErr {
				var missing *starmaperrors.NotFoundError
				require.ErrorAs(t, err, &missing)
				return
			}
			require.NoError(t, err)
			value, found := material.Value("api-key")
			require.True(t, found)
			require.Equal(t, test.wantValue, value)
		})
	}
}

// TestAcquisitionResolverRefusesWithoutDeploymentLookup proves the resolver
// reads no other plane when the deployment supplies no lookup. It refuses
// instead of falling back.
func TestAcquisitionResolverRefusesWithoutDeploymentLookup(t *testing.T) {
	resolver := NewAcquisitionResolver(nil)
	_, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(true))
	var configErr *starmaperrors.ConfigError
	require.ErrorAs(t, err, &configErr)
}

// TestAcquisitionResolverAllowsUnauthenticatedPlane proves a provider whose
// catalog plane needs no credential stays eligible.
func TestAcquisitionResolverAllowsUnauthenticatedPlane(t *testing.T) {
	resolver := NewAcquisitionResolver(func(string) (string, bool) { return "", false })
	material, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(false))
	require.NoError(t, err)
	_, found := material.Value("api-key")
	require.False(t, found)
}

// TestAcquisitionResolverComposesTheAcquirer proves the acquirer accepts this
// resolver, so the deployment plane is the one the connected runtime reads.
func TestAcquisitionResolverComposesTheAcquirer(t *testing.T) {
	acquirer, err := acquisition.NewAcquirer(
		acquisition.WithAcquirerCredentialResolver(
			NewAcquisitionResolver(func(string) (string, bool) { return "", false }),
		),
	)
	require.NoError(t, err)
	require.NotNil(t, acquirer)
}

// acquisitionTestProvider returns one provider whose catalog-acquisition plane
// names a single API key profile.
func acquisitionTestProvider(required bool) *catalogs.Provider {
	return &catalogs.Provider{
		ID:   "testprovider",
		Name: "Test Provider",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID:          "api-key",
				Required:    true,
				Environment: []string{"TESTPROVIDER_API_KEY"},
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID:     "api-key",
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
			}},
			CatalogAcquisition: catalogs.ProviderCredentialPlane{
				Required:     required,
				Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
}
