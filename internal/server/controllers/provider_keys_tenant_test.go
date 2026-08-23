package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/tenant"
)

// scopeRecordingKeyManager remembers the credential scope the controller asked
// for. The scope is the whole point of these routes: it decides whose provider
// credential a request reads and writes.
type scopeRecordingKeyManager struct {
	mockKeyManager
	scopes []string
}

func (m *scopeRecordingKeyManager) ListKeys(ctx context.Context, scope string) ([]*credentials.ProviderKey, error) {
	m.scopes = append(m.scopes, scope)
	return m.mockKeyManager.ListKeys(ctx, scope)
}

func (m *scopeRecordingKeyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	m.scopes = append(m.scopes, scope)
	return m.mockKeyManager.DeleteKey(ctx, scope, provider)
}

// listProviderKeysAs issues one request that names urlKeyID in its path while
// running under tenantID, and reports the scope the controller used.
func listProviderKeysAs(t *testing.T, manager *scopeRecordingKeyManager, tenantID, urlKeyID string) {
	t.Helper()

	handler := NewProviderKeysController(manager, nil)
	router := chi.NewRouter()
	router.Get("/api/v1/keys/{key_id}/provider-keys", handler.List)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/keys/"+urlKeyID+"/provider-keys", nil)
	request = request.WithContext(requestctx.WithTenantID(request.Context(), tenantID))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
}

// TestProviderCredentialScopeFollowsTheTenant is the behavior AON2 changes. On
// the baseline the scope was "user:" plus the gateway API key ID, so a tenant's
// second key saw an empty credential set and deleting a key stranded the
// credentials that were applied through it.
func TestProviderCredentialScopeFollowsTheTenant(t *testing.T) {
	manager := &scopeRecordingKeyManager{}

	listProviderKeysAs(t, manager, "acme", "STARPORT_ci")
	listProviderKeysAs(t, manager, "acme", "STARPORT_laptop")

	require.Len(t, manager.scopes, 2)
	assert.Equal(t, byok.UserScope("acme"), manager.scopes[0])
	assert.Equal(t, manager.scopes[0], manager.scopes[1],
		"two keys in one tenant must read one credential scope")
	assert.NotContains(t, manager.scopes[0], "STARPORT_",
		"no credential scope may contain a gateway API key ID")
}

// TestProviderCredentialScopeSeparatesTenants proves the scope still isolates,
// and that the key ID in the URL never widens or narrows what a request reaches.
func TestProviderCredentialScopeSeparatesTenants(t *testing.T) {
	manager := &scopeRecordingKeyManager{}

	listProviderKeysAs(t, manager, "acme", "STARPORT_shared")
	listProviderKeysAs(t, manager, "globex", "STARPORT_shared")

	require.Len(t, manager.scopes, 2)
	assert.NotEqual(t, manager.scopes[0], manager.scopes[1],
		"two tenants must not share a credential scope")
	assert.Equal(t, byok.UserScope("globex"), manager.scopes[1])
}

// TestProviderCredentialScopeFallsBackToTheDefaultTenant covers the request that
// carries no authenticated identity, which AON6 makes reachable when an
// operator disables authentication.
func TestProviderCredentialScopeFallsBackToTheDefaultTenant(t *testing.T) {
	manager := &scopeRecordingKeyManager{}

	handler := NewProviderKeysController(manager, nil)
	router := chi.NewRouter()
	router.Delete("/api/v1/keys/{key_id}/provider-keys/{provider}", handler.Delete)

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/keys/STARPORT_any/provider-keys/openai", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	require.Len(t, manager.scopes, 1)
	assert.Equal(t, byok.UserScope(tenant.DefaultID), manager.scopes[0])
}
