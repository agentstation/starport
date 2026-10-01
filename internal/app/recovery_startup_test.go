package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryStartupRefusesBeforeApplicationEffects(t *testing.T) {
	for _, kind := range []string{"closed-import", "corrupt-receipt"} {
		t.Run(kind, func(t *testing.T) {
			private := t.TempDir()
			require.NoError(t, os.Chmod(private, 0700))
			root := filepath.Join(private, "installation")
			cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(root)).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("k", 32)}).Load(t.Context())
			require.NoError(t, err)
			sourceDir := filepath.Join(private, "private-source")
			_, err = productfiles.CreateDirectory(sourceDir)
			require.NoError(t, err)
			sourceConfig := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(sourceDir, "source.db")}}
			source, err := sqlstore.Open(sourceConfig)
			require.NoError(t, err)
			require.NoError(t, source.Migrate(t.Context()))
			require.NoError(t, os.Chmod(sourceConfig.SQLite.Path, 0600))
			witness, err := recovery.New(source)
			require.NoError(t, err)
			_, err = witness.Initialize(t.Context(), cfg.EffectivePaths().DeploymentID)
			require.NoError(t, err)
			snapshots := filepath.Join(private, "snapshots")
			snapshot, err := source.SnapshotRelational(t.Context(), snapshots)
			require.NoError(t, err)
			require.NoError(t, source.Close())
			target, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
			require.NoError(t, err)
			require.NoError(t, target.Migrate(t.Context()))
			journal := filepath.Join(private, "journal")
			_, err = productfiles.CreateDirectory(journal)
			require.NoError(t, err)
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(snapshots, "starport.db"), snapshot.Snapshot, journal, sqlstore.RelationalImportIdentity{OperationID: "startup-test", RestrictionID: "closed"}, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=0,bootstrap_allowed=0")
				return err
			}))
			if kind == "corrupt-receipt" {
				_, err = target.ExecContext(t.Context(), "DELETE FROM sqlstore_meta WHERE name='relational-import-v1'")
				require.NoError(t, err)
				_, err = target.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('relational-activation-current-v1','corrupt')")
				require.NoError(t, err)
			}
			require.NoError(t, target.Close())
			kv, kvTransfer, err := storage.OpenImportTarget(t.Context(), cfg.RuntimeStorage())
			require.NoError(t, err)
			claim := []byte("partially-activated")
			require.NoError(t, kvTransfer.Claim(t.Context(), claim))
			require.NoError(t, kvTransfer.(storage.ImportActivator).ActivateImport(t.Context(), claim, strings.Repeat("a", 64)))
			require.NoError(t, kv.Close())
			factories := defaultRuntimeFactories()
			factories.openStorage = func(storage.Config) (storage.KVStore, error) {
				t.Fatal("SQL preflight must refuse before KV open or maintenance")
				return nil, nil
			}
			factories.openSQL = func(config.StorageConfig) (*sqlstore.DB, error) {
				t.Fatal("preflight must not migrate SQL")
				return nil, nil
			}
			application, err := New(cfg, withRuntimeFactories(factories))
			require.ErrorIs(t, err, sqlstore.ErrImportRestricted)
			require.Nil(t, application)
			require.NoDirExists(t, cfg.InferenceCredentialPolicyDirectory())
			require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile)
			require.NoDirExists(t, cfg.Files.Path)
			require.NoDirExists(t, filepath.Join(cfg.EffectivePaths().ConfigDir, ".starport-setup"))
			require.NoDirExists(t, filepath.Join(filepath.Dir(cfg.Storage.Badger.Path), ".starport-setup-"+filepath.Base(cfg.Storage.Badger.Path)))
		})
	}
}

func TestRecoveryStartupJoinsNativeOwnersBeforeMaintenance(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Storage.Badger.SyncWrites = true
	opened, err := storage.OpenForStartup(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer opened.Close()
	transfer, err := storage.OpenRecordTransfer(t.Context(), opened, "")
	require.NoError(t, err)
	claim := []byte("released-KV-without-SQL")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	require.NoError(t, transfer.(storage.ImportActivator).ActivateImport(t.Context(), claim, strings.Repeat("a", 64)))
	builder := runtimeBuilder{application: &App{store: opened}, config: cfg, factories: defaultRuntimeFactories()}
	require.NoError(t, builder.inspectRecoveryStartup())
	require.ErrorIs(t, builder.checkRecoveryStartup(), storage.ErrImportRestricted)
	// Native owner tests check the deferred lifecycle. Joining refuses before
	// policy initialization, SQL migrations, authorization, or catalog construction.
	require.Empty(t, builder.application.lifecycle)
}

func TestRecoveryStartupFreshStandalonePasses(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Storage.Badger.SyncWrites = true
	factories := defaultRuntimeFactories()
	builder := runtimeBuilder{application: &App{}, config: cfg, factories: factories}
	require.NoError(t, builder.inspectRecoveryStartup())
	require.NoError(t, builder.openStorage())
	require.NoError(t, builder.checkRecoveryStartup())
	require.NoError(t, builder.application.closeLifecycle(context.Background()))
}
