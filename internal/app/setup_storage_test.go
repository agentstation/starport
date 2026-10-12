package app

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/endpoint"
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

// TestSecondGatewayNamesTheRunningGateway refuses a gateway on a state root
// that another gateway holds, and names the URL and the process ID from the
// live endpoint record.
func TestSecondGatewayNamesTheRunningGateway(t *testing.T) {
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

	// The other gateway holds the setup guard, listens, and records its
	// address. The parent process stands in for it, because it is alive and
	// is not this process.
	guard, err := setup.GuardLocalStorage(t.Context(), paths)
	require.NoError(t, err)
	t.Cleanup(func() { _ = guard.Close() })
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	port := endpoint.Port(listener.Addr())
	require.NoError(t, endpoint.WriteRecord(cfg.EndpointRecordFile(), endpoint.Record{
		SchemaVersion: endpoint.RecordSchemaVersion, Scheme: "http", Host: "127.0.0.1", Port: port,
		PID: os.Getppid(), StartedAt: time.Now().UTC(),
	}))

	application, err := New(cfg)
	require.ErrorIs(t, err, setup.ErrPartialState)
	require.Nil(t, application)
	require.ErrorContains(t, err, "another gateway (PID "+strconv.Itoa(os.Getppid())+")")
	require.ErrorContains(t, err, "http://127.0.0.1:"+strconv.Itoa(port))
}
