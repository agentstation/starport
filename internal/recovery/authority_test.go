package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBudgetAuthorityRequiresBothApprovals(t *testing.T) {
	w, kv, backend := freshTestStores(t)
	_, err := w.OpenAuthority(t.Context(), backend, "test-deployment")
	require.ErrorIs(t, err, ErrClosed)
	approved, err := w.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: "first", Evidence: "test/fresh-state"})
	require.NoError(t, err)
	authority, err := w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.NoError(t, err)
	require.NoError(t, authority.Check(t.Context()))
	guard, err := kv.Get(t.Context(), authorityKey)
	require.NoError(t, err)
	require.NoError(t, kv.SetWithTTL(t.Context(), authorityKey, guard, time.Minute))
	_, err = w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.ErrorIs(t, err, ErrClosed)
	require.NoError(t, kv.Delete(t.Context(), authorityKey))
	_, err = w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.ErrorIs(t, err, ErrClosed, "a live backend cannot recreate a missing epoch")
	require.ErrorIs(t, authority.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{{Key: "budget-test", NewValue: []byte("not-written")}}, storage.TimeWindow{}), storage.ErrConflict)
	_, err = kv.Get(t.Context(), "budget-test")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

type interruptedAuthorityStore struct {
	storage.TimeBoundStore
	before, after func()
}

func (s interruptedAuthorityStore) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if s.before != nil {
		s.before()
	}
	err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window)
	if s.after != nil {
		s.after()
	}
	return err
}

func TestBudgetAuthorityFencesDelayedWrites(t *testing.T) {
	for _, boundary := range []string{"before-native-write", "after-native-write"} {
		t.Run(boundary, func(t *testing.T) {
			w, kv, backend := freshTestStores(t)
			t.Cleanup(func() { require.NoError(t, kv.Delete(context.Background(), "budget-test")) })
			approved, err := w.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: "first", Evidence: "test/fresh-state"})
			require.NoError(t, err)
			authority, err := w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
			require.NoError(t, err)
			fault := interruptedAuthorityStore{TimeBoundStore: authority.store}
			if boundary == "before-native-write" {
				fault.before = func() {
					closed, err := w.Close(t.Context(), approved)
					require.NoError(t, err)
					reopened, err := w.ApproveAuthority(t.Context(), backend, closed, approved.BackendID, "test/reconciled-history", "second")
					require.NoError(t, err)
					require.Greater(t, reopened.Epoch, approved.Epoch)
					// A lost approval response permits only the exact operation.
					again, err := w.ApproveAuthority(t.Context(), backend, closed, approved.BackendID, "test/reconciled-history", "second")
					require.NoError(t, err)
					require.Equal(t, reopened, again)
					_, err = w.ApproveAuthority(t.Context(), backend, closed, approved.BackendID, "test/reconciled-history", "different")
					require.ErrorIs(t, err, ErrConflict)
				}
			} else {
				fault.after = func() { _, err := w.Close(t.Context(), approved); require.NoError(t, err) }
			}
			authority.store = fault
			err = authority.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{{Key: "budget-test", NewValue: []byte("reserved")}}, storage.TimeWindow{})
			if boundary == "before-native-write" {
				require.ErrorIs(t, err, storage.ErrConflict)
				_, err = kv.Get(t.Context(), "budget-test")
				require.ErrorIs(t, err, storage.ErrNotFound)
			} else {
				require.ErrorIs(t, err, ErrClosed)
				data, err := kv.Get(t.Context(), "budget-test")
				require.NoError(t, err)
				require.Equal(t, "reserved", string(data), "uncertain acknowledgement must not refund committed capacity")
			}
			require.Error(t, authority.Check(t.Context()))
		})
	}
}

func TestBudgetAuthorityClosedAndChangedEpoch(t *testing.T) {
	w, kv, backend := freshTestStores(t)
	t.Cleanup(func() { require.NoError(t, kv.Delete(context.Background(), "budget-test")) })
	approved, err := w.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: "first", Evidence: "test/fresh-state"})
	require.NoError(t, err)
	old, err := w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.NoError(t, err)
	closed, err := w.Close(t.Context(), approved)
	require.NoError(t, err)
	write := []storage.CompareAndSwapMutation{{Key: "budget-test", NewValue: []byte("reserved")}}
	require.ErrorIs(t, old.CompareAndSwapInWindow(t.Context(), write, storage.TimeWindow{}), ErrClosed)
	_, err = w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.ErrorIs(t, err, ErrClosed)
	_, err = w.ApproveAuthority(t.Context(), backend, closed, approved.BackendID, "test/reconciled-history", "second")
	require.NoError(t, err)
	require.ErrorIs(t, old.CompareAndSwapInWindow(t.Context(), write, storage.TimeWindow{}), ErrConflict)
	current, err := w.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.NoError(t, err)
	require.NoError(t, current.CompareAndSwapInWindow(t.Context(), write, storage.TimeWindow{}))
}
