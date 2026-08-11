package server

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/storage"
)

type testServerConfig struct {
	store     storage.KVStore
	masterKey []byte
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

func withTestMasterKey(masterKey []byte) testServerOption {
	return func(config *testServerConfig) {
		config.masterKey = append([]byte(nil), masterKey...)
	}
}

// newTestServer is the explicit server test composition root.
func newTestServer(tb testing.TB, config *Config, options ...testServerOption) *Server {
	tb.Helper()
	testConfig := testServerConfig{
		store:     storage.NewMockStore(),
		masterKey: make([]byte, 32),
	}
	for _, option := range options {
		option(&testConfig)
	}

	identities, err := identity.Open(testConfig.store)
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
	validator, err := byok.NewCatalogCredentialValidator(func(providerID catalogs.ProviderID) (catalogs.Provider, bool) {
		provider, lookupErr := catalog.Provider(providerID)
		return provider, lookupErr == nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	providerKeys, err := byok.NewProviderKeys(credentialsRepository, testConfig.masterKey, validator)
	if err != nil {
		tb.Fatal(err)
	}
	rateLimits, err := ratelimit.Open(testConfig.store, nil)
	if err != nil {
		tb.Fatal(err)
	}

	reg := registry.NewEmpty()
	mockConfig := connectors.ProviderConfig{BaseURL: "http://mock"}
	if err := reg.Register("mock", connectors.NewMockConnector(mockConfig)); err != nil {
		tb.Fatal(err)
	}
	modelRouter := router.New(testRegistryAdapter{registry: reg}, router.WithCatalog(reg.Catalog()))
	service := proxy.New(reg, modelRouter)

	result, err := New(config, Dependencies{
		Service: service, Identities: identities, ProviderKeys: providerKeys, RateLimits: rateLimits,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return result
}
