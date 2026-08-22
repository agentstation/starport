package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providers"
	providerstate "github.com/agentstation/starport/internal/providers/state"
)

func TestAdminProviderStatusContract(t *testing.T) {
	operations := &providerOperationsStub{state: providerstate.Snapshot{
		Revision: 7, CatalogGenerationID: "catalog-1",
		Providers: []providerstate.ProviderStatus{{
			ProviderID: catalogs.ProviderIDOpenAI,
			Adapter: providerstate.AdapterStatus{
				State: providerstate.AdapterReady,
			},
			OperatorCredential: providerstate.CredentialStatus{
				State: providerstate.CredentialReady, Usable: true,
			},
		}},
	}}
	server := newTestServer(
		t, &Config{MaxRequestSize: 1 << 20}, withTestProviderOperations(operations),
	)
	secret := createServerIdentity(t, server, "provider-admin", []string{"admin"})

	recorder := serveAuthorized(
		server, http.MethodGet, "/api/v1/admin/providers", secret, context.Background(),
	)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var response providerstate.Snapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, uint64(7), response.Revision)
	require.Equal(t, "catalog-1", response.CatalogGenerationID)
	require.Len(t, response.Providers, 1)
	require.NotContains(t, recorder.Body.String(), "material-version")
	require.NotContains(t, recorder.Body.String(), "provider-secret")
}

func TestAdminProviderRefreshContract(t *testing.T) {
	t.Run("reports bounded revisions and safe changes", func(t *testing.T) {
		operations := &providerOperationsStub{
			state: providerstate.Snapshot{Revision: 7, CatalogGenerationID: "catalog-1"},
			after: providerstate.Snapshot{Revision: 8, CatalogGenerationID: "catalog-1"},
			report: providers.ReconcileReport{
				Revision: 12, Changed: true,
				ConfiguredProviders: []catalogs.ProviderID{catalogs.ProviderIDOpenAI},
				Failures: []providers.ReconcileFailure{{
					ProviderID: catalogs.ProviderIDAnthropic,
					Err:        errors.New("private-source-reference"),
				}},
			},
		}
		server := newTestServer(
			t, &Config{MaxRequestSize: 1 << 20}, withTestProviderOperations(operations),
		)
		secret := createServerIdentity(t, server, "provider-admin", []string{"admin"})

		recorder := serveAuthorized(
			server, http.MethodPost, "/api/v1/admin/providers/refresh", secret, context.Background(),
		)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		var response struct {
			ReconciliationRevision uint64   `json:"reconciliation_revision"`
			Changed                bool     `json:"changed"`
			ConfiguredProviders    []string `json:"configured_providers"`
			FailureCount           int      `json:"failure_count"`
			Failures               []struct {
				ProviderID string `json:"provider_id"`
				Reason     string `json:"reason"`
			} `json:"failures"`
			PreviousProviderStateRevision uint64 `json:"previous_provider_state_revision"`
			ProviderStateRevision         uint64 `json:"provider_state_revision"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, uint64(12), response.ReconciliationRevision)
		require.True(t, response.Changed)
		require.Equal(t, []string{string(catalogs.ProviderIDOpenAI)}, response.ConfiguredProviders)
		require.Equal(t, 1, response.FailureCount)
		require.Len(t, response.Failures, 1)
		require.Equal(t, string(catalogs.ProviderIDAnthropic), response.Failures[0].ProviderID)
		require.Equal(t, "credential source is unavailable", response.Failures[0].Reason)
		require.Equal(t, uint64(7), response.PreviousProviderStateRevision)
		require.Equal(t, uint64(8), response.ProviderStateRevision)
		require.NotContains(t, recorder.Body.String(), "private-source-reference")
		require.Equal(t, 1, operations.callCount())
	})

	t.Run("propagates request cancellation", func(t *testing.T) {
		operations := newBlockingProviderOperations()
		server := newTestServer(
			t, &Config{MaxRequestSize: 1 << 20}, withTestProviderOperations(operations),
		)
		secret := createServerIdentity(t, server, "provider-admin", []string{"admin"})
		requestContext, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/admin/providers/refresh", nil,
		).WithContext(requestContext)
		request.Header.Set("Authorization", "Bearer "+secret)
		recorder := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			server.Router().ServeHTTP(recorder, request)
			close(served)
		}()
		select {
		case <-operations.started:
		case <-time.After(time.Second):
			t.Fatal("provider refresh did not start")
		}
		cancel()
		select {
		case <-served:
		case <-time.After(time.Second):
			t.Fatal("provider refresh did not stop after cancellation")
		}
		require.Equal(t, http.StatusRequestTimeout, recorder.Code, recorder.Body.String())
		assertOpenRouterError(t, recorder, http.StatusRequestTimeout, "invalid_request_error")
		require.Equal(t, 1, operations.callCount())
	})

	t.Run("redacts internal reconciliation failure", func(t *testing.T) {
		operations := &providerOperationsStub{refresh: errors.New("private-source-reference")}
		server := newTestServer(
			t, &Config{MaxRequestSize: 1 << 20}, withTestProviderOperations(operations),
		)
		secret := createServerIdentity(t, server, "provider-admin", []string{"admin"})

		recorder := serveAuthorized(
			server, http.MethodPost, "/api/v1/admin/providers/refresh", secret, context.Background(),
		)
		require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
		assertOpenRouterError(t, recorder, http.StatusInternalServerError, "server_error")
		require.NotContains(t, recorder.Body.String(), "private-source-reference")
	})
}

func TestAdminProviderRoutesRequireAuthentication(t *testing.T) {
	operations := &providerOperationsStub{}
	server := newTestServer(
		t, &Config{MaxRequestSize: 1 << 20}, withTestProviderOperations(operations),
	)

	unauthorized := httptest.NewRecorder()
	server.Router().ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/refresh", nil),
	)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assertOpenRouterError(t, unauthorized, http.StatusUnauthorized, "authentication_error")

	secret := createServerIdentity(t, server, "provider-reader", []string{"models:read"})
	forbidden := serveAuthorized(
		server, http.MethodPost, "/api/v1/admin/providers/refresh", secret, context.Background(),
	)
	require.Equal(t, http.StatusForbidden, forbidden.Code)
	assertOpenRouterError(t, forbidden, http.StatusForbidden, "permission_error")
	require.Equal(t, 0, operations.callCount())
}

type providerOperationsStub struct {
	mu      sync.Mutex
	state   providerstate.Snapshot
	after   providerstate.Snapshot
	report  providers.ReconcileReport
	refresh error
	calls   int
	started chan struct{}
}

func newBlockingProviderOperations() *providerOperationsStub {
	return &providerOperationsStub{started: make(chan struct{})}
}

func (o *providerOperationsStub) ProviderStates() providerstate.Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

func (o *providerOperationsStub) RefreshProviders(ctx context.Context) (providers.ReconcileReport, error) {
	o.mu.Lock()
	o.calls++
	started := o.started
	report := o.report
	refreshErr := o.refresh
	after := o.after
	o.mu.Unlock()
	if started != nil {
		close(started)
		<-ctx.Done()
		return providers.ReconcileReport{}, ctx.Err()
	}
	if refreshErr != nil {
		return providers.ReconcileReport{}, refreshErr
	}
	o.mu.Lock()
	o.state = after
	o.mu.Unlock()
	return report, nil
}

func (o *providerOperationsStub) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func createServerIdentity(t *testing.T, server *Server, id string, scopes []string) string {
	t.Helper()
	secret := "secret-" + id
	hash := sha256.Sum256([]byte(secret))
	_, err := server.identities.Create(t.Context(), identity.APIKey{
		ID: id, Name: strings.ReplaceAll(id, "-", "_"),
		Hash: hex.EncodeToString(hash[:]), Scopes: scopes, Active: true,
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	return secret
}

func serveAuthorized(
	server *Server,
	method string,
	path string,
	secret string,
	ctx context.Context,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, request)
	return recorder
}

func assertOpenRouterError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	errorType string,
) {
	t.Helper()
	var response struct {
		Error struct {
			Code     int `json:"code"`
			Metadata struct {
				ErrorType string `json:"error_type"`
			} `json:"metadata"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, status, response.Error.Code)
	require.Equal(t, errorType, response.Error.Metadata.ErrorType)
}
