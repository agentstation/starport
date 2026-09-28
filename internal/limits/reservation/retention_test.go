package reservation

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReconciliationRetention(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		for _, loss := range []string{"none", "attempt", "correction"} {
			t.Run(backend+"/"+loss, func(t *testing.T) {
				f, clock, attempt := horizonFixture(t, backend)
				evidence := Evidence{ID: "measured", Tokens: 140, Quantities: Quantities{"output": 120}}
				settleCorrectionFixture(t, f, attempt, evidence)
				correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Tokens: 100, Quantities: Quantities{"output": 80}})
				_, err := f.repository.Correct(t.Context(), attempt.ID, correction)
				require.NoError(t, err)
				before, err := f.repository.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				windows := make([]WindowState, len(attempt.Rules))
				for i, rule := range attempt.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, before.AdmittedAt)
					require.NoError(t, err)
					windows[i] = *state
				}
				clock.now = clock.now.Add(365 * 24 * time.Hour)
				switch loss {
				case "attempt":
					require.NoError(t, f.raw.Delete(t.Context(), storageKey("attempt", attempt.ID)))
				case "correction":
					require.NoError(t, f.raw.Delete(t.Context(), correctionKey(attempt.ID, correction.ID)))
				}
				reopened, err := Open(clock)
				require.NoError(t, err)
				if loss == "attempt" {
					require.ErrorIs(t, reopened.Reconcile(t.Context(), attempt.ID, correction.Evidence), storage.ErrNotFound)
				} else {
					require.NoError(t, reopened.Reconcile(t.Context(), attempt.ID, correction.Evidence))
				}
				require.Error(t, reopened.Reconcile(t.Context(), attempt.ID, Evidence{ID: "late-refund", NoCharge: true}))
				_, err = reopened.Correct(t.Context(), attempt.ID, correction)
				if loss == "correction" {
					require.Error(t, err, "a missing receipt cannot authorize a repeated correction")
				} else {
					require.NoError(t, err, "an immutable receipt can acknowledge an accepted correction without applying it again")
				}
				for i, rule := range attempt.Rules {
					state, err := reopened.Window(t.Context(), rule.Meter, before.AdmittedAt)
					require.NoError(t, err)
					require.Equal(t, windows[i], *state, "missing or old evidence cannot refund a settled charge")
				}
			})
		}
	}
}
