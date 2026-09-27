package recovery

import (
	"context"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestFreshFleetBootstrapPermission(t *testing.T) {
	w, _, backend := freshTestStores(t)
	record, err := w.InitializeFresh(t.Context(), backend, "bootstrap", FreshRequest{OperationID: "first", Evidence: "test/fresh"})
	require.NoError(t, err)
	require.NoError(t, w.CheckBootstrap(t.Context(), record))
	for _, field := range []string{"deployment", "epoch", "backend", "evidence"} {
		other := record
		switch field {
		case "deployment":
			other.DeploymentID += "-other"
		case "epoch":
			other.Epoch++
		case "backend":
			other.BackendID += "-other"
		case "evidence":
			other.Evidence += "-other"
		}
		require.ErrorIs(t, w.CheckBootstrap(t.Context(), other), ErrConflict)
		require.ErrorIs(t, w.ConsumeBootstrap(t.Context(), other), ErrConflict)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, w.ConsumeBootstrap(canceled, record))
	require.NoError(t, w.CheckBootstrap(t.Context(), record))
	const callers = 8
	results := make(chan error, callers)
	var work sync.WaitGroup
	for range callers {
		work.Go(func() { results <- w.ConsumeBootstrap(t.Context(), record) })
	}
	work.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrConflict)
		}
	}
	require.Equal(t, 1, winners)
	require.ErrorIs(t, w.CheckBootstrap(t.Context(), record), ErrBootstrapConsumed)
	_, err = w.InitializeFresh(t.Context(), backend, "bootstrap", FreshRequest{OperationID: "first", Evidence: "test/fresh"})
	require.ErrorIs(t, err, sqlstore.ErrNotFresh)
	require.ErrorIs(t, w.CheckBootstrap(t.Context(), record), ErrBootstrapConsumed)
	closed, err := w.Close(t.Context(), record)
	require.NoError(t, err)
	require.ErrorIs(t, w.CheckBootstrap(t.Context(), closed), ErrClosed)
	approved, err := w.Approve(t.Context(), closed, record.BackendID, "reconciled")
	require.NoError(t, err)
	require.ErrorIs(t, w.CheckBootstrap(t.Context(), approved), ErrBootstrapConsumed)
}

func TestRecoveryApprovalCannotInferBootstrap(t *testing.T) {
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	w, err := New(db)
	require.NoError(t, err)
	closed, err := w.Initialize(t.Context(), "existing")
	require.NoError(t, err)
	approved, err := w.Approve(t.Context(), closed, "backend", "recovery")
	require.NoError(t, err)
	require.ErrorIs(t, w.CheckBootstrap(t.Context(), approved), ErrBootstrapConsumed)
	require.ErrorIs(t, w.ConsumeBootstrap(t.Context(), approved), ErrConflict)
}
