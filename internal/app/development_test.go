package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
)

func TestDevUsesInMemoryBadger(t *testing.T) {
	cfg := validProductionConfig(t)
	persistentPath := filepath.Join(t.TempDir(), "persistent-badger")
	cfg.Storage.Badger.Path = persistentPath
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	projected := cfg.Storage.RuntimeStorage()
	require.Equal(t, storage.StorageTypeBadger, projected.Type)
	require.True(t, projected.Badger.InMemory)
	require.Empty(t, projected.Badger.Path)
	_, isBadger := runtime.application.store.(*storage.BadgerStore)
	require.True(t, isBadger)
	_, err = os.Stat(persistentPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDevBindsLoopbackOnly(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 18991
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	require.Equal(t, "127.0.0.1", cfg.Server.Host)
	require.Equal(t, "http://127.0.0.1:18991", runtime.URL())
}

func TestDevRejectsCanceledContextBeforeCreatingState(t *testing.T) {
	cfg := validProductionConfig(t)
	persistentPath := filepath.Join(t.TempDir(), "persistent-badger")
	cfg.Storage.Badger.Path = persistentPath
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	runtime, err := NewDevelopment(cancelled, cfg)
	require.Nil(t, runtime)
	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(persistentPath)
	require.True(t, errors.Is(statErr, os.ErrNotExist))
}
