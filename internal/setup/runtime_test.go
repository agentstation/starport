package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeGuardExcludesSetupAndOtherRuntimes(t *testing.T) {
	paths := productSetupPaths(t)
	guard, err := GuardLocalStorage(t.Context(), paths)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, guard.Close()) })
	_, err = New(paths).Initialize(t.Context(), Request{APIKeyName: "admin"})
	require.ErrorIs(t, err, ErrPartialState)
	_, err = GuardLocalStorage(t.Context(), paths)
	require.ErrorIs(t, err, ErrPartialState)
	require.NoFileExists(t, paths.ConfigFile)
	require.NoDirExists(t, paths.BadgerDir)
	require.NoError(t, guard.Close())
	result, err := New(paths).Initialize(t.Context(), Request{APIKeyName: "admin"})
	require.NoError(t, err)
	guard, err = GuardLocalStorage(t.Context(), paths)
	require.NoError(t, err)
	require.ErrorIs(t, New(paths).Rollback(t.Context(), result), ErrRollbackRefused)
	require.NoError(t, guard.Close())
	require.NoError(t, New(paths).Rollback(t.Context(), result))
}

func TestRuntimeGuardRefusesPendingSetup(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	prepared, err := service.prepareAcrossRoots(t.Context(), Request{APIKeyName: "admin"})
	require.NoError(t, err)
	_, err = GuardLocalStorage(t.Context(), paths)
	require.ErrorIs(t, err, ErrPartialState)
	require.NoDirExists(t, paths.BadgerDir)
	require.NoError(t, prepared.writer.close())
	_, err = GuardLocalStorage(t.Context(), paths)
	require.ErrorIs(t, err, ErrPartialState)
	_, err = New(paths).Initialize(t.Context(), Request{APIKeyName: "replacement"})
	require.NoError(t, err)
	guard, err := GuardLocalStorage(t.Context(), paths)
	require.NoError(t, err)
	require.NoError(t, guard.Close())
}

func TestRollbackPreservesConflictingRestoreDestination(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	result, err := service.Initialize(t.Context(), Request{APIKeyName: "admin"})
	require.NoError(t, err)
	service.checkpoint = func(phase string) error {
		if phase == "rollback-isolated" {
			require.NoError(t, os.Mkdir(paths.BadgerDir, privateDirMode))
			require.NoError(t, os.WriteFile(filepath.Join(paths.BadgerDir, "operator-state"), []byte("preserve"), configFileMode))
			return ErrPartialState
		}
		return nil
	}
	require.ErrorIs(t, service.Rollback(t.Context(), result), ErrRollbackRefused)
	require.Error(t, New(paths).recoverAcrossRoots(t.Context()))
	require.Equal(t, "preserve", string(mustReadFile(t, filepath.Join(paths.BadgerDir, "operator-state"))))
	require.DirExists(t, filepath.Join(paths.DataDir, result.recovery.Stage))
	require.FileExists(t, paths.ConfigFile)
	require.FileExists(t, filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile))
}
