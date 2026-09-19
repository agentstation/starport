package connectors

import (
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
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

func TestDispatchRejectsMaterialRevokedWhileWaitingForConnection(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if received.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	transport := &http.Transport{MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	firstDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err == nil {
			var response *http.Response
			response, err = client.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
		}
		firstDone <- err
	}()
	<-firstStarted
	validity := credentials.NewMaterialValidity(time.Now().Add(time.Minute))
	material := credentials.NewMaterial(catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Placements: []catalogs.ProviderCredentialPlacement{{Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}},
	}, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, credentials.MaterialMetadata{}).WithValidity(validity)
	waiting := make(chan struct{})
	trace := &httptrace.ClientTrace{GetConn: func(string) { close(waiting) }}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	require.NoError(t, applyRequestAuthentication(material, request))
	secondDone := make(chan error, 1)
	go func() {
		response, err := doRequest(client, request)
		if response != nil {
			_ = response.Body.Close()
		}
		secondDone <- err
	}()
	<-waiting
	validity.Revoke()
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, credentials.ErrMaterialRevoked)
	require.EqualValues(t, 1, received.Load(), "revoked material must not reach the provider")
}
