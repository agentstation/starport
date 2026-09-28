package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func bundleRestoreTargets(t *testing.T, shared bool) (BundleTargets, storage.KVStore, string) {
	t.Helper()
	kind := storage.StorageTypeBadger
	config := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(privateKVDirectory(t), "restored.db")}}
	if shared {
		kind = storage.StorageTypeValkey
		config = isolatedWitnessPostgres(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: os.Getenv("TEST_POSTGRES_URL")}})
	}
	kv, transfer, _ := kvTransferStores(t, kind)
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	var target blob.RestoreTarget
	location := ""
	if shared {
		store := bundleObjectFixture(t).(*blob.ObjectStore)
		target, err = blob.ObjectRestoreTarget(store)
	} else {
		location = filepath.Join(privateKVDirectory(t), "restored-blobs")
		target, err = blob.FilesystemRestoreTarget(location)
	}
	require.NoError(t, err)
	return BundleTargets{KV: transfer, SQL: db, Blobs: target, FilesDirectory: filepath.Join(privateKVDirectory(t), "staged-files")}, kv, location
}

func TestPrepareBundleLocalAndSharedRecipes(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			source, capture, directory := backupBundleRecipe(t, shared)
			manifest, err := BackupBundle(t.Context(), directory, source, capture)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			target, kv, blobDirectory := bundleRestoreTargets(t, shared)
			request := VerifyRequest{Directory: directory, ManifestSHA256: digest, ScratchDirectory: privateKVDirectory(t)}
			result, err := PrepareBundle(t.Context(), target, request, "recovery-one", source.Encryption)
			require.NoError(t, err)
			require.False(t, result.Boundary.Open)
			require.Equal(t, capture.Boundary.Epoch+1, result.Boundary.Epoch)
			require.Equal(t, manifest.KV.Records, result.KV.ProcessedRecords)
			require.Equal(t, 1, result.Files)
			require.ErrorIs(t, target.SQL.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), kv), storage.ErrImportRestricted)
			if !shared {
				_, err := blob.NewFilesystem(blobDirectory)
				require.ErrorIs(t, err, blob.ErrImportRestricted)
			}
			bytes, err := os.ReadFile(filepath.Join(target.FilesDirectory, "files/configuration/config.env"))
			require.NoError(t, err)
			require.Equal(t, "STARPORT_CATALOG_SOURCE=embedded\n", string(bytes))
			require.FileExists(t, filepath.Join(target.FilesDirectory, preparedBundleFile))
			again, err := PrepareBundle(t.Context(), target, request, "recovery-one", source.Encryption)
			require.NoError(t, err)
			require.Equal(t, result, again)
			_, err = PrepareBundle(t.Context(), target, request, "different", source.Encryption)
			require.Error(t, err)
			// A receipt does not conceal changed selected files on a subsequent retry.
			require.NoError(t, os.WriteFile(filepath.Join(target.FilesDirectory, "files/configuration/config.env"), []byte("changed"), 0o600))
			_, err = PrepareBundle(t.Context(), target, request, "recovery-one", source.Encryption)
			require.Error(t, err)
		})
	}
}

type interruptedRestoreBlobs struct {
	blob.RestoreTarget
	failAfter bool
}

func (s interruptedRestoreBlobs) Restore(ctx context.Context, source, scratch, operation string, snapshot blob.Snapshot) error {
	if s.failAfter {
		if err := s.RestoreTarget.Restore(ctx, source, scratch, operation, snapshot); err != nil {
			return err
		}
	}
	return errors.New("test: lost restore acknowledgement")
}

type interruptedRestoreKV struct{ storage.RecordTransfer }

func (s interruptedRestoreKV) Import(ctx context.Context, claim []byte, record storage.TransferRecord) error {
	if err := s.RecordTransfer.Import(ctx, claim, record); err != nil {
		return err
	}
	return errors.New("test: lost import acknowledgement")
}

func TestPrepareBundleResumesAfterInterruptedComponents(t *testing.T) {
	for _, stage := range []string{"kv", "before-blobs", "after-blobs", "files"} {
		t.Run(stage, func(t *testing.T) {
			source, capture, directory := backupBundleFixture(t)
			manifest, err := BackupBundle(t.Context(), directory, source, capture)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			target, kv, _ := bundleRestoreTargets(t, false)
			request := VerifyRequest{Directory: directory, ManifestSHA256: digest}
			interrupted := target
			switch stage {
			case "kv":
				interrupted.KV = interruptedRestoreKV{target.KV}
			case "before-blobs":
				interrupted.Blobs = interruptedRestoreBlobs{target.Blobs, false}
			case "after-blobs":
				interrupted.Blobs = interruptedRestoreBlobs{target.Blobs, true}
			case "files":
				require.NoError(t, os.Mkdir(target.FilesDirectory, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(target.FilesDirectory, "operator-file"), []byte("preserve"), 0o600))
			}
			_, err = PrepareBundle(t.Context(), interrupted, request, "retry", source.Encryption)
			require.Error(t, err)
			require.ErrorIs(t, target.SQL.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), kv), storage.ErrImportRestricted)
			_, err = os.Stat(filepath.Join(target.FilesDirectory, preparedBundleFile))
			require.ErrorIs(t, err, os.ErrNotExist)
			if stage == "files" {
				content, err := os.ReadFile(filepath.Join(target.FilesDirectory, "operator-file"))
				require.NoError(t, err)
				require.Equal(t, "preserve", string(content))
				// The operator selects a different empty staging path; no prior file is overwritten.
				target.FilesDirectory += "-new"
			}
			result, err := PrepareBundle(t.Context(), target, request, "retry", source.Encryption)
			require.NoError(t, err)
			require.Equal(t, capture.Boundary.Epoch+1, result.Boundary.Epoch, "retry must not repeat SQL restrictions")
			require.False(t, result.Boundary.Open)
		})
	}
}

func TestPrepareBundleRefusesBeforeMutation(t *testing.T) {
	for _, mode := range []string{"wrong-digest", "missing-artifact", "changed-files", "source-overlap", "scratch-overlap", "empty-operation", "control-operation"} {
		t.Run(mode, func(t *testing.T) {
			source, capture, directory := backupBundleFixture(t)
			manifest, err := BackupBundle(t.Context(), directory, source, capture)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			target, kv, blobDirectory := bundleRestoreTargets(t, false)
			request := VerifyRequest{Directory: directory, ManifestSHA256: digest}
			operation := "restore"
			switch mode {
			case "wrong-digest":
				request.ManifestSHA256 = strings.Repeat("0", 64)
			case "missing-artifact":
				require.NoError(t, os.Remove(filepath.Join(directory, bundleBlobFile)))
			case "changed-files":
				require.NoError(t, os.WriteFile(filepath.Join(directory, "files/configuration/config.env"), []byte("different"), 0o600))
			case "source-overlap":
				target.FilesDirectory = filepath.Join(directory, "new")
			case "scratch-overlap":
				request.ScratchDirectory = target.FilesDirectory
			case "empty-operation":
				operation = ""
			case "control-operation":
				operation = "restore\x00"
			}
			_, err = PrepareBundle(t.Context(), target, request, operation, source.Encryption)
			require.Error(t, err)
			require.NoError(t, target.SQL.CheckImportBarrier(t.Context()))
			require.NoError(t, storage.CheckImportBarrier(t.Context(), kv))
			var count int
			require.NoError(t, target.SQL.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM catalog_recovery").Scan(&count))
			require.Zero(t, count)
			_, err = os.Stat(blobDirectory)
			require.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(target.FilesDirectory)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRestoreFilesRejectsUnexpectedAndMissingEntries(t *testing.T) {
	for _, mode := range []string{"extra-file", "extra-directory", "missing", "symlink", "changed-receipt"} {
		t.Run(mode, func(t *testing.T) {
			source, capture, directory := backupBundleFixture(t)
			manifest, err := BackupBundle(t.Context(), directory, source, capture)
			require.NoError(t, err)
			destination := filepath.Join(privateKVDirectory(t), "inactive")
			digest, err := manifest.Digest()
			require.NoError(t, err)
			prepared := PreparedBundle{Version: 1, OperationID: "files", ManifestSHA256: digest, Files: 1}
			require.NoError(t, stageRestoreFiles(t.Context(), destination, directory, manifest, prepared))
			selected := filepath.Join(destination, "files/configuration/config.env")
			switch mode {
			case "extra-file":
				require.NoError(t, os.WriteFile(filepath.Join(destination, "extra"), []byte("unexpected"), 0o600))
			case "extra-directory":
				require.NoError(t, os.Mkdir(filepath.Join(destination, "extra"), 0o700))
			case "missing":
				require.NoError(t, os.Remove(selected))
			case "symlink":
				require.NoError(t, os.Remove(selected))
				require.NoError(t, os.Symlink(source.Files[0].Path, selected))
			case "changed-receipt":
				require.NoError(t, os.WriteFile(filepath.Join(destination, preparedBundleFile), []byte("{}"), 0o600))
			}
			require.Error(t, stageRestoreFiles(t.Context(), destination, directory, manifest, prepared))
		})
	}
}

type changingRestoreBoundary struct {
	blob.RestoreTarget
	db *sqlstore.DB
}

func (s changingRestoreBoundary) Restore(ctx context.Context, source, scratch, operation string, snapshot blob.Snapshot) error {
	if err := s.RestoreTarget.Restore(ctx, source, scratch, operation, snapshot); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
	return err
}

func TestPrepareBundleRechecksSQLBeforeCompletionReceipt(t *testing.T) {
	source, capture, directory := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), directory, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	target, _, _ := bundleRestoreTargets(t, false)
	target.Blobs = changingRestoreBoundary{target.Blobs, target.SQL}
	_, err = PrepareBundle(t.Context(), target, VerifyRequest{Directory: directory, ManifestSHA256: digest}, "boundary-change", source.Encryption)
	require.ErrorIs(t, err, ErrConflict)
	_, err = os.Stat(filepath.Join(target.FilesDirectory, preparedBundleFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, target.SQL.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
}

func TestRestorePathIdentityDetectsAliasedTrees(t *testing.T) {
	parent := privateKVDirectory(t)
	original := filepath.Join(parent, "original")
	require.NoError(t, os.Mkdir(original, 0o700))
	alias := filepath.Join(parent, "alias")
	require.NoError(t, os.Symlink(original, alias))
	inside, err := restoreDirectoryContains(original, filepath.Join(alias, "unused-destination"))
	require.NoError(t, err)
	require.True(t, inside)
	outside, err := restoreDirectoryContains(original, filepath.Join(parent, "unrelated"))
	require.NoError(t, err)
	require.False(t, outside)
}
