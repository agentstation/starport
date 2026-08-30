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
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/availability"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/telemetry"
	"github.com/agentstation/starport/internal/tokenize"
	"github.com/agentstation/starport/internal/usage"
)

var (
	// ErrConfigRequired reports an absent application configuration.
	ErrConfigRequired = errors.New("application config is required")
	// ErrStorageRequired reports a storage factory that returned no adapter.
	ErrStorageRequired = errors.New("storage factory returned no storage")

	// ErrBlobStorageRequired reports a file storage factory that returned no
	// store.
	ErrBlobStorageRequired = errors.New("file storage factory returned no store")
	// ErrCatalogRequired reports a catalog factory that returned no control plane.
	ErrCatalogRequired = errors.New("catalog factory returned no catalog")
	// ErrCredentialsRequired reports an absent provider-credential master key.
	ErrCredentialsRequired = errors.New("provider credential master key is required")
	// ErrAPIKeyRequired reports empty gateway API key storage.
	ErrAPIKeyRequired = errors.New("a gateway API key is required; run \"starport init\"")
	// ErrLocalTokenPathRequired reports a configuration with nowhere to keep this
	// machine's local admin token. The loader derives the path from the platform
	// paths, so an empty value means the configuration did not come from the
	// loader, and guessing a path here would put a credential somewhere the CLI
	// never looks.
	ErrLocalTokenPathRequired = errors.New("a local admin token path is required")
	// ErrLocalTokenExposed reports a gateway that would serve a never-rotated
	// local admin token on an address the network can reach.
	ErrLocalTokenExposed = errors.New("the local admin token has never been rotated")
	// ErrProviderCatalogChanged reports a credential result that was resolved
	// from a catalog generation that is no longer current.
	ErrProviderCatalogChanged = errors.New("provider catalog changed during reconciliation")
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
	registry            *registry.Registry
	catalogRuntime      catalogRuntime
	catalogUpdates      catalogUpdateRuntime
	catalog             *runtimecatalog.ControlPlane
	catalogFreshness    *runtimecatalog.FreshnessService
	store               storage.KVStore
	blobStore           blob.Store
	files               *files.Service
	jobs                *jobs.Service
	cacheManager        *cache.Manager
	transports          *connectors.TransportRegistry
	authentication      *providerauth.Registry
	providerReconciler  *providers.Reconciler
	providerStates      *providerstate.Store
	incidentHistory     *statuspage.HistoryReader
	incidentTransitions providerstate.TransitionRepository
	availability        *availability.Tracker
	// localGate mints and redeems console launch tickets against this
	// machine's local admin token. The runtime keeps it so a caller that owns
	// the process — `starport dev` — can sign a browser in without reading the
	// token file a second time and without the secret leaving this package.
	localGate    *localauth.Gate
	newConnector func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error)
	lifecycle    []lifecycleEntry
	runtimeWG    sync.WaitGroup
	runtimeMu    sync.Mutex
	closeOnce    sync.Once
	closeErr     error
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
		providerSettings: config.CloneProvidersConfig(cfg.Providers),
		newConnector:     build.factories.newConnector,
		lifecycle:        make([]lifecycleEntry, 0, 5),
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
	apiKeys      apikey.Repository
	accounts     account.Repository
	providerKeys keyring.ProviderKeys
	rateLimits   ratelimit.Repository
	usageRecords usage.Repository
	presets      presets.Repository
	sqlDB        *sqlstore.DB
	templates    account.TemplateRepository
	files        *files.Service
	jobs         *jobs.Service
	gateway      proxy.Proxy
	console      console.PageServer
	metrics      *telemetry.Metrics
	tracing      *telemetry.Tracing
	auth         authRuntime
	gate         *localauth.Gate
	identityAuth *identity.Authenticator
	// identityRepos is the durable people plane openIdentity opened, or zero
	// when this deployment configured no identity. The HTTP server receives
	// it either way and degrades the members surface to 503 when zero.
	identityRepos identity.Repositories
}

// authRuntime is the resolved authentication mode and the store that keeps a
// console change. The two travel together because a mode nobody can persist is
// a mode the console must not offer to change: accepting a switch that a
// restart silently undoes is worse than refusing it.
type authRuntime struct {
	setting authmode.Setting
	store   authmode.Repository
}

func (b *runtimeBuilder) compose() error {
	steps := []func() error{
		b.openStorage,
		b.openSQLStore,
		b.openBlob,
		b.openConcepts,
		b.openRegistry,
		b.openCache,
		b.buildGateway,
		b.openConsole,
		b.openIdentity,
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

// openSQLStore opens the relational store beside the key-value one and
// brings its schema current, so every repository that rides it reads a
// migrated database. The repositories themselves open in openConcepts with
// the rest.
func (b *runtimeBuilder) openSQLStore() error {
	db, err := b.factories.openSQL(b.config.Storage)
	if err != nil {
		return fmt.Errorf("open relational storage: %w", err)
	}
	if db == nil {
		return errors.New("relational storage is required")
	}
	b.sqlDB = db
	b.application.own("relational storage", func(context.Context) error { return db.Close() })
	migrateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(migrateCtx); err != nil {
		return fmt.Errorf("migrate relational storage: %w", err)
	}
	log.Info().Str("dialect", db.Dialect()).Msg("relational storage ready")
	return nil
}

// openBlob opens the store that holds file bytes and reports which one it is.
//
// The report prints once at startup rather than per upload. An operator needs
// to know where the bytes land before the first request, because a deployment
// that meant to share a bucket and got a per-node directory looks healthy until
// a second node answers not-found.
func (b *runtimeBuilder) openBlob() error {
	store, err := b.factories.openBlob(context.Background(), b.config.Files)
	if err != nil {
		return fmt.Errorf("open file storage: %w", err)
	}
	if store == nil {
		return ErrBlobStorageRequired
	}
	b.application.blobStore = store
	event := log.Info().Str("backend", store.Backend())
	if b.config.Files.SelectedBackend() == config.BlobBackendFilesystem {
		event = event.Str("path", b.config.Files.Path)
	} else {
		// The bucket and the prefix identify the destination. Neither key ever
		// reaches a log line.
		event = event.Str("bucket", b.config.Files.ObjectStore.Bucket).
			Str("prefix", b.config.Files.ObjectStore.Prefix)
	}
	event.Msg("file byte storage ready")
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
	generations, err := runtimecatalog.NewGenerationStore(b.application.store)
	if err != nil {
		return fmt.Errorf("open catalog generation store: %w", err)
	}
	b.application.catalogFreshness = runtimecatalog.NewFreshnessService(b.application.catalog, generations)
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
	b.application.providerStates = providerstate.New()
	incidentTransitions, err := providerstate.OpenTransitions(b.sqlDB)
	if err != nil {
		return fmt.Errorf("open incident transition storage: %w", err)
	}
	b.application.incidentTransitions = incidentTransitions
	incidentHistory, err := statuspage.NewHistoryReader(
		statuspage.DefaultConfig(),
		catalogHealthAPISource{catalog: b.application.catalog},
	)
	if err != nil {
		return fmt.Errorf("open incident history reader: %w", err)
	}
	b.application.incidentHistory = incidentHistory
	if err := b.application.publishProviderCatalogState(); err != nil {
		return fmt.Errorf("project provider catalog state: %w", err)
	}
	providerReconciler, err := providers.NewReconciler(
		b.application.currentProviderCatalogView,
		b.config,
		b.application.providerSettings,
		b.application.publishProviderRuntime,
		b.config.CredentialSources.ReconcileTimeout,
		b.application.providerStates,
	)
	if err != nil {
		return fmt.Errorf("open provider reconciler: %w", err)
	}
	b.application.providerReconciler = providerReconciler
	startupProviders, startupFailures, err := b.config.ResolveProviderSetLocalIsolated(
		context.Background(),
		b.application.catalog.Current().Catalog().Providers(),
		b.application.providerSettings,
	)
	if err != nil {
		return fmt.Errorf("reconcile startup provider configuration: %w", err)
	}
	b.config.Providers = startupProviders
	startupView, err := b.application.currentProviderCatalogView()
	if err != nil {
		return err
	}
	if err := providerReconciler.Adopt(startupView, startupProviders); err != nil {
		return fmt.Errorf("adopt startup provider configuration: %w", err)
	}
	logProviderFailureIDs(
		"startup provider reconciliation",
		resolutionFailureIDs(startupFailures),
	)

	if err := b.openAccountAccess(); err != nil {
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
	b.usageRecords, err = usage.Open(b.application.store, usage.Options{})
	if err != nil {
		return fmt.Errorf("open usage repository: %w", err)
	}
	b.presets, err = presets.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open preset repository: %w", err)
	}
	b.templates, err = account.OpenTemplates(b.sqlDB)
	if err != nil {
		return fmt.Errorf("open account template repository: %w", err)
	}
	if err := b.openFileService(); err != nil {
		return err
	}
	if err := b.openJobService(); err != nil {
		return err
	}
	masterKey := []byte(b.config.Security.MasterKey)
	if len(masterKey) < 32 {
		masterKey = credentials.DeriveKeyFromPassword(b.config.Security.MasterKey)
	}
	credentialValidator, err := keyring.NewCatalogCredentialValidator(
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
	b.providerKeys, err = keyring.NewProviderKeys(credentialRepository, masterKey, credentialValidator)
	if err != nil {
		return fmt.Errorf("open provider key service: %w", err)
	}
	return nil
}

// openAccountAccess opens who a request belongs to and how it proves it.
//
// The four steps run together because each one is unreadable without the ones
// before it: a gateway API key resolves to an account, and the authentication
// mode decides whether a deployment is allowed to hold no key at all.
func (b *runtimeBuilder) openAccountAccess() error {
	var err error
	b.accounts, err = account.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open account repository: %w", err)
	}
	// Every gateway API key resolves to an account, so the canonical account has
	// to exist before the first key is read. The call is idempotent and safe
	// against a concurrent boot.
	if _, err := b.accounts.EnsureDefault(context.Background()); err != nil {
		return fmt.Errorf("ensure the default account: %w", err)
	}
	b.apiKeys, err = apikey.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open API key repository: %w", err)
	}
	if err := b.resolveAuthMode(context.Background()); err != nil {
		return err
	}
	if err := requireAPIKey(context.Background(), b.apiKeys, b.auth.setting.Mode); err != nil {
		return err
	}
	return b.resolveLocalToken(context.Background())
}

// openFileService joins the two stores one stored file needs: the record
// repository in the key-value store and the bytes in the byte store. It runs
// after openBlob, because the service refuses to open without a byte store.
func (b *runtimeBuilder) openFileService() error {
	records, err := files.OpenRepository(b.application.store)
	if err != nil {
		return fmt.Errorf("open file repository: %w", err)
	}
	// The meter counts every account's stored bytes whether or not a bound is
	// set. A deployment that sets one later reads a true total rather than a
	// zero over storage that is already full.
	meter, err := limits.NewStorageMeter(b.application.store)
	if err != nil {
		return fmt.Errorf("open stored byte meter: %w", err)
	}
	b.files, err = files.NewService(records, b.application.blobStore,
		files.WithRetention(b.config.Files.RetentionWindow()),
		files.WithMeter(meter))
	if err != nil {
		return fmt.Errorf("open file service: %w", err)
	}
	b.application.files = b.files
	return nil
}

// openJobService joins the two stores one video job needs: the record
// repository in the key-value store and the finished asset in the byte store.
// It runs after openBlob for the same reason the file service does.
func (b *runtimeBuilder) openJobService() error {
	records, err := jobs.OpenRepository(b.application.store)
	if err != nil {
		return fmt.Errorf("open job repository: %w", err)
	}
	meter, err := limits.NewJobMeter(b.application.store)
	if err != nil {
		return fmt.Errorf("open job meter: %w", err)
	}
	// The accountant reads the catalog through a closure rather than a captured
	// snapshot. A job ends long after the request that started it, so the price
	// it draws comes from whatever the catalog holds at that moment.
	accountant := proxy.NewJobAccountant(b.application.currentRoutableSnapshot, b.usageRecords)
	b.jobs, err = jobs.NewService(records,
		jobs.WithAssetStore(b.application.blobStore),
		jobs.WithRetention(b.config.Jobs.AssetRetentionWindow()),
		jobs.WithAssetBound(b.config.Jobs.AssetBound()),
		jobs.WithJobMeter(meter),
		jobs.WithAccountant(accountant))
	if err != nil {
		return fmt.Errorf("open job service: %w", err)
	}
	b.application.jobs = b.jobs
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
	availabilityOwner, err := availability.New(
		availability.DefaultConfig(),
		nil,
		providerAvailabilityPublisher{
			catalog: b.application.catalog, states: b.application.providerStates,
		},
	)
	if err != nil {
		return fmt.Errorf("open provider availability owner: %w", err)
	}
	b.application.availability = availabilityOwner
	modelRouter := router.New(
		registryAdapter,
		router.WithCatalog(b.application.catalog),
		router.WithAvailability(availabilityOwner),
		router.WithOutcomePublisher(b.application.providerStates),
		router.WithOperatorCredentialGate(b.application.providerStates),
		router.WithStoredCredentials(b.providerKeys),
	)
	proxyOptions := make([]proxy.Option, 0, 3)
	// Codecs build once here and serve every request concurrently.
	proxyOptions = append(proxyOptions, proxy.WithTokenEstimator(tokenize.NewEstimator()))
	if b.files != nil {
		proxyOptions = append(proxyOptions, proxy.WithFiles(storedDocuments{service: b.files}))
	}
	// A parser plugin reads an attachment once per account, engine, and
	// catalog generation. The entries hold text rather than bytes, so they sit
	// in the key-value store under their own prefix and expire on their own
	// window.
	if b.application.store != nil {
		extractions, err := document.NewCache(
			cache.NewDistributedCache(b.application.store, storage.KeyPrefixExtraction),
			nil, 0,
		)
		if err != nil {
			return fmt.Errorf("open extraction cache: %w", err)
		}
		proxyOptions = append(proxyOptions, proxy.WithDocumentCache(extractions))
	}
	if b.application.cacheManager != nil {
		proxyOptions = append(proxyOptions, proxy.WithCache(b.application.cacheManager, &proxy.CacheConfig{
			EnableChatCache: true, EnableEmbeddingCache: true,
			EnableModelCache: true, EnableProviderCache: true,
			CacheControlHeader: "X-Cache-Control",
		}))
	}
	b.gateway = proxy.New(b.application.registry, modelRouter, proxyOptions...)
	// Preset references resolve before caching and routing so cache keys and
	// routes see the resolved request.
	b.gateway = proxy.NewPresetResolver(b.presets).Wrap(b.gateway)
	// The metric surface composes with the gateway even when the route is
	// admin-guarded; only "off" removes it entirely.
	if b.config.Telemetry.Metrics != config.TelemetryMetricsOff {
		b.metrics = telemetry.NewMetrics()
	}
	// The tracer builds only when an OTLP endpoint is configured, so an
	// unconfigured deployment dials nothing and every span call is a no-op.
	if b.config.Telemetry.TracesEndpoint != "" {
		tracing, err := telemetry.NewTracing(context.Background())
		if err != nil {
			return fmt.Errorf("open trace exporter: %w", err)
		}
		b.tracing = tracing
		b.application.own("trace exporter", b.tracing.Shutdown)
	}
	// Usage capture wraps outside the proxy middleware chain so cache hits
	// and every terminal outcome produce a record. The metric surface rides
	// the same choke point: one completed request, one record, one scrape
	// observation.
	observers := []proxy.UsageObserver{b.metrics}
	// The export sink rides the same observer seam: every finalized record
	// streams to the configured target, and a drop lands on the scrape.
	if target := b.config.Telemetry.UsageExport; target != "" {
		sink, err := openUsageSink(target, b.metrics)
		if err != nil {
			return err
		}
		b.application.own("usage export sink", sink.Close)
		observers = append(observers, usageSinkObserver{sink: sink})
	}
	usageCapture := proxy.NewUsageCapture(b.usageRecords, observers...)
	b.gateway = usageCapture.Wrap(b.gateway)
	b.application.own("usage capture", func(context.Context) error {
		usageCapture.Flush()
		return nil
	})
	return nil
}

// openUsageSink builds the export sink the configuration names: an http or
// https URL selects the posting sink, anything else a local NDJSON file.
// Dropped records count on the metric surface either way.
func openUsageSink(target string, metrics *telemetry.Metrics) (usage.Sink, error) {
	options := usage.SinkOptions{OnDrop: metrics.ObserveUsageExportDrops}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return usage.NewHTTPSink(target, options), nil
	}
	sink, err := usage.NewFileSink(target, options)
	if err != nil {
		return nil, fmt.Errorf("open usage export sink: %w", err)
	}
	return sink, nil
}

// usageSinkObserver adapts the sink onto the capture observer seam. Receive
// only buffers, so the synchronous observer contract holds.
type usageSinkObserver struct {
	sink usage.Sink
}

func (o usageSinkObserver) ObserveUsage(record usage.Record) {
	o.sink.Receive(record)
}

func (b *runtimeBuilder) openConsole() error {
	if !b.config.Console.Enabled {
		return nil
	}
	spa, err := console.NewSPAHandler(&log.Logger)
	if err != nil {
		return fmt.Errorf("open console: %w", err)
	}
	b.console = spa
	return nil
}

// fileBackend names where stored file bytes land, or nothing when this
// deployment stores no files. The admin surface reports it, so an operator
// confirms the destination without reading the process configuration.
func (b *runtimeBuilder) fileBackend() string {
	if b.application == nil || b.application.blobStore == nil {
		return ""
	}
	return b.application.blobStore.Backend()
}

// openIdentity turns on the identity seam when the operator configured any
// acquisition path — OAuth applications, WorkOS SSO, or both. It runs after
// openConcepts so the gate and the relational store both exist. A deployment
// with no identity configuration skips all of it: the identity grant stays
// inert and the console keeps its machine-local grants.
func (b *runtimeBuilder) openIdentity() error {
	if !b.config.Identity.Enabled() {
		return nil
	}
	repositories, err := identity.Open(b.sqlDB)
	if err != nil {
		return fmt.Errorf("open identity repositories: %w", err)
	}
	acquisition := b.config.Identity.RuntimeAcquisition()
	if acquisition.CallbackBaseURL == "" {
		// The bind address is the one URL this gateway certainly serves. An
		// operator fronting it with a proxy sets the base explicitly.
		acquisition.CallbackBaseURL = fmt.Sprintf("http://%s:%d",
			b.config.Server.Host, b.config.Server.Port)
	}
	authenticator, err := identity.NewAuthenticator(acquisition, repositories.Users)
	if err != nil {
		return fmt.Errorf("open identity acquisition: %w", err)
	}
	b.gate.UseIdentityProvider(authenticator)
	resolver, err := identity.NewAccountResolver(repositories.Users, repositories.AccountGrants)
	if err != nil {
		return fmt.Errorf("open identity account resolver: %w", err)
	}
	b.gate.UseAccountResolver(resolver)
	b.identityAuth = authenticator
	b.identityRepos = repositories
	log.Info().
		Strs("providers", authenticator.Providers()).
		Msg("Identity acquisition ready")
	return nil
}

// identityAuthenticator hands the acquisition seam across as the server's
// contract. The nil check matters: a nil *identity.Authenticator wrapped in a
// non-nil interface would make the routes call a nil receiver instead of
// reading "not configured".
func (b *runtimeBuilder) identityAuthenticator() controllers.IdentityAuthenticator {
	if b.identityAuth == nil {
		return nil
	}
	return b.identityAuth
}

func (b *runtimeBuilder) openHTTPServer() error {
	httpServer, err := b.factories.newServer(serverConfig(b.config, b.auth), server.Dependencies{
		Service: b.gateway, APIKeys: b.apiKeys, Accounts: b.accounts,
		ProviderKeys: b.providerKeys,
		RateLimits:   b.rateLimits, ProviderOperations: b.application, Console: b.console,
		Usage: b.usageRecords, Catalog: b.application, Presets: b.presets,
		Templates:    b.templates,
		Files:        b.files,
		Jobs:         b.jobs,
		FileBackend:  b.fileBackend(),
		LocalGate:    b.gate,
		IdentityAuth: b.identityAuthenticator(),
		Identity:     b.identityRepos,
		Telemetry:    b.metrics,
		Tracing:      b.tracing,
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

// requireAPIKey refuses to start a gateway no one can reach. A deployment
// that requires a gateway API key and holds none serves 401 to every request,
// which is a misconfiguration worth failing on rather than a state worth
// running in.
//
// With authentication disabled the same empty store is the expected state:
// there is nothing to authenticate, and an operator trying Starport for the
// first time has not issued a key yet. That is the whole point of the mode.
func requireAPIKey(ctx context.Context, apiKeys apikey.Repository, mode authmode.Mode) error {
	if mode.Effective() == authmode.Disabled {
		return nil
	}
	records, err := apiKeys.List(ctx, 1, 0)
	if err != nil {
		return fmt.Errorf("list gateway API keys: %w", err)
	}
	if len(records) == 0 {
		return ErrAPIKeyRequired
	}
	return nil
}

// resolveAuthMode decides the authentication mode this process runs in, from
// what the operator stated and what a previous console change stored.
//
// It runs before requireAPIKey because the two answer one question in
// sequence: what mode is this, and given that mode is this gateway reachable.
// Reading the raw configuration value here instead would judge a deployment by
// a mode it is not running in.
func (b *runtimeBuilder) resolveAuthMode(ctx context.Context) error {
	store, err := authmode.Open(b.application.store)
	if err != nil {
		return fmt.Errorf("open authentication mode repository: %w", err)
	}
	var persisted authmode.Setting
	record, err := store.Get(ctx)
	switch {
	case err == nil:
		persisted = record.Setting
	case errors.Is(err, authmode.ErrNotFound):
		// Nothing stored is the ordinary first-boot state.
	default:
		return fmt.Errorf("read the stored authentication mode: %w", err)
	}

	setting := authmode.Resolve(b.config.Security.AuthMode, b.config.AuthModeSource(), persisted).Effective()
	// A stored "disabled" is validated against this process's bind address and
	// not the one it was stored on. An operator who disabled authentication on
	// a laptop and redeployed the same data directory behind a public address
	// would otherwise carry an open gateway across with it. A stated mode is
	// not repaired here, because startup validation already refused it.
	if setting.Mode == authmode.Disabled && setting.Source == authmode.SourceConsole &&
		!authmode.AllowsDisabled(b.config.Server.Host, b.config.Security.AllowRemoteNoAuth) {
		log.Warn().
			Str("bind_host", b.config.Server.Host).
			Msg("Ignoring the stored disabled authentication mode: the bind address is not loopback")
		setting = authmode.Setting{Mode: authmode.Required, Source: authmode.SourceDefault}
	}
	b.auth = authRuntime{setting: setting, store: store}
	return nil
}

// resolveLocalToken mints this machine's local admin token if it does not exist
// yet, and refuses to start when serving it would expose it.
//
// Minting at startup rather than on first use is what makes the credential
// available to an operator who has just installed Starport: the file is there
// before they need it, and `starport auth token` prints the same value the
// gateway is holding. Two gateways starting at once take a file lock and agree
// on one token, so the value an operator reads is the value both processes
// accept.
func (b *runtimeBuilder) resolveLocalToken(ctx context.Context) error {
	// A development gateway never writes the token file. It reads the
	// machine's token when one exists, so `starport auth token` and the
	// console paste path agree with it; a machine that holds none gets an
	// ephemeral in-memory token, minted at startup and gone with the
	// process, because writing the file would break the development promise
	// that nothing lands on disk.
	if b.config.Security.LocalTokenReadOnly() {
		token, held, err := peekMachineToken(ctx, b.config.Security.LocalTokenPath)
		if err != nil {
			return err
		}
		if !localauth.AllowsExposure(b.config.Server.Host, token) {
			return fmt.Errorf(
				"%w and this gateway binds %s: a read-only token is never rotated here, so it serves loopback alone",
				ErrLocalTokenExposed, b.config.Server.Host,
			)
		}
		b.gate = localauth.NewGate(token, b.config.Server.Host)
		b.application.localGate = b.gate
		log.Info().
			Uint64("generation", token.Generation).
			Bool("machine_token", held).
			Msg("Local admin token ready: read-only, nothing written to disk")
		return nil
	}
	if b.config.Security.LocalTokenPath == "" {
		return ErrLocalTokenPathRequired
	}
	store, err := localauth.NewStore(b.config.Security.LocalTokenPath)
	if err != nil {
		return fmt.Errorf("open the local admin token: %w", err)
	}
	token, minted, err := store.LoadOrMint(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("read the local admin token: %w", err)
	}
	// The refusal is here rather than in configuration validation because it
	// depends on what is on disk: the same configuration is safe with a rotated
	// token and unsafe with a first-boot one, and validation does not read files.
	if !localauth.AllowsExposure(b.config.Server.Host, token) {
		return fmt.Errorf(
			"%w and this gateway binds %s: the first-boot token was printed to this machine's "+
				"terminal, so it is safe only where it was born. Run %q and start again",
			ErrLocalTokenExposed, b.config.Server.Host, localauth.RotateCommand,
		)
	}
	// The gate holds the token the gateway just read, so a launch ticket minted
	// by the CLI from the same file verifies here. It is the only thing that
	// keeps the secret after this function returns; nothing else in the runtime
	// needs it, and the value is never logged.
	b.gate = localauth.NewGate(token, b.config.Server.Host)
	b.application.localGate = b.gate
	log.Info().
		Str("token_file", store.Path()).
		Uint64("generation", token.Generation).
		Bool("minted", minted).
		Msg("Local admin token ready")
	return nil
}

// peekMachineToken reads the machine's local admin token without touching the
// disk, and mints an ephemeral one when the machine holds none. The second
// return value reports whether the token is the machine's, so the log can say
// which credential the paste path will accept. A token file that exists but
// does not read is an error, not a mint: an ephemeral token would silently
// disagree with the one `starport auth token` refuses to print.
func peekMachineToken(ctx context.Context, path string) (localauth.Token, bool, error) {
	if path != "" {
		store, err := localauth.NewStore(path)
		if err != nil {
			return localauth.Token{}, false, fmt.Errorf("open the local admin token: %w", err)
		}
		token, err := store.Peek(ctx)
		switch {
		case err == nil:
			return token, true, nil
		case !errors.Is(err, localauth.ErrNotFound):
			return localauth.Token{}, false, fmt.Errorf("read the local admin token: %w", err)
		}
	}
	token, err := localauth.Mint(1, time.Now())
	if err != nil {
		return localauth.Token{}, false, fmt.Errorf("mint an ephemeral local admin token: %w", err)
	}
	return token, false, nil
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
	if a.providerReconciler != nil {
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
			a.providerReconcileLoop(runCtx)
		}()
	}
	if a.files != nil && a.config.Files.SweepEvery() > 0 {
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
			a.fileSweepLoop(runCtx)
		}()
	}
	if a.jobs != nil && a.config.Jobs.SweepEvery() > 0 {
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
			a.jobSweepLoop(runCtx)
		}()
	}
	if a.providerStates != nil && a.catalog != nil {
		incidentPoller, err := statuspage.New(
			statuspage.DefaultConfig(),
			catalogHealthAPISource{catalog: a.catalog},
			providerIncidentPublisher{states: a.providerStates, transitions: a.incidentTransitions},
		)
		if err != nil {
			return errors.Join(fmt.Errorf("open status-page poller: %w", err), a.closeWithTimeout())
		}
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
			incidentPoller.Run(runCtx)
		}()
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
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
			a.remoteCatalogLoop(runCtx)
		}()
	}
	if a.catalogUpdates == nil && a.config.Catalog.RefreshInterval > 0 {
		a.runtimeWG.Add(1)
		go func() {
			defer a.runtimeWG.Done()
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
	a.runtimeWG.Wait()
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
		openSQL:     openSQL,
		openBlob:    openBlob,
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
	return a.catalogRuntime.RefreshCandidate(ctx, a.config.Catalog.RefreshTimeout)
}

func (a *App) refreshRuntime(ctx context.Context) error {
	state, err := a.syncCatalog(ctx)
	if err != nil {
		return err
	}
	return a.activateRuntimeState(ctx, state)
}

func (a *App) activateRuntimeState(ctx context.Context, state starmap.CatalogState) error {
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	if state.Catalog == nil || strings.TrimSpace(state.GenerationID) == "" {
		return ErrCatalogRequired
	}
	current := a.catalog.Current()
	if current != nil &&
		current.GenerationID() == state.GenerationID &&
		current.PayloadChecksum() == state.PayloadChecksum {
		return nil
	}
	resolved, failures, err := a.config.ResolveProviderSetLocalIsolated(
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
	if err := a.publishProviderCatalogState(); err != nil {
		return err
	}
	a.config.Providers = config.CloneProvidersConfig(resolved)
	if a.providerReconciler != nil {
		if err := a.providerReconciler.Adopt(providerCatalogView(state), resolved); err != nil {
			return err
		}
	}
	logProviderFailureIDs(
		"catalog provider reconciliation",
		resolutionFailureIDs(failures),
	)
	return nil
}

// fileSweepLoop reclaims expired and abandoned file storage on an interval.
//
// The pass runs once at startup, because a deployment that restarts more often
// than the interval would otherwise never reclaim anything. Every later pass
// waits for the tick.
func (a *App) fileSweepLoop(ctx context.Context) {
	interval := a.config.Files.SweepEvery()
	a.sweepFiles(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sweepFiles(ctx)
		}
	}
}

// sweepFiles runs one pass and reports what it reclaimed.
//
// A quiet pass logs nothing. An operator reading a log for the reason a disk
// filled needs to see the passes that removed something, and a line every hour
// saying zero would bury them.
func (a *App) sweepFiles(ctx context.Context) {
	result, err := a.files.Sweep(ctx)
	if err != nil {
		log.Warn().Err(err).
			Int("reclaimed", result.Total()).
			Msg("file sweep did not finish; the next pass retries the rest")
	}
	if result.Total() == 0 {
		return
	}
	log.Info().
		Int("expired", result.Expired).
		Int("abandoned", result.Abandoned).
		Int("resumed", result.Resumed).
		Msg("file sweep reclaimed storage")
}

// jobSweepLoop closes the books on jobs nobody came back for, on an interval.
// It follows the file sweep: one pass at startup, then one per tick.
func (a *App) jobSweepLoop(ctx context.Context) {
	interval := a.config.Jobs.SweepEvery()
	a.sweepJobAssets(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sweepJobAssets(ctx)
		}
	}
}

// sweepJobAssets runs one pass and reports what it reclaimed. A quiet pass logs
// nothing, for the reason the file sweep gives.
func (a *App) sweepJobAssets(ctx context.Context) {
	result, err := a.jobs.Sweep(ctx)
	if err != nil {
		log.Warn().Err(err).
			Int("reclaimed", result.Expired).
			Msg("job sweep did not finish; the next pass retries the rest")
	}
	if result.Expired == 0 && result.Abandoned == 0 && result.Accounted == 0 {
		return
	}
	log.Info().
		Int("expired", result.Expired).
		Int("abandoned", result.Abandoned).
		Int("accounted", result.Accounted).
		Msg("job sweep reclaimed storage and closed finished work")
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
	if factories.openStorage == nil || factories.openSQL == nil ||
		factories.openBlob == nil ||
		factories.openCatalog == nil || factories.newConnector == nil ||
		factories.newCache == nil || factories.newServer == nil {
		return errors.New("application runtime factories are incomplete")
	}
	return nil
}

func openStorage(cfg config.StorageConfig) (storage.KVStore, error) {
	return storage.Open(cfg.RuntimeStorage())
}

func openSQL(cfg config.StorageConfig) (*sqlstore.DB, error) {
	return sqlstore.Open(cfg.RuntimeSQL())
}

// openBlob selects the backend from configuration. The filesystem is the
// default, and it needs nothing configured. Validation has already refused an
// incomplete object store selection, so this function reads a decision rather
// than making one.
func openBlob(ctx context.Context, cfg config.FilesConfig) (blob.Store, error) {
	switch cfg.SelectedBackend() {
	case config.BlobBackendObjectStore:
		return blob.NewObjectStore(ctx, blob.ObjectStoreOptions{
			Bucket:          cfg.ObjectStore.Bucket,
			Region:          cfg.ObjectStore.Region,
			Endpoint:        cfg.ObjectStore.Endpoint,
			Prefix:          cfg.ObjectStore.Prefix,
			AccessKeyID:     cfg.ObjectStore.AccessKeyID,
			SecretAccessKey: cfg.ObjectStore.SecretAccessKey,
		})
	default:
		return blob.NewFilesystem(cfg.Path)
	}
}

func serverConfig(cfg *config.Config, auth authRuntime) *server.Config {
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
		maxRequestSize = config.DefaultMaxRequestSize
	}
	return &server.Config{
		Port: cfg.Server.Port, Host: cfg.Server.Host,
		ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout: cfg.Server.IdleTimeout, RequestTimeout: requestTimeout,
		ShutdownTimeout: cfg.Server.ShutdownTimeout, MaxRequestSize: maxRequestSize,
		MaxFileUploadSize:          cfg.Files.UploadBound(),
		MaxHeaderBytes:             cfg.Server.MaxHeaderBytes,
		AuthMode:                   auth.setting.Effective().Mode,
		AuthModeSource:             auth.setting.Effective().Source,
		AuthModeStore:              auth.store,
		AllowRemoteNoAuth:          cfg.Security.AllowRemoteNoAuth,
		UnauthenticatedScopes:      cfg.Security.UnauthenticatedScopes,
		MetricsMode:                cfg.Telemetry.Metrics,
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
