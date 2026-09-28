package reservation

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func retainFixture(t *testing.T, f fixture) Attempt {
	t.Helper()
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, f.repository.RetainEvidence(t.Context(), attempt.ID, Evidence{
		ID: "measured", Quantities: Quantities{"output": 200}, Tokens: 250,
	}))
	return attempt
}

func checkRecovered(t *testing.T, f fixture, attempt Attempt) {
	t.Helper()
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, Settled, record.State)
	require.Nil(t, record.Pending)
	require.Equal(t, "generation-a", record.Attempt.CatalogGeneration)
	require.EqualValues(t, 200, *record.NanoUSD)
	for _, rule := range attempt.Rules {
		state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
		require.NoError(t, err)
		require.Zero(t, state.Reserved)
		expected := int64(200)
		if rule.Meter.Dimension == limits.DimensionTokens {
			expected = 250
		}
		require.Equal(t, expected, state.Consumed)
	}
}

func TestRecoveryPreservesUnmeasuredAndCorruptAttempts(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			measured := retainFixture(t, f)
			missing := attemptFixture()
			f.provision(t, &missing)
			_, err := f.repository.Reserve(t.Context(), missing)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), missing.ID))
			require.NoError(t, f.repository.MarkUncertain(t.Context(), missing.ID, "provider_usage_unconfirmed"))
			require.NoError(t, f.raw.Set(t.Context(), attemptPrefix+"corrupt", []byte("broken-json")))
			// A valid record at a foreign key must not grant settlement permission.
			data, err := f.raw.Get(t.Context(), storageKey("attempt", missing.ID))
			require.NoError(t, err)
			require.NoError(t, f.raw.Set(t.Context(), storageKey("attempt", "foreign"), data))
			worker, err := NewRecovery(f.repository, f.raw)
			require.NoError(t, err)
			var scanned, recovered, held, failed int
			scanContext, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			for {
				require.NoError(t, scanContext.Err(), "complete the namespace scan before asserting record counts")
				result, err := worker.Pass(scanContext, 16)
				if result.Failed > 0 {
					require.ErrorIs(t, err, ErrUnavailable)
				} else {
					require.NoError(t, err)
				}
				scanned += result.Scanned
				recovered += result.Recovered
				held += result.Held
				failed += result.Failed
				if result.Complete {
					break
				}
			}
			require.Equal(t, 4, scanned)
			require.Equal(t, 1, recovered)
			require.Equal(t, 1, held)
			require.Equal(t, 2, failed)
			checkRecovered(t, f, measured)
			for _, rule := range missing.Rules {
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 600, state.Reserved)
				require.Zero(t, state.Consumed)
			}
		})
	}
}

func TestRecoveryConcurrentWorkersSettleOnce(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := retainFixture(t, f)
			var group sync.WaitGroup
			outcomes := make(chan error, 4)
			for range 4 {
				worker, err := NewRecovery(f.repository, f.raw)
				require.NoError(t, err)
				group.Go(func() {
					for range 100 {
						result, err := worker.Pass(t.Context(), 32)
						if err != nil || result.Complete {
							outcomes <- err
							return
						}
					}
					outcomes <- errors.New("scan did not complete")
				})
			}
			group.Wait()
			close(outcomes)
			for err := range outcomes {
				require.NoError(t, err)
			}
			checkRecovered(t, f, attempt)
		})
	}
}

type recoveryAckLoss struct {
	storage.TimeBoundStore
	lost atomic.Bool
}

func (s *recoveryAckLoss) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window)
	if err == nil && !s.lost.Swap(true) {
		return ErrUnavailable
	}
	return err
}

func TestRecoveryRetriesLostSettlementAcknowledgement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := retainFixture(t, f)
			fault := &recoveryAckLoss{TimeBoundStore: f.store}
			repository, err := Open(fault)
			require.NoError(t, err)
			worker, err := NewRecovery(repository, f.raw)
			require.NoError(t, err)
			failures := 0
			for range 100 {
				result, err := worker.Pass(t.Context(), 32)
				if err != nil {
					require.ErrorIs(t, err, ErrUnavailable)
				}
				failures += result.Failed
				if result.Complete {
					break
				}
			}
			require.Equal(t, 1, failures)
			for range 100 {
				result, err := worker.Pass(t.Context(), 32)
				require.NoError(t, err)
				require.Zero(t, result.Recovered, "committed usage must not be charged again")
				if result.Complete {
					break
				}
			}
			checkRecovered(t, f, attempt)
		})
	}
}

func TestRecoveryNativePaginationReachesEveryAttempt(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := retainFixture(t, f)
			require.NoError(t, f.repository.ReconcileRetained(t.Context(), attempt.ID))
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			// Seed settled history to exercise enumeration without manufacturing
			// outstanding balances. These records must never mutate meters.
			for range 1001 {
				record.Attempt.ID = rand.Text()
				data, err := json.Marshal(record)
				require.NoError(t, err)
				require.NoError(t, f.raw.Set(t.Context(), storageKey("attempt", record.Attempt.ID), data))
			}
			pending := retainFixture(t, f)
			worker, err := NewRecovery(f.repository, f.raw)
			require.NoError(t, err)
			var scanned int
			complete := false
			for range 500 {
				result, err := worker.Pass(t.Context(), 17)
				require.NoError(t, err)
				require.LessOrEqual(t, result.Scanned, 17)
				scanned += result.Scanned
				if result.Complete {
					complete = true
					break
				}
			}
			require.True(t, complete)
			require.GreaterOrEqual(t, scanned, 1003, "native scans may duplicate keys but cannot truncate history")
			checkRecovered(t, f, pending)
			checkRecovered(t, f, attempt)
		})
	}
}

type recoveryPages func(context.Context, string, string, int) (storage.KeyPage, error)

func (s recoveryPages) ScanPage(ctx context.Context, prefix, cursor string, count int) (storage.KeyPage, error) {
	return s(ctx, prefix, cursor, count)
}

func TestRecoveryRetainsOversizedPageAndEmptyContinuation(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := retainFixture(t, f)
	key := storageKey("attempt", attempt.ID)
	pages := 0
	scanner := recoveryPages(func(_ context.Context, prefix, cursor string, _ int) (storage.KeyPage, error) {
		require.Equal(t, attemptPrefix, prefix)
		pages++
		switch pages {
		case 1:
			require.Empty(t, cursor)
			return storage.KeyPage{Next: "continue"}, nil
		case 2:
			require.Equal(t, "continue", cursor)
			// Native COUNT is a hint. Duplicates must remain safe as well.
			return storage.KeyPage{Keys: []string{key, key, key, key, key}}, nil
		default:
			return storage.KeyPage{}, fmt.Errorf("unexpected scan %d", pages)
		}
	})
	worker, err := NewRecovery(f.repository, scanner)
	require.NoError(t, err)
	for range 2 {
		result, err := worker.Pass(t.Context(), 1)
		require.NoError(t, err)
		require.False(t, result.Complete)
		require.Zero(t, result.Scanned)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = worker.Pass(ctx, 1)
	require.ErrorIs(t, err, context.Canceled)
	for i := range 5 {
		result, err := worker.Pass(t.Context(), 1)
		require.NoError(t, err)
		require.Equal(t, i == 4, result.Complete)
		require.Equal(t, 1, result.Scanned)
		if i == 0 {
			require.Equal(t, 1, result.Recovered)
		} else {
			require.Zero(t, result.Recovered)
		}
	}
	require.Equal(t, 2, pages)
	checkRecovered(t, f, attempt)
}

func TestRecoveryBoundsEmptyScansAndResetsFailedCursor(t *testing.T) {
	f := openFixture(t, "badger")
	calls := 0
	fail := false
	scanner := recoveryPages(func(_ context.Context, _, cursor string, _ int) (storage.KeyPage, error) {
		calls++
		if fail {
			require.Equal(t, "next", cursor)
			return storage.KeyPage{}, storage.ErrInvalidScan
		}
		if calls == 1 || calls == 5 {
			require.Empty(t, cursor)
		}
		return storage.KeyPage{Next: "next"}, nil
	})
	worker, err := NewRecovery(f.repository, scanner)
	require.NoError(t, err)
	result, err := worker.Pass(t.Context(), 3)
	require.NoError(t, err)
	require.False(t, result.Complete)
	require.Equal(t, 3, calls)
	fail = true
	_, err = worker.Pass(t.Context(), 3)
	require.ErrorIs(t, err, storage.ErrInvalidScan)
	require.Equal(t, 4, calls)
	fail = false
	_, err = worker.Pass(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, 5, calls)
}

type recoveryDuringRead struct {
	storage.TimeBoundStore
	key      string
	settle   func() error
	finished bool
}

func (s *recoveryDuringRead) ReadWithLifetime(ctx context.Context, key string, bound int) ([]byte, time.Duration, error) {
	if err := s.beforeRead(key); err != nil {
		return nil, 0, err
	}
	return s.TimeBoundStore.ReadWithLifetime(ctx, key, bound)
}

func (s *recoveryDuringRead) ReadBatchWithLifetime(ctx context.Context, keys []string, bound int) ([]storage.LifetimeValue, error) {
	for _, key := range keys {
		if err := s.beforeRead(key); err != nil {
			return nil, err
		}
	}
	return s.TimeBoundStore.ReadBatchWithLifetime(ctx, keys, bound)
}

func (s *recoveryDuringRead) beforeRead(key string) error {
	if key == s.key && !s.finished {
		s.finished = true
		if err := s.settle(); err != nil {
			return err
		}
	}
	return nil
}

func TestRecoveryHandlesConcurrentSettlementBetweenReads(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := retainFixture(t, f)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			fault := &recoveryDuringRead{
				TimeBoundStore: f.store,
				key:            meterKey(record.Bindings[0].Rule.Meter, record.Bindings[0].Window),
				settle:         func() error { return f.repository.ReconcileRetained(t.Context(), attempt.ID) },
			}
			repository, err := Open(fault)
			require.NoError(t, err)
			require.NoError(t, repository.ReconcileRetained(t.Context(), attempt.ID))
			require.True(t, fault.finished, "a peer must settle between the attempt and meter reads")
			checkRecovered(t, f, attempt)
		})
	}
}

func TestRecoveryDoesNotRepairUnchangedInconsistentBalances(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := retainFixture(t, f)
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	binding := record.Bindings[0]
	state, err := f.repository.Window(t.Context(), binding.Rule.Meter, f.now)
	require.NoError(t, err)
	state.Reserved = 0
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, f.raw.Set(t.Context(), meterKey(binding.Rule.Meter, binding.Window), data))
	require.ErrorIs(t, f.repository.ReconcileRetained(t.Context(), attempt.ID), ErrUnavailable)
	retained, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, record, retained)
}
