package recovery

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryAuthorityKnownWithdrawalIsSticky(t *testing.T) {
	url := os.Getenv("TEST_VALKEY_URL")
	if url == "" {
		t.Skip("UNVERIFIED: native recovery admission requires Valkey")
	}
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "witness.db")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := New(db)
	require.NoError(t, err)
	deployment := "admission-recovery-" + rand.Text()
	store, err := storage.OpenValkey(storage.ValkeyConfig{DeploymentID: deployment, URL: url})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.BatchDelete(context.Background(), []string{authorityKey, "admission-must-remain-absent"}))
		require.NoError(t, store.Close())
	})
	backend := store.(storage.IncarnationProvider)
	identity, err := backend.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	closed, err := witness.Initialize(t.Context(), deployment)
	require.NoError(t, err)
	approved, err := witness.ApproveAuthority(t.Context(), backend, closed, identity, "isolated-admission-fixture", "first")
	require.NoError(t, err)
	authority, err := witness.OpenAuthority(t.Context(), backend, deployment)
	require.NoError(t, err)
	copied := *authority
	require.NoError(t, authority.CheckAdmission())
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, authority.Check(canceled), context.Canceled)
	require.NoError(t, authority.CheckAdmission(), "a failed query is not a known withdrawal")
	require.NoError(t, copied.CheckAdmission())
	closed, err = witness.Close(t.Context(), approved)
	require.NoError(t, err)
	require.ErrorIs(t, authority.Check(t.Context()), ErrClosed)
	require.ErrorIs(t, authority.CheckAdmission(), ErrClosed)
	require.ErrorIs(t, copied.CheckAdmission(), ErrClosed, "copied owners share the observed withdrawal")
	// Fault injection restores the original independent row while the old native
	// guard remains intact. This cannot renew an owner that observed withdrawal.
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET epoch=?,gate_open=1,backend_id=?,evidence=? WHERE deployment_id=?"), approved.Epoch, approved.BackendID, approved.Evidence, deployment)
	require.NoError(t, err)
	require.ErrorIs(t, authority.Check(t.Context()), ErrClosed)
	require.ErrorIs(t, authority.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{{Key: "admission-must-remain-absent", NewValue: []byte("not-authorized")}}, storage.TimeWindow{}), ErrClosed)
	_, err = store.Get(t.Context(), "admission-must-remain-absent")
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET epoch=?,gate_open=0,backend_id=?,evidence=? WHERE deployment_id=?"), closed.Epoch, closed.BackendID, closed.Evidence, deployment)
	require.NoError(t, err)
	_, err = witness.ApproveAuthority(t.Context(), backend, closed, identity, "independent-reconciled-fixture", "second")
	require.NoError(t, err)
	require.ErrorIs(t, authority.Check(t.Context()), ErrConflict)
	require.ErrorIs(t, authority.CheckAdmission(), ErrClosed, "a later approval cannot renew the old owner")
	current, err := witness.OpenAuthority(t.Context(), backend, deployment)
	require.NoError(t, err)
	require.NoError(t, current.CheckAdmission())
	require.Zero(t, testing.AllocsPerRun(1000, func() { _ = current.CheckAdmission() }))
	require.Zero(t, testing.AllocsPerRun(1000, func() { _ = authority.CheckAdmission() }))
}

func TestRecoveryAuthorityNativeObservationAndConcurrentAdmission(t *testing.T) {
	authority := &Authority{invalid: new(atomic.Bool)}
	copied := *authority
	transient := errors.New("fixture transient query failure")
	require.ErrorIs(t, authority.observeNativeFailure(transient), transient)
	require.NoError(t, copied.CheckAdmission())
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				_ = copied.CheckAdmission()
			}
		})
	}
	require.ErrorIs(t, authority.observeNativeFailure(errors.Join(transient, storage.ErrIncarnationChanged)), storage.ErrIncarnationChanged)
	workers.Wait()
	require.ErrorIs(t, copied.CheckAdmission(), ErrClosed)
	require.NoError(t, authority.observeNativeFailure(nil))
	require.ErrorIs(t, authority.CheckAdmission(), ErrClosed)
	var absent *Authority
	require.ErrorIs(t, absent.CheckAdmission(), ErrClosed)
	require.ErrorIs(t, (&Authority{}).CheckAdmission(), ErrClosed)
}
