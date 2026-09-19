package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
)

func approvedDestinationMaterial(t *testing.T, material credentials.Material, method, target string) (credentials.Material, *credentials.DestinationGrant) {
	t.Helper()
	identity := credentials.DestinationIdentity{Provider: "acme", Role: "fixture-role", Handle: "opaque-handle"}
	grant, err := credentials.NewDestinationGrant(identity, material.Profile(), []credentials.Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: method, URL: target}})
	require.NoError(t, err)
	return material.WithDestinationGrant(grant, identity, catalogs.ProviderOperationChatCompletions), grant
}

func TestGrantedAuthenticationRefusesDestinationChangesBeforeEgress(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		require.Equal(t, "Bearer fixture-secret", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	material, _ := approvedDestinationMaterial(t, testAPIMaterial("fixture-secret"), http.MethodPost, server.URL+"/inference")
	for _, change := range []string{"host", "scheme", "port", "path", "query", "placement", "missing-grant"} {
		t.Run(change, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/inference", nil)
			require.NoError(t, err)
			selected := material
			switch change {
			case "host":
				request.URL.Host = "localhost:" + request.URL.Port()
				request.Host = request.URL.Host
			case "scheme":
				request.URL.Scheme = "http"
			case "port":
				request.URL.Host = request.URL.Hostname() + ":1"
				request.Host = request.URL.Host
			case "path":
				request.URL.Path = "/other"
			case "query":
				request.URL.RawQuery = "destination=other"
			case "placement":
				identity := credentials.DestinationIdentity{Provider: "acme", Role: "fixture-role", Handle: "opaque-handle"}
				grant, err := credentials.NewDestinationGrant(identity, material.Profile(), []credentials.Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
				require.NoError(t, err)
				selected = testGoogleMaterial("fixture-secret").WithDestinationGrant(grant, identity, catalogs.ProviderOperationChatCompletions)
			case "missing-grant":
				selected = material.WithDestinationGrant(nil, credentials.DestinationIdentity{}, catalogs.ProviderOperationChatCompletions)
			}
			require.ErrorIs(t, applyRequestAuthentication(selected, request), credentials.ErrDestinationUnapproved)
			require.Empty(t, request.Header.Get("Authorization"))
			require.Empty(t, request.Header.Get("x-goog-api-key"))
			require.Zero(t, received.Load())
		})
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/inference", nil)
	require.NoError(t, err)
	require.NoError(t, applyRequestAuthentication(material, request))
	response, err := doRequest(server.Client(), request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 1, received.Load())
}

func TestGrantedQueryAuthenticationChecksFinalTarget(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		require.Equal(t, "fixture-secret", r.URL.Query().Get("key"))
		require.Equal(t, "1", r.URL.Query().Get("version"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	profile := testGoogleMaterial("fixture-secret").Profile()
	profile.Placements[0].Kind = catalogs.ProviderCredentialPlacementQuery
	profile.Placements[0].Name = "key"
	raw := credentials.NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, credentials.MaterialMetadata{Handle: "opaque-handle"})
	material, grant := approvedDestinationMaterial(t, raw, http.MethodPost, server.URL+"/inference?version=1")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/inference?version=1", nil)
	require.NoError(t, err)
	require.NoError(t, applyRequestAuthentication(material, request))
	response, err := doRequest(server.Client(), request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 1, received.Load())
	changed := request.Clone(request.Context())
	changed.URL.RawQuery = url.Values{"key": {"fixture-secret"}, "version": {"2"}}.Encode()
	_, err = doRequest(server.Client(), changed)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	require.EqualValues(t, 1, received.Load())
	grant.Revoke()
	_, err = doRequest(server.Client(), request)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	require.EqualValues(t, 1, received.Load())
}

func TestDispatchRejectsDestinationRevokedWhileWaiting(t *testing.T) {
	for _, protocol := range []string{"http1", "http2"} {
		t.Run(protocol, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			firstHeaders := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var received atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if protocol == "http2" {
					require.Equal(t, 2, r.ProtoMajor)
				}
				require.Equal(t, "Bearer fixture-secret", r.Header.Get("Authorization"))
				_, _ = io.WriteString(w, "start")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-ctx.Done():
				}
				_, _ = io.WriteString(w, "finish")
			}))
			server.EnableHTTP2 = protocol == "http2"
			server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
			server.StartTLS()
			defer server.Close()
			defer unblock()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.MaxConnsPerHost = 1
			transport.ForceAttemptHTTP2 = true
			client := &http.Client{Transport: newDispatchTransport(transport)}
			defer client.CloseIdleConnections()
			material, grant := approvedDestinationMaterial(t, testAPIMaterial("fixture-secret"), http.MethodGet, server.URL)
			first, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			require.NoError(t, applyRequestAuthentication(material, first))
			done := make(chan error, 1)
			go func() {
				response, err := doRequest(client, first)
				if err == nil {
					close(firstHeaders)
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						err = readErr
					} else if string(body) != "startfinish" {
						err = io.ErrUnexpectedEOF
					}
				}
				done <- err
			}()
			select {
			case <-firstHeaders:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			waiting := make(chan struct{})
			signalWaiting := sync.OnceFunc(func() { close(waiting) })
			trace := &httptrace.ClientTrace{GetConn: func(string) { signalWaiting() }}
			second, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			require.NoError(t, applyRequestAuthentication(material, second))
			secondDone := make(chan error, 1)
			go func() {
				response, err := doRequest(client, second)
				if response != nil {
					_ = response.Body.Close()
				}
				secondDone <- err
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			grant.Revoke()
			unblock()
			require.NoError(t, <-done)
			require.ErrorIs(t, <-secondDone, credentials.ErrDestinationUnapproved)
			require.EqualValues(t, 1, received.Load())
		})
	}
}

func TestDestinationRefusalDoesNotChangeProviderAvailability(t *testing.T) {
	normalized := NormalizeFailure("acme", credentials.ErrDestinationUnapproved)
	require.Equal(t, failure.GatewayUnavailable, normalized.Kind())
	require.Equal(t, failure.ScopeNone, normalized.StateScope())
}

func TestGrantedAuthenticationRejectsTargetMutationAfterPlacement(t *testing.T) {
	for _, change := range []string{"host", "scheme", "port", "path", "method", "host-header"} {
		t.Run(change, func(t *testing.T) {
			material, _ := approvedDestinationMaterial(t, testAPIMaterial("fixture-secret"), http.MethodPost, "https://provider.example/inference")
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/inference", nil)
			require.NoError(t, err)
			require.NoError(t, applyRequestAuthentication(material, request))
			switch change {
			case "host":
				request.URL.Host = "other.example"
				request.Host = request.URL.Host
			case "scheme":
				request.URL.Scheme = "http"
			case "port":
				request.URL.Host += ":8443"
				request.Host = request.URL.Host
			case "path":
				request.URL.Path = "/other"
			case "method":
				request.Method = http.MethodGet
			case "host-header":
				request.Host = "other.example"
			}
			calls := 0
			client := &http.Client{Transport: validityTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}
			_, err = doRequest(client, request)
			require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
			require.Zero(t, calls)
		})
	}
}

func TestGrantedDispatchRefusesRedirectToUnapprovedDestination(t *testing.T) {
	var received atomic.Int64
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("approved origin did not receive its credential")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	material, _ := approvedDestinationMaterial(t, testAPIMaterial("fixture-secret"), http.MethodGet, origin.URL)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL, nil)
	require.NoError(t, err)
	require.NoError(t, applyRequestAuthentication(material, request))
	transport := origin.Client().Transport.(*http.Transport).Clone()
	client := &http.Client{Transport: newDispatchTransport(transport)}
	defer client.CloseIdleConnections()
	response, err := doRequest(client, request)
	if response != nil {
		_ = response.Body.Close()
	}
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	require.Zero(t, received.Load())
}
