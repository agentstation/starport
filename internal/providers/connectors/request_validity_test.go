package connectors

import (
	"net/http"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
)

func TestDispatchRejectsMaterialRevokedAfterAuthentication(t *testing.T) {
	validity := credentials.NewMaterialValidity(time.Now().Add(time.Minute))
	material := credentials.NewMaterial(catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Placements: []catalogs.ProviderCredentialPlacement{{Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}},
	}, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, credentials.MaterialMetadata{}).WithValidity(validity)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/inference", nil)
	require.NoError(t, err)
	require.NoError(t, applyRequestAuthentication(material, request))
	validity.Revoke()
	calls := 0
	client := &http.Client{Transport: validityTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}
	_, err = doRequest(client, request)
	require.ErrorIs(t, err, credentials.ErrMaterialRevoked)
	require.Zero(t, calls)
}

type validityTransport func(*http.Request) (*http.Response, error)

func (f validityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestMaterialValidityFailureDoesNotMarkProviderUnavailable(t *testing.T) {
	for _, err := range []error{credentials.ErrMaterialExpired, credentials.ErrMaterialRevoked} {
		normalized := NormalizeFailure("acme", err)
		require.Equal(t, failure.GatewayUnavailable, normalized.Kind())
		require.True(t, normalized.Retryable())
		require.NotEqual(t, failure.ScopeOffering, normalized.StateScope())
	}
}
