package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
)

// requestIdentity is what one authenticated request resolved to. The two
// values are separate on purpose: the key authenticates, and the account
// decides what the request may reach.
type requestIdentity struct {
	accountID string
	keyID     string
}

// authenticate runs one secret through RequireAPIKey and reports the identity
// the middleware put on the request context.
func authenticate(t *testing.T, middleware *AuthMiddleware, secret string) (requestIdentity, int) {
	t.Helper()

	var resolved requestIdentity
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved.accountID = requestctx.AccountIDOrDefault(r.Context())
		resolved.keyID, _ = requestctx.GetAPIKeyID(r.Context())
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return resolved, recorder.Code
}

// TestTwoKeysInOneAccountShareARequestAccount is the AON2 acceptance case. On the
// baseline the request account was the API key ID, so two keys in one account
// could never agree on anything scoped to the account: credentials, limits, or
// cached responses.
func TestTwoKeysInOneAccountShareARequestAccount(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := identity.Open(store)
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = accounts.Create(ctx, account.Account{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)

	issuer, err := identity.NewIssuer(identities, identity.WithAccountChecker(accounts))
	require.NoError(t, err)

	first, err := issuer.Issue(ctx, identity.IssueRequest{
		Name: "Acme-CI", AccountID: "acme", Scopes: []string{"chat:write"},
	})
	require.NoError(t, err)
	second, err := issuer.Issue(ctx, identity.IssueRequest{
		Name: "Acme-Laptop", AccountID: "acme", Scopes: []string{"chat:write"},
	})
	require.NoError(t, err)
	require.NotEqual(t, first.APIKey.ID, second.APIKey.ID, "the two keys must be distinct identities")

	middleware := NewAuthMiddleware(identities)

	firstRequest, status := authenticate(t, middleware, first.Secret)
	require.Equal(t, http.StatusOK, status)
	secondRequest, status := authenticate(t, middleware, second.Secret)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, "acme", firstRequest.accountID)
	assert.Equal(t, "acme", secondRequest.accountID)
	assert.Equal(t, firstRequest.accountID, secondRequest.accountID,
		"two keys issued to one account must produce one request account")

	// The key still travels, and it is not the account. Usage attribution and
	// per-key limits read this value, so collapsing the two would silently
	// re-key every usage record onto the account.
	assert.Equal(t, first.APIKey.ID, firstRequest.keyID)
	assert.Equal(t, second.APIKey.ID, secondRequest.keyID)
	assert.NotEqual(t, firstRequest.keyID, secondRequest.keyID)
	assert.NotEqual(t, firstRequest.keyID, firstRequest.accountID,
		"the request key ID and the request account must stay distinct values")
}

// TestKeyWithNoAccountResolvesToDefault proves the resolution contract holds at
// read time and not only at issue time.
func TestKeyWithNoAccountResolvesToDefault(t *testing.T) {
	store := storage.NewMockStore()
	identities, err := identity.Open(store)
	require.NoError(t, err)

	secret := "sk-starport-unaccounted"
	_, err = identities.Create(context.Background(), identity.APIKey{
		ID:     "STARPORT_unaccounted",
		Name:   "Unaccounted",
		Hash:   hashSecret(secret),
		Scopes: []string{"chat:write"},
		Active: true,
	})
	require.NoError(t, err)

	resolved, status := authenticate(t, NewAuthMiddleware(identities), secret)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, account.DefaultID, resolved.accountID)
	assert.Equal(t, "STARPORT_unaccounted", resolved.keyID)
}

// TestUnauthenticatedRequestAccountIsDecidedInOnePlace pins the seam AON6
// extends when an operator disables authentication. A request with no
// authenticated identity still has to attribute usage and select credentials,
// and AccountIDOrDefault is the only place that decides which account it uses.
func TestUnauthenticatedRequestAccountIsDecidedInOnePlace(t *testing.T) {
	assert.Equal(t, account.DefaultID, requestctx.AccountIDOrDefault(context.Background()))
}

// hashSecret mirrors how RequireAPIKey looks a secret up.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
