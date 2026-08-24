package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/tenant"
)

// unauthenticatedConfig is a server configured the way `serve --no-auth` and
// `dev --no-auth` configure it.
func unauthenticatedConfig(scopes ...string) *Config {
	return &Config{
		Port: 8080, Host: "127.0.0.1",
		AuthMode:              authmode.Disabled,
		UnauthenticatedScopes: scopes,
	}
}

// TestDisabledAuthenticationServesInferenceWithoutAKey is the AON6 acceptance
// case. An operator trying Starport for the first time has no key and no
// identity store, and the whole point of the mode is that the first request
// still works.
func TestDisabledAuthenticationServesInferenceWithoutAKey(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock/test-model","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

// TestDisabledAuthenticationMetersTheAnonymousIdentity pins what everything
// downstream of authentication reads. Rate limits, budgets, and usage records
// all key off the request context, so an unauthenticated deployment has to
// carry a complete identity or those seams meet a state they never see in
// production and behave differently in a mode operators actually run.
func TestDisabledAuthenticationMetersTheAnonymousIdentity(t *testing.T) {
	middleware := NewAuthMiddleware(nil)
	middleware.Govern(authmode.NewPolicy(authmode.Setting{Mode: authmode.Disabled}), nil)

	resolved, status := authenticate(t, middleware, "")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, identity.AnonymousKeyID, resolved.keyID)
	assert.Equal(t, tenant.DefaultID, resolved.tenantID)
}

// TestDisabledAuthenticationIgnoresAPresentedKey pins the rule that makes the
// mode predictable: disabling authentication turns the check off, it does not
// make it optional. Honoring a key when one happens to arrive would let a
// stale or mistyped secret silently move a caller onto another account's
// limits, budgets, and credentials, and the caller would have no way to see it.
func TestDisabledAuthenticationIgnoresAPresentedKey(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := identity.Open(store)
	require.NoError(t, err)
	tenants, err := tenant.Open(store)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = tenants.Create(ctx, tenant.Tenant{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)

	issuer, err := identity.NewIssuer(identities, identity.WithTenantChecker(tenants))
	require.NoError(t, err)
	issued, err := issuer.Issue(ctx, identity.IssueRequest{
		Name: "Acme-CI", TenantID: "acme", Scopes: []string{"chat:write"},
	})
	require.NoError(t, err)

	middleware := NewAuthMiddleware(identities, tenants)
	middleware.Govern(authmode.NewPolicy(authmode.Setting{Mode: authmode.Disabled}), nil)

	resolved, status := authenticate(t, middleware, issued.Secret)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, identity.AnonymousKeyID, resolved.keyID)
	assert.Equal(t, tenant.DefaultID, resolved.tenantID)
}

// TestDisabledAuthenticationKeepsTheAdminPlaneClosed is the security boundary
// of the whole task. Opening inference on a trusted port must not hand every
// caller the power to issue keys, apply deployment-wide provider credentials,
// or delete accounts.
func TestDisabledAuthenticationKeepsTheAdminPlaneClosed(t *testing.T) {
	server := newTestServer(t, unauthenticatedConfig())

	response := doRequest(t, server, http.MethodGet, "/api/v1/admin/keys")

	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

// TestDisabledAuthenticationGrantsAdminOnlyWhenNamed is the other half of that
// boundary: an operator who wants the admin plane open without a key can say
// so, and saying so takes naming the scope.
func TestDisabledAuthenticationGrantsAdminOnlyWhenNamed(t *testing.T) {
	server := newTestServer(t, unauthenticatedConfig("admin"))

	response := doRequest(t, server, http.MethodGet, "/api/v1/admin/keys")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

// TestRequiredAuthenticationRefusesAKeylessRequest pins the default. It is the
// behavior every other case in this file is a deliberate departure from.
func TestRequiredAuthenticationRefusesAKeylessRequest(t *testing.T) {
	server := newTestServer(t, &Config{Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock/test-model","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
}

// TestAuthModeRouteAnswersWithoutAKey covers both modes. A client that holds
// no key needs to learn whether it has to go get one, and answering that
// question with 401 tells it nothing it can act on.
func TestAuthModeRouteAnswersWithoutAKey(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   authmode.Mode
	}{
		{name: "required", config: &Config{Port: 8080, Host: "127.0.0.1"}, want: authmode.Required},
		{name: "disabled", config: unauthenticatedConfig(), want: authmode.Disabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, test.config)

			response := doRequest(t, server, http.MethodGet, "/api/v1/auth/mode")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body controllers.AuthModeResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, string(test.want), body.Mode)
			// httptest gives the request a non-loopback RemoteAddr, which is
			// the case AON7 refuses. The read still answers.
			assert.False(t, body.CanChange)
			assert.NotEmpty(t, body.Reason)
		})
	}
}

// TestAnonymousScopesExcludeAdmin guards the default set directly. The route
// tests above prove the current route table; this proves the policy, so
// widening the set stays a deliberate edit rather than a side effect.
func TestAnonymousScopesExcludeAdmin(t *testing.T) {
	anonymous := identity.Anonymous(nil)

	assert.False(t, anonymous.HasScope("admin"))
	assert.False(t, anonymous.HasScope("*"))
	assert.True(t, anonymous.HasScope("chat:write"))
	assert.True(t, anonymous.HasScope("models:read"))
}

// doRequest sends one request with no credentials of any kind through the
// whole router, so what it proves is what an operator would see from curl.
func doRequest(t *testing.T, server *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}
