package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
)

func TestDeploymentIsolationIncludesGatewayRecords(t *testing.T) {
	endpoint := os.Getenv("TEST_VALKEY_URL")
	if endpoint == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	open := func() storage.KVStore {
		t.Helper()
		cfg := validProductionConfig(t)
		cfg.Storage.Mode = "valkey"
		cfg.Storage.Valkey.URL = endpoint
		cfg.Storage.Valkey.MaxConnections = 10
		cfg.Storage.SQL.Mode = "postgres"
		cfg.Storage.SQL.Postgres.URL = "postgres://unused.example/test"
		cfg = isolatedFleetConfig(t, cfg)
		store, err := openStorage(cfg.RuntimeStorage())
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			keys, err := store.Scan(ctx, "*", 0)
			require.NoError(t, err)
			require.NoError(t, store.BatchDelete(ctx, keys))
			require.NoError(t, store.Close())
		})
		return store
	}
	a, b := open(), open()
	ctx := t.Context()
	t.Run("gateway keys", func(t *testing.T) {
		left, err := apikey.Open(a)
		require.NoError(t, err)
		right, err := apikey.Open(b)
		require.NoError(t, err)
		key := testAPIKey()
		created, err := left.Create(ctx, key)
		require.NoError(t, err)
		_, err = right.GetByHash(ctx, key.Hash)
		require.ErrorIs(t, err, apikey.ErrNotFound)
		_, err = right.GetByID(ctx, key.ID)
		require.ErrorIs(t, err, apikey.ErrNotFound)
		key.Name = "other-deployment"
		_, err = right.Create(ctx, key)
		require.NoError(t, err)
		got, err := left.GetByID(ctx, key.ID)
		require.NoError(t, err)
		require.Equal(t, created, got)
	})
	t.Run("provider credentials", func(t *testing.T) {
		left, err := credentials.Open(a)
		require.NoError(t, err)
		right, err := credentials.Open(b)
		require.NoError(t, err)
		key := credentials.ProviderKey{Scope: "account:same", Provider: "test-provider", EncryptedCredential: "fixture-ciphertext-a"}
		_, err = left.Create(ctx, key)
		require.NoError(t, err)
		_, err = right.Get(ctx, key.Scope, key.Provider)
		require.ErrorIs(t, err, credentials.ErrNotFound)
		key.EncryptedCredential = "fixture-ciphertext-b"
		_, err = right.Create(ctx, key)
		require.NoError(t, err)
		got, err := left.Get(ctx, key.Scope, key.Provider)
		require.NoError(t, err)
		require.Equal(t, "fixture-ciphertext-a", got.Key.EncryptedCredential)
	})
	t.Run("account budget policy", func(t *testing.T) {
		left, err := account.Open(a)
		require.NoError(t, err)
		right, err := account.Open(b)
		require.NoError(t, err)
		value := account.Account{ID: "same-account", Name: "Test", Active: true,
			Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalMonth}}}
		_, err = left.Create(ctx, value)
		require.NoError(t, err)
		_, err = right.GetByID(ctx, value.ID)
		require.ErrorIs(t, err, account.ErrNotFound)
		value.Limits.Spend.Limit = 2000
		_, err = right.Create(ctx, value)
		require.NoError(t, err)
		got, err := left.GetByID(ctx, value.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1000, got.Account.Limits.Spend.Limit)
	})
	t.Run("budget usage", func(t *testing.T) {
		left, err := usage.Open(a, usage.Options{})
		require.NoError(t, err)
		right, err := usage.Open(b, usage.Options{})
		require.NoError(t, err)
		now := time.Now().UTC()
		value := usage.Record{RequestID: "same-request", KeyID: "same-key", AccountID: "same-account", TeamID: "same-team",
			Timestamp: now, Operation: usage.OperationChat, Status: usage.StatusOK,
			Tokens: usage.Tokens{Input: 2, Output: 3, Total: 5}, Cost: &usage.Cost{NanoUSD: 7, Currency: "USD"}}
		require.NoError(t, left.Put(ctx, value))
		for _, scope := range []usage.Scope{usage.KeyScope(value.KeyID), usage.AccountScope(value.AccountID), usage.TeamScope(value.TeamID), usage.GatewayScope()} {
			got, err := right.Totals(ctx, scope, usage.IntervalDay, now)
			require.NoError(t, err)
			require.Zero(t, got)
		}
		value.Tokens.Input = 6
		value.Tokens.Total = 9
		require.NoError(t, right.Put(ctx, value))
		got, err := left.Totals(ctx, usage.KeyScope(value.KeyID), usage.IntervalDay, now)
		require.NoError(t, err)
		require.EqualValues(t, 5, got.Tokens)
	})
	t.Run("jobs", func(t *testing.T) {
		left, err := jobs.OpenRepository(a)
		require.NoError(t, err)
		right, err := jobs.OpenRepository(b)
		require.NoError(t, err)
		job, err := jobs.New("same-job", "same-account", "test-provider", "test-model", routing.OperationImagesGenerations, time.Now().UTC())
		require.NoError(t, err)
		require.NoError(t, left.Create(ctx, job))
		_, err = right.Get(ctx, job.Account, job.ID)
		require.ErrorIs(t, err, jobs.ErrJobNotFound)
		require.NoError(t, right.Delete(ctx, job.Account, job.ID))
		_, err = left.Get(ctx, job.Account, job.ID)
		require.NoError(t, err)
		listed, err := right.List(ctx, job.Account, 10)
		require.NoError(t, err)
		require.Empty(t, listed)
	})
}
