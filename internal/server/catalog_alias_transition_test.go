package server

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

type aliasHTTPSource struct{ state starmap.CatalogState }

func (s aliasHTTPSource) CurrentCatalogState() starmap.CatalogState { return s.state }

type observedDiscoveryCache struct {
	*cache.Manager
	hits    int
	writes  int
	lastKey string
}

func (c *observedDiscoveryCache) GetModel(ctx context.Context, key string, target any) (bool, error) {
	found, err := c.Manager.GetModel(ctx, key, target)
	if found {
		c.hits++
	}
	return found, err
}

func (c *observedDiscoveryCache) SetModel(ctx context.Context, key string, value any) error {
	c.writes++
	c.lastKey = key
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
	cacheConfig.Models.SizeMB = 1
	manager, err := cache.NewCacheManager(cacheConfig, nil)
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
				require.Eventually(t, func() bool {
					var decoded any
					found, err := manager.GetModel(t.Context(), observed.lastKey, &decoded)
					return err == nil && found
				}, time.Second, time.Millisecond)
			}
		})
	}
	require.Positive(t, observed.hits, "metadata must traverse the real serialized cache")
	require.Equal(t, 1, observed.writes, "warm model lists must decode without rebuilding")
	endpoint := serveAuthorized(s, http.MethodGet, "/api/v1/models/author%2Fold+variant/endpoints", secret, t.Context())
	require.Equal(t, http.StatusOK, endpoint.Code, endpoint.Body.String())
	require.Contains(t, endpoint.Body.String(), "https://provider.test/v1/chat/completions")
	require.Eventually(t, func() bool {
		var decoded any
		found, err := manager.GetModel(t.Context(), observed.lastKey, &decoded)
		return err == nil && found
	}, time.Second, time.Millisecond)
	endpointWrites := observed.writes
	endpointHits := observed.hits
	cachedEndpoint := serveAuthorized(s, http.MethodGet, "/api/v1/models/author%2Fold+variant/endpoints", secret, t.Context())
	require.Equal(t, http.StatusOK, cachedEndpoint.Code)
	require.JSONEq(t, endpoint.Body.String(), cachedEndpoint.Body.String())
	require.Greater(t, observed.hits, endpointHits, "endpoint request must read the real serialized cache")
	require.Equal(t, endpointWrites, observed.writes, "endpoint cache hit must not rebuild and rewrite the result")
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
		require.Eventually(t, func() bool {
			var decoded any
			found, err := manager.GetModel(t.Context(), observed.lastKey, &decoded)
			return err == nil && found
		}, time.Second, time.Millisecond)
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

func TestMissingModelReturnsNotFoundInBothInferenceProtocols(t *testing.T) {
	empty, err := catalogs.NewEmpty().Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: empty, GenerationID: "removed-model", Sequence: 2}})
	require.NoError(t, err)
	reg, err := registry.Open(plane, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close()) })
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(config *testServerConfig) { config.runtimeRegistry = reg })
	secret := createServerAPIKey(t, s, "inference-reader", []string{"chat:write"})
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, tc := range []struct{ name, route, body string }{
			{"chat", "/chat/completions", `{"model":"author/removed","messages":[{"role":"user","content":"hello"}]}`},
			{"stream", "/chat/completions", `{"model":"author/removed","messages":[{"role":"user","content":"hello"}],"stream":true}`},
			{"embeddings", "/embeddings", `{"model":"author/removed","input":"hello"}`},
		} {
			t.Run(prefix+"/"+tc.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, prefix+tc.route, strings.NewReader(tc.body)).WithContext(t.Context())
				request.Header.Set("Authorization", "Bearer "+secret)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				s.Router().ServeHTTP(response, request)
				require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
				require.Contains(t, response.Header().Get("Content-Type"), "application/json")
				require.Contains(t, response.Body.String(), "not_found_error")
				require.NotContains(t, response.Body.String(), "data:")
			})
		}
	}
}

func TestInferenceErrorsDoNotDiscloseDeniedCatalogMembership(t *testing.T) {
	for _, tc := range []struct {
		name               string
		registered, denied bool
		route, body        string
		status             int
		errorType          string
		operation          catalogs.ProviderOperation
	}{
		{"unavailable", false, false, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}]}`, 503, "service_unavailable", catalogs.ProviderOperationChatCompletions},
		{"unsupported", true, false, "/embeddings", `{"model":"author/current","input":"hello"}`, 400, "invalid_request_error", catalogs.ProviderOperationChatCompletions},
		{"denied chat", true, true, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}]}`, 404, "not_found_error", catalogs.ProviderOperationChatCompletions},
		{"denied unsupported", true, true, "/embeddings", `{"model":"author/current","input":"hello"}`, 404, "not_found_error", catalogs.ProviderOperationChatCompletions},
		{"denied unavailable", false, true, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}]}`, 404, "not_found_error", catalogs.ProviderOperationChatCompletions},
		{"unsupported chat", true, false, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}]}`, 400, "invalid_request_error", catalogs.ProviderOperationEmbeddings},
		{"unsupported stream", true, false, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}],"stream":true}`, 400, "invalid_request_error", catalogs.ProviderOperationEmbeddings},
		{"denied unsupported chat", true, true, "/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}]}`, 404, "not_found_error", catalogs.ProviderOperationEmbeddings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := catalogs.NewEmpty()
			author := catalogs.Author{ID: "author", Name: "Author"}
			require.NoError(t, builder.SetAuthor(author))
			features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
			if tc.operation == catalogs.ProviderOperationEmbeddings {
				features.Modalities.Output = []catalogs.ModelModality{catalogs.ModelModalityEmbedding}
			}
			require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current", Name: "Current", Authors: []catalogs.Author{author}, Features: features}))
			require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: tc.operation, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"private/opaque@001": {ID: "private/opaque@001", ModelRef: "author/current", Name: "Private offering", Status: catalogs.ModelStatusActive, Features: features}}}))
			accepted, err := builder.Build()
			require.NoError(t, err)
			plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "error-policy"}})
			require.NoError(t, err)
			var registrations []registry.Registration
			if tc.registered {
				registrations = []registry.Registration{{Provider: "acme", Connector: connectors.NewMockConnector(connectors.ProviderConfig{}), Operations: []catalogs.ProviderOperation{tc.operation}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}}
			}
			reg, err := registry.Open(plane, registrations)
			require.NoError(t, err)
			if tc.registered {
				require.Len(t, plane.Current().Routes(), 1)
			}
			t.Cleanup(func() { require.NoError(t, reg.Close()) })
			s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(config *testServerConfig) { config.runtimeRegistry = reg })
			secret := createServerAPIKey(t, s, "error-reader", []string{"chat:write"})
			if tc.denied {
				record, err := s.accounts.GetByID(t.Context(), account.DefaultID)
				require.NoError(t, err)
				record.Account.Access = []account.ProviderAccess{{Provider: "other"}}
				_, err = s.accounts.Update(t.Context(), record.Account, record.Revision)
				require.NoError(t, err)
			}
			for _, prefix := range []string{"/v1", "/api/v1"} {
				t.Run(prefix, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, prefix+tc.route, strings.NewReader(tc.body)).WithContext(t.Context())
					request.Header.Set("Authorization", "Bearer "+secret)
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					s.Router().ServeHTTP(response, request)
					require.Equal(t, tc.status, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), tc.errorType)
					if tc.denied {
						require.NotContains(t, response.Body.String(), "private/opaque@001")
						require.NotContains(t, response.Body.String(), "acme")
					}
				})
			}
		})
	}
}
