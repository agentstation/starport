package auth

import (
	"net/http"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
)

func TestRequestAuthenticationAppliesCatalogPlacements(t *testing.T) {
	registry, err := ProductionRegistry()
	require.NoError(t, err)
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key", "tenant"},
		Placements: []catalogs.ProviderCredentialPlacement{
			{
				Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
				Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
			},
			{
				Field: "tenant", Kind: catalogs.ProviderCredentialPlacementQuery,
				Name: "tenant", Scheme: catalogs.ProviderCredentialSchemeDirect,
			},
		},
	}
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{
			"api-key": "secret-value", "tenant": "tenant-a",
		},
		credentials.MaterialMetadata{Version: "test"},
	)
	request, err := http.NewRequest(http.MethodPost, "https://provider.example/inference", nil)
	require.NoError(t, err)
	require.NoError(t, registry.Apply(material, request))
	require.Equal(t, "Bearer secret-value", request.Header.Get("Authorization"))
	require.Equal(t, "tenant-a", request.URL.Query().Get("tenant"))
}

func TestRequestAuthenticationRejectsQueryPlacementOnHTTP(t *testing.T) {
	registry, err := ProductionRegistry()
	require.NoError(t, err)
	profile := catalogs.ProviderCredentialProfile{
		ID: "query", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementQuery,
			Name: "key", Scheme: catalogs.ProviderCredentialSchemeDirect,
		}},
	}
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": "secret-value"},
		credentials.MaterialMetadata{Version: "test"},
	)
	request, err := http.NewRequest(http.MethodPost, "http://provider.example/inference", nil)
	require.NoError(t, err)
	require.ErrorContains(t, registry.Apply(material, request), "requires an HTTPS request")
	require.Empty(t, request.URL.RawQuery)
}

func TestGoogleDefaultAppliesQuotaProjectFromTypedOptions(t *testing.T) {
	registry, err := ProductionRegistry()
	require.NoError(t, err)
	profile := catalogs.ProviderCredentialProfile{
		ID: "workload-identity", Primitive: catalogs.ProviderAuthenticationGoogleDefault,
		Fields: []catalogs.ProviderCredentialFieldID{"access-token", "quota-project"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
		ProtocolOptions: catalogs.ProviderAuthenticationProtocolOptions{
			GoogleDefault: &catalogs.ProviderGoogleDefaultProtocolOptions{
				QuotaProjectField: "quota-project",
			},
		},
	}
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{
			"access-token": "token", "quota-project": "billing-project",
		},
		credentials.MaterialMetadata{Version: "test"},
	)
	request, err := http.NewRequest(http.MethodPost, "https://provider.example/inference", nil)
	require.NoError(t, err)
	require.NoError(t, registry.Apply(material, request))
	require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
	require.Equal(t, "billing-project", request.Header.Get("x-goog-user-project"))
}
