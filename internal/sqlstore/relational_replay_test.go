package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationalReplayOrdersExactTransitions(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			identity := RelationalImportIdentity{OperationID: "ordered-replay", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			readEpoch := func() int64 {
				var epoch int64
				require.NoError(t, target.QueryRowContext(t.Context(), "SELECT epoch FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&epoch))
				return epoch
			}
			initial := readEpoch()
			apply := func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+10 WHERE deployment_id='deployment'")
				return err
			}
			step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
			interrupted := errors.New("interrupted replay")
			_, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, apply(ctx, conn))
				return interrupted
			})
			require.ErrorIs(t, err, interrupted)
			require.Equal(t, initial, readEpoch())
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			var group sync.WaitGroup
			type outcome struct {
				receipt string
				err     error
			}
			outcomes := make(chan outcome, 2)
			for _, db := range []*DB{target, second} {
				group.Go(func() {
					r, err := db.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, apply)
					outcomes <- outcome{r, err}
				})
			}
			group.Wait()
			first := <-outcomes
			other := <-outcomes
			require.NoError(t, first.err)
			require.NoError(t, other.err)
			require.Equal(t, first.receipt, other.receipt)
			require.Len(t, first.receipt, 64)
			require.Equal(t, initial+10, readEpoch())
			noApply := func(context.Context, *sql.Conn) error { return errors.New("unexpected callback") }
			for _, mode := range []string{"zero-sequence", "first-has-previous", "bad-evidence", "bad-transition", "canceled"} {
				t.Run(mode, func(t *testing.T) {
					changed := step
					ctx := t.Context()
					switch mode {
					case "zero-sequence":
						changed.Sequence = 0
					case "first-has-previous":
						changed.PreviousSHA256 = first.receipt
					case "bad-evidence":
						changed.EvidenceSHA256 = "missing"
					case "bad-transition":
						changed.TransitionSHA256 = "missing"
					case "canceled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					_, err := target.ReplayRelationalImport(ctx, snapshot.Snapshot, identity, changed, noApply)
					if mode == "canceled" {
						require.ErrorIs(t, err, context.Canceled)
					} else {
						require.ErrorIs(t, err, ErrImportRestricted)
					}
				})
			}
			var originalCursor string
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), relationalReplayCurrent).Scan(&originalCursor))
			for _, broken := range []string{"{}", strings.Repeat("x", 1025), strings.TrimSuffix(originalCursor, "}") + `,"unknown":true}`} {
				_, err := target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), broken, relationalReplayCurrent)
				require.NoError(t, err)
				_, err = target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, noApply)
				require.ErrorIs(t, err, ErrImportRestricted)
			}
			_, err = target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), originalCursor, relationalReplayCurrent)
			require.NoError(t, err)
			for _, mode := range []string{"gap", "previous", "evidence", "transition", "claim"} {
				t.Run(mode, func(t *testing.T) {
					changed, owner := step, identity
					switch mode {
					case "gap":
						changed.Sequence, changed.PreviousSHA256 = 3, first.receipt
					case "previous":
						changed.Sequence, changed.PreviousSHA256 = 2, strings.Repeat("c", 64)
					case "evidence":
						changed.EvidenceSHA256 = strings.Repeat("c", 64)
					case "transition":
						changed.TransitionSHA256 = strings.Repeat("c", 64)
					case "claim":
						owner.OperationID = "different"
					}
					_, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, owner, changed, noApply)
					require.ErrorIs(t, err, ErrImportRestricted)
				})
			}
			next := step
			next.Sequence, next.PreviousSHA256 = 2, first.receipt
			next.TransitionSHA256 = strings.Repeat("d", 64)
			for _, mutation := range []string{"DELETE FROM sqlstore_meta WHERE name='relational-import-v1'", "DELETE FROM sqlstore_meta WHERE name='relational-replay-current-v1'"} {
				_, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, next, func(ctx context.Context, conn *sql.Conn) error { _, err := conn.ExecContext(ctx, mutation); return err })
				require.ErrorIs(t, err, ErrImportRestricted)
			}
			final, err := second.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, next, apply)
			require.NoError(t, err)
			require.NotEqual(t, first.receipt, final)
			repeated, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, noApply)
			require.NoError(t, err)
			require.Equal(t, first.receipt, repeated)
			require.Equal(t, initial+20, readEpoch(), "an old receipt cannot restore earlier domain state")
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, strings.Repeat("e", 64), func(context.Context, *sql.Conn) error { return nil }))
			_, err = target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, next, noApply)
			require.ErrorIs(t, err, ErrImportRestricted)
		})
	}
}

func TestRelationalReplayHistoryDoesNotAuthorizeAnotherImport(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	first := transferDB(t, Config{Type: TypeSQLite})
	oldID := RelationalImportIdentity{OperationID: "old-replay", RestrictionID: "closed"}
	callback := func(context.Context, *sql.Conn) error { return nil }
	step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
	require.NoError(t, first.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), oldID, restrictImportedFixture))
	_, err = first.ReplayRelationalImport(t.Context(), snapshot.Snapshot, oldID, step, callback)
	require.NoError(t, err)
	require.NoError(t, first.ActivateRelationalImport(t.Context(), snapshot.Snapshot, oldID, strings.Repeat("c", 64), callback))
	backup, err := first.SnapshotRelational(t.Context(), filepath.Join(parent, "again"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			newID := RelationalImportIdentity{OperationID: "new-replay", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "again", "starport.db"), backup.Snapshot, transferDirectory(t), newID, restrictImportedFixture))
			var current, history int
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT COUNT(*) FROM sqlstore_meta WHERE name=?"), relationalReplayCurrent).Scan(&current))
			require.Zero(t, current)
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name LIKE 'relational-replay-v1:%'").Scan(&history))
			require.Equal(t, 1, history)
			_, err = target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, oldID, step, callback)
			require.ErrorIs(t, err, ErrImportRestricted)
			_, err = target.ReplayRelationalImport(t.Context(), backup.Snapshot, newID, step, callback)
			require.NoError(t, err)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
		})
	}
}
