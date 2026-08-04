// Package app owns Starport production composition and lifecycle.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/chatui"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrConfigRequired reports an absent application configuration.
	ErrConfigRequired = errors.New("application config is required")
	// ErrStorageRequired reports a storage factory that returned no adapter.
	ErrStorageRequired = errors.New("storage factory returned no storage")
	// ErrCatalogRequired reports a catalog factory that returned no control plane.
	ErrCatalogRequired = errors.New("catalog factory returned no catalog")
	// ErrCredentialsRequired reports an absent provider-credential master key.
	ErrCredentialsRequired = errors.New("provider credential master key is required")
	// ErrBootstrapRequired reports empty identity storage without a bootstrap key.
	ErrBootstrapRequired = errors.New("bootstrap API key is required when identity storage is empty")
)

type lifecycleEntry struct {
	name  string
	close func(context.Context) error
}

// App owns all constructed runtime dependencies.
type App struct {
	config           *config.Config
	httpServer       httpRuntime
	hotReloader      hotReloadRuntime
	registry         *registry.Registry
	catalogRuntime   catalogRuntime
	catalog          *runtimecatalog.ControlPlane
	store            storage.KVStore
	cacheManager     *cache.Manager
	adapters         *connectors.AdapterRegistry
	lifecycle        []lifecycleEntry
	catalogRefreshWG sync.WaitGroup
	closeOnce        sync.Once
	closeErr         error
}

// New creates the complete production runtime without starting background work.
func New(cfg *config.Config, options ...Option) (*App, error) {
	factories, err := prepareComposition(cfg, options)
	if err != nil {
		return nil, err
	}
	adapterRegistry, err := connectors.ProductionAdapterRegistry()
	if err != nil {
		return nil, fmt.Errorf("open adapter registry: %w", err)
	}
	application := &App{
		config: cfg, adapters: adapterRegistry, lifecycle: make([]lifecycleEntry, 0, 5),
	}
	builder := runtimeBuilder{application: application, config: cfg, factories: factories}
	if err := builder.compose(); err != nil {
		rollbackErr := application.closeLifecycle(context.Background())
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback application construction: %w", rollbackErr))
		}
		return nil, err
	}
	return application, nil
}

func prepareComposition(cfg *config.Config, options []Option) (bootstrapFactories, error) {
	if cfg == nil {
		return bootstrapFactories{}, ErrConfigRequired
	}
	if err := cfg.Validate(); err != nil {
		return bootstrapFactories{}, fmt.Errorf("validate application config: %w", err)
	}
	if strings.TrimSpace(cfg.Security.MasterKey) == "" {
		return bootstrapFactories{}, ErrCredentialsRequired
	}

	build := buildOptions{factories: defaultBootstrapFactories()}
	for _, option := range options {
		option(&build)
	}
	if err := validateFactories(build.factories); err != nil {
		return bootstrapFactories{}, err
	}
	return build.factories, nil
}

type runtimeBuilder struct {
	application  *App
	config       *config.Config
	factories    bootstrapFactories
	identities   identity.Repository
	providerKeys byok.ProviderKeys
	rateLimits   ratelimit.Repository
	gateway      proxy.Proxy
	chatUI       *chatui.Handler
}

func (b *runtimeBuilder) compose() error {
	steps := []func() error{
		b.openStorage,
		b.openConcepts,
		b.openRegistry,
		b.openCache,
		b.buildGateway,
		b.openChatUI,
		b.openHotReload,
		b.openHTTPServer,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (b *runtimeBuilder) openStorage() error {
	store, err := b.factories.openStorage(b.config.Storage)
	if err != nil {
		if store != nil {
			if closeErr := store.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close failed storage: %w", closeErr))
			}
		}
		return fmt.Errorf("open storage: %w", err)
	}
	if store == nil {
		return ErrStorageRequired
	}
	b.application.store = store
	b.application.own("storage", func(context.Context) error { return store.Close() })
	return nil
}

func (b *runtimeBuilder) openConcepts() error {
	catalogRuntime, err := b.factories.openCatalog(
		context.Background(),
		b.application.store,
		b.config.Catalog.WorkspacePath,
	)
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	if catalogRuntime == nil || catalogRuntime.ControlPlane() == nil {
		return ErrCatalogRequired
	}
	b.application.catalogRuntime = catalogRuntime
	b.application.catalog = catalogRuntime.ControlPlane()
	if b.config.Catalog.RefreshOnStart {
		if err := b.application.refreshCatalog(context.Background()); err != nil {
			log.Warn().Err(err).Msg("startup Starmap catalog refresh failed; retaining current generation")
		}
	}

	b.identities, err = identity.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open identity repository: %w", err)
	}
	if err := ensureBootstrapIdentity(context.Background(), b.identities, b.config.Security.BootstrapAPIKey); err != nil {
		return err
	}
	credentialRepository, err := credentials.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open provider credential repository: %w", err)
	}
	b.rateLimits, err = ratelimit.Open(b.application.store, nil)
	if err != nil {
		return fmt.Errorf("open rate-limit repository: %w", err)
	}
	masterKey := []byte(b.config.Security.MasterKey)
	if len(masterKey) < 32 {
		masterKey = credentials.DeriveKeyFromPassword(b.config.Security.MasterKey)
	}
	b.providerKeys, err = byok.NewProviderKeys(credentialRepository, masterKey, b.application.adapters)
	if err != nil {
		return fmt.Errorf("open provider key service: %w", err)
	}
	return nil
}

func (b *runtimeBuilder) openRegistry() error {
	registrations, err := buildRegistrations(
		b.application.catalog,
		b.application.adapters,
		providerConfigurations(b.config.Providers),
		b.factories.newConnector,
	)
	if err != nil {
		return err
	}
	b.application.registry, err = registry.Open(b.application.catalog, registrations)
	if err != nil {
		return fmt.Errorf("open provider registry: %w", err)
	}
	b.application.own("registry", func(context.Context) error { return b.application.registry.Close() })
	return nil
}

func (b *runtimeBuilder) openCache() error {
	if b.config.Cache.Enabled {
		cacheManager, err := b.factories.newCache(cache.ManagerConfig{}, b.application.store)
		if err != nil {
			if cacheManager != nil {
				if closeErr := cacheManager.Close(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("close failed cache manager: %w", closeErr))
				}
			}
			return fmt.Errorf("open cache manager: %w", err)
		}
		if cacheManager == nil {
			return errors.New("cache factory returned no cache manager")
		}
		b.application.cacheManager = cacheManager
		b.application.own("cache", func(context.Context) error { return cacheManager.Close() })
	}
	return nil
}

func (b *runtimeBuilder) buildGateway() error {
	registryAdapter := connectorRegistryAdapter{registry: b.application.registry}
	modelRouter := router.New(registryAdapter, router.WithCatalog(b.application.catalog))
	proxyOptions := make([]proxy.Option, 0, 1)
	if b.application.cacheManager != nil {
		proxyOptions = append(proxyOptions, proxy.WithCache(b.application.cacheManager, &proxy.CacheConfig{
			EnableChatCache: true, EnableEmbeddingCache: true,
			EnableModelCache: true, EnableProviderCache: true,
			CacheControlHeader: "X-Cache-Control",
		}))
	}
	b.gateway = proxy.New(b.application.registry, modelRouter, proxyOptions...)
	return nil
}

func (b *runtimeBuilder) openChatUI() error {
	if b.config.ChatUI.Enabled {
		var err error
		b.chatUI, err = chatui.NewHandler(&log.Logger, chatui.Config{
			Title: b.config.ChatUI.Title, Theme: b.config.ChatUI.Theme,
			APIBaseURL: fmt.Sprintf("http://localhost:%d", b.config.Server.Port),
		})
		if err != nil {
			return fmt.Errorf("open ChatUI: %w", err)
		}
	}
	return nil
}

func (b *runtimeBuilder) openHotReload() error {
	if b.config.RateLimiting.EnableHotReload {
		var err error
		b.application.hotReloader, err = b.factories.newHotReload(
			b.config.RateLimiting.ConfigPath, b.config.RateLimiting.ReloadCheckInterval,
		)
		if err != nil {
			return fmt.Errorf("open rate-limit hot reload: %w", err)
		}
		if b.application.hotReloader == nil {
			return errors.New("hot-reload factory returned no runtime")
		}
		b.application.own("hot reload", func(context.Context) error {
			b.application.hotReloader.Stop()
			return nil
		})
	}
	return nil
}

func (b *runtimeBuilder) openHTTPServer() error {
	httpServer, err := b.factories.newServer(serverConfig(b.config), server.Dependencies{
		Service: b.gateway, Identities: b.identities, ProviderKeys: b.providerKeys,
		RateLimits: b.rateLimits, ChatUI: b.chatUI,
	})
	if err != nil {
		if httpServer != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), b.config.Server.ShutdownTimeout)
			shutdownErr := httpServer.Shutdown(shutdownCtx)
			cancel()
			if shutdownErr != nil {
				err = errors.Join(err, fmt.Errorf("close failed HTTP server: %w", shutdownErr))
			}
		}
		return fmt.Errorf("open HTTP server: %w", err)
	}
	if httpServer == nil {
		return errors.New("server factory returned no HTTP server")
	}
	b.application.httpServer = httpServer
	b.application.own("HTTP server", httpServer.Shutdown)
	return nil
}

func ensureBootstrapIdentity(ctx context.Context, identities identity.Repository, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		records, err := identities.List(ctx, 1)
		if err != nil {
			return fmt.Errorf("list bootstrap identities: %w", err)
		}
		if len(records) == 0 {
			return ErrBootstrapRequired
		}
		return nil
	}

	hash := sha256.Sum256([]byte(apiKey))
	hashValue := hex.EncodeToString(hash[:])
	if _, err := identities.GetByHash(ctx, hashValue); err == nil {
		return nil
	} else if !errors.Is(err, identity.ErrNotFound) {
		return fmt.Errorf("read bootstrap identity: %w", err)
	}

	_, err := identities.Create(ctx, identity.APIKey{
		ID:        "bootstrap-" + hashValue[:16],
		Name:      "bootstrap_admin",
		Hash:      hashValue,
		Scopes:    []string{"*"},
		Active:    true,
		CreatedAt: time.Now().UTC(),
		Metadata:  map[string]any{"source": "bootstrap"},
	})
	if err != nil {
		return fmt.Errorf("create bootstrap identity: %w", err)
	}
	return nil
}

// Run starts explicit runtime work and closes all dependencies on exit.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application run context is required")
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if err := a.registry.Start(runCtx); err != nil {
		return errors.Join(fmt.Errorf("start registry: %w", err), a.closeWithTimeout())
	}
	if a.hotReloader != nil {
		if err := a.hotReloader.Start(runCtx); err != nil {
			return errors.Join(fmt.Errorf("start hot reload: %w", err), a.closeWithTimeout())
		}
	}
	if a.config.Catalog.RefreshInterval > 0 {
		a.catalogRefreshWG.Add(1)
		go func() {
			defer a.catalogRefreshWG.Done()
			a.refreshCatalogLoop(runCtx)
		}()
	}

	serverResult := make(chan error, 1)
	go func() { serverResult <- a.httpServer.Start() }()

	var runErr error
	select {
	case <-runCtx.Done():
	case err := <-serverResult:
		if err == nil {
			runErr = errors.New("HTTP server stopped before application cancellation")
		} else {
			runErr = fmt.Errorf("HTTP server: %w", err)
		}
	}
	cancelRun()
	a.catalogRefreshWG.Wait()
	return errors.Join(runErr, a.closeWithTimeout())
}

// Close stops owned dependencies in reverse construction order.
func (a *App) Close(ctx context.Context) error {
	a.closeOnce.Do(func() { a.closeErr = a.closeLifecycle(ctx) })
	return a.closeErr
}

func (a *App) own(name string, closeResource func(context.Context) error) {
	a.lifecycle = append(a.lifecycle, lifecycleEntry{name: name, close: closeResource})
}

func (a *App) closeLifecycle(ctx context.Context) error {
	var closeErrors []error
	for index := len(a.lifecycle) - 1; index >= 0; index-- {
		entry := a.lifecycle[index]
		if err := entry.close(ctx); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close %s: %w", entry.name, err))
		}
	}
	a.lifecycle = nil
	return errors.Join(closeErrors...)
}

func (a *App) closeWithTimeout() error {
	timeout := a.config.Server.ShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.Close(ctx)
}

func defaultBootstrapFactories() bootstrapFactories {
	return bootstrapFactories{
		openStorage: openStorage,
		openCatalog: func(
			ctx context.Context,
			store storage.KVStore,
			workspacePath string,
		) (catalogRuntime, error) {
			runtime, err := runtimecatalog.OpenRuntime(ctx, store, workspacePath)
			if err != nil {
				return nil, err
			}
			return runtime, nil
		},
		newConnector: func(provider string, config connectors.ProviderConfig) (connectors.Connector, error) {
			adapterRegistry, err := connectors.ProductionAdapterRegistry()
			if err != nil {
				return nil, err
			}
			return adapterRegistry.NewConnector(catalogs.ProviderID(provider), config)
		},
		newCache: cache.NewCacheManager,
		newHotReload: func(path string, interval time.Duration) (hotReloadRuntime, error) {
			reloader, err := config.NewHotReloader(path, interval)
			if err != nil {
				return nil, err
			}
			return reloader, nil
		},
		newServer: func(cfg *server.Config, dependencies server.Dependencies) (httpRuntime, error) {
			httpServer, err := server.New(cfg, dependencies)
			if err != nil {
				return nil, err
			}
			return httpServer, nil
		},
	}
}

func (a *App) refreshCatalog(ctx context.Context) error {
	if a == nil || a.catalogRuntime == nil {
		return ErrCatalogRequired
	}
	timeout := a.config.Catalog.RefreshTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := a.catalogRuntime.Refresh(
		refreshCtx,
		pkgsync.WithSources(sources.ProvidersID, sources.LocalCatalogID),
		pkgsync.WithTimeout(timeout),
	)
	return err
}

func (a *App) refreshCatalogLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.Catalog.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.refreshCatalog(ctx); err != nil {
				log.Warn().Err(err).Msg("Starmap catalog refresh failed; retaining current generation")
			}
		}
	}
}

func validateFactories(factories bootstrapFactories) error {
	if factories.openStorage == nil || factories.openCatalog == nil || factories.newConnector == nil ||
		factories.newCache == nil || factories.newHotReload == nil || factories.newServer == nil {
		return errors.New("application bootstrap factories are incomplete")
	}
	return nil
}

func openStorage(cfg config.StorageConfig) (storage.KVStore, error) {
	switch cfg.Mode {
	case "badger":
		store, err := storage.OpenBadger(storage.BadgerConfig{
			Path: cfg.Badger.Path, SyncWrites: cfg.Badger.SyncWrites,
			Compression: cfg.Badger.Compression != "none", NumVersions: 1,
			NumLevelZero: 5, MemTableSize: 64 << 20,
		})
		if err != nil {
			return nil, err
		}
		return store, nil
	case "valkey":
		return storage.OpenValkey(storage.ValkeyConfig{
			URL: cfg.Valkey.URL, Password: cfg.Valkey.Password,
			MaxRetries: 3, MinIdleConns: cfg.Valkey.MinIdleConns,
			ReadTimeout: cfg.Valkey.ReadTimeout, WriteTimeout: cfg.Valkey.WriteTimeout,
			ClusterMode: cfg.Valkey.ClusterMode,
		})
	default:
		return nil, fmt.Errorf("unknown storage mode %q", cfg.Mode)
	}
}

func serverConfig(cfg *config.Config) *server.Config {
	allowedOrigins := splitCommaSeparated(cfg.Security.AllowedOrigins)
	if !cfg.Security.EnableCORS {
		allowedOrigins = nil
	}
	allowCredentials := len(allowedOrigins) > 0 && !containsString(allowedOrigins, "*")
	requestTimeout := cfg.Server.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = 60 * time.Second
	}
	maxRequestSize := cfg.Server.MaxRequestSize
	if maxRequestSize == 0 {
		maxRequestSize = 10 * 1024 * 1024
	}
	return &server.Config{
		Port: cfg.Server.Port, Host: cfg.Server.Host,
		ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout: cfg.Server.IdleTimeout, RequestTimeout: requestTimeout,
		ShutdownTimeout: cfg.Server.ShutdownTimeout, MaxRequestSize: maxRequestSize,
		MaxHeaderBytes:             cfg.Server.MaxHeaderBytes,
		EnableRateLimiting:         cfg.Security.EnableRateLimiting,
		RateLimitRequestsPerWindow: int64(cfg.RateLimiting.DefaultRequestsPerMinute),
		RateLimitWindow:            cfg.RateLimiting.WindowSize,
		CORS: server.CORSConfig{
			AllowedOrigins:   allowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-API-Key"},
			AllowCredentials: allowCredentials, MaxAge: 300,
		},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

type connectorRegistryAdapter struct{ registry *registry.Registry }

func (a connectorRegistryAdapter) Get(provider string) connectors.Connector {
	connector, _ := a.registry.Get(provider)
	return connector
}

func (a connectorRegistryAdapter) List() []string { return a.registry.ListProviders() }
