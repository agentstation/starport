package server

import (
	"encoding/json/v2"
	"net/http"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestWarmAuthorizationDiscoveryUsesCurrentAccountPolicy(t *testing.T) {
	store := storage.NewMockStore()
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store), withRoutableCatalog())
	secret := createServerAPIKey(t, s, "cached-discovery-reader", []string{"models:read"})
	clock := func() (time.Time, bool) { return time.Now(), true }
	middleware, _, accounts := cachedAuthFixture(t, store, clock)
	s.auth.UseAuthorization(middleware.authorization, clock)

	read := func() int {
		t.Helper()
		response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog/discovery", secret, t.Context())
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		var body struct {
			Models []any `json:"models"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return len(body.Models)
	}

	initial := read()
	require.Positive(t, initial)
	require.Equal(t, initial, read())
	owner, err := accounts.GetByID(t.Context(), account.DefaultID)
	require.NoError(t, err)
	owner.Account.Access = []account.ProviderAccess{{Provider: "not-in-this-catalog"}}
	owner, err = accounts.Update(t.Context(), owner.Account, owner.Revision)
	require.NoError(t, err)
	require.Zero(t, read(), "warm authorization must not preserve withdrawn catalog access")
	owner.Account.Access = nil
	_, err = accounts.Update(t.Context(), owner.Account, owner.Revision)
	require.NoError(t, err)
	require.Equal(t, initial, read(), "fresh authorization must observe restored account access")
}
