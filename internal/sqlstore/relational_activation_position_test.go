package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type positionedRelationalActivator interface {
	ActivateRelationalImportAt(context.Context, SQLiteSnapshot, RelationalImportIdentity, RelationalReplayPosition, string, func(context.Context, *sql.Conn) error) error
}

func TestRelationalActivationBindsFinalReplayPosition(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			activate, ok := any(target).(positionedRelationalActivator)
			require.True(t, ok, "SQL activation must bind the final replay cursor")
			identity := RelationalImportIdentity{OperationID: "position-activation", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
			first, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, step, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1 WHERE deployment_id='deployment'")
				return err
			})
			require.NoError(t, err)
			position := RelationalReplayPosition{Sequence: 1, ReceiptSHA256: first}
			calls := 0
			approve := func(ctx context.Context, conn *sql.Conn) error {
				calls++
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1 WHERE deployment_id='deployment'")
				return err
			}
			decision := strings.Repeat("d", 64)
			for _, wrong := range []RelationalReplayPosition{{}, {Sequence: 1, ReceiptSHA256: strings.Repeat("c", 64)}, {Sequence: 2, ReceiptSHA256: first}, {Sequence: -1}} {
				require.Error(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, wrong, decision, approve))
				require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
				require.Zero(t, calls)
			}
			require.NoError(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision, approve))
			require.Equal(t, 1, calls)
			require.NoError(t, target.CheckActivatedRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision))
			require.NoError(t, target.CheckImportBarrier(t.Context()))
			_, err = target.ExecContext(t.Context(), "UPDATE catalog_recovery SET gate_open=0,epoch=epoch+1 WHERE deployment_id='deployment'")
			require.NoError(t, err)
			require.NoError(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision, approve))
			require.Equal(t, 1, calls)
			var opened int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT gate_open FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&opened))
			require.Zero(t, opened, "historical retry must preserve later withdrawal")
			require.Error(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{}, decision, approve))
		})
	}
}

func TestRelationalActivationRefusesCallbackReplayChange(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			activate, ok := any(target).(positionedRelationalActivator)
			require.True(t, ok)
			identity := RelationalImportIdentity{OperationID: "changed-activation", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			err := activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{}, strings.Repeat("d", 64), func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, target.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), relationalReplayCurrent, "changed replay")
				return err
			})
			require.Error(t, err)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			var count int
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT count(*) FROM sqlstore_meta WHERE name=?"), relationalReplayCurrent).Scan(&count))
			require.Zero(t, count, "SQL callback changes roll back with refused activation")
			require.NoError(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{}, strings.Repeat("d", 64), func(context.Context, *sql.Conn) error { return nil }))
			require.NoError(t, activate.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{}, strings.Repeat("d", 64), func(context.Context, *sql.Conn) error { return errors.New("historical callback must not repeat") }))
		})
	}
}

type positionedRelationalActivationInspector interface {
	CheckActivatedRelationalImportAt(context.Context, SQLiteSnapshot, RelationalImportIdentity, RelationalReplayPosition, string) error
}

func TestRelationalActivationRetainsFinalPositionForPassiveRestart(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			inspect, ok := any(target).(positionedRelationalActivationInspector)
			require.True(t, ok, "restart requires passive native position-bound SQL activation evidence")
			identity := RelationalImportIdentity{OperationID: "retained-position", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			position, decision := RelationalReplayPosition{}, strings.Repeat("a", 64)
			require.Error(t, inspect.CheckActivatedRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision))
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision, func(context.Context, *sql.Conn) error { return nil }))
			var encoded string
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), relationalActivationCurrent).Scan(&encoded))
			var retained struct {
				Version  int                      `json:"version"`
				Position RelationalReplayPosition `json:"position"`
			}
			require.NoError(t, json.Unmarshal([]byte(encoded), &retained))
			require.Equal(t, 2, retained.Version)
			require.Equal(t, position, retained.Position)
			_, err := target.ExecContext(t.Context(), "UPDATE catalog_recovery SET gate_open=0,epoch=epoch+1 WHERE deployment_id='deployment'")
			require.NoError(t, err)
			require.NoError(t, inspect.CheckActivatedRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, decision))
			require.Error(t, inspect.CheckActivatedRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{Sequence: 1, ReceiptSHA256: strings.Repeat("c", 64)}, decision))
			require.Error(t, inspect.CheckActivatedRelationalImportAt(t.Context(), snapshot.Snapshot, identity, position, strings.Repeat("d", 64)))
			var opened int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT gate_open FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&opened))
			require.Zero(t, opened)
		})
	}
}
