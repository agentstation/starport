package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
)

func TestRuntimeRequiresNamedIdentity(t *testing.T) {
	identities, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)
	require.ErrorIs(t, requireIdentity(context.Background(), identities), ErrIdentityRequired)

	_, err = identities.Create(context.Background(), testIdentity())
	require.NoError(t, err)
	require.NoError(t, requireIdentity(context.Background(), identities))
}

func TestProductionCompositionFailsClosed(t *testing.T) {
	baseConfig := validProductionConfig(t)
	tests := []struct {
		name   string
		mutate func(*config.Config, *runtimeFactories)
		cause  error
	}{
		{
			name: "missing storage",
			mutate: func(_ *config.Config, factories *runtimeFactories) {
				factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) { return nil, nil }
			},
			cause: ErrStorageRequired,
		},
		{
			name: "missing catalog",
			mutate: func(_ *config.Config, factories *runtimeFactories) {
				factories.openCatalog = func(
					context.Context,
					storage.KVStore,
					string,
				) (catalogRuntime, error) {
					return nil, nil
				}
			},
			cause: ErrCatalogRequired,
		},
		{
			name: "missing credentials",
			mutate: func(cfg *config.Config, _ *runtimeFactories) {
				cfg.Security.MasterKey = ""
			},
			cause: ErrCredentialsRequired,
		},
		{
			name: "missing identity",
			mutate: func(_ *config.Config, factories *runtimeFactories) {
				factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) {
					return storage.NewMockStore(), nil
				}
			},
			cause: ErrIdentityRequired,
		},
		{
			name: "missing providers",
			mutate: func(cfg *config.Config, _ *runtimeFactories) {
				cfg.Providers = config.ProvidersConfig{}
			},
			cause: ErrProvidersRequired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := *baseConfig
			factories := explicitTestFactories()
			test.mutate(&cfg, &factories)
			application, err := New(&cfg, withRuntimeFactories(factories))
			if application != nil {
				t.Cleanup(func() { _ = application.Close(context.Background()) })
			}
			require.ErrorIs(t, err, test.cause)
		})
	}
}

func TestStartupCatalogRefreshIsExplicitAndResilient(t *testing.T) {
	refreshErr := errors.New("catalog source unavailable")
	tests := []struct {
		name           string
		refreshOnStart bool
		workspacePath  string
		wantCalls      int
	}{
		{name: "workspace does not imply refresh", workspacePath: "configured-workspace"},
		{name: "requested refresh retains current generation on failure", refreshOnStart: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validProductionConfig(t)
			cfg.Catalog.RefreshOnStart = test.refreshOnStart
			cfg.Catalog.WorkspacePath = test.workspacePath
			baseRuntime, err := runtimecatalog.OpenRuntime(context.Background(), storage.NewMockStore(), "")
			require.NoError(t, err)
			catalog := &failingCatalogRuntime{Runtime: baseRuntime, err: refreshErr}
			factories := explicitTestFactories()
			factories.openCatalog = func(context.Context, storage.KVStore, string) (catalogRuntime, error) {
				return catalog, nil
			}

			application, err := New(cfg, withRuntimeFactories(factories))
			require.NoError(t, err)
			require.Equal(t, test.wantCalls, catalog.calls)
			require.NoError(t, application.Close(context.Background()))
		})
	}
}

func TestDefaultFactoryErrorsReturnNilInterfaces(t *testing.T) {
	factories := defaultRuntimeFactories()

	httpServer, err := factories.newServer(nil, server.Dependencies{})
	require.Error(t, err)
	require.Nil(t, httpServer)

	configPath := filepath.Join(t.TempDir(), "invalid.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("invalid: [yaml document"), 0o600))
	reloader, err := factories.newHotReload(configPath, time.Second)
	require.Error(t, err)
	require.Nil(t, reloader)

	storagePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(storagePath, []byte("occupied"), 0o600))
	store, err := openStorage(config.StorageConfig{
		Mode: "badger", Badger: config.BadgerConfig{Path: storagePath, Compression: "snappy"},
	})
	require.Error(t, err)
	require.Nil(t, store)
}

func TestServerConfigCredentialsFollowOriginScope(t *testing.T) {
	cfg := validProductionConfig(t)
	require.False(t, serverConfig(cfg).CORS.AllowCredentials)

	cfg.Security.AllowedOrigins = "https://console.example.com"
	require.True(t, serverConfig(cfg).CORS.AllowCredentials)

	cfg.Security.EnableCORS = false
	require.False(t, serverConfig(cfg).CORS.AllowCredentials)
}

func TestNewBuildsReadyProductionDependencies(t *testing.T) {
	cfg := validProductionConfig(t)
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	require.NotNil(t, application.store)
	require.NotNil(t, application.catalog)
	require.NotNil(t, application.registry)
	require.NotNil(t, application.httpServer)
	require.Equal(t, []string{"openai"}, application.registry.ListProviders())
	require.NoError(t, application.Close(context.Background()))
}

func TestNewMapsExternalServerConfigurationOnce(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.RequestTimeout = 17 * time.Second
	cfg.Server.MaxRequestSize = 23 << 20
	cfg.Server.MaxHeaderBytes = 2 << 20
	factories := explicitTestFactories()
	var captured *server.Config
	factories.newServer = func(value *server.Config, _ server.Dependencies) (httpRuntime, error) {
		captured = value
		return newBlockingHTTPRuntime(), nil
	}

	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	require.NotNil(t, captured)
	require.Equal(t, 17*time.Second, captured.RequestTimeout)
	require.Equal(t, int64(23<<20), captured.MaxRequestSize)
	require.Equal(t, 2<<20, captured.MaxHeaderBytes)
	require.NoError(t, application.Close(context.Background()))
}

func TestLifecycleClosesInReverseOrderOnce(t *testing.T) {
	var order []string
	application := &App{}
	for _, name := range []string{"storage", "registry", "cache", "server"} {
		name := name
		application.own(name, func(context.Context) error {
			order = append(order, name)
			return nil
		})
	}
	require.NoError(t, application.Close(context.Background()))
	require.NoError(t, application.Close(context.Background()))
	require.Equal(t, []string{"server", "cache", "registry", "storage"}, order)
}

func TestRunCancellationStopsHTTPAndDependencies(t *testing.T) {
	cfg := validProductionConfig(t)
	fakeHTTP := newBlockingHTTPRuntime()
	factories := explicitTestFactories()
	factories.newServer = func(*server.Config, server.Dependencies) (httpRuntime, error) {
		return fakeHTTP, nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- application.Run(ctx) }()
	select {
	case <-fakeHTTP.started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP runtime did not start")
	}
	cancel()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("application did not stop after cancellation")
	}
	require.True(t, fakeHTTP.wasStopped())
}

func validProductionConfig(t *testing.T) *config.Config {
	t.Helper()
	credentialPath := filepath.Join(t.TempDir(), "openai-api-key")
	require.NoError(t, os.WriteFile(credentialPath, []byte("sk-test-key"), 0o600))
	return &config.Config{
		Server: config.ServerConfig{
			Port: 18080, Host: "127.0.0.1", ReadTimeout: time.Second,
			WriteTimeout: time.Second, IdleTimeout: time.Second,
			MaxHeaderBytes: 1 << 20, ShutdownTimeout: time.Second,
		},
		Storage: config.StorageConfig{
			Mode: "badger",
			Badger: config.BadgerConfig{
				Path: t.TempDir(), Compression: "snappy", GCInterval: time.Minute,
				GCDiscardRatio: 0.5,
			},
		},
		Providers: config.ProvidersConfig{
			catalogs.ProviderIDOpenAI: {
				BaseURL: "https://api.openai.com/v1",
				CredentialReferences: map[catalogs.ProviderCredentialFieldID]config.CredentialReference{
					"api-key": {Reference: "file:" + credentialPath},
				},
				Timeout: time.Second, MaxConnections: 10,
			},
		},
		RateLimiting: config.RateLimitingConfig{
			GlobalRequestsPerSecond: 100, GlobalBurstMultiplier: 2,
			DefaultRequestsPerMinute: 60, DefaultRequestsPerHour: 1000,
			DefaultTokensPerMinute: 1000, DefaultTokensPerHour: 10000,
			DefaultBurst: 10, WindowSize: time.Minute,
			SyncInterval: time.Second, CleanupInterval: time.Minute,
			EnableHotReload: false,
		},
		Security: config.SecurityConfig{
			MasterKey:      strings.Repeat("k", 32),
			AllowedOrigins: "*", EnableCORS: true,
		},
		Logging: config.LoggingConfig{
			Level: "info", Format: "json", Output: "stdout",
			MaxSize: 100, MaxBackups: 3, MaxAge: 7,
		},
		Cache:  config.CacheConfig{Enabled: false},
		ChatUI: config.ChatUIConfig{Title: "Starport", Theme: "light"},
	}
}

func explicitTestFactories() runtimeFactories {
	factories := defaultRuntimeFactories()
	store := storage.NewMockStore()
	identities, _ := identity.Open(store)
	_, _ = identities.Create(context.Background(), testIdentity())
	factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) {
		return store, nil
	}
	factories.newConnector = func(
		_ string,
		_ []catalogs.EndpointType,
		providerConfig connectors.ProviderConfig,
	) (connectors.Connector, error) {
		return connectors.NewMockConnector(providerConfig), nil
	}
	return factories
}

func testIdentity() identity.APIKey {
	return identity.APIKey{
		ID: "STARPORT_TEST", Name: "test-admin", Hash: "test-hash",
		Scopes: []string{"*"}, Active: true, CreatedAt: time.Now().UTC(),
		Metadata: map[string]any{"source": "test"},
	}
}

type failingCatalogRuntime struct {
	*runtimecatalog.Runtime
	err   error
	calls int
}

func (runtime *failingCatalogRuntime) Sync(
	context.Context,
	...pkgsync.Option,
) (*pkgsync.Result, starmap.CatalogState, error) {
	runtime.calls++
	return nil, starmap.CatalogState{}, runtime.err
}

type blockingHTTPRuntime struct {
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newBlockingHTTPRuntime() *blockingHTTPRuntime {
	return &blockingHTTPRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (runtime *blockingHTTPRuntime) Start() error {
	close(runtime.started)
	<-runtime.stopped
	return nil
}

func (runtime *blockingHTTPRuntime) Shutdown(context.Context) error {
	runtime.once.Do(func() { close(runtime.stopped) })
	return nil
}

func (runtime *blockingHTTPRuntime) wasStopped() bool {
	select {
	case <-runtime.stopped:
		return true
	default:
		return false
	}
}
