package reservation

import (
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRetainedUsageSurvivesRestart(t *testing.T) {
	options := storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20}
	store, err := storage.OpenBadger(options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	repository, err := Open(store)
	require.NoError(t, err)
	now, err := store.AuthorityTime(t.Context())
	require.NoError(t, err)
	f := fixture{repository: repository, store: store, raw: store, now: now}
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err = repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, repository.Begin(t.Context(), attempt.ID))
	evidence := Evidence{ID: "provider-measured", Quantities: Quantities{"output": 200}, Tokens: 250}
	require.NoError(t, repository.RetainEvidence(t.Context(), attempt.ID, evidence))
	require.NoError(t, store.Close())
	store, err = storage.OpenBadger(options)
	require.NoError(t, err)
	repository, err = Open(store)
	require.NoError(t, err)
	record, err := repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Pending)
	require.Equal(t, Uncertain, record.State)
	for _, rule := range attempt.Rules {
		state, err := repository.Window(t.Context(), rule.Meter, now)
		require.NoError(t, err)
		require.EqualValues(t, 600, state.Reserved)
	}
	worker, err := NewRecovery(repository, store)
	require.NoError(t, err)
	result, err := worker.Pass(t.Context(), 16)
	require.NoError(t, err)
	require.True(t, result.Complete)
	require.Equal(t, 1, result.Recovered)
	require.NoError(t, repository.ReconcileRetained(t.Context(), attempt.ID))
	record, err = repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, Settled, record.State)
	require.Equal(t, &evidence, record.Evidence)
	require.Nil(t, record.Pending)
	require.EqualValues(t, 200, *record.NanoUSD)
}

func TestRetainedUsageCannotChangeEvidenceOrRefund(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			evidence := Evidence{ID: "measured", Quantities: Quantities{"output": 200}, Tokens: 250}
			require.ErrorIs(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence), ErrTransition)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			require.NoError(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence))
			require.NoError(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence))
			evidence.Tokens = 0
			require.ErrorIs(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence), ErrIdentityConflict)
			require.ErrorIs(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence), ErrIdentityConflict)
			require.ErrorIs(t, f.repository.CancelBeforeDispatch(t.Context(), attempt.ID), ErrTransition)
			require.NoError(t, f.repository.ReconcileRetained(t.Context(), attempt.ID))
			state, err := f.repository.Window(t.Context(), attempt.Rules[1].Meter, f.now)
			require.NoError(t, err)
			require.EqualValues(t, 250, state.Consumed)
			require.Zero(t, state.Reserved)
		})
	}
}
