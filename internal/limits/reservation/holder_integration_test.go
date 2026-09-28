package reservation_test

import (
	"crypto/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHolderCreationAndBudgetHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			store, authority := holderBackend(t, backend)
			ledger, err := reservation.Open(authority)
			require.NoError(t, err)
			accounts, err := account.Open(store)
			require.NoError(t, err)
			keys, err := apikey.Open(store)
			require.NoError(t, err)
			owner, err := accounts.Create(t.Context(), account.Account{
				ID: "account-" + rand.Text(), Name: "budget owner", Active: true,
				Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay, HistoryID: "forged"}},
			})
			require.NoError(t, err)
			key, err := keys.CreateInitial(t.Context(), apikey.APIKey{
				ID: "key-" + rand.Text(), Hash: "hash-" + rand.Text(), Name: "metered", AccountID: owner.Account.ID,
				Active: true, CreatedAt: time.Now(), Scopes: []string{"chat:write"},
				Limits: &limits.Limits{Tokens: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay, HistoryID: "forged"}},
			})
			require.NoError(t, err)
			require.NotEqual(t, "forged", key.APIKey.Limits.Tokens.HistoryID)
			require.NotEqual(t, "forged", owner.Account.Limits.Spend.HistoryID)
			attempt := holderAttempt(owner, key)
			_, err = ledger.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, ledger.Begin(t.Context(), attempt.ID))
			require.NoError(t, ledger.Reconcile(t.Context(), attempt.ID, reservation.Evidence{ID: "provider-usage", Quantities: reservation.Quantities{"output": 600}, Tokens: 600}))
			_, err = ledger.Reserve(t.Context(), holderAttempt(owner, key))
			require.ErrorIs(t, err, reservation.ErrExhausted)
			oldKeyHistory := key.APIKey.Limits.Tokens.HistoryID
			oldAccountHistory := owner.Account.Limits.Spend.HistoryID
			owner.Account.Limits.Spend.Limit = 2000
			owner.Account.Limits.Spend.HistoryID = "forged-reset"
			owner, err = accounts.Update(t.Context(), owner.Account, owner.Revision)
			require.NoError(t, err)
			key.APIKey.Limits.Tokens.Limit = 2000
			key.APIKey.Limits.Tokens.HistoryID = "forged-reset"
			key, err = keys.Update(t.Context(), key.APIKey, key.Revision)
			require.NoError(t, err)
			require.Equal(t, oldAccountHistory, owner.Account.Limits.Spend.HistoryID)
			require.Equal(t, oldKeyHistory, key.APIKey.Limits.Tokens.HistoryID)
			second := holderAttempt(owner, key)
			_, err = ledger.Reserve(t.Context(), second)
			require.NoError(t, err)
			require.NoError(t, ledger.CancelBeforeDispatch(t.Context(), second.ID))
			state, err := ledger.Window(t.Context(), second.Rules[0].Meter, time.Now())
			require.NoError(t, err)
			require.EqualValues(t, 600, state.Consumed)
			// Removing a holder retains its identity and spending history.
			require.NoError(t, keys.Delete(t.Context(), key.APIKey.ID, key.Revision))
			key, err = keys.Create(t.Context(), key.APIKey)
			require.NoError(t, err)
			require.NotEqual(t, oldKeyHistory, key.APIKey.Limits.Tokens.HistoryID)
			_, err = ledger.Reserve(t.Context(), holderAttempt(owner, key))
			require.ErrorIs(t, err, reservation.ErrHistoryUnknown)
			state, err = ledger.Window(t.Context(), second.Rules[0].Meter, time.Now())
			require.NoError(t, err)
			require.EqualValues(t, 600, state.Consumed)
		})
	}
}

func holderAttempt(owner account.Record, key apikey.Record) reservation.Attempt {
	spend, tokens := owner.Account.Limits.Spend, key.APIKey.Limits.Tokens
	return reservation.Attempt{
		ID: rand.Text(), RequestID: rand.Text(), AccountID: owner.Account.ID, KeyID: key.APIKey.ID,
		OfferingID: "fixture/model", CatalogGeneration: "generation", Operation: "chat",
		Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}},
		Bound:     reservation.Quantities{"output": 600}, TokenBound: 600,
		Rules: []reservation.Rule{
			{Meter: reservation.Meter{Scope: limits.ScopeAccount, Holder: owner.Account.ID, Dimension: limits.DimensionSpend, Interval: spend.Interval}, Limit: spend.Limit, HistoryID: spend.HistoryID, PolicyRevision: strconv.FormatUint(owner.Revision, 10)},
			{Meter: reservation.Meter{Scope: limits.ScopeKey, Holder: key.APIKey.ID, Dimension: limits.DimensionTokens, Interval: tokens.Interval}, Limit: tokens.Limit, HistoryID: tokens.HistoryID, PolicyRevision: strconv.FormatUint(key.Revision, 10)},
		},
	}
}

func holderBackend(t *testing.T, backend string) (storage.KVStore, storage.TimeBoundStore) {
	t.Helper()
	if backend == "badger" {
		store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		return store, store
	}
	address := os.Getenv("TEST_VALKEY_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: "holder-" + rand.Text(), AllowInsecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	provider := store.(storage.IncarnationProvider)
	id, err := provider.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	bound, err := provider.BindIncarnation(t.Context(), id)
	require.NoError(t, err)
	return store, bound.(storage.TimeBoundStore)
}
