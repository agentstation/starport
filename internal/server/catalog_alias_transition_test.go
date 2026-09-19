package server

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type aliasHTTPSource struct{ state starmap.CatalogState }

func (s aliasHTTPSource) CurrentCatalogState() starmap.CatalogState { return s.state }

type observedDiscoveryCache struct {
	*cache.Manager
	hits   int
	writes int
}

func (c *observedDiscoveryCache) GetModel(ctx context.Context, key string) (any, bool, error) {
	value, found, err := c.Manager.GetModel(ctx, key)
	if found {
		c.hits++
	}
	return value, found, err
}

func (c *observedDiscoveryCache) SetModel(ctx context.Context, key string, value any) error {
	c.writes++
	return c.Manager.SetModel(ctx, key, value)
}

func TestHTTPAliasRemovalWithSerializedDiscoveryCache(t *testing.T) {
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current+variant", Name: "Current", Authors: []catalogs.Author{author}, Features: features}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{
		BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}},
	}, Models: map[string]*catalogs.Model{"opaque/model@002": {ID: "opaque/model@002", ModelRef: "author/current+variant", Name: "Serving", Status: catalogs.ModelStatusActive, Features: features}}}))
	alias := catalogs.CanonicalAlias{ID: "author/old+variant", TargetID: "author/current+variant", PublisherID: "publisher", State: catalogs.CanonicalAliasActive}
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	accepted, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "http-alias-active", Sequence: 1}})
	require.NoError(t, err)
	registration := func() registry.Registration {
		return registry.Registration{Provider: "acme", Connector: connectors.NewMockConnector(connectors.ProviderConfig{}), Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}
	}
	reg, err := registry.Open(plane, []registry.Registration{registration()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close()) })
	var cacheConfig cache.ManagerConfig
	cacheConfig.Responses.Strategy = "distributed"
	cacheConfig.Models.SizeMB = 1
	manager, err := cache.NewCacheManager(cacheConfig, storage.NewMockStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	observed := &observedDiscoveryCache{Manager: manager}
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(config *testServerConfig) { config.runtimeRegistry = reg; config.cacheManager = observed })
	secret := createServerAPIKey(t, s, "alias-reader", []string{"models:read"})
	for _, prefix := range []string{"/v1", "/api/v1"} {
		t.Run(prefix, func(t *testing.T) {
			for range 2 {
				response := serveAuthorized(s, http.MethodGet, prefix+"/models/author%2Fold+variant", secret, t.Context())
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "author/current+variant")
				require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			}
		})
	}
	require.Positive(t, observed.hits, "metadata must traverse the real serialized cache")
	require.Equal(t, 1, observed.writes, "warm model lists must decode without rebuilding")
	endpoint := serveAuthorized(s, http.MethodGet, "/api/v1/models/author%2Fold+variant/endpoints", secret, t.Context())
	require.Equal(t, http.StatusOK, endpoint.Code, endpoint.Body.String())
	require.Contains(t, endpoint.Body.String(), "https://provider.test/v1/chat/completions")
	writesBeforeReplacement := observed.writes
	alias.State = catalogs.CanonicalAliasRemoved
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	removed, err := builder.Build()
	require.NoError(t, err)
	candidate, err := reg.Prepare([]registry.Registration{registration()})
	require.NoError(t, err)
	defer func() { require.NoError(t, candidate.Close()) }()
	snapshot, err := plane.ReplaceRuntime(starmap.CatalogState{Catalog: removed, GenerationID: "http-alias-removed", Sequence: 2}, candidate.Availability())
	require.NoError(t, err)
	require.NoError(t, reg.Publish(candidate, snapshot))
	hitsBeforeRemoval := observed.hits
	endpoint = serveAuthorized(s, http.MethodGet, "/api/v1/models/author%2Fold+variant/endpoints", secret, t.Context())
	require.Equal(t, http.StatusNotFound, endpoint.Code, endpoint.Body.String())
	require.NotContains(t, endpoint.Body.String(), "provider.test")
	require.Equal(t, hitsBeforeRemoval, observed.hits, "removed aliases must be refused before cache lookup")
	for _, prefix := range []string{"/v1", "/api/v1"} {
		response := serveAuthorized(s, http.MethodGet, prefix+"/models/author%2Fold+variant", secret, t.Context())
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "author/current+variant")
		var envelope struct {
			Error struct {
				Type     string         `json:"type"`
				Code     int            `json:"code"`
				Metadata map[string]any `json:"metadata"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		if prefix == "/v1" {
			require.Equal(t, "not_found_error", envelope.Error.Type)
		} else {
			require.Equal(t, 404, envelope.Error.Code)
			require.Equal(t, "not_found_error", envelope.Error.Metadata["error_type"])
		}
		response = serveAuthorized(s, http.MethodGet, prefix+"/models/author%2Fcurrent+variant", secret, t.Context())
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	require.Equal(t, writesBeforeReplacement+1, observed.writes, "replacement generation must use a different cache entry")
	record, err := s.accounts.GetByID(t.Context(), account.DefaultID)
	require.NoError(t, err)
	record.Account.Access = []account.ProviderAccess{{Provider: "denied-provider"}}
	_, err = s.accounts.Update(t.Context(), record.Account, record.Revision)
	require.NoError(t, err)
	for _, prefix := range []string{"/v1", "/api/v1"} {
		response := serveAuthorized(s, http.MethodGet, prefix+"/models/author%2Fcurrent+variant", secret, t.Context())
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "opaque/model@002")
	}
}
