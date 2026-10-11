package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAuthBootstrapRecoversPartialSetup proves the setup half of
// "starport auth bootstrap --recover". Only a child process that terminates at
// a setup checkpoint leaves a real pending transaction, and the checkpoint is
// internal to this package, so the test lives here. The command composition
// passes --recover straight to Service.Recover.
func TestAuthBootstrapRecoversPartialSetup(t *testing.T) {
	for _, test := range []struct {
		phase     string
		published bool
	}{
		{phase: "prepared"},
		{phase: "data-published"},
		{phase: "configuration-published", published: true},
	} {
		t.Run(test.phase, func(t *testing.T) {
			paths := productSetupPaths(t)
			interruptSetup(t, paths, test.phase)
			state, _ := Inspect(paths)
			require.NotEqual(t, StateReady, state)

			result, err := New(paths).Recover(t.Context(), Request{APIKeyName: DefaultAPIKeyName})

			require.NoError(t, err)
			if test.published {
				// The interrupted run stored its key before it stopped, so
				// recovery settles the root and has no key to show.
				require.Empty(t, result.APIKey)
			} else {
				require.NotEmpty(t, result.APIKey)
				require.Equal(t, DefaultAPIKeyName, result.APIKeyName)
			}
			state, err = Inspect(paths)
			require.NoError(t, err)
			require.Equal(t, StateReady, state)
			guard, err := GuardLocalStorage(t.Context(), paths)
			require.NoError(t, err)
			require.NoError(t, guard.Close())
			require.NoFileExists(t, filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile))

			_, err = New(paths).Recover(t.Context(), Request{APIKeyName: DefaultAPIKeyName})
			require.ErrorIs(t, err, ErrNoPendingSetup)
		})
	}
}

func TestRecoverRefusesAnEmptyRootAndWritesNothing(t *testing.T) {
	paths := productSetupPaths(t)

	result, err := New(paths).Recover(t.Context(), Request{APIKeyName: DefaultAPIKeyName})

	require.ErrorIs(t, err, ErrNoPendingSetup)
	require.ErrorContains(t, err, "starport serve")
	require.Empty(t, result.APIKey)
	for _, directory := range []string{paths.ConfigDir, paths.DataDir} {
		_, err := os.Stat(directory)
		require.True(t, errors.Is(err, os.ErrNotExist), "%s: %v", directory, err)
	}
}

func TestRecoverRefusesAReadyRoot(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	_, err := service.Initialize(t.Context(), Request{APIKeyName: DefaultAPIKeyName})
	require.NoError(t, err)
	before := mustReadFile(t, paths.ConfigFile)

	result, err := service.Recover(t.Context(), Request{APIKeyName: DefaultAPIKeyName})

	require.ErrorIs(t, err, ErrNoPendingSetup)
	require.ErrorContains(t, err, "already initialized")
	require.Empty(t, result.APIKey)
	require.Equal(t, before, mustReadFile(t, paths.ConfigFile))
}

func TestRecoverRefusesAPartialRootWithoutATransaction(t *testing.T) {
	paths := productSetupPaths(t)
	require.NoError(t, os.MkdirAll(paths.ConfigDir, 0o700))
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("# operator file\n"), 0o600))

	_, err := New(paths).Recover(t.Context(), Request{APIKeyName: DefaultAPIKeyName})

	require.ErrorIs(t, err, ErrNoPendingSetup)
	require.ErrorContains(t, err, paths.ConfigDir)
	require.Equal(t, "# operator file\n", string(mustReadFile(t, paths.ConfigFile)))
}
