package reservation

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDisputedChargeBlocksOriginalWindows(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			evidence := Evidence{ID: "administrator", NoCharge: true}
			require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence))
			settled, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late-response"))
			reopened, err := Open(f.store)
			require.NoError(t, err)
			require.NoError(t, reopened.FlagDispute(t.Context(), attempt.ID, "late-response"))
			record, err := reopened.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, "late-response", record.DisputeID)
			require.Equal(t, settled.Evidence, record.Evidence)
			require.Zero(t, *record.NanoUSD)
			for _, rule := range attempt.Rules {
				window, err := reopened.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.True(t, window.ReconciliationRequired)
				require.Zero(t, window.Reserved)
				require.Zero(t, window.Consumed)
			}
			attempt.ID += "-next"
			_, err = reopened.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, ErrUnavailable)
			require.ErrorIs(t, reopened.ReconcileRetained(t.Context(), record.Attempt.ID), ErrUnavailable)
			require.ErrorIs(t, reopened.Reconcile(t.Context(), record.Attempt.ID, evidence), ErrUnavailable)
			independent := attemptFixture()
			f.provision(t, &independent)
			_, err = reopened.Reserve(t.Context(), independent)
			require.NoError(t, err, "unrelated populations remain available")
		})
	}
}
