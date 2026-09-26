//go:build linux || darwin

package config

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderChecksPrimaryAndDotenvAccess(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	file := filepath.Join(paths.ConfigDir, "service.env")
	require.NoError(t, os.WriteFile(file, []byte("STARPORT_SERVER_PORT=9191\n"), 0o600))
	require.NoError(t, os.Chmod(file, 0o640))
	env := map[string]string{"STARPORT_CONFIG_FILE": file}
	_, err := NewLoader().WithPaths(paths).WithEnvironment(env).Load(t.Context())
	require.Error(t, err)
	env["STARPORT_CONFIG_ACCESS"] = "service-managed"
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(env).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, 9191, cfg.Server.Port)
	_, err = NewLoader().WithPaths(paths).WithEnvironment(env).WithEnvFiles(file).Load(t.Context())
	require.Error(t, err)
	require.NoError(t, os.Chmod(file, 0o660))
	_, err = NewLoader().WithPaths(paths).WithEnvironment(env).Load(t.Context())
	require.Error(t, err)
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o660), info.Mode().Perm())
}
