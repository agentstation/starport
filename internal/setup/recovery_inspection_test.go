package setup

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverySetupInspectionPreservesSettledStateAndRefusesPendingWork(t *testing.T) {
	paths := productSetupPaths(t)
	_, err := New(paths).Initialize(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	original, err := InspectRecoveryState(t.Context(), paths)
	require.NoError(t, err)
	second, err := InspectRecoveryState(t.Context(), paths)
	require.NoError(t, err)
	require.Equal(t, original, second)
	configuration, err := os.ReadFile(paths.ConfigFile)
	require.NoError(t, err)
	stage := filepath.Join(filepath.Dir(paths.BadgerDir), ".starport-init-uncertain")
	require.NoError(t, os.Mkdir(stage, 0700))
	_, err = InspectRecoveryState(t.Context(), paths)
	require.ErrorIs(t, err, ErrPartialState)
	require.DirExists(t, stage, "inspection cannot repair uncertain native setup")
	actual, err := os.ReadFile(paths.ConfigFile)
	require.NoError(t, err)
	require.Equal(t, configuration, actual)
	require.NoError(t, os.Remove(stage))
	metadata := filepath.Join(filepath.Dir(paths.ConfigFile), setupMetadataDirectory)
	require.NoError(t, os.WriteFile(filepath.Join(metadata, "unexpected-journal"), []byte("pending operator state"), 0600))
	_, err = InspectRecoveryState(t.Context(), paths)
	require.ErrorIs(t, err, ErrPartialState)
	require.FileExists(t, filepath.Join(metadata, "unexpected-journal"))
}
