package reservation

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExplicitNoChargeSettlement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			evidence := Evidence{ID: "administrator-decision", NoCharge: true}
			require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence))
			reopened, err := Open(f.store)
			require.NoError(t, err)
			require.NoError(t, reopened.Reconcile(t.Context(), attempt.ID, evidence))
			record, err := reopened.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.True(t, record.Evidence.NoCharge)
			require.Empty(t, record.Evidence.Quantities, "no-charge must not fabricate measured units")
			require.NotNil(t, record.NanoUSD)
			require.Zero(t, *record.NanoUSD)
			for _, rule := range attempt.Rules {
				window, err := reopened.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.Zero(t, window.Reserved)
				require.Zero(t, window.Consumed)
			}
			require.ErrorIs(t, reopened.Reconcile(t.Context(), attempt.ID, Evidence{ID: evidence.ID, Quantities: Quantities{"output": 0}}), ErrIdentityConflict)
		})
	}
}

func TestNoChargeRejectsInventedMeasurements(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	for _, evidence := range []Evidence{
		{ID: "d", NoCharge: true, Tokens: 1},
		{ID: "d", NoCharge: true, Quantities: Quantities{"output": 0}},
	} {
		require.ErrorIs(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence), ErrInvalid)
		require.ErrorIs(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence), ErrInvalid)
	}
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, Dispatched, record.State)
}

func TestNoChargeRetainedRecovery(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			evidence := Evidence{ID: "admin-retained", NoCharge: true}
			require.NoError(t, f.repository.RetainEvidence(t.Context(), attempt.ID, evidence))
			for _, rule := range attempt.Rules {
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 600, state.Reserved)
			}
			reopened, err := Open(f.store)
			require.NoError(t, err)
			require.NoError(t, reopened.ReconcileRetained(t.Context(), attempt.ID))
			record, err := reopened.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, Settled, record.State)
			require.True(t, record.Evidence.NoCharge)
			require.Nil(t, record.Pending)
			require.Zero(t, *record.NanoUSD)
		})
	}
}

func TestExplicitNoChargeResolvesUnknownTokenOnlyCost(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	attempt.TokenOnly, attempt.Valuation, attempt.Bound = true, Valuation{}, nil
	attempt.Rules = []Rule{attempt.Rules[1]}
	record, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.Nil(t, record.NanoUSD)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "provider-no-charge", NoCharge: true}))
	record, err = f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.NotNil(t, record.NanoUSD)
	require.Zero(t, *record.NanoUSD)
}
