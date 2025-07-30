package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppWithValkey tests the full application initialization with Valkey
func TestAppWithValkey(t *testing.T) {
	// Skip if no Valkey URL is provided
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping app Valkey integration tests: TEST_VALKEY_URL not set")
	}

	t.Run("Initialize with Valkey storage", func(t *testing.T) {
		// Create app with Valkey storage
		app, err := New(
			WithServerConfig(server.Config{
				Port: 8081,
				Host: "127.0.0.1",
			}),
			WithStorageMode("valkey"),
			WithStorageConfig(&config.StorageConfig{
				Mode: "valkey",
				Valkey: config.ValkeyConfig{
					URL:          valkeyURL + "/13", // Different DB for app tests
					MinIdleConns: 2,
					ReadTimeout:  3 * time.Second,
					WriteTimeout: 3 * time.Second,
				},
			}),
			WithLogLevel("debug"),
			WithCache(true),
		)
		require.NoError(t, err)
		assert.NotNil(t, app)
		assert.NotNil(t, app.store)
		assert.NotNil(t, app.cacheManager)

		// Test that storage is working
		ctx := context.Background()
		err = app.store.Set(ctx, "test:app:key", []byte("test value"))
		require.NoError(t, err)

		val, err := app.store.Get(ctx, "test:app:key")
		require.NoError(t, err)
		assert.Equal(t, []byte("test value"), val)

		// Cleanup
		err = app.store.Close()
		assert.NoError(t, err)
	})

	t.Run("Initialize without cache", func(t *testing.T) {
		// Create app with Valkey storage but no cache
		app, err := New(
			WithStorageMode("valkey"),
			WithStorageConfig(&config.StorageConfig{
				Mode: "valkey",
				Valkey: config.ValkeyConfig{
					URL: valkeyURL + "/13",
				},
			}),
			WithCache(false),
		)
		require.NoError(t, err)
		assert.NotNil(t, app)
		assert.NotNil(t, app.store)
		assert.Nil(t, app.cacheManager)

		// Cleanup
		err = app.store.Close()
		assert.NoError(t, err)
	})

	t.Run("Initialize with invalid Valkey URL", func(t *testing.T) {
		// Try to create app with invalid Valkey URL
		app, err := New(
			WithStorageMode("valkey"),
			WithStorageConfig(&config.StorageConfig{
				Mode: "valkey",
				Valkey: config.ValkeyConfig{
					URL: "valkey://invalid-host:12345",
				},
			}),
		)
		assert.Error(t, err)
		assert.Nil(t, app)
		assert.Contains(t, err.Error(), "failed to initialize storage")
	})

	t.Run("Run and shutdown with Valkey", func(t *testing.T) {
		// Create app
		app, err := New(
			WithServerConfig(server.Config{
				Port: 8082,
				Host: "127.0.0.1",
			}),
			WithStorageMode("valkey"),
			WithStorageConfig(&config.StorageConfig{
				Mode: "valkey",
				Valkey: config.ValkeyConfig{
					URL: valkeyURL + "/13",
				},
			}),
			WithCache(true),
		)
		require.NoError(t, err)

		// Create context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Run app in goroutine
		errCh := make(chan error, 1)
		go func() {
			errCh <- app.Run(ctx)
		}()

		// Give server time to start
		time.Sleep(500 * time.Millisecond)

		// Cancel context to trigger shutdown
		cancel()

		// Wait for app to shutdown
		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("app failed to shutdown in time")
		}
	})
}

// TestStorageModeValidation tests storage mode validation
func TestStorageModeValidation(t *testing.T) {
	t.Run("Invalid storage mode", func(t *testing.T) {
		app, err := New(
			WithStorageMode("invalid"),
		)
		assert.Error(t, err)
		assert.Nil(t, app)
		assert.Contains(t, err.Error(), "invalid storage mode")
	})

	t.Run("Valkey without config", func(t *testing.T) {
		app, err := New(
			WithStorageMode("valkey"),
			// No storage config provided
		)
		assert.Error(t, err)
		assert.Nil(t, app)
		assert.Contains(t, err.Error(), "valkey storage configuration not provided")
	})
}

// TestCacheManagerLifecycle tests cache manager lifecycle in app
func TestCacheManagerLifecycle(t *testing.T) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping cache manager lifecycle tests: TEST_VALKEY_URL not set")
	}

	// Create app with cache
	app, err := New(
		WithStorageMode("valkey"),
		WithStorageConfig(&config.StorageConfig{
			Mode: "valkey",
			Valkey: config.ValkeyConfig{
				URL: valkeyURL + "/13",
			},
		}),
		WithCache(true),
	)
	require.NoError(t, err)
	require.NotNil(t, app.cacheManager)

	// Test cache manager is accessible to server
	assert.NotNil(t, app.httpServer)

	// Create context for shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Start app
	go func() {
		_ = app.Run(ctx)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Trigger shutdown
	cancel()

	// Give it time to shutdown
	time.Sleep(100 * time.Millisecond)

	// Verify clean shutdown (no panics)
}
