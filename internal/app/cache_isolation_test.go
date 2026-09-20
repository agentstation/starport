package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestDefaultResponseCacheDoesNotWriteDurableKV(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Cache.Enabled = true
	store, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = keys.Create(t.Context(), testAPIKey())
	require.NoError(t, err)
	require.NoError(t, store.Close())
	factories := explicitTestFactories()
	factories.openStorage = openStorage
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NoError(t, application.cacheManager.SetResponse(t.Context(), "answer", []byte("response")))
	require.Eventually(t, func() bool {
		_, found, err := application.cacheManager.GetResponse(t.Context(), "answer")
		return err == nil && found
	}, time.Second, time.Millisecond)
	value, found, err := application.cacheManager.GetResponse(t.Context(), "answer")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "response", string(value))
	storedKeys, err := application.store.ScanWithPrefix(t.Context(), storage.KeyPrefixResponse, 10)
	require.NoError(t, err)
	require.Empty(t, storedKeys)
}

func TestExtractionCacheHasIndependentLocalLifecycle(t *testing.T) {
	application := &App{}
	builder := &runtimeBuilder{application: application}
	extractions, err := builder.openExtractionCache()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	key := document.CacheKey{AccountID: "account", ContentHash: "hash", Engine: "native", Generation: "generation"}
	require.NoError(t, extractions.Put(t.Context(), key, document.Reading{Text: "text"}))
	value, found, err := extractions.Get(t.Context(), key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "text", value.Text)
	require.Nil(t, application.store)
	require.Nil(t, application.cacheManager)
	require.Len(t, application.lifecycle, 1)
}

func TestSharedCacheCompositionUsesSeparateService(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_URL is not set")
	}
	cfg := validProductionConfig(t)
	cfg.Cache.Enabled = true
	cfg.Cache.Backend = "valkey"
	cfg.Cache.URL = raw
	cfg.Cache.Namespace = "app-composition"
	application := &App{}
	builder := &runtimeBuilder{application: application, config: cfg, factories: defaultRuntimeFactories()}
	require.NoError(t, builder.openCache())
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	require.Nil(t, application.store)
	require.Eventually(t, func() bool { return application.cacheManager.FillStatus().Shared.Available }, 5*time.Second, time.Millisecond)
	require.NoError(t, application.cacheManager.SetResponse(t.Context(), "composition", []byte("answer")))
	require.Eventually(t, func() bool {
		value, found, err := application.cacheManager.GetResponse(t.Context(), "composition")
		return err == nil && found && string(value) == "answer"
	}, time.Second, time.Millisecond)
}
