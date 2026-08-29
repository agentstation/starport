package server

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/blob"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

type testServerConfig struct {
	store              storage.KVStore
	masterKey          []byte
	providerOperations controllers.ProviderOperations
	blobs              blob.Store
	// routableCatalog gives the registry the catalog generation a running
	// gateway holds. Without it the registry carries no snapshot, and a route
	// test cannot separate a model name no catalog holds from a gateway that
	// has not read a catalog yet.
	routableCatalog bool
}

type testRegistryAdapter struct{ registry *registry.Registry }

func (a testRegistryAdapter) Get(provider string) connectors.Connector {
	connector, _ := a.registry.Get(provider)
	return connector
}

func (a testRegistryAdapter) List() []string { return a.registry.ListProviders() }

func (a testRegistryAdapter) ResolveMaterial(
	context.Context,
	string,
) (credentials.Material, error) {
	return credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone},
		nil,
		credentials.MaterialMetadata{Version: "test"},
	), nil
}

type testServerOption func(*testServerConfig)

func withTestStore(store storage.KVStore) testServerOption {
	return func(config *testServerConfig) { config.store = store }
}

func withTestProviderOperations(operations controllers.ProviderOperations) testServerOption {
	return func(config *testServerConfig) { config.providerOperations = operations }
}

// withTestBlobStore replaces the byte store the file service writes to. A test
// that needs a failing store, or one it can inspect, supplies its own.
func withTestBlobStore(store blob.Store) testServerOption {
	return func(config *testServerConfig) { config.blobs = store }
}

// withRoutableCatalog composes the registry against a real catalog generation.
// A test that asserts what the gateway answers for one model name needs it,
// because the answer differs when no generation is loaded.
func withRoutableCatalog() testServerOption {
	return func(config *testServerConfig) { config.routableCatalog = true }
}

type staticTestProviderOperations struct{}

func (staticTestProviderOperations) ProviderStates() providerstate.Snapshot {
	return providerstate.Snapshot{}
}

func (staticTestProviderOperations) RefreshProviders(context.Context) (providers.ReconcileReport, error) {
	return providers.ReconcileReport{}, nil
}

func (staticTestProviderOperations) ProviderIncidentLog(context.Context, catalogs.ProviderID) (statuspage.History, bool) {
	return statuspage.History{}, false
}

func (staticTestProviderOperations) ProviderIncidentTransitions(context.Context, catalogs.ProviderID) ([]providerstate.IncidentTransition, error) {
	return nil, nil
}

// newTestServer is the explicit server test composition root.
func newTestServer(tb testing.TB, config *Config, options ...testServerOption) *Server {
	tb.Helper()
	testConfig := testServerConfig{
		store:              storage.NewMockStore(),
		masterKey:          make([]byte, 32),
		providerOperations: staticTestProviderOperations{},
	}
	for _, option := range options {
		option(&testConfig)
	}

	accounts, err := account.Open(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	// Production composition ensures the canonical account before the first key
	// is read, so a test server that skipped it would not be the real gateway.
	if _, err := accounts.EnsureDefault(context.Background()); err != nil {
		tb.Fatal(err)
	}

	apiKeys, err := apikey.Open(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	credentialsRepository, err := credentials.Open(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	client, err := starmap.NewContext(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	catalog := client.CurrentCatalogState().Catalog
	validator, err := keyring.NewCatalogCredentialValidator(func(providerID catalogs.ProviderID) (catalogs.Provider, bool) {
		provider, lookupErr := catalog.Provider(providerID)
		return provider, lookupErr == nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	providerKeys, err := keyring.NewProviderKeys(credentialsRepository, testConfig.masterKey, validator)
	if err != nil {
		tb.Fatal(err)
	}
	rateLimits, err := ratelimit.Open(testConfig.store, nil)
	if err != nil {
		tb.Fatal(err)
	}

	reg := registry.NewEmpty()
	if testConfig.routableCatalog {
		plane, planeErr := runtimecatalog.Open(client)
		if planeErr != nil {
			tb.Fatal(planeErr)
		}
		reg = registry.NewEmptyWithCatalog(plane)
	}
	mockConfig := connectors.ProviderConfig{BaseURL: "http://mock"}
	if err := reg.Register("mock", connectors.NewMockConnector(mockConfig)); err != nil {
		tb.Fatal(err)
	}
	modelRouter := router.New(testRegistryAdapter{registry: reg}, router.WithCatalog(reg.Catalog()))
	presetRepository, err := presets.Open(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	if testConfig.blobs == nil {
		byteStore, blobErr := blob.NewFilesystem(tb.TempDir())
		if blobErr != nil {
			tb.Fatal(blobErr)
		}
		testConfig.blobs = byteStore
	}
	fileRecords, err := files.OpenRepository(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	// The meter is part of production composition, so a route test that skipped
	// it would exercise an upload path no deployment runs.
	storedBytes, err := limits.NewStorageMeter(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	fileService, err := files.NewService(fileRecords, testConfig.blobs, files.WithMeter(storedBytes))
	if err != nil {
		tb.Fatal(err)
	}
	// The job store is part of production composition too. A route test that
	// omitted it would read the unconfigured answer on every video path and
	// prove nothing about the surface.
	jobRecords, err := jobs.OpenRepository(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	// The byte store is production composition too: without it a finished job
	// stores no asset, and the content route would answer the same way for a
	// gateway that never fetched one.
	// The outstanding job meter is production composition as well. Without it
	// every submission is admitted, and the refusal this surface publishes
	// would be untestable through the router.
	outstandingJobs, err := limits.NewJobMeter(testConfig.store)
	if err != nil {
		tb.Fatal(err)
	}
	jobService, err := jobs.NewService(jobRecords,
		jobs.WithAssetStore(testConfig.blobs),
		jobs.WithRetention(jobs.DefaultAssetRetention),
		jobs.WithJobMeter(outstandingJobs))
	if err != nil {
		tb.Fatal(err)
	}

	// Match production composition: preset references resolve before routing.
	service := proxy.NewPresetResolver(presetRepository).Wrap(proxy.New(reg, modelRouter))

	// Production always composes an authentication-mode store, and whether one
	// exists changes what the switch answers, so the test server composes one
	// too. A test that wants the storage-less answer clears it explicitly.
	if config.AuthModeStore == nil {
		modes, storeErr := authmode.Open(testConfig.store)
		if storeErr != nil {
			tb.Fatal(storeErr)
		}
		config.AuthModeStore = modes
	}

	// Production composition migrates the relational store before any
	// repository rides it, so the test server does the same on an in-memory
	// database.
	sqlDB, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlDB.Migrate(context.Background()); err != nil {
		tb.Fatal(err)
	}
	templates, err := account.OpenTemplates(sqlDB)
	if err != nil {
		tb.Fatal(err)
	}

	result, err := New(config, Dependencies{
		Service: service, APIKeys: apiKeys, Accounts: accounts,
		ProviderKeys: providerKeys, RateLimits: rateLimits,
		ProviderOperations: testConfig.providerOperations, Presets: presetRepository,
		Templates: templates,
		Files:     fileService, Jobs: jobService,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return result
}
