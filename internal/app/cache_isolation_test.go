package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
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
	created, err := keys.Create(t.Context(), testAPIKey())
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
	application.cacheManager.InvalidateModels()
	require.NoError(t, application.cacheManager.Close())
	durableKeys, err := apikey.Open(application.store)
	require.NoError(t, err)
	retained, err := durableKeys.GetByID(t.Context(), created.APIKey.ID)
	require.NoError(t, err)
	require.Equal(t, created, retained, "cache cleanup must preserve authoritative API key records")
}

func TestExtractionCacheHasIndependentLocalLifecycle(t *testing.T) {
	application := &App{}
	cfg := validProductionConfig(t)
	cfg.Cache.Enabled = true
	builder := &runtimeBuilder{application: application, config: cfg}
	extractions, err := builder.openExtractionCache()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	key := document.CacheKey{AccountID: "account", ContentHash: "hash", Engine: "native", Generation: "generation"}
	require.NoError(t, extractions.Put(t.Context(), key, document.Reading{Text: "text"}))
	require.Eventually(t, func() bool { _, found, err := extractions.Get(t.Context(), key); return err == nil && found }, time.Second, time.Millisecond)
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
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CACHE_BACKEND": "valkey", "STARPORT_CACHE_URL": raw,
		"STARPORT_DEPLOYMENT_ID": "app-composition",
	}).Load(t.Context())
	require.NoError(t, err)
	cfg.Cache.Enabled = true
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

func TestSharedCacheCanonicalDeploymentIsolation(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_URL is not set")
	}
	open := func(id string) *App {
		t.Helper()
		cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
			"STARPORT_DEPLOYMENT_ID": id, "STARPORT_CACHE_BACKEND": "valkey", "STARPORT_CACHE_URL": raw,
		}).Load(t.Context())
		require.NoError(t, err)
		cfg.Cache.Enabled = true
		application := &App{}
		builder := &runtimeBuilder{application: application, config: cfg, factories: defaultRuntimeFactories()}
		require.NoError(t, builder.openCache())
		t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
		require.Eventually(t, func() bool { return application.cacheManager.FillStatus().Shared.Available }, 5*time.Second, time.Millisecond)
		return application
	}
	id := t.Name() + time.Now().Format("150405.000000000")
	first, replica, other := open(id+"/東京:*"), open(id+"/東京:*"), open(id+"/東京:?")
	require.NoError(t, first.cacheManager.SetResponse(t.Context(), "same-key", []byte("private-answer")))
	require.Eventually(t, func() bool {
		value, found, err := replica.cacheManager.GetResponse(t.Context(), "same-key")
		return err == nil && found && string(value) == "private-answer"
	}, time.Second, time.Millisecond)
	_, found, err := other.cacheManager.GetResponse(t.Context(), "same-key")
	require.NoError(t, err)
	require.False(t, found, "different canonical deployments must not share cached responses")
}

func TestExtractionCacheOversizeReportsDrop(t *testing.T) {
	application := &App{}
	cfg := validProductionConfig(t)
	cfg.Cache.Enabled = true
	builder := &runtimeBuilder{application: application, config: cfg}
	extractions, err := builder.openExtractionCache()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	key := document.CacheKey{AccountID: "account", ContentHash: "hash", Engine: "native", Generation: "generation"}
	require.NoError(t, extractions.Put(t.Context(), key, document.Reading{Text: string(make([]byte, document.MaxCacheInputBytes+1))}))
	require.Equal(t, uint64(1), application.extractionCache.FillStatus().DroppedFills)
	require.Zero(t, application.extractionCache.FillStatus().RetainedBytes)
}

func TestMasterCacheSwitchDisablesExtraction(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Cache.Enabled = false
	application := &App{}
	builder := &runtimeBuilder{application: application, config: cfg, factories: defaultRuntimeFactories()}
	require.NoError(t, builder.openCache())
	require.Nil(t, application.cacheManager)
	extractions, err := builder.openExtractionCache()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	require.Nil(t, extractions)
	require.Nil(t, application.extractionCache)
	require.Empty(t, application.lifecycle)
}

func TestCacheKindComposition(t *testing.T) {
	for _, kind := range []string{"none", "chat", "embeddings", "models", "providers", "extractions"} {
		t.Run(kind, func(t *testing.T) {
			cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(nil).Load(t.Context())
			require.NoError(t, err)
			cfg.Cache = config.CacheConfig{
				Enabled: true, Backend: "valkey", URL: "valkey://127.0.0.1:1",
				ChatEnabled: kind == "chat", EmbeddingsEnabled: kind == "embeddings",
				ModelsEnabled: kind == "models", ProvidersEnabled: kind == "providers",
				ExtractionsEnabled: kind == "extractions",
			}
			application := &App{}
			builder := &runtimeBuilder{application: application, config: cfg, factories: defaultRuntimeFactories()}
			require.NoError(t, builder.openCache())
			extractions, err := builder.openExtractionCache()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
			if kind == "none" || kind == "extractions" {
				require.Nil(t, application.cacheManager)
			} else {
				require.NotNil(t, application.cacheManager)
				require.Equal(t, kind == "chat" || kind == "embeddings", application.cacheManager.FillStatus().Shared.Configured)
			}
			if kind == "extractions" {
				require.NotNil(t, extractions)
			} else {
				require.Nil(t, extractions)
				require.Nil(t, application.extractionCache)
			}
		})
	}
}
