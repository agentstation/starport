package reservation

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type snapshotConflict struct {
	storage.TimeBoundStore
	beforeWrite func() error
	reads       int
}

func (s *snapshotConflict) ReadBatchWithLifetime(ctx context.Context, keys []string, bound int) ([]storage.LifetimeValue, error) {
	s.reads++
	return s.TimeBoundStore.ReadBatchWithLifetime(ctx, keys, bound)
}

func (s *snapshotConflict) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if before := s.beforeWrite; before != nil {
		s.beforeWrite = nil
		if err := before(); err != nil {
			return err
		}
	}
	return s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window)
}

func TestReservationSnapshotRefreshAfterConflict(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			peer := attempt
			peer.ID, peer.Bound, peer.TokenBound = rand.Text(), Quantities{"output": 300}, 300
			fault := &snapshotConflict{TimeBoundStore: f.store, beforeWrite: func() error {
				if _, err := f.repository.Reserve(t.Context(), peer); err != nil {
					return err
				}
				if err := f.repository.Begin(t.Context(), peer.ID); err != nil {
					return err
				}
				return f.repository.Reconcile(t.Context(), peer.ID, Evidence{ID: "peer-usage", Tokens: 300, Quantities: Quantities{"output": 300}})
			}}
			repository, err := Open(fault)
			require.NoError(t, err)
			record, err := repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.Equal(t, 2, fault.reads, "a failed conditional write must discard its read snapshot")
			for _, binding := range record.Bindings {
				window, err := f.repository.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				require.EqualValues(t, 300, window.Consumed)
				require.EqualValues(t, 600, window.Reserved)
			}
		})
	}
}

func TestReservationSnapshotRefusesExpiringHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			require.NoError(t, f.raw.ExpireAt(t.Context(), storageKey("history", attempt.Rules[0].Meter), time.Now().Add(time.Minute)))
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.ErrorIs(t, err, ErrUnavailable)
			_, err = f.repository.Inspect(t.Context(), attempt.ID)
			require.ErrorIs(t, err, storage.ErrNotFound)
			for _, rule := range attempt.Rules {
				window, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.Zero(t, window.Reserved)
			}
		})
	}
}
