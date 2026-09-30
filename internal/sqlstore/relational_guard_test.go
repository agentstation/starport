package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRelationalGuardExactClosedPosition(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for backend, config := range contractConfigs(t) {
		t.Run(backend, func(t *testing.T) {
			target := transferDB(t, config)
			identity := RelationalImportIdentity{OperationID: "guarded-import", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			position := RelationalReplayPosition{}
			guard := func(ctx context.Context, callback func(context.Context, *sql.Conn) error) error {
				return target.GuardRelationalImport(ctx, snapshot.Snapshot, identity, position, callback)
			}
			bounded := func() context.Context {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				t.Cleanup(cancel)
				return ctx
			}
			calls := 0
			callback := func(context.Context, *sql.Conn) error { calls++; return nil }
			require.ErrorIs(t, guard(t.Context(), callback), ErrGuardDeadline)
			require.Zero(t, calls)
			ctx, cancel := context.WithCancel(bounded())
			cancel()
			require.ErrorIs(t, guard(ctx, callback), context.Canceled)
			require.Zero(t, calls)
			require.ErrorIs(t, guard(bounded(), nil), ErrImportRestricted)
			require.NoError(t, guard(bounded(), callback))
			require.Equal(t, 1, calls)
			step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
			receipt, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, func(context.Context, *sql.Conn) error { return nil })
			require.NoError(t, err)
			require.ErrorIs(t, guard(bounded(), callback), ErrImportRestricted)
			require.Equal(t, 1, calls)
			position = RelationalReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			require.NoError(t, guard(bounded(), callback))
			require.Equal(t, 2, calls)
			for _, mode := range []string{"claim", "cursor", "receipt", "sequence", "snapshot"} {
				t.Run("pre-"+mode, func(t *testing.T) {
					changedID, changedPosition, changedSnapshot := identity, position, snapshot.Snapshot
					switch mode {
					case "claim":
						changedID.OperationID = "other-import"
					case "cursor":
						changedPosition = RelationalReplayPosition{}
					case "receipt":
						changedPosition.ReceiptSHA256 = strings.Repeat("f", 64)
					case "sequence":
						changedPosition.Sequence++
					case "snapshot":
						changedSnapshot.Size++
					}
					require.ErrorIs(t, target.GuardRelationalImport(bounded(), changedSnapshot, changedID, changedPosition, callback), ErrImportRestricted)
					require.Equal(t, 2, calls)
				})
			}
			for _, marker := range []string{relationalImportMarker, relationalReplayCurrent} {
				t.Run("pre-native-"+marker, func(t *testing.T) {
					var retained string
					require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), marker).Scan(&retained))
					_, err := target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value='{}' WHERE name=?"), marker)
					require.NoError(t, err)
					err = guard(bounded(), callback)
					require.ErrorIs(t, err, ErrImportRestricted)
					require.Equal(t, 2, calls)
					_, err = target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), retained, marker)
					require.NoError(t, err)
				})
				t.Run("post-"+marker, func(t *testing.T) {
					err := guard(bounded(), func(ctx context.Context, conn *sql.Conn) error {
						_, err := conn.ExecContext(ctx, target.Bind("DELETE FROM sqlstore_meta WHERE name=?"), marker)
						return err
					})
					require.ErrorIs(t, err, ErrImportRestricted)
					require.NoError(t, target.CheckRelationalImportPosition(t.Context(), snapshot.Snapshot, identity, position))
				})
			}
			t.Run("callback-error", func(t *testing.T) {
				interrupted := errors.New("callback interrupted")
				err := guard(bounded(), func(ctx context.Context, conn *sql.Conn) error {
					_, err := conn.ExecContext(ctx, "INSERT INTO sqlstore_meta(name,value) VALUES('guard-probe','discard')")
					return errors.Join(err, interrupted)
				})
				require.ErrorIs(t, err, interrupted)
				var count int
				require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name='guard-probe'").Scan(&count))
				require.Zero(t, count)
			})
			t.Run("deadline-during-callback", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				err := guard(ctx, func(ctx context.Context, _ *sql.Conn) error { <-ctx.Done(); return ctx.Err() })
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, target.CheckRelationalImportPosition(t.Context(), snapshot.Snapshot, identity, position))
			})
			t.Run("native-migration-ownership", func(t *testing.T) {
				second, err := Open(config)
				require.NoError(t, err)
				defer second.Close()
				entered, release := make(chan struct{}), make(chan struct{})
				finished := make(chan error, 1)
				go func() {
					finished <- guard(bounded(), func(ctx context.Context, _ *sql.Conn) error {
						close(entered)
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					})
				}()
				<-entered
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				_, err = second.ReplayRelationalImport(ctx, snapshot.Snapshot, identity, step, func(context.Context, *sql.Conn) error { return errors.New("must not run") })
				close(release)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, <-finished)
			})
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			require.NoError(t, target.CheckRelationalImportPosition(t.Context(), snapshot.Snapshot, identity, position))
		})
	}
}
