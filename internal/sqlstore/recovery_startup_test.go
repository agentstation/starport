package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryStartupNoDatabaseCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "starport.db")
	cfg := Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: path}}
	state, err := InspectRecoveryStartup(t.Context(), cfg, "deployment")
	require.NoError(t, err)
	require.False(t, state.Activated)
	require.NoDirExists(t, filepath.Dir(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("corrupt SQL database"), 0600))
	_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "corrupt SQL database", string(data))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = InspectRecoveryStartup(ctx, cfg, "deployment")
	require.ErrorIs(t, err, context.Canceled)
}

func TestRecoveryStartupNativeSQLControls(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			state, err := InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.NoError(t, err)
			require.False(t, state.Activated)
			identity := RelationalImportIdentity{OperationID: "startup-inspection", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.ErrorIs(t, err, ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, strings.Repeat("a", 64), func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
				return err
			}))
			state, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.NoError(t, err)
			require.True(t, state.Activated)
			require.Equal(t, strings.Repeat("a", 64), state.DecisionSHA256)
			_, err = target.ExecContext(t.Context(), "UPDATE catalog_recovery SET gate_open=0,epoch=epoch+1")
			require.NoError(t, err)
			_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.ErrorIs(t, err, ErrImportRestricted)
			_, err = target.ExecContext(t.Context(), "UPDATE catalog_recovery SET gate_open=1")
			require.NoError(t, err)
			_, err = target.ExecContext(t.Context(), target.Bind("DELETE FROM sqlstore_meta WHERE name=?"), relationalActivationCurrent)
			require.NoError(t, err)
			_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.ErrorIs(t, err, ErrImportRestricted, "orphan history must not become first boot")
		})
	}
}

func TestRecoveryStartupMissingNativeSchemaRefuses(t *testing.T) {
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			_, err := target.ExecContext(t.Context(), "DROP TABLE sqlstore_meta")
			require.NoError(t, err)
			_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.ErrorIs(t, err, ErrImportRestricted)
		})
	}
}

func TestRecoveryStartupPositionedSQLReceipts(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, cfg := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, cfg)
			identity := RelationalImportIdentity{OperationID: "positioned-startup", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			first, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, identity, RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1")
				return err
			})
			require.NoError(t, err)
			require.NoError(t, target.ActivateRelationalImportAt(t.Context(), snapshot.Snapshot, identity, RelationalReplayPosition{Sequence: 1, ReceiptSHA256: first}, strings.Repeat("a", 64), func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
				return err
			}))
			state, err := InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.NoError(t, err)
			require.True(t, state.Activated)
			var current string
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), relationalActivationCurrent).Scan(&current))
			var activation relationalActivationReceipt
			require.NoError(t, json.Unmarshal([]byte(current), &activation))
			activation.Position = &RelationalReplayPosition{}
			changed, err := json.Marshal(activation)
			require.NoError(t, err)
			_, err = target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=? OR name=?"), string(changed), relationalActivationCurrent, relationalActivationPrefix+activation.ClaimSHA256)
			require.NoError(t, err)
			_, err = InspectRecoveryStartup(t.Context(), cfg, "deployment")
			require.ErrorIs(t, err, ErrImportRestricted, "matching native receipts cannot override the final replay cursor")
		})
	}
}
