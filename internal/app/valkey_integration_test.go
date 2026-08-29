package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
)

func TestAppWithValkey(t *testing.T) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = valkeyURL
	cfg.Storage.Valkey.MaxConnections = 10
	cfg.Storage.Valkey.MinIdleConns = 1
	cfg.Storage.Valkey.DialTimeout = time.Second
	cfg.Storage.Valkey.ReadTimeout = time.Second
	cfg.Storage.Valkey.WriteTimeout = time.Second
	cfg.Cache.Enabled = true
	store, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	apiKeys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = apiKeys.Create(context.Background(), testAPIKey())
	if err != nil && !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("seed Valkey API key: %v", err)
	}
	require.NoError(t, store.Close())

	factories := explicitTestFactories()
	factories.openStorage = openStorage

	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	require.NotNil(t, application.cacheManager)
	require.NoError(t, application.store.Set(context.Background(), "app:test", []byte("value")))
	value, err := application.store.Get(context.Background(), "app:test")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), value)
	require.NoError(t, application.Close(context.Background()))
}

func TestStorageModeValidation(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "invalid"
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.Error(t, err)
	require.Nil(t, application)
}
