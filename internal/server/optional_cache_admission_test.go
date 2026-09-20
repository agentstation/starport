package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	bytecache "github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

type observedResponseCache struct {
	*bytecache.Manager
	reads       int
	hits        int
	unavailable bool
}

func (c *observedResponseCache) GetResponse(ctx context.Context, key string) ([]byte, bool, error) {
	c.reads++
	if c.unavailable {
		return nil, false, errors.New("cache transport unavailable")
	}
	value, found, err := c.Manager.GetResponse(ctx, key)
	if found {
		c.hits++
	}
	return value, found, err
}

func TestHTTPCachePreservesBudgetAndKeyAdmission(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		t.Run(prefix, func(t *testing.T) {
			builder := catalogs.NewEmpty()
			author := catalogs.Author{ID: "author", Name: "Author"}
			require.NoError(t, builder.SetAuthor(author))
			features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
			require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "model", Name: "Model", Authors: []catalogs.Author{author}, Features: features}))
			require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"opaque": {ID: "opaque", ModelRef: "author/model", Limits: &catalogs.ModelLimits{ContextWindow: 4096}, Status: catalogs.ModelStatusActive, Features: features}}}))
			accepted, err := builder.Build()
			require.NoError(t, err)
			plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "cache-admission", Sequence: 1}})
			require.NoError(t, err)
			reg, err := registry.Open(plane, []registry.Registration{{Provider: "acme", Connector: connectors.NewMockConnector(connectors.ProviderConfig{}), Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, Anonymous: credentials.NewMaterial(catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone}, nil, credentials.MaterialMetadata{})}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reg.Close()) })
			manager, err := bytecache.NewCacheManager(bytecache.ManagerConfig{}, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			observed := &observedResponseCache{Manager: manager}
			s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(c *testServerConfig) {
				c.runtimeRegistry = reg
				c.cacheManager = observed
				c.cacheConfig = &proxy.CacheConfig{EnableChatCache: true}
			})
			secret := createServerAPIKey(t, s, "cache-budget", []string{"chat:write"})
			send := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, prefix+"/chat/completions", strings.NewReader(`{"model":"author/model","messages":[{"role":"user","content":"hello"}]}`))
				req.Header.Set("Authorization", "Bearer "+secret)
				req.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				s.Router().ServeHTTP(out, req)
				return out
			}
			first := send()
			require.Equal(t, http.StatusOK, first.Code, first.Body.String())
			require.Eventually(t, func() bool {
				return manager.FillStatus().CompletedFills > 0 && manager.FillStatus().RetainedEntries == 0
			}, time.Second, time.Millisecond)
			second := send()
			require.Equal(t, http.StatusOK, second.Code, second.Body.String())
			require.Positive(t, observed.hits)
			key, err := s.apiKeys.GetByID(t.Context(), "cache-budget")
			require.NoError(t, err)
			key.APIKey.Limits = &limits.Limits{Spend: &limits.Budget{Limit: 1, Interval: limits.IntervalDay}}
			key, err = s.apiKeys.Update(t.Context(), key.APIKey, key.Revision)
			require.NoError(t, err)
			for _, unavailable := range []bool{false, true} {
				observed.unavailable = unavailable
				for _, unknown := range []bool{false, true} {
					s.usage = stubUsageTotals{totals: usage.Totals{SpendNanoUSD: 1}}
					want := http.StatusPaymentRequired
					if unknown {
						s.usage = stubUsageTotals{err: errors.New("budget authority unavailable")}
						want = http.StatusServiceUnavailable
					}
					reads := observed.reads
					refused := send()
					require.Equal(t, want, refused.Code, refused.Body.String())
					require.Equal(t, reads, observed.reads, "required budget refusal must precede optional cache lookup")
				}
			}
			s.usage = stubUsageTotals{}
			allowed := send()
			require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
			require.Positive(t, observed.reads)
			expired := key.APIKey.CreatedAt.Add(time.Nanosecond)
			key.APIKey.ExpiresAt = &expired
			key, err = s.apiKeys.Update(t.Context(), key.APIKey, key.Revision)
			require.NoError(t, err)
			for _, unavailable := range []bool{false, true} {
				observed.unavailable = unavailable
				reads := observed.reads
				denied := send()
				require.Equal(t, http.StatusUnauthorized, denied.Code, denied.Body.String())
				require.Equal(t, reads, observed.reads, "expired key must not reach optional cache")
			}
			key.APIKey.ExpiresAt = nil
			key.APIKey.Active = false
			_, err = s.apiKeys.Update(t.Context(), key.APIKey, key.Revision)
			require.NoError(t, err)
			reads := observed.reads
			denied := send()
			require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
			require.Equal(t, reads, observed.reads, "withdrawn key must not reach optional cache")
		})
	}
}
