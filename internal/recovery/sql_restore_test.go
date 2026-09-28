package recovery

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestSQLRestoreStaysRestrictedAcrossRetry(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	// A second retained witness and unused team grant cannot retain approval after restore.
	_, err := source.SQL.ExecContext(t.Context(), "INSERT INTO catalog_recovery VALUES('another',9,1,'former-primary','older-proof',1)")
	require.NoError(t, err)
	_, err = source.SQL.ExecContext(t.Context(), "INSERT INTO team_budget_origins VALUES('deleted-team','unused-history',1)")
	require.NoError(t, err)
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	config := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(privateKVDirectory(t), "target.db")}}
	target, err := sqlstore.Open(config)
	require.NoError(t, err)
	require.NoError(t, target.Migrate(t.Context()))
	verify := VerifyRequest{Directory: destination, ManifestSHA256: digest, ScratchDirectory: privateKVDirectory(t)}
	prepared, err := PrepareSQLRestore(t.Context(), target, verify, "restore-one", source.Encryption)
	require.NoError(t, err)
	require.False(t, prepared.Boundary.Open)
	require.Equal(t, request.Boundary.Epoch+1, prepared.Boundary.Epoch)
	require.Empty(t, prepared.Boundary.BackendID)
	restrictedSources := source
	restrictedSources.SQL = target
	restrictedRequest := request
	restrictedRequest.Boundary = prepared.Boundary
	_, err = BackupBundle(t.Context(), filepath.Join(privateKVDirectory(t), "unapproved-backup"), restrictedSources, restrictedRequest)
	require.ErrorIs(t, err, sqlstore.ErrImportRestricted)
	require.NoError(t, target.Close())
	target, err = sqlstore.Open(config)
	require.NoError(t, err)
	defer func() { require.NoError(t, target.Close()) }()
	again, err := PrepareSQLRestore(t.Context(), target, verify, "restore-one", source.Encryption)
	require.NoError(t, err)
	require.Equal(t, prepared, again)
	_, err = PrepareSQLRestore(t.Context(), target, verify, "different", source.Encryption)
	require.Error(t, err)
	require.ErrorIs(t, target.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	witness, err := New(target)
	require.NoError(t, err)
	_, err = witness.Approved(t.Context(), "deployment")
	require.ErrorIs(t, err, ErrClosed)
	other, err := witness.Current(t.Context(), "another")
	require.NoError(t, err)
	require.False(t, other.Open)
	require.EqualValues(t, 10, other.Epoch)
	var allowed int
	require.NoError(t, target.QueryRowContext(t.Context(), "SELECT initialize_allowed FROM team_budget_origins WHERE team_id='deleted-team'").Scan(&allowed))
	require.Zero(t, allowed)
	// A changed target cannot claim the previous restricted preparation succeeded.
	_, err = target.ExecContext(t.Context(), "UPDATE catalog_recovery SET bootstrap_allowed=1 WHERE deployment_id='deployment'")
	require.NoError(t, err)
	_, err = PrepareSQLRestore(t.Context(), target, verify, "restore-one", source.Encryption)
	require.ErrorIs(t, err, ErrConflict)
}

func TestSQLRestoreRefusesBeforeTargetMutation(t *testing.T) {
	for _, mode := range []string{"wrong-digest", "exhausted-epoch"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			if mode == "exhausted-epoch" {
				request.Boundary.Epoch = math.MaxInt64
				_, err := source.SQL.ExecContext(t.Context(), "UPDATE catalog_recovery SET epoch=9223372036854775807")
				require.NoError(t, err)
			}
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			if mode == "wrong-digest" {
				digest = strings.Repeat("0", 64)
			}
			target, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
			require.NoError(t, err)
			defer func() { require.NoError(t, target.Close()) }()
			require.NoError(t, target.Migrate(t.Context()))
			_, err = PrepareSQLRestore(t.Context(), target, VerifyRequest{Directory: destination, ManifestSHA256: digest}, "restore", source.Encryption)
			require.Error(t, err)
			require.NoError(t, target.CheckImportBarrier(t.Context()))
			var count int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM catalog_recovery").Scan(&count))
			require.Zero(t, count)
		})
	}
}
