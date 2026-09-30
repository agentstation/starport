//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
)

func TestRecoverySelectionUsesNativePrivateAndServicePolicies(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "service")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	path := filepath.Join(parent, "service.env")
	require.NoError(t, os.WriteFile(path, []byte("STARPORT_SERVER_PORT=7777\n"), 0640))
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(parent)).WithEnvironment(map[string]string{"STARPORT_CONFIG_FILE": path, "STARPORT_CONFIG_ACCESS": "service-managed"}).Load(t.Context())
	require.NoError(t, err)
	entry := cfg.fileInputs[0]
	body, binding, err := readRecoveryFile(t.Context(), mustRecoveryPrimaryEntry(t, cfg))
	require.NoError(t, err)
	require.NotEmpty(t, body)
	require.Equal(t, entry.access, binding.Access)
	require.NoError(t, os.Chmod(path, 0660))
	_, _, err = readRecoveryFile(t.Context(), mustRecoveryPrimaryEntry(t, cfg))
	require.Error(t, err)
	require.NoError(t, os.Chmod(path, 0640))
	_, err = NewLoader().WithPaths(PathsForConfigDir(parent)).WithEnvironment(map[string]string{}).WithEnvFiles(path).Load(t.Context())
	require.Error(t, err)
	private := recoverySelectionFixture(t, "", "")
	require.NoError(t, os.Chmod(private.EffectivePaths().LocalTokenFile, 0640))
	_, err = private.InspectRecoverySelection(t.Context())
	require.Error(t, err)
}
