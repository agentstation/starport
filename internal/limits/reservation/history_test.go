package reservation

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBudgetHistoryRollover(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC)}
			repository, err := Open(clock)
			require.NoError(t, err)
			f.repository, f.store, f.now = repository, clock, clock.now
			attempt := attemptFixture()
			f.provision(t, &attempt)
			first, err := repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, repository.Begin(t.Context(), attempt.ID))
			clock.now = clock.now.Add(2 * time.Hour)
			attempt.ID = rand.Text()
			second, err := repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NotEqual(t, first.Bindings[0].Window, second.Bindings[0].Window)
			require.NoError(t, repository.Reconcile(t.Context(), first.Attempt.ID, Evidence{ID: "late", Quantities: Quantities{"output": 100}, Tokens: 120}))
			old, err := repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
			require.NoError(t, err)
			require.EqualValues(t, 100, old.Consumed)
			require.Zero(t, old.Reserved)
			current, err := repository.Window(t.Context(), attempt.Rules[0].Meter, clock.now)
			require.NoError(t, err)
			require.Zero(t, current.Consumed)
			require.EqualValues(t, 600, current.Reserved)
			clock.now = f.now
			attempt.ID = rand.Text()
			_, err = repository.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, storage.ErrTimeWindowChanged, "clock rollback cannot reopen a prior budget window")
		})
	}
}

func TestBudgetHistoryDoesNotInventZero(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			meter := Meter{Scope: limits.ScopeKey, Holder: attempt.KeyID, Dimension: limits.DimensionTokens, Interval: limits.IntervalDay}
			attempt.Rules = []Rule{{Meter: meter, Limit: 1000, PolicyRevision: "1", HistoryID: "new-holder-history"}}
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, ErrHistoryUnknown)
			marker, err := FreshHistoryMutation(meter, attempt.Rules[0].HistoryID)
			require.NoError(t, err)
			// The fixture supplies a fresh-holder absence check in the same batch.
			holder := storage.CompareAndSwapMutation{Key: "fixture-holder:" + attempt.KeyID, NewValue: []byte("new")}
			require.NoError(t, f.raw.CompareAndSwapBatch(t.Context(), []storage.CompareAndSwapMutation{holder, marker}))
			record, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			attempt.ID = rand.Text()
			_, err = f.repository.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, ErrExhausted)
			// A missing counter under an opened history is lost state, not a fresh window.
			require.NoError(t, f.raw.Delete(t.Context(), meterKey(meter, record.Bindings[0].Window)))
			_, err = f.repository.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, ErrHistoryUnknown)
			require.Error(t, f.repository.EstablishWindow(t.Context(), meter, f.now, 0, History{ID: marker.Key, Proof: "attempted-reset"}))
			_, err = f.repository.Window(t.Context(), meter, f.now)
			require.ErrorIs(t, err, ErrHistoryUnknown, "failed restoration must not install a zero counter")
		})
	}
}

func TestChangedHistoryRequiresReconciliation(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	attempt.ID = rand.Text()
	for index := range attempt.Rules {
		attempt.Rules[index].HistoryID = "enabled-again-after-gap"
	}
	_, err = f.repository.Reserve(t.Context(), attempt)
	require.ErrorIs(t, err, ErrHistoryUnknown)
	state, err := f.repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
	require.NoError(t, err)
	require.EqualValues(t, 600, state.Reserved)
}
