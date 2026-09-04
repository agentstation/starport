package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
)

func TestRuntimeRequiresNamedAPIKey(t *testing.T) {
	apiKeys, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)
	require.ErrorIs(t, requireAPIKey(context.Background(), apiKeys, ""), ErrAPIKeyRequired)
	require.ErrorIs(t,
		requireAPIKey(context.Background(), apiKeys, config.AuthModeRequired),
		ErrAPIKeyRequired)

	// An empty API key store is the expected state of a gateway that requires
	// no key, and refusing to start there would make the mode unusable for the
	// operator it exists for.
	require.NoError(t, requireAPIKey(context.Background(), apiKeys, config.AuthModeDisabled))

	_, err = apiKeys.Create(context.Background(), testAPIKey())
	require.NoError(t, err)
	require.NoError(t, requireAPIKey(context.Background(), apiKeys, config.AuthModeRequired))
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
					runtimecatalog.Settings,
					runtimecatalog.DeploymentLookup,
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
			name: "missing API key",
			mutate: func(_ *config.Config, factories *runtimeFactories) {
				factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) {
					return storage.NewMockStore(), nil
				}
			},
			cause: ErrAPIKeyRequired,
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

func TestRuntimeStartsWithoutOperatorCredentials(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Providers = config.ProvidersConfig{}
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotEmpty(t, application.registry.ListProviders())
	_, err = application.registry.ResolveMaterial(t.Context(), string(catalogs.ProviderIDOpenAI))
	require.ErrorIs(t, err, credentials.ErrProviderNotConfigured)
}

// TestCompositionPassesCatalogAcquisitionThrough proves the composition root
// changes no catalog setting. A gateway that reads no catalog routes nothing,
// so only the operator turns automatic acquisition off.
func TestCompositionPassesCatalogAcquisitionThrough(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Catalog.WorkspacePath = t.TempDir()
	// The operator enabled acquisition. The composition passes that setting
	// through unchanged, so no runtime mode turns automatic catalog work off.
	cfg.Catalog.AcquisitionEnabled = true
	opened := 0
	factories := explicitTestFactories()
	inner := factories.openCatalog
	factories.openCatalog = func(
		ctx context.Context,
		store storage.KVStore,
		settings runtimecatalog.Settings,
		lookup runtimecatalog.DeploymentLookup,
	) (catalogRuntime, error) {
		opened++
		require.True(t, settings.AcquisitionEnabled)
		return inner(ctx, store, settings, lookup)
	}

	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	require.Equal(t, 1, opened)
	require.NoError(t, application.Close(context.Background()))
}

func TestDefaultFactoryErrorsReturnNilInterfaces(t *testing.T) {
	factories := defaultRuntimeFactories()

	httpServer, err := factories.newServer(nil, server.Dependencies{})
	require.Error(t, err)
	require.Nil(t, httpServer)

	storagePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(storagePath, []byte("occupied"), 0o600))
	store, err := openStorage(config.StorageConfig{
		Mode: "badger", Badger: config.BadgerConfig{Path: storagePath, Compression: "snappy"},
	})
	require.Error(t, err)
	require.Nil(t, store)
}

// TestDefaultCatalogFactoryComposesOneConnectedRuntime proves the composition
// root makes no local-or-remote choice. A source address and no source address
// both reach the same connected runtime type.
func TestDefaultCatalogFactoryComposesOneConnectedRuntime(t *testing.T) {
	factories := defaultRuntimeFactories()
	tests := []struct {
		name     string
		settings runtimecatalog.Settings
	}{
		{name: "no source address", settings: testCatalogSettings(t)},
		{
			name: "deployment source address",
			settings: func() runtimecatalog.Settings {
				settings := testCatalogSettings(t)
				settings.WorkspacePath = t.TempDir()
				return settings
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := factories.openCatalog(
				t.Context(),
				storage.NewMockStore(),
				test.settings,
				func(string) (string, bool) { return "", false },
			)
			require.NoError(t, err)
			connected, ok := runtime.(*runtimecatalog.Runtime)
			require.True(t, ok)
			require.NoError(t, connected.Close(t.Context()))
		})
	}
}

func TestServerConfigCredentialsFollowOriginScope(t *testing.T) {
	cfg := validProductionConfig(t)
	require.False(t, serverConfig(cfg, authRuntime{}).CORS.AllowCredentials)

	cfg.Security.AllowedOrigins = "https://console.example.com"
	require.True(t, serverConfig(cfg, authRuntime{}).CORS.AllowCredentials)

	cfg.Security.EnableCORS = false
	require.False(t, serverConfig(cfg, authRuntime{}).CORS.AllowCredentials)
}

func TestNewBuildsReadyProductionDependencies(t *testing.T) {
	cfg := validProductionConfig(t)
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	require.NotNil(t, application.store)
	require.NotNil(t, application.catalog)
	require.NotNil(t, application.registry)
	require.NotNil(t, application.httpServer)
	require.Contains(t, application.registry.ListProviders(), "openai")
	require.Greater(t, len(application.registry.ListProviders()), 1)
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
			SQL: config.SQLConfig{Mode: "sqlite"},
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
			DefaultRequestsPerMinute: 60, WindowSize: time.Minute,
		},
		Security: config.SecurityConfig{
			MasterKey:      strings.Repeat("k", 32),
			AllowedOrigins: "*", EnableCORS: true,
			LocalTokenPath: filepath.Join(t.TempDir(), "local-admin-token.json"),
		},
		Logging: config.LoggingConfig{
			Level: "info", Format: "json", Output: "stdout",
			MaxSize: 100, MaxBackups: 3, MaxAge: 7,
		},
		Catalog: testCatalogConfig(),
		Cache:   config.CacheConfig{Enabled: false},
		Console: config.ConsoleConfig{},
		// The loader always resolves a path for the filesystem backend, so a
		// production configuration always carries one.
		Files: config.FilesConfig{Path: t.TempDir()},
	}
}

// testCatalogConfig reads the embedded catalog, so a composition test reaches
// no network address for its catalog source.
func testCatalogConfig() config.CatalogConfig {
	settings := config.DefaultCatalogConfig()
	settings.Source = config.CatalogSourceEmbedded
	settings.AcquisitionEnabled = false
	return settings
}

// testCatalogSettings returns the catalog settings a test opens a runtime
// with. The state directory is a fresh directory per test, so no test shares
// an instance identity with another.
func testCatalogSettings(t *testing.T) runtimecatalog.Settings {
	t.Helper()
	deployment := &config.Config{Catalog: testCatalogConfig()}
	deployment.Server.Host = "127.0.0.1"
	deployment.Server.Port = 8080
	deployment.Catalog.StateDirectory = t.TempDir()
	return catalogSettings(deployment)
}

func explicitTestFactories() runtimeFactories {
	factories := defaultRuntimeFactories()
	store := storage.NewMockStore()
	apiKeys, _ := apikey.Open(store)
	_, _ = apiKeys.Create(context.Background(), testAPIKey())
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

func testAPIKey() apikey.APIKey {
	return apikey.APIKey{
		ID: "STARPORT_TEST", Name: "test-admin", Hash: "test-hash",
		Scopes: []string{"*"}, Active: true, CreatedAt: time.Now().UTC(),
		Metadata: map[string]any{"source": "test"},
	}
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
