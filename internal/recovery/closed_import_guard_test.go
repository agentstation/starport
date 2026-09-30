package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func closedImportGuardFixture(t *testing.T, backend string) (*Witness, sqlstore.Config, ClosedImportGuardRequest) {
	t.Helper()
	source, err := sqlstore.Open(historySQLConfig(t, sqlstore.TypeSQLite))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	require.NoError(t, source.Migrate(t.Context()))
	witness, err := New(source)
	require.NoError(t, err)
	boundary, err := witness.Initialize(t.Context(), "guard-deployment")
	require.NoError(t, err)
	boundary, err = witness.Approve(t.Context(), boundary, "prior-backend", "prior-proof")
	require.NoError(t, err)
	boundary, err = witness.Close(t.Context(), boundary)
	require.NoError(t, err)
	root := privateKVDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(root, "source"))
	require.NoError(t, err)
	config := historySQLConfig(t, backend)
	target, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	require.NoError(t, target.Migrate(t.Context()))
	request := ClosedImportGuardRequest{
		Snapshot: snapshot.Snapshot,
		Import:   sqlstore.RelationalImportIdentity{OperationID: "guard-import", RestrictionID: "closed"},
		Boundary: boundary,
	}
	require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(root, "source", "starport.db"), request.Snapshot, root, request.Import, func(context.Context, *sql.Conn) error { return nil }))
	step := sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
	receipt, err := target.ReplayRelationalImport(t.Context(), request.Snapshot, request.Import, step, func(context.Context, *sql.Conn) error { return nil })
	require.NoError(t, err)
	request.Position = sqlstore.RelationalReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
	witness, err = New(target)
	require.NoError(t, err)
	return witness, config, request
}

func TestClosedImportGuardNativeBoundary(t *testing.T) {
	for _, backend := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(backend, func(t *testing.T) {
			witness, _, request := closedImportGuardFixture(t, backend)
			witness.db.SetMaxOpenConns(1)
			bounded := func() context.Context {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				t.Cleanup(cancel)
				return ctx
			}
			assertClosed := func() {
				current, err := witness.Current(t.Context(), request.Boundary.DeploymentID)
				require.NoError(t, err)
				require.Equal(t, request.Boundary, current)
				_, err = witness.Approved(t.Context(), current.DeploymentID)
				require.ErrorIs(t, err, ErrClosed)
				require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
				require.NoError(t, witness.db.CheckRelationalImportPosition(t.Context(), request.Snapshot, request.Import, request.Position))
			}
			calls := 0
			callback := func(ctx context.Context, conn *sql.Conn) error {
				calls++
				var epoch int64
				return conn.QueryRowContext(ctx, witness.db.Bind("SELECT epoch FROM catalog_recovery WHERE deployment_id=?"), request.Boundary.DeploymentID).Scan(&epoch)
			}
			require.NoError(t, witness.GuardClosedImport(bounded(), request, callback))
			require.Equal(t, 1, calls)
			for _, field := range []string{"deployment", "epoch", "open", "backend", "evidence", "claim", "cursor"} {
				t.Run("pre-"+field, func(t *testing.T) {
					changed := request
					switch field {
					case "deployment":
						changed.Boundary.DeploymentID = "other-deployment"
					case "epoch":
						changed.Boundary.Epoch++
					case "open":
						changed.Boundary.Open = true
					case "backend":
						changed.Boundary.BackendID = "other-backend"
					case "evidence":
						changed.Boundary.Evidence = "other-proof"
					case "claim":
						changed.Import.OperationID = "other-import"
					case "cursor":
						changed.Position.Sequence++
					}
					require.Error(t, witness.GuardClosedImport(bounded(), changed, callback))
					require.Equal(t, 1, calls)
					assertClosed()
				})
			}
			for _, mutation := range []string{
				"UPDATE catalog_recovery SET deployment_id='changed'",
				"UPDATE catalog_recovery SET epoch=epoch+1",
				"UPDATE catalog_recovery SET gate_open=1",
				"UPDATE catalog_recovery SET backend_id='changed'",
				"UPDATE catalog_recovery SET evidence='changed'",
				"UPDATE catalog_recovery SET bootstrap_allowed=1",
				"DELETE FROM catalog_recovery",
			} {
				t.Run("post-"+mutation, func(t *testing.T) {
					err := witness.GuardClosedImport(bounded(), request, func(ctx context.Context, conn *sql.Conn) error {
						_, err := conn.ExecContext(ctx, mutation)
						return err
					})
					require.Error(t, err)
					assertClosed()
				})
			}

			interrupted := errors.New("boundary callback interrupted")
			err := witness.GuardClosedImport(bounded(), request, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
				return errors.Join(err, interrupted)
			})
			require.ErrorIs(t, err, interrupted)
			assertClosed()
			err = witness.GuardClosedImport(t.Context(), request, callback)
			require.ErrorIs(t, err, sqlstore.ErrGuardDeadline)
			assertClosed()
		})
	}
}

func TestClosedImportGuardIndependentKVCommitSurvivesFailure(t *testing.T) {
	path, snapshot := kvSnapshotFixture(t)
	for _, backend := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(backend, func(t *testing.T) {
			witness, _, request := closedImportGuardFixture(t, backend)
			kind := storage.StorageTypeBadger
			if backend == sqlstore.TypePostgres {
				kind = storage.StorageTypeValkey
			}
			store, transfer, _ := kvTransferStores(t, kind)
			_, err := ImportKV(t.Context(), transfer, "guard-kv", path, privateKVDirectory(t), snapshot)
			require.NoError(t, err)
			claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: "guard-kv", Snapshot: snapshot})
			require.NoError(t, err)
			reconciler, ok := transfer.(storage.ImportReconciler)
			require.True(t, ok)
			mutation := []storage.CompareAndSwapMutation{{Key: "guard:retained", NewValue: []byte("committed")}}
			var committed string
			lostReply := errors.New("independent commit reply lost")
			apply := func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "INSERT INTO sqlstore_meta(name,value) VALUES('guard-commit','pending')")
				if err != nil {
					return err
				}
				receipt, err := reconciler.ReconcileImport(ctx, claim, 1, "", strings.Repeat("c", 64), mutation)
				if err == nil {
					if committed != "" && committed != receipt {
						return errors.New("independent receipt changed")
					}
					committed = receipt
				}
				return err
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = witness.GuardClosedImport(ctx, request, func(ctx context.Context, conn *sql.Conn) error {
				if err := apply(ctx, conn); err != nil {
					return err
				}
				return lostReply
			})
			require.ErrorIs(t, err, lostReply)
			require.Len(t, committed, 64)
			value, err := store.Get(t.Context(), mutation[0].Key)
			require.NoError(t, err)
			require.Equal(t, mutation[0].NewValue, value)
			var count int
			require.NoError(t, witness.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name='guard-commit'").Scan(&count))
			require.Zero(t, count)
			ctx, cancelDeadline := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancelDeadline()
			err = witness.GuardClosedImport(ctx, request, func(ctx context.Context, conn *sql.Conn) error {
				if err := apply(ctx, conn); err != nil {
					return err
				}
				<-ctx.Done()
				return ctx.Err()
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, witness.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name='guard-commit'").Scan(&count))
			require.Zero(t, count)
			ctx, cancelRetry := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancelRetry()
			restarted, err := New(witness.db)
			require.NoError(t, err)
			require.NoError(t, restarted.GuardClosedImport(ctx, request, apply))
			require.NoError(t, witness.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name='guard-commit'").Scan(&count))
			require.Equal(t, 1, count)
			value, err = store.Get(t.Context(), mutation[0].Key)
			require.NoError(t, err)
			require.Equal(t, mutation[0].NewValue, value)
			current, err := witness.Current(t.Context(), request.Boundary.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, request.Boundary, current)
			require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
			require.NoError(t, witness.db.CheckRelationalImportPosition(t.Context(), request.Snapshot, request.Import, request.Position))
		})
	}
}

func TestClosedImportGuardOrdinaryApprovalWaitsForCallback(t *testing.T) {
	path, snapshot := kvSnapshotFixture(t)
	for _, backend := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(backend, func(t *testing.T) {
			witness, config, request := closedImportGuardFixture(t, backend)
			witness.db.SetMaxOpenConns(1)
			other, err := sqlstore.Open(config)
			require.NoError(t, err)
			defer other.Close()
			other.SetMaxOpenConns(1)
			if backend == sqlstore.TypeSQLite {
				_, err := other.ExecContext(t.Context(), "PRAGMA busy_timeout=25")
				require.NoError(t, err)
			}
			approver, err := New(other)
			require.NoError(t, err)
			kind := storage.StorageTypeBadger
			if backend == sqlstore.TypePostgres {
				kind = storage.StorageTypeValkey
			}
			store, transfer, _ := kvTransferStores(t, kind)
			_, err = ImportKV(t.Context(), transfer, "guard-concurrent-kv", path, privateKVDirectory(t), snapshot)
			require.NoError(t, err)
			claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: "guard-concurrent-kv", Snapshot: snapshot})
			require.NoError(t, err)
			reconciler, ok := transfer.(storage.ImportReconciler)
			require.True(t, ok)
			entered, write, committed, release := make(chan struct{}), make(chan struct{}), make(chan string, 1), make(chan struct{})
			finished := make(chan error, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			go func() {
				finished <- witness.GuardClosedImport(ctx, request, func(ctx context.Context, conn *sql.Conn) error {
					close(entered)
					select {
					case <-write:
					case <-ctx.Done():
						return ctx.Err()
					}
					receipt, err := reconciler.ReconcileImport(ctx, claim, 1, "", strings.Repeat("d", 64), []storage.CompareAndSwapMutation{{Key: "guard:concurrent", NewValue: []byte("committed")}})
					if err != nil {
						return err
					}
					committed <- receipt
					select {
					case <-release:
						return witness.checkLockedClosedBoundary(ctx, conn, request.Boundary)
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			<-entered
			approved := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				_, err := approver.Approve(ctx, request.Boundary, "new-backend", "new-proof")
				approved <- err
			}()
			<-started
			close(write)
			select {
			case receipt := <-committed:
				require.Len(t, receipt, 64)
			case <-ctx.Done():
				t.Fatal("native KV commit did not finish within the guard deadline")
			}
			var approvalErr error
			returned := false
			select {
			case approvalErr = <-approved:
				returned = true
				require.Equal(t, sqlstore.TypeSQLite, backend)
				require.Error(t, approvalErr, "ordinary approval must not commit while the callback holds the lock")
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			require.NoError(t, <-finished)
			if !returned {
				approvalErr = <-approved
			}
			current, err := witness.Current(t.Context(), request.Boundary.DeploymentID)
			require.NoError(t, err)
			if approvalErr == nil {
				require.Equal(t, Record{DeploymentID: request.Boundary.DeploymentID, Epoch: request.Boundary.Epoch, Open: true, BackendID: "new-backend", Evidence: "new-proof"}, current)
			} else {
				require.Equal(t, sqlstore.TypeSQLite, backend)
				require.Equal(t, request.Boundary, current)
			}
			value, err := store.Get(t.Context(), "guard:concurrent")
			require.NoError(t, err)
			require.Equal(t, []byte("committed"), value)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
			require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}
