package server

import (
	"encoding/json/v2"
	"net/http"
	"testing"

	"github.com/agentstation/starport/internal/account"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/stretchr/testify/require"
)

func TestCatalogReaderRoutesExcludeDeniedMembership(t *testing.T) {
	operations := &catalogOperationsStub{status: operatorStatus()}
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog(), withTestCatalogOperations(operations))
	lease, err := s.discoveryRegistry.AcquireRuntime()
	require.NoError(t, err)
	defer lease.Release()
	generation := lease.Snapshot().GenerationID()
	operations.status.Provenance.Effective.GenerationID = generation
	operations.diff = runtimecatalog.Diff{
		Available: true, FromGenerationID: "previous", ToGenerationID: generation,
		ModelsAdded: []string{"private/new"}, ModelsRemoved: []string{"private/removed"},
		OfferingsAdded:   []runtimecatalog.OfferingChange{{Provider: "private-provider", ProviderModelID: "private-new", DefinitionID: "private/new"}},
		OfferingsRemoved: []runtimecatalog.OfferingChange{{Provider: "private-provider", ProviderModelID: "private-old", DefinitionID: "private/removed"}},
		PriceChanges:     []runtimecatalog.PriceChange{{Provider: "private-provider", ProviderModelID: "private-new", DefinitionID: "private/new", CurrentPer1M: 42}},
	}
	secret := createServerAPIKey(t, s, "reader", []string{"models:read"})
	record, err := s.accounts.GetByID(t.Context(), account.DefaultID)
	require.NoError(t, err)
	record.Account.Access = []account.ProviderAccess{{Provider: "not-in-this-catalog"}}
	_, err = s.accounts.Update(t.Context(), record.Account, record.Revision)
	require.NoError(t, err)
	t.Run("summary", func(t *testing.T) {
		response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog", secret, t.Context())
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var summary runtimecatalog.Summary
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
		require.Zero(t, summary.Models)
		require.Zero(t, summary.Providers)
	})
	t.Run("changes", func(t *testing.T) {
		response := serveAuthorized(s, http.MethodGet, "/api/v1/catalog/changes", secret, t.Context())
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "private")
		var diff runtimecatalog.Diff
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &diff))
		require.Empty(t, diff.ModelsAdded)
		require.Empty(t, diff.ModelsRemoved)
		require.Empty(t, diff.OfferingsAdded)
		require.Empty(t, diff.OfferingsRemoved)
		require.Empty(t, diff.PriceChanges)
		require.True(t, diff.SemanticallyEqual)
	})
}

func TestCatalogReaderRoutesRefuseDifferentGeneration(t *testing.T) {
	operations := &catalogOperationsStub{status: operatorStatus(), diff: runtimecatalog.Diff{Available: true, ToGenerationID: "different", ModelsAdded: []string{"private"}}}
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog(), withTestCatalogOperations(operations))
	secret := createServerAPIKey(t, s, "reader", []string{"models:read"})
	for _, path := range []string{"/api/v1/catalog", "/api/v1/catalog/changes"} {
		t.Run(path, func(t *testing.T) {
			response := serveAuthorized(s, http.MethodGet, path, secret, t.Context())
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.NotContains(t, response.Body.String(), "private")
			require.NotContains(t, response.Body.String(), "different")
			require.Equal(t, "30", response.Header().Get("Retry-After"))
		})
	}
}
