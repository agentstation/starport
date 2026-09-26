package server

import (
	"context"
	"encoding/json/v2"
	"errors"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/account"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryRouteWithoutProviderCredentials(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "discovery-reader", []string{"models:read"})
	response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog/discovery", secret, t.Context())
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var body struct {
		GenerationID string `json:"generation_id"`
		Models       []struct {
			ID        string `json:"id"`
			Offerings []struct {
				Readiness string `json:"readiness"`
			} `json:"offerings"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEmpty(t, body.GenerationID)
	require.NotEmpty(t, body.Models)
	for _, model := range body.Models {
		for _, offering := range model.Offerings {
			require.Equal(t, "unknown", offering.Readiness)
		}
	}
	require.NotContains(t, response.Body.String(), secret)
	require.NotContains(t, response.Body.String(), "\"hash\"")
	require.NotContains(t, response.Body.String(), "\"endpoint\"")
}

func TestDiscoveryRouteAppliesCurrentAccountDisclosure(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "discovery-reader", []string{"models:read"})
	record, err := s.accounts.GetByID(t.Context(), account.DefaultID)
	require.NoError(t, err)
	record.Account.Access = []account.ProviderAccess{{Provider: "not-in-this-catalog"}}
	_, err = s.accounts.Update(t.Context(), record.Account, record.Revision)
	require.NoError(t, err)
	response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog/discovery", secret, t.Context())
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Models []any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Empty(t, body.Models)
}

func TestDiscoveryRouteRequiresReadScope(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "inference-only", []string{"chat:write"})
	response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog/discovery", secret, t.Context())
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestDiscoveryViewerRejectsLostAccount(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "discovery-reader", []string{"models:read"})
	var captured *http.Request
	s.auth.RequireAPIKey(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r })).ServeHTTP(httptest.NewRecorder(), discoveryRequest(secret))
	require.NotNil(t, captured)
	before, err := s.auth.discoveryViewer(captured)
	require.NoError(t, err)
	require.True(t, before.Account.Active)
	// A failed account read must not reuse the middleware's earlier record.
	s.auth.accounts = discoveryUnavailableAccount{}
	_, err = s.auth.discoveryViewer(captured)
	require.Error(t, err)
}

type discoveryUnavailableAccount struct{}

func (discoveryUnavailableAccount) GetByID(context.Context, string) (account.Record, error) {
	return account.Record{}, errors.New("private storage failure")
}

func discoveryRequest(secret string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/discovery", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	return request
}

func TestDiscoveryViewerRevalidatesConsoleSession(t *testing.T) {
	store := storage.NewMockStore()
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(t.Context())
	require.NoError(t, err)
	token := sessionToken(t, 1)
	middleware := NewAuthMiddleware(keys, accounts)
	middleware.AcceptSessions(localauth.NewGate(token, "127.0.0.1"))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/discovery", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: openSession(t, token)})
	var captured *http.Request
	middleware.RequireAPIKey(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r })).ServeHTTP(httptest.NewRecorder(), request)
	require.NotNil(t, captured)
	viewer, err := middleware.discoveryViewer(captured)
	require.NoError(t, err)
	require.Equal(t, apikey.LocalOperatorKeyID, viewer.Key.ID)
	middleware.AcceptSessions(localauth.NewGate(sessionToken(t, 2), "127.0.0.1"))
	_, err = middleware.discoveryViewer(captured)
	require.Error(t, err)
}

func TestCompatibilityAuthorsApplyCurrentDisclosure(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "compatibility-reader", []string{"models:read"})
	record, err := s.accounts.GetByID(t.Context(), account.DefaultID)
	require.NoError(t, err)
	record.Account.Access = []account.ProviderAccess{{Provider: "not-in-this-catalog"}}
	_, err = s.accounts.Update(t.Context(), record.Account, record.Revision)
	require.NoError(t, err)
	response := serveAuthorized(s, http.MethodGet, "/api/v1/authors", secret, t.Context())
	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Authors []any `json:"authors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Empty(t, body.Authors)
}

func TestCompatibilityDisclosureRefusesPolicyChangeBeforeDelivery(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	secret := createServerAPIKey(t, s, "compatibility-reader", []string{"models:read"})
	handler := s.auth.RequireAPIKey(s.requireCatalogDisclosure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"private":"must-not-escape"}`))
		record, err := s.accounts.GetByID(r.Context(), account.DefaultID)
		require.NoError(t, err)
		record.Account.Access = []account.ProviderAccess{{Provider: "not-in-this-catalog"}}
		_, err = s.accounts.Update(r.Context(), record.Account, record.Revision)
		require.NoError(t, err)
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, discoveryRequest(secret))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.NotContains(t, response.Body.String(), "must-not-escape")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestCompatibilityRoutesRequireCurrentCatalog(t *testing.T) {
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	secret := createServerAPIKey(t, s, "compatibility-reader", []string{"models:read"})
	for _, path := range []string{"/v1/models", "/v1/models/unknown", "/api/v1/models", "/api/v1/models/unknown", "/api/v1/models/unknown/endpoints", "/api/v1/providers", "/api/v1/authors", "/api/v1/authors/unknown"} {
		t.Run(path, func(t *testing.T) {
			response := serveAuthorized(s, http.MethodGet, path, secret, t.Context())
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}
