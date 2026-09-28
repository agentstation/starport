package reservation

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func correctionFixture(t *testing.T, f fixture, id string, evidence Evidence) Correction {
	t.Helper()
	record, err := f.repository.Inspect(t.Context(), id)
	require.NoError(t, err)
	return Correction{ID: "correction-1", ExpectedBinding: CorrectionBinding(*record), Actor: "key:administrator", EvidenceReference: "provider-statement:42", Reason: "Verified provider charge", Evidence: evidence}
}

func settleCorrectionFixture(t *testing.T, f fixture, attempt Attempt, evidence Evidence) {
	t.Helper()
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence))
}

func TestCorrectionPreservesOtherDisputesAndAudit(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			first := attemptFixture()
			f.provision(t, &first)
			second := first
			second.ID += "-other"
			original := Evidence{ID: "admin-original", NoCharge: true, Quantities: Quantities{}}
			settleCorrectionFixture(t, f, first, original)
			settleCorrectionFixture(t, f, second, original)
			for _, attempt := range []Attempt{first, second} {
				require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late-provider"))
			}
			evidence := Evidence{ID: "admin-corrected", Quantities: Quantities{"output": 120}, Tokens: 140}
			correction := correctionFixture(t, f, first.ID, evidence)
			receipt, err := f.repository.Correct(t.Context(), first.ID, correction)
			require.NoError(t, err)
			require.Equal(t, &original, receipt.Before.Evidence)
			require.Equal(t, "late-provider", receipt.Before.DisputeID)
			require.Equal(t, first.CatalogGeneration, receipt.Before.Attempt.CatalogGeneration)
			reopened, err := Open(f.store)
			require.NoError(t, err)
			retry, err := reopened.Correct(t.Context(), first.ID, correction)
			require.NoError(t, err)
			require.Equal(t, receipt, retry)
			require.NoError(t, reopened.FlagDispute(t.Context(), first.ID, "late-provider"), "a repeated observation cannot restore the resolved dispute")
			for _, rule := range first.Rules {
				state, err := reopened.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 1, state.ActiveDisputes)
				require.True(t, state.ReconciliationRequired)
				expected := int64(120)
				if rule.Meter.Dimension == limits.DimensionTokens {
					expected = 140
				}
				require.Equal(t, expected, state.Consumed)
				require.Zero(t, state.Reserved)
			}
			next := first
			next.ID += "-next"
			_, err = reopened.Reserve(t.Context(), next)
			require.ErrorIs(t, err, ErrUnavailable)
			secondCorrection := correctionFixture(t, f, second.ID, original)
			_, err = reopened.Correct(t.Context(), second.ID, secondCorrection)
			require.NoError(t, err)
			_, err = reopened.Reserve(t.Context(), next)
			require.NoError(t, err)
			require.NoError(t, reopened.CancelBeforeDispatch(t.Context(), next.ID))
			// A second decision appends history and refunds the first corrected charge.
			refund := correctionFixture(t, f, first.ID, original)
			refund.ID = "correction-2"
			refundReceipt, err := reopened.Correct(t.Context(), first.ID, refund)
			require.NoError(t, err)
			require.Equal(t, correction.ID, refundReceipt.Before.CorrectionID)
			require.Equal(t, &evidence, refundReceipt.Before.Evidence)
			retained, err := reopened.InspectCorrection(t.Context(), first.ID, correction.ID)
			require.NoError(t, err)
			require.Equal(t, receipt, retained)
			retry, err = reopened.Correct(t.Context(), first.ID, correction)
			require.NoError(t, err, "retrying an older receipt cannot undo its successor")
			require.Equal(t, receipt, retry)
			for _, rule := range first.Rules {
				state, err := reopened.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.Zero(t, state.Consumed)
				require.Zero(t, state.ActiveDisputes)
				require.False(t, state.ReconciliationRequired)
			}
			changed := correction
			changed.Reason = "different"
			_, err = reopened.Correct(t.Context(), first.ID, changed)
			require.ErrorIs(t, err, ErrIdentityConflict)
			stale := correction
			stale.ID = "stale"
			_, err = reopened.Correct(t.Context(), first.ID, stale)
			require.ErrorIs(t, err, ErrIdentityConflict)
		})
	}
}

func TestCorrectionConcurrentRetriesAndLostAcknowledgement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			require.NoError(t, f.repository.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", Quantities: Quantities{"output": 180}, Tokens: 200})
			fault := &correctionFault{TimeBoundStore: f.store, after: true}
			broken, err := Open(fault)
			require.NoError(t, err)
			_, err = broken.Correct(t.Context(), attempt.ID, correction)
			require.ErrorIs(t, err, errCorrectionFault)
			var group sync.WaitGroup
			results := make(chan error, 12)
			for range 12 {
				group.Go(func() { _, err := f.repository.Correct(t.Context(), attempt.ID, correction); results <- err })
			}
			group.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, Settled, record.State)
			require.EqualValues(t, 180, *record.NanoUSD)
			for _, rule := range attempt.Rules {
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				expected := int64(180)
				if rule.Meter.Dimension == limits.DimensionTokens {
					expected = 200
				}
				require.Equal(t, expected, state.Consumed)
				require.Zero(t, state.Reserved)
			}
		})
	}
}

var errCorrectionFault = errors.New("injected correction storage failure")

type correctionFault struct {
	storage.TimeBoundStore
	after bool
}

func (s *correctionFault) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if s.after {
		if err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window); err != nil {
			return err
		}
	}
	return errCorrectionFault
}

func TestCorrectionRefusesUnsafeCapacityRelease(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"write-failure", "overflow", "new-overflow", "counter-missing", "old-window-version", "unknown-charge", "unaudited"} {
				t.Run(scenario, func(t *testing.T) {
					f := openFixture(t, backend)
					attempt := attemptFixture()
					f.provision(t, &attempt)
					settleCorrectionFixture(t, f, attempt, Evidence{ID: "accepted", NoCharge: true})
					require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late-provider"))
					correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", NoCharge: true})
					repository := f.repository
					if scenario == "write-failure" {
						var err error
						repository, err = Open(&correctionFault{TimeBoundStore: f.store})
						require.NoError(t, err)
					}
					if scenario == "unknown-charge" {
						correction.Evidence = Evidence{ID: "incomplete"}
					}
					if scenario == "unaudited" {
						correction.Actor = ""
					}
					if scenario == "overflow" || scenario == "new-overflow" || scenario == "counter-missing" || scenario == "old-window-version" {
						binding := attempt.Rules[len(attempt.Rules)-1]
						window, previous, err := f.repository.readWindow(t.Context(), binding.Meter, windowFor(binding.Meter.Interval, f.now))
						require.NoError(t, err)
						switch scenario {
						case "overflow":
							window.Overflow, window.Consumed = true, math.MaxInt64
						case "new-overflow":
							window.Consumed = math.MaxInt64 - 1
							correction.Evidence = Evidence{ID: "too-large", Quantities: Quantities{"output": 2}, Tokens: 2}
						case "counter-missing":
							window.ActiveDisputes = 0
						case "old-window-version":
							window.Version = 1
						}
						mutation, err := encodeMutation(meterKey(window.Meter, window.Window), previous, window)
						require.NoError(t, err)
						require.NoError(t, f.store.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{mutation}, storage.TimeWindow{}))
					}
					before, err := f.repository.Inspect(t.Context(), attempt.ID)
					require.NoError(t, err)
					_, err = repository.Correct(t.Context(), attempt.ID, correction)
					require.Error(t, err)
					after, err := f.repository.Inspect(t.Context(), attempt.ID)
					require.NoError(t, err)
					require.Equal(t, before, after)
					_, err = f.repository.InspectCorrection(t.Context(), attempt.ID, correction.ID)
					require.ErrorIs(t, err, storage.ErrNotFound)
					// The first five counters must remain unchanged even when the last fails.
					for _, rule := range attempt.Rules[:len(attempt.Rules)-1] {
						state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
						require.NoError(t, err)
						require.EqualValues(t, 1, state.ActiveDisputes)
						require.True(t, state.ReconciliationRequired)
						require.Zero(t, state.Consumed)
					}
				})
			}
		})
	}
}

func TestConcurrentCorrectionsRequireCurrentBinding(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", Quantities: Quantities{"output": 80}, Tokens: 70})
			var group sync.WaitGroup
			results := make(chan error, 12)
			for i := range 12 {
				group.Go(func() {
					candidate := correction
					candidate.ID += string(rune('a' + i))
					_, err := f.repository.Correct(t.Context(), attempt.ID, candidate)
					results <- err
				})
			}
			group.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else {
					require.ErrorIs(t, err, ErrIdentityConflict)
				}
			}
			require.Equal(t, 1, successes)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.EqualValues(t, 80, *record.NanoUSD)
		})
	}
}

func TestCorrectionRequiresIntactPreviousAudit(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", NoCharge: true})
	_, err := f.repository.Correct(t.Context(), attempt.ID, correction)
	require.NoError(t, err)
	next := correctionFixture(t, f, attempt.ID, Evidence{ID: "refunded", NoCharge: true})
	next.ID = "second"
	require.NoError(t, f.raw.Delete(t.Context(), correctionKey(attempt.ID, correction.ID)))
	_, err = f.repository.Correct(t.Context(), attempt.ID, next)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = f.repository.InspectCorrection(t.Context(), attempt.ID, next.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestCorrectionConcurrentExactFirstSubmission(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", Quantities: Quantities{"output": 80}, Tokens: 70})
			var group sync.WaitGroup
			results := make(chan error, 12)
			start := make(chan struct{})
			for range 12 {
				group.Go(func() { <-start; _, err := f.repository.Correct(t.Context(), attempt.ID, correction); results <- err })
			}
			close(start)
			group.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			for _, rule := range attempt.Rules {
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				expected := int64(80)
				if rule.Meter.Dimension == limits.DimensionTokens {
					expected = 70
				}
				require.Equal(t, expected, state.Consumed)
				require.Zero(t, state.ActiveDisputes)
			}
		})
	}
}

func TestCorrectionTokenOnlyPreservesUnknownCost(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	var rules []Rule
	for _, rule := range attempt.Rules {
		if rule.Meter.Dimension == limits.DimensionTokens {
			rules = append(rules, rule)
		}
	}
	attempt.Rules, attempt.Valuation, attempt.Bound, attempt.TokenOnly = rules, Valuation{}, nil, true
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "measured", Tokens: 80})
	_, err := f.repository.Correct(t.Context(), attempt.ID, correction)
	require.NoError(t, err)
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Nil(t, record.NanoUSD)
	for _, rule := range attempt.Rules {
		state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
		require.NoError(t, err)
		require.EqualValues(t, 80, state.Consumed)
	}
}
