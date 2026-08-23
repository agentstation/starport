package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/tenant"
)

// resolveStrategy runs one secret through RequireAPIKey and reports the
// governing credential strategy the request ended up under.
func resolveStrategy(
	t *testing.T,
	middleware *AuthMiddleware,
	secret string,
) (tenant.CredentialStrategy, int) {
	t.Helper()

	var resolved tenant.CredentialStrategy
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved = requestctx.TenantCredentialStrategyOrDefault(r.Context())
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return resolved, recorder.Code
}

// TestAuthenticatedRequestCarriesItsTenantCredentialStrategy is the seam AON3
// needs: the operator sets the policy on the account, and the request has to
// arrive at the router already knowing it. Without this the key's own metadata
// would be the only strategy the gateway ever saw, and a tenant could widen it.
func TestAuthenticatedRequestCarriesItsTenantCredentialStrategy(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := identity.Open(store)
	require.NoError(t, err)
	tenants, err := tenant.Open(store)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = tenants.Create(ctx, tenant.Tenant{
		ID: "acme", Name: "Acme", Active: true,
		CredentialStrategy: tenant.StrategyBYOKOnly,
	})
	require.NoError(t, err)
	issuer, err := identity.NewIssuer(identities, identity.WithTenantChecker(tenants))
	require.NoError(t, err)
	issued, err := issuer.Issue(ctx, identity.IssueRequest{
		Name: "Acme-CI", TenantID: "acme", Scopes: []string{"chat:write"},
	})
	require.NoError(t, err)

	strategy, status := resolveStrategy(t, NewAuthMiddleware(identities, tenants), issued.Secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, tenant.StrategyBYOKOnly, strategy)
}

// TestUnreadableTenantStillServesTheRequest states the availability call. The
// key authenticated, so a storage fault on the account record must not take a
// working deployment offline; the request falls back to the default policy,
// which is the one the operator gets by not choosing.
func TestUnreadableTenantStillServesTheRequest(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := identity.Open(store)
	require.NoError(t, err)

	secret := "test-secret-for-unreadable-tenant"
	_, err = identities.Create(context.Background(), identity.APIKey{
		ID: "STARPORT_unreadable", Name: "Unreadable", Hash: hashSecret(secret),
		TenantID: "acme", Scopes: []string{"chat:write"}, Active: true,
	})
	require.NoError(t, err)

	failing := failingTenantReader{err: errors.New("store unavailable")}
	strategy, status := resolveStrategy(t, NewAuthMiddleware(identities, failing), secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, tenant.StrategyOperatorFirst, strategy,
		"an unreadable account resolves to the default policy, not to no policy")

	// A deployment wired without a tenant reader behaves the same way, so the
	// fallback is one behavior and not two.
	strategy, status = resolveStrategy(t, NewAuthMiddleware(identities), secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, tenant.StrategyOperatorFirst, strategy)
}

type failingTenantReader struct{ err error }

func (r failingTenantReader) GetByID(context.Context, string) (tenant.Record, error) {
	return tenant.Record{}, r.err
}
