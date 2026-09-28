package reservation

import (
	"encoding/json/v2"
	"fmt"
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

func TestDisputeCountRetainsEveryAttempt(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			ids := []string{attempt.ID, attempt.ID + "-second"}
			for _, id := range ids {
				attempt.ID = id
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				require.NoError(t, f.repository.Begin(t.Context(), id))
				require.NoError(t, f.repository.Reconcile(t.Context(), id, Evidence{ID: "accepted", NoCharge: true}))
			}
			for i, id := range ids {
				require.NoError(t, f.repository.FlagDispute(t.Context(), id, fmt.Sprintf("dispute-%d", i)))
				require.NoError(t, f.repository.FlagDispute(t.Context(), id, fmt.Sprintf("dispute-%d", i)))
			}
			for _, rule := range attempt.Rules {
				data, err := f.repository.read(t.Context(), meterKey(rule.Meter, windowFor(rule.Meter.Interval, f.now)))
				require.NoError(t, err)
				var persisted struct {
					ActiveDisputes int64 `json:"active_disputes"`
				}
				require.NoError(t, json.Unmarshal(data, &persisted))
				require.EqualValues(t, 2, persisted.ActiveDisputes, "each disputed attempt must retain its own restriction")
			}
		})
	}
}
