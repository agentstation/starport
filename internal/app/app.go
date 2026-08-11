// Package app owns Starport production composition and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap"
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
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
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
	// ErrIdentityRequired reports empty gateway identity storage.
	ErrIdentityRequired = errors.New("gateway identity is required; run \"starport init\"")
)

type lifecycleEntry struct {
	name  string
	close func(context.Context) error
}

// App owns all constructed runtime dependencies.
type App struct {
	config              *config.Config
	providerSettings    config.ProvidersConfig
	httpServer          httpRuntime
	hotReloader         hotReloadRuntime
	registry            *registry.Registry
	catalogRuntime      catalogRuntime
	catalogUpdates      catalogUpdateRuntime
	catalog             *runtimecatalog.ControlPlane
	store               storage.KVStore
	cacheManager        *cache.Manager
	transports          *connectors.TransportRegistry
	authentication      *providerauth.Registry
	newConnector        func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error)
	allowEmptyProviders bool
	lifecycle           []lifecycleEntry
	catalogRefreshWG    sync.WaitGroup
	closeOnce           sync.Once
	closeErr            error
}

// New creates the complete production runtime without starting background work.
func New(cfg *config.Config, options ...Option) (*App, error) {
	build, err := prepareComposition(cfg, options)
	if err != nil {
		return nil, err
	}
	transportRegistry, err := connectors.ProductionTransportRegistry()
	if err != nil {
		return nil, fmt.Errorf("open transport registry: %w", err)
	}
	authenticationRegistry, err := providerauth.ProductionRegistry()
	if err != nil {
		return nil, fmt.Errorf("open provider authentication registry: %w", err)
	}
	application := &App{
		config: cfg, transports: transportRegistry, authentication: authenticationRegistry,
		providerSettings:    config.CloneProvidersConfig(cfg.Providers),
		newConnector:        build.factories.newConnector,
		allowEmptyProviders: build.allowEmptyProviders,
		lifecycle:           make([]lifecycleEntry, 0, 5),
	}
	builder := runtimeBuilder{application: application, config: cfg, factories: build.factories}
	if err := builder.compose(); err != nil {
		rollbackErr := application.closeLifecycle(context.Background())
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback application construction: %w", rollbackErr))
		}
		return nil, err
	}
	return application, nil
}

func prepareComposition(cfg *config.Config, options []Option) (buildOptions, error) {
	if cfg == nil {
		return buildOptions{}, ErrConfigRequired
	}
	if err := cfg.Validate(); err != nil {
		return buildOptions{}, fmt.Errorf("validate application config: %w", err)
	}
	if strings.TrimSpace(cfg.Security.MasterKey) == "" {
		return buildOptions{}, ErrCredentialsRequired
	}

	build := buildOptions{factories: defaultRuntimeFactories()}
	for _, option := range options {
		option(&build)
	}
	if err := validateFactories(build.factories); err != nil {
		return buildOptions{}, err
	}
	return build, nil
}

type runtimeBuilder struct {
	application  *App
	config       *config.Config
	factories    runtimeFactories
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
		b.config.Catalog,
	)
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	if catalogRuntime == nil || catalogRuntime.ControlPlane() == nil {
		return ErrCatalogRequired
	}
	b.application.catalogRuntime = catalogRuntime
	b.application.catalog = catalogRuntime.ControlPlane()
	if updates, ok := catalogRuntime.(catalogUpdateRuntime); ok {
		b.application.catalogUpdates = updates
		b.application.own("remote catalog", updates.Close)
	}
	if b.config.Catalog.RefreshOnStart {
		state, err := b.application.syncCatalog(context.Background())
		if err == nil {
			err = b.application.catalog.Activate(state)
		}
		if err != nil {
			log.Warn().Err(err).Msg("startup Starmap catalog refresh failed; retaining current generation")
		}
	}
	resolvedProviders, err := b.config.ResolveProviderSet(
		context.Background(),
		b.application.catalog.Current().Catalog().Providers(),
		b.application.providerSettings,
	)
	if err != nil {
		return fmt.Errorf("resolve provider configuration: %w", err)
	}
	b.config.Providers = resolvedProviders

	b.identities, err = identity.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open identity repository: %w", err)
	}
	if err := requireIdentity(context.Background(), b.identities); err != nil {
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
	credentialValidator, err := byok.NewCatalogCredentialValidator(
		func(providerID catalogs.ProviderID) (catalogs.Provider, bool) {
			var snapshot *runtimecatalog.RoutableSnapshot
			if b.application.registry != nil {
				snapshot = b.application.registry.Snapshot()
			} else {
				snapshot = b.application.catalog.Current()
			}
			if snapshot == nil {
				return catalogs.Provider{}, false
			}
			provider, lookupErr := snapshot.Catalog().Provider(providerID)
			return provider, lookupErr == nil
		},
	)
	if err != nil {
		return fmt.Errorf("open provider credential validator: %w", err)
	}
	b.providerKeys, err = byok.NewProviderKeys(credentialRepository, masterKey, credentialValidator)
	if err != nil {
		return fmt.Errorf("open provider key service: %w", err)
	}
	return nil
}

func (b *runtimeBuilder) openRegistry() error {
	providerConfigs := providers.Configurations(b.config.Providers)
	registrations, err := buildRegistrations(
		b.application.catalog.Current().Catalog(),
		b.application.transports,
		b.application.authentication,
		providerConfigs,
		b.factories.newConnector,
	)
	if err != nil {
		if b.application.allowEmptyProviders && errors.Is(err, ErrProvidersRequired) {
			b.application.registry = registry.NewEmptyWithCatalog(b.application.catalog)
			if err := b.application.catalog.ReplaceAdapters(nil); err != nil {
				return fmt.Errorf("open empty provider registry: %w", err)
			}
			b.application.own("registry", func(context.Context) error {
				return b.application.registry.Close()
			})
			return nil
		}
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
	modelRouter := router.New(
		registryAdapter,
		router.WithCatalog(b.application.catalog),
		router.WithUserCredentials(b.providerKeys),
	)
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

func requireIdentity(ctx context.Context, identities identity.Repository) error {
	records, err := identities.List(ctx, 1)
	if err != nil {
		return fmt.Errorf("list gateway identities: %w", err)
	}
	if len(records) == 0 {
		return ErrIdentityRequired
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
	if a.catalogUpdates != nil {
		if err := a.catalogUpdates.Start(runCtx); err != nil {
			return errors.Join(
				fmt.Errorf("start remote catalog: %w", err),
				a.closeWithTimeout(),
			)
		}
		if err := a.activateRuntimeState(
			runCtx,
			a.catalogUpdates.CurrentCandidate(),
		); err != nil {
			log.Warn().Err(err).Msg(
				"remote catalog candidate failed; serving the current complete runtime",
			)
		}
		a.catalogRefreshWG.Add(1)
		go func() {
			defer a.catalogRefreshWG.Done()
			a.remoteCatalogLoop(runCtx)
		}()
	}
	if a.hotReloader != nil {
		if err := a.hotReloader.Start(runCtx); err != nil {
			return errors.Join(fmt.Errorf("start hot reload: %w", err), a.closeWithTimeout())
		}
	}
	if a.catalogUpdates == nil && a.config.Catalog.RefreshInterval > 0 {
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

func defaultRuntimeFactories() runtimeFactories {
	return runtimeFactories{
		openStorage: openStorage,
		openCatalog: func(
			ctx context.Context,
			store storage.KVStore,
			catalogConfig config.CatalogConfig,
		) (catalogRuntime, error) {
			if strings.TrimSpace(catalogConfig.RemoteURL) != "" {
				runtime, err := runtimecatalog.OpenRemoteRuntime(
					ctx,
					store,
					runtimecatalog.RemoteConfig{
						BaseURL:            catalogConfig.RemoteURL,
						APIKey:             catalogConfig.RemoteAPIKey,
						ActivationInterval: catalogConfig.RemoteActivationInterval,
						FetchTimeout:       catalogConfig.RefreshTimeout,
					},
				)
				if err != nil {
					return nil, err
				}
				return runtime, nil
			}
			runtime, err := runtimecatalog.OpenRuntime(
				ctx,
				store,
				catalogConfig.WorkspacePath,
			)
			if err != nil {
				return nil, err
			}
			return runtime, nil
		},
		newConnector: func(
			provider string,
			endpointTypes []catalogs.EndpointType,
			config connectors.ProviderConfig,
		) (connectors.Connector, error) {
			transportRegistry, err := connectors.ProductionTransportRegistry()
			if err != nil {
				return nil, err
			}
			return transportRegistry.NewProviderConnector(
				catalogs.ProviderID(provider),
				endpointTypes,
				config,
			)
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

func (a *App) syncCatalog(ctx context.Context) (starmap.CatalogState, error) {
	if a == nil || a.catalogRuntime == nil {
		return starmap.CatalogState{}, ErrCatalogRequired
	}
	timeout := a.config.Catalog.RefreshTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, state, err := a.catalogRuntime.Sync(
		refreshCtx,
		pkgsync.WithSources(sources.ProvidersID, sources.LocalCatalogID),
		pkgsync.WithTimeout(timeout),
	)
	return state, err
}

func (a *App) refreshRuntime(ctx context.Context) error {
	state, err := a.syncCatalog(ctx)
	if err != nil {
		return err
	}
	return a.activateRuntimeState(ctx, state)
}

func (a *App) activateRuntimeState(ctx context.Context, state starmap.CatalogState) error {
	if state.Catalog == nil || strings.TrimSpace(state.GenerationID) == "" {
		return ErrCatalogRequired
	}
	current := a.catalog.Current()
	if current != nil &&
		current.GenerationID() == state.GenerationID &&
		current.PayloadChecksum() == state.PayloadChecksum {
		return nil
	}
	resolved, err := a.config.ResolveProviderSet(
		ctx,
		state.Catalog.Providers(),
		a.providerSettings,
	)
	if err != nil {
		return fmt.Errorf("resolve provider runtime configuration: %w", err)
	}
	registrations, err := buildRegistrations(
		state.Catalog,
		a.transports,
		a.authentication,
		providers.Configurations(resolved),
		a.newConnector,
	)
	if err != nil {
		return err
	}
	candidate, err := a.registry.Prepare(registrations)
	if err != nil {
		return err
	}
	availability := candidate.Availability()
	if err := a.catalog.ValidateRuntime(state, availability); err != nil {
		return errors.Join(err, candidate.Close())
	}
	if a.catalogUpdates != nil {
		if err := a.catalogUpdates.Accept(ctx, state); err != nil {
			return errors.Join(
				fmt.Errorf("record accepted remote catalog generation: %w", err),
				candidate.Close(),
			)
		}
	}
	snapshot, err := a.catalog.ReplaceRuntime(state, availability)
	if err != nil {
		return errors.Join(err, candidate.Close())
	}
	if err := a.registry.Publish(candidate, snapshot); err != nil {
		return errors.Join(err, candidate.Close())
	}
	return nil
}

func (a *App) refreshCatalogLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.Catalog.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.refreshRuntime(ctx); err != nil {
				log.Warn().Err(err).Msg("provider runtime refresh failed; retaining current generation")
			}
		}
	}
}

func (a *App) remoteCatalogLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case state, ok := <-a.catalogUpdates.Updates():
			if !ok {
				return
			}
			if err := a.activateRuntimeState(ctx, state); err != nil {
				log.Warn().Err(err).Msg(
					"remote catalog candidate failed; serving the current complete runtime",
				)
			}
		}
	}
}

func validateFactories(factories runtimeFactories) error {
	if factories.openStorage == nil || factories.openCatalog == nil || factories.newConnector == nil ||
		factories.newCache == nil || factories.newHotReload == nil || factories.newServer == nil {
		return errors.New("application runtime factories are incomplete")
	}
	return nil
}

func openStorage(cfg config.StorageConfig) (storage.KVStore, error) {
	return storage.Open(cfg.RuntimeStorage())
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

var _ connectors.LeasingRegistry = connectorRegistryAdapter{}

func (a connectorRegistryAdapter) AcquireRuntime() (connectors.RuntimeLease, error) {
	return a.registry.AcquireRuntime()
}

func (a connectorRegistryAdapter) Get(provider string) connectors.Connector {
	connector, _ := a.registry.Get(provider)
	return connector
}

func (a connectorRegistryAdapter) List() []string { return a.registry.ListProviders() }

func (a connectorRegistryAdapter) ResolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	return a.registry.ResolveMaterial(ctx, provider)
}
