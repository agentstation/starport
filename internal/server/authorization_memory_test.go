package server

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
)

type authorizationReadStore struct {
	storage.KVStore
	reads atomic.Int64
}

func (s *authorizationReadStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.reads.Add(1)
	return s.KVStore.Get(ctx, key)
}

func authorizationFixture(t *testing.T, store storage.KVStore) (apikey.Repository, account.Repository, string) {
	t.Helper()
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)
	_, err = accounts.Create(t.Context(), account.Account{
		ID: "authorization-account", Name: "Authorization account", Active: true,
		CredentialStrategy: account.StrategyBYOKOnly,
	})
	require.NoError(t, err)
	const secret = "test-authorization-memory-secret"
	_, err = keys.Create(t.Context(), apikey.APIKey{
		ID: "STARPORT_authorization_memory", Name: "Authorization-memory", Hash: hashSecret(secret),
		AccountID: "authorization-account", Scopes: []string{"chat:write"}, Active: true,
	})
	require.NoError(t, err)
	return keys, accounts, secret
}

func TestAuthorizationWarmRequestsAvoidStableReads(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backend storage.KVStore) {
		store := &authorizationReadStore{KVStore: backend}
		_, _, secret := authorizationFixture(t, store)
		middleware, _, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return time.Now(), true })
		strategy, status := resolveStrategy(t, middleware, secret)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, account.StrategyBYOKOnly, strategy)
		store.reads.Store(0)
		for range 10 {
			strategy, status = resolveStrategy(t, middleware, secret)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, account.StrategyBYOKOnly, strategy)
		}
		require.Zero(t, store.reads.Load(), "warm valid authorization must not read stable records from storage")
	})
}

func TestAuthorizationUnknownAccountRefusesAdmission(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		keys, _, secret := authorizationFixture(t, store)
		for _, tc := range []struct {
			name   string
			err    error
			status int
		}{
			{name: "unavailable", err: errors.New("account authority unavailable"), status: http.StatusServiceUnavailable},
			{name: "missing", err: account.ErrNotFound, status: http.StatusForbidden},
		} {
			t.Run(tc.name, func(t *testing.T) {
				strategy, status := resolveStrategy(t, NewAuthMiddleware(keys, failingAccountReader{err: tc.err}), secret)
				require.Equal(t, tc.status, status, "unknown or missing account must not grant default provider access")
				require.Empty(t, strategy, "refused requests must not reach the protected handler")
			})
		}
	})
}

func defaultAuthAccounts(t *testing.T) account.Repository {
	t.Helper()
	accounts, err := account.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(t.Context())
	require.NoError(t, err)
	return accounts
}
