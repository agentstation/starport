package reservation

import (
	"context"
	"strings"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func horizonFixture(t *testing.T, backend string) (fixture, *selectedAuthorityTime, Attempt) {
	t.Helper()
	f := openFixture(t, backend)
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: f.now.Truncate(time.Second).Add(time.Second)}
	var err error
	f.repository, err = Open(clock)
	require.NoError(t, err)
	f.store, f.now = clock, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	return f, clock, attempt
}

func TestCorrectionHorizon(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f, clock, attempt := horizonFixture(t, backend)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			first := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Tokens: 20, Quantities: Quantities{"output": 10}})
			clock.now = clock.now.Add(90*24*time.Hour - time.Second)
			receipt, err := f.repository.Correct(t.Context(), attempt.ID, first)
			require.NoError(t, err)
			next := correctionFixture(t, f, attempt.ID, Evidence{ID: "late", NoCharge: true})
			next.ID = "late-correction"
			clock.now = clock.now.Add(time.Second)
			_, err = f.repository.Correct(t.Context(), attempt.ID, next)
			require.ErrorIs(t, err, limits.ErrCorrectionExpired, "a correction must not restart the 90-day horizon")
			retry, err := f.repository.Correct(t.Context(), attempt.ID, first)
			require.NoError(t, err, "accepted exact retries remain valid after the deadline")
			require.Equal(t, receipt, retry)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, &first.Evidence, record.Evidence)
			for _, binding := range record.Bindings {
				state, err := f.repository.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				require.Zero(t, state.Reserved)
			}
		})
	}
}

func TestUncertainReservationHasNoCorrectionDeadline(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f, clock, attempt := horizonFixture(t, backend)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			require.NoError(t, f.repository.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
			clock.now = clock.now.Add(365 * 24 * time.Hour)
			reopened, err := Open(clock)
			require.NoError(t, err)
			record, err := reopened.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, Uncertain, record.State)
			for _, binding := range record.Bindings {
				state, err := reopened.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				require.Equal(t, binding.Amount, state.Reserved)
			}
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "provider-no-charge", NoCharge: true})
			_, err = reopened.Correct(t.Context(), attempt.ID, correction)
			require.NoError(t, err, "unresolved evidence has no retention deadline")
		})
	}
}

type delayedCorrectionAuthority struct {
	*selectedAuthorityTime
	deadline time.Time
}

func (s *delayedCorrectionAuthority) CompareAndSwapInWindow(ctx context.Context, changes []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	for _, change := range changes {
		if strings.HasPrefix(change.Key, "budget:v1:correction:") {
			s.now = s.deadline
		}
	}
	return s.selectedAuthorityTime.CompareAndSwapInWindow(ctx, changes, window)
}

func TestCorrectionDeadlineGuardsAtomicWrite(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f, clock, attempt := horizonFixture(t, backend)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			before, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "delayed", Quantities: Quantities{"output": 10}, Tokens: 10})
			deadline := clock.now.Add(limits.CorrectionHorizon)
			delayed := &delayedCorrectionAuthority{selectedAuthorityTime: clock, deadline: deadline}
			repository, err := Open(delayed)
			require.NoError(t, err)
			clock.now = deadline.Add(-time.Second)
			_, err = repository.Correct(t.Context(), attempt.ID, correction)
			require.Error(t, err)
			after, err := repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, before, after, "a delayed write cannot publish any part of a correction")
			_, err = repository.InspectCorrection(t.Context(), attempt.ID, correction.ID)
			require.ErrorIs(t, err, storage.ErrNotFound)
		})
	}
}
