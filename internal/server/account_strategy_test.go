package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
)

// resolveStrategy runs one secret through RequireAPIKey and reports the
// governing credential strategy the request ended up under.
func resolveStrategy(
	t *testing.T,
	middleware *AuthMiddleware,
	secret string,
) (account.CredentialStrategy, int) {
	t.Helper()

	var resolved account.CredentialStrategy
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved = requestctx.AccountCredentialStrategyOrDefault(r.Context())
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return resolved, recorder.Code
}

// TestAuthenticatedRequestCarriesItsAccountCredentialStrategy is the seam AON3
// needs: the operator sets the policy on the account, and the request has to
// arrive at the router already knowing it. Without this the key's own metadata
// would be the only strategy the gateway ever saw, and an account could widen it.
func TestAuthenticatedRequestCarriesItsAccountCredentialStrategy(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := apikey.Open(store)
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = accounts.Create(ctx, account.Account{
		ID: "acme", Name: "Acme", Active: true,
		CredentialStrategy: account.StrategyBYOKOnly,
	})
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(identities, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	issued, err := issuer.Issue(ctx, apikey.IssueRequest{
		Name: "Acme-CI", AccountID: "acme", Scopes: []string{"chat:write"},
	})
	require.NoError(t, err)

	strategy, status := resolveStrategy(t, NewAuthMiddleware(identities, accounts), issued.Secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, account.StrategyBYOKOnly, strategy)
}

// TestUnreadableAccountStillServesTheRequest states the availability call. The
// key authenticated, so a storage fault on the account record must not take a
// working deployment offline; the request falls back to the default policy,
// which is the one the operator gets by not choosing.
func TestUnreadableAccountStillServesTheRequest(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := apikey.Open(store)
	require.NoError(t, err)

	secret := "test-secret-for-unreadable-account"
	_, err = identities.Create(context.Background(), apikey.APIKey{
		ID: "STARPORT_unreadable", Name: "Unreadable", Hash: hashSecret(secret),
		AccountID: "acme", Scopes: []string{"chat:write"}, Active: true,
	})
	require.NoError(t, err)

	failing := failingAccountReader{err: errors.New("store unavailable")}
	strategy, status := resolveStrategy(t, NewAuthMiddleware(identities, failing), secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, account.StrategyOperatorFirst, strategy,
		"an unreadable account resolves to the default policy, not to no policy")

	// A deployment wired without an account reader behaves the same way, so the
	// fallback is one behavior and not two.
	strategy, status = resolveStrategy(t, NewAuthMiddleware(identities), secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, account.StrategyOperatorFirst, strategy)
}

type failingAccountReader struct{ err error }

func (r failingAccountReader) GetByID(context.Context, string) (account.Record, error) {
	return account.Record{}, r.err
}
