package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/stretchr/testify/require"
)

const (
	configFileMode = 0o600
	privateDirMode = 0o700
)

func TestPreparedSetupAcrossRoots(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	prepared, err := service.prepareAcrossRoots(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.writer.close()) })
	require.NoFileExists(t, paths.ConfigFile)
	require.NoDirExists(t, paths.BadgerDir)
	require.NotContains(t, string(prepared.journalBytes), prepared.secret)
	require.NotEmpty(t, prepared.journal.Receipt.Directory)
	result, err := prepared.publish(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, result.APIKey)
	require.FileExists(t, paths.ConfigFile)
	require.DirExists(t, paths.BadgerDir)
	require.NoDirExists(t, filepath.Join(paths.DataDir, prepared.journal.Stage))
	_, err = prepared.writer.directory.ReadFile(setupJournalFile, setupFileLimit)
	require.True(t, os.IsNotExist(err), "%v", err)
	store, err := openLocalStore(paths.BadgerDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	repository, err := apikey.Open(store)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(result.APIKey))
	record, err := repository.GetByHash(t.Context(), hex.EncodeToString(digest[:]))
	require.NoError(t, err)
	require.Equal(t, "local-admin", record.APIKey.Name)
}

func TestPreparedSetupRefusesChangedStage(t *testing.T) {
	paths := productSetupPaths(t)
	prepared, err := New(paths).prepareAcrossRoots(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.writer.close()) })
	stagePath := filepath.Join(paths.DataDir, prepared.journal.Stage)
	marker := filepath.Join(stagePath, "unrelated-file")
	require.NoError(t, os.WriteFile(marker, []byte("preserve"), privateDirMode))
	_, err = prepared.publish(t.Context())
	require.ErrorIs(t, err, ErrPartialState)
	require.NoDirExists(t, paths.BadgerDir)
	require.NoFileExists(t, paths.ConfigFile)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
}

func TestPreparedSetupRetainsInterruptedPublication(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	interrupted := errors.New("test interruption")
	service.checkpoint = func(phase string) error {
		if phase == "data-published" {
			return interrupted
		}
		return nil
	}
	prepared, err := service.prepareAcrossRoots(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.writer.close()) })
	_, err = prepared.publish(t.Context())
	require.ErrorIs(t, err, interrupted)
	require.NoFileExists(t, paths.ConfigFile)
	require.DirExists(t, paths.BadgerDir)
	journal, err := prepared.writer.directory.ReadFile(setupJournalFile, setupFileLimit)
	require.NoError(t, err)
	require.Equal(t, prepared.journalBytes, journal)
	database, err := prepared.data.ExistingChild(filepath.Base(paths.BadgerDir))
	require.NoError(t, err)
	require.NoError(t, verifySetupReceipt(database, prepared.journal.Receipt))
}

func TestPreparedSetupExcludesConcurrentWriter(t *testing.T) {
	paths := productSetupPaths(t)
	prepared, err := New(paths).prepareAcrossRoots(t.Context(), Request{APIKeyName: "first"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.writer.close()) })
	_, err = New(paths).prepareAcrossRoots(t.Context(), Request{APIKeyName: "second"})
	require.ErrorIs(t, err, ErrPartialState)
	_, err = prepared.publish(t.Context())
	require.NoError(t, err)
}

func TestSetupReceiptRefusesChangedFiles(t *testing.T) {
	for _, change := range []string{"contents", "identity", "added", "missing"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private")
			directory, err := productfiles.NewDirectory(path)
			require.NoError(t, err)
			file := filepath.Join(path, "database")
			require.NoError(t, os.WriteFile(file, []byte("initial"), configFileMode))
			receipt, err := captureSetupReceipt(directory)
			require.NoError(t, err)
			switch change {
			case "contents":
				require.NoError(t, os.WriteFile(file, []byte("changed"), configFileMode))
			case "identity":
				require.NoError(t, os.Rename(file, filepath.Join(filepath.Dir(path), "previous")))
				require.NoError(t, os.WriteFile(file, []byte("initial"), configFileMode))
				modified := time.Unix(0, receipt.Files["database"].Modified)
				require.NoError(t, os.Chtimes(file, modified, modified))
			case "added":
				require.NoError(t, os.WriteFile(filepath.Join(path, "other"), []byte("new"), configFileMode))
			case "missing":
				require.NoError(t, os.Remove(file))
			}
			require.ErrorIs(t, verifySetupReceipt(directory, receipt), ErrPartialState)
		})
	}
}

func TestPreparedSetupNeverPersistsGatewaySecret(t *testing.T) {
	paths := productSetupPaths(t)
	prepared, err := New(paths).prepareAcrossRoots(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.writer.close()) })
	journal, err := prepared.writer.directory.ReadFile(setupJournalFile, setupFileLimit)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(journal), prepared.secret))
}
