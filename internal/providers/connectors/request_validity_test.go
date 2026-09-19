package connectors

import (
	"fmt"
	"io"
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
	for _, protocol := range []string{"http1", "http2"} {
		t.Run(protocol, func(t *testing.T) { testDispatchRevocationDuringConnectionWait(t, protocol, false) })
	}
}

func TestDispatchRejectsMaterialExpiredWhileWaitingForConnection(t *testing.T) {
	for _, protocol := range []string{"http1", "http2"} {
		t.Run(protocol, func(t *testing.T) { testDispatchRevocationDuringConnectionWait(t, protocol, true) })
	}
}

func testDispatchRevocationDuringConnectionWait(t *testing.T, protocol string, expire bool) {
	t.Helper()
	firstStarted := make(chan struct{})
	firstHeaders := make(chan struct{})
	releaseFirst := make(chan struct{})
	var received atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if protocol == "http2" && r.ProtoMajor != 2 {
			t.Errorf("protocol = %s, want HTTP/2", r.Proto)
		}
		if received.Add(1) == 1 {
			_, _ = io.WriteString(w, "start")
			w.(http.Flusher).Flush()
			close(firstStarted)
			<-releaseFirst
			_, _ = io.WriteString(w, "finish")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if protocol == "http2" {
		server.EnableHTTP2 = true
		server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 1
	transport.ForceAttemptHTTP2 = true
	client := &http.Client{Transport: newDispatchTransport(transport)}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(time.Minute)
	if expire {
		deadline = time.Now().Add(200 * time.Millisecond)
	}
	validity := credentials.NewMaterialValidity(deadline)
	material := credentials.NewMaterial(catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Placements: []catalogs.ProviderCredentialPlacement{{Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}},
	}, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, credentials.MaterialMetadata{}).WithValidity(validity)
	firstDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err == nil {
			err = applyRequestAuthentication(material, request)
			var response *http.Response
			if err == nil {
				response, err = client.Do(request)
			}
			if response != nil {
				close(firstHeaders)
				data, readErr := io.ReadAll(response.Body)
				if readErr != nil {
					err = readErr
				} else if string(data) != "startfinish" {
					err = fmt.Errorf("stream body = %q", data)
				}
				_ = response.Body.Close()
			}
		}
		firstDone <- err
	}()
	<-firstStarted
	<-firstHeaders

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
	expected := credentials.ErrMaterialRevoked
	if expire {
		time.Sleep(time.Until(deadline))
		expected = credentials.ErrMaterialExpired
	} else {
		validity.Revoke()
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, expected)
	require.EqualValues(t, 1, received.Load(), "revoked material must not reach the provider")
}
