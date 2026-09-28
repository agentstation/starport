package recovery

import (
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecoveryRequiresOriginalIndependentApproval(t *testing.T) {
	raw, _, config := kvTransferStores(t, storage.StorageTypeValkey)
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := New(db)
	require.NoError(t, err)
	closed, err := witness.Initialize(t.Context(), config.Valkey.DeploymentID)
	require.NoError(t, err)
	backend := raw.(storage.IncarnationProvider)
	identity, err := backend.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	approved, err := witness.ApproveAuthority(t.Context(), backend, closed, identity, "test/reconciled", "first")
	require.NoError(t, err)
	authority, err := witness.OpenAuthority(t.Context(), backend, config.Valkey.DeploymentID)
	require.NoError(t, err)
	repository, err := reservation.Open(authority)
	require.NoError(t, err)
	now, err := authority.AuthorityTime(t.Context())
	require.NoError(t, err)
	attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "account", KeyID: "key", TeamID: "team", OfferingID: "fixture/model", CatalogGeneration: "generation-a", Operation: "chat", Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 600}, TokenBound: 600}
	for _, holder := range []struct {
		scope limits.Scope
		id    string
	}{{limits.ScopeAccount, attempt.AccountID}, {limits.ScopeKey, attempt.KeyID}, {limits.ScopeTeam, attempt.TeamID}} {
		for _, dimension := range []limits.Dimension{limits.DimensionSpend, limits.DimensionTokens} {
			meter := reservation.Meter{Scope: holder.scope, Holder: holder.id, Dimension: dimension, Interval: limits.IntervalDay}
			attempt.Rules = append(attempt.Rules, reservation.Rule{Meter: meter, Limit: 1000, PolicyRevision: "revision-1", HistoryID: "history-1"})
			require.NoError(t, repository.EstablishWindow(t.Context(), meter, now, 0, reservation.History{ID: "history-1", Proof: "empty-fixture-history"}))
		}
	}
	_, err = repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, repository.RetainEvidence(t.Context(), attempt.ID, reservation.Evidence{ID: "measured", Quantities: reservation.Quantities{"output": 200}, Tokens: 250}))
	worker, err := reservation.NewRecovery(repository, raw)
	require.NoError(t, err)
	closed, err = witness.Close(t.Context(), approved)
	require.NoError(t, err)
	checkRefused := func() {
		t.Helper()
		failures := 0
		complete := false
		for range 100 {
			result, err := worker.Pass(t.Context(), 32)
			if result.Failed > 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			failures += result.Failed
			if result.Complete {
				complete = true
				break
			}
		}
		require.True(t, complete)
		require.Equal(t, 1, failures)
		record, err := repository.Inspect(t.Context(), attempt.ID)
		require.NoError(t, err)
		require.Equal(t, reservation.Uncertain, record.State)
		require.NotNil(t, record.Pending)
		for _, rule := range attempt.Rules {
			state, err := repository.Window(t.Context(), rule.Meter, now)
			require.NoError(t, err)
			require.EqualValues(t, 600, state.Reserved)
			require.Zero(t, state.Consumed)
		}
	}
	checkRefused()
	_, err = witness.ApproveAuthority(t.Context(), backend, closed, identity, "test/reconciled-again", "second")
	require.NoError(t, err)
	checkRefused() // An old worker must not adopt the new approval silently.
	authority, err = witness.OpenAuthority(t.Context(), backend, config.Valkey.DeploymentID)
	require.NoError(t, err)
	repository, err = reservation.Open(authority)
	require.NoError(t, err)
	worker, err = reservation.NewRecovery(repository, raw)
	require.NoError(t, err)
	complete := false
	for range 100 {
		result, err := worker.Pass(t.Context(), 32)
		require.NoError(t, err)
		if result.Complete {
			complete = true
			break
		}
	}
	require.True(t, complete)
	record, err := repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, reservation.Settled, record.State)
	require.Nil(t, record.Pending)
	require.Equal(t, "generation-a", record.Attempt.CatalogGeneration)
	require.EqualValues(t, 200, *record.NanoUSD)
	for _, rule := range attempt.Rules {
		state, err := repository.Window(t.Context(), rule.Meter, now)
		require.NoError(t, err)
		require.Zero(t, state.Reserved)
		expected := int64(200)
		if rule.Meter.Dimension == limits.DimensionTokens {
			expected = 250
		}
		require.Equal(t, expected, state.Consumed)
	}
}
