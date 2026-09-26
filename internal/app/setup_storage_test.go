package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/setup"
	"github.com/stretchr/testify/require"
)

func TestPendingSetupRefusesBeforeGatewayStores(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gateway")
	loader := config.NewLoader().WithEnvironment(map[string]string{
		"STARPORT_HOME": home, "STARPORT_CATALOG_NETWORK_MODE": "offline", "STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).WithEnvFiles()
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	paths := cfg.EffectivePaths()
	_, err = setup.New(paths).Initialize(t.Context(), setup.Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	cfg, err = loader.WithEnvFiles(paths.ConfigFile).Load(t.Context())
	require.NoError(t, err)
	journal := filepath.Join(paths.ConfigDir, ".starport-setup", "transaction.json")
	require.NoError(t, os.WriteFile(journal, []byte("incomplete-transaction"), 0o600))
	application, err := New(cfg)
	require.ErrorIs(t, err, setup.ErrPartialState)
	require.Nil(t, application)
	require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
	require.NoDirExists(t, cfg.Files.Path)
	require.NoDirExists(t, paths.BaselineDir)
	require.FileExists(t, paths.ConfigFile)
	require.DirExists(t, paths.BadgerDir)
	contents, err := os.ReadFile(journal)
	require.NoError(t, err)
	require.Equal(t, "incomplete-transaction", string(contents))
}

func TestGatewayHoldsSetupGuardUntilStorageCloses(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gateway")
	loader := config.NewLoader().WithEnvironment(map[string]string{
		"STARPORT_HOME": home, "STARPORT_CATALOG_NETWORK_MODE": "offline", "STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).WithEnvFiles()
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	paths := cfg.EffectivePaths()
	_, err = setup.New(paths).Initialize(t.Context(), setup.Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	cfg, err = loader.WithEnvFiles(paths.ConfigFile).Load(t.Context())
	require.NoError(t, err)
	application, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(t.Context())) })
	_, err = setup.GuardLocalStorage(t.Context(), paths)
	require.ErrorIs(t, err, setup.ErrPartialState)
	require.NoError(t, application.Close(t.Context()))
	guard, err := setup.GuardLocalStorage(t.Context(), paths)
	require.NoError(t, err)
	require.NoError(t, guard.Close())
}
