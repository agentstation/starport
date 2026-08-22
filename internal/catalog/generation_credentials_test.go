package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// TestGenerationRoundTripPreservesCredentialContracts locks in that a
// provider's credential contract — including the ambient environment names
// that drive operator credential resolution — survives the generation
// store's persist-and-load cycle. A dropped Environment list silently
// disables ambient key discovery for that provider.
func TestGenerationRoundTripPreservesCredentialContracts(t *testing.T) {
	provider := catalogs.Provider{
		ID: "credential-provider", Name: "Credential Provider",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
				Environment: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetProvider(provider))
	source, err := builder.Build()
	require.NoError(t, err)

	generation := runtimeTestGeneration(
		t, "credential-generation", source, time.Now().UTC(),
	)
	store, err := NewGenerationStore(storage.NewMockStore())
	require.NoError(t, err)
	require.NoError(t, store.Commit(t.Context(), generation, ""))

	loaded, err := store.Current(t.Context())
	require.NoError(t, err)
	catalog, err := catalogs.DecodeCatalogPayload(loaded.Payload)
	require.NoError(t, err)
	decoded, err := catalog.Provider(provider.ID)
	require.NoError(t, err)
	require.NotNil(t, decoded.Credentials)
	require.Len(t, decoded.Credentials.Fields, 1)
	require.Equal(
		t,
		[]string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		decoded.Credentials.Fields[0].Environment,
	)
	require.Len(t, decoded.Credentials.Profiles, 1)
	require.True(t, decoded.Credentials.Inference.Required)
}
