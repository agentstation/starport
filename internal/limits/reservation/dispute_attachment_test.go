package reservation

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDisputePublicationRejectsStaleJob(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			key := "jobs:fixture:" + attempt.ID
			require.NoError(t, f.raw.Set(t.Context(), key, []byte("concurrent correction")))
			mutation := storage.CompareAndSwapMutation{Key: key, ExpectedValue: []byte("inspected"), NewValue: []byte("late evidence")}
			require.ErrorIs(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "late", mutation), storage.ErrConflict)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Empty(t, record.DisputeID, "failed evidence publication must not change the budget")
			for _, rule := range attempt.Rules {
				window, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.Zero(t, window.ActiveDisputes)
			}
		})
	}
}

// disputeWriteFault interrupts the atomic write or changes the job after its read.
type disputeWriteFault struct {
	storage.TimeBoundStore
	before func()
	after  bool
	fail   bool
}

var errDisputeWrite = errors.New("dispute write interrupted")

func (s *disputeWriteFault) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if s.before != nil {
		s.before()
		s.before = nil
	}
	if s.fail && !s.after {
		return errDisputeWrite
	}
	if err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window); err != nil {
		return err
	}
	if s.fail {
		return errDisputeWrite
	}
	return nil
}

func TestDisputePublicationAtomicWrite(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		for _, mode := range []string{"success", "before", "after", "race", "existing dispute"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := openFixture(t, backend)
				attempt := attemptFixture()
				f.provision(t, &attempt)
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
				key := "jobs:fixture:" + attempt.ID
				require.NoError(t, f.raw.Set(t.Context(), key, []byte("inspected")))
				mutation := storage.CompareAndSwapMutation{Key: key, ExpectedValue: []byte("inspected"), NewValue: []byte("late evidence")}
				if mode == "existing dispute" {
					require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
				}
				fault := &disputeWriteFault{TimeBoundStore: f.store, fail: mode == "before" || mode == "after", after: mode == "after"}
				if mode == "race" {
					fault.before = func() { require.NoError(t, f.raw.Set(t.Context(), key, []byte("concurrent correction"))) }
				}
				repository, err := Open(fault)
				require.NoError(t, err)
				err = repository.FlagDisputeWith(t.Context(), attempt.ID, "late", mutation)
				committed := mode == "success" || mode == "after" || mode == "existing dispute"
				switch mode {
				case "before", "after":
					require.ErrorIs(t, err, errDisputeWrite)
				case "race":
					require.ErrorIs(t, err, storage.ErrConflict)
				default:
					require.NoError(t, err)
				}
				value, err := f.raw.Get(t.Context(), key)
				require.NoError(t, err)
				require.Equal(t, committed, string(value) == "late evidence")
				record, err := f.repository.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				require.Equal(t, committed, record.DisputeID == "late")
				for _, rule := range attempt.Rules {
					window, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					count := int64(0)
					if committed {
						count = 1
					}
					require.Equal(t, count, window.ActiveDisputes)
				}
				if mode == "before" {
					require.NoError(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "late", mutation))
				}
				if committed {
					require.ErrorIs(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "late", mutation), storage.ErrConflict, "caller must reread the committed job after a lost acknowledgement")
					require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
				}
			})
		}
	}
}

func TestDisputePublicationCannotAttachResolvedEvidence(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Quantities: Quantities{"output": 100}, Tokens: 100})
			_, err := f.repository.Correct(t.Context(), attempt.ID, correction)
			require.NoError(t, err)
			key := "jobs:fixture:" + attempt.ID
			require.NoError(t, f.raw.Set(t.Context(), key, []byte("inspected")))
			mutation := storage.CompareAndSwapMutation{Key: key, ExpectedValue: []byte("inspected"), NewValue: []byte("late evidence")}
			require.ErrorIs(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "late", mutation), ErrIdentityConflict)
			stored, err := f.raw.Get(t.Context(), key)
			require.NoError(t, err)
			require.Equal(t, "inspected", string(stored))
			mutation.Key = storageKey("attempt", attempt.ID)
			require.ErrorIs(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "other", mutation), ErrInvalid)
		})
	}
}
