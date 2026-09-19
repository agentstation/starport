package controllers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

type discoveryAuthority struct {
	*starmap.Client
	allowed atomic.Bool
}

func (s *discoveryAuthority) AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool {
	return s.allowed.Load()
}

func TestDiscoveryDeliveryRechecksViewerAndAuthority(t *testing.T) {
	for _, test := range []string{"policy revision", "key revoked", "account revoked", "reader outage", "authority withdrawal", "unchanged"} {
		t.Run(test, func(t *testing.T) {
			client, err := starmap.New()
			require.NoError(t, err)
			source := &discoveryAuthority{Client: client}
			source.allowed.Store(true)
			plane, err := catalog.Open(source)
			require.NoError(t, err)
			reg, err := registry.Open(plane, nil)
			require.NoError(t, err)
			viewer := DiscoveryViewer{Key: apikey.APIKey{ID: "viewer", Active: true, Scopes: []string{"models:read"}}, Account: account.Account{ID: account.DefaultID, Active: true}}
			calls := 0
			controller := NewDiscoveryController(reg, func(*http.Request) (DiscoveryViewer, error) {
				calls++
				if calls == 2 {
					switch test {
					case "policy revision":
						viewer.AccountRevision++
					case "key revoked":
						viewer.Key.Active = false
					case "account revoked":
						viewer.Account.Active = false
					case "reader outage":
						return DiscoveryViewer{}, errors.New("private backend diagnostic")
					case "authority withdrawal":
						source.allowed.Store(false)
					}
				}
				return viewer, nil
			})
			response := httptest.NewRecorder()
			controller.List(response, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/discovery", nil))
			require.Equal(t, 2, calls)
			if test == "unchanged" {
				require.Equal(t, http.StatusOK, response.Code)
				return
			}
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.NotContains(t, response.Body.String(), "generation_id")
			require.NotContains(t, response.Body.String(), "private backend diagnostic")
			require.NotContains(t, response.Body.String(), "\"models\"")
		})
	}
}

func TestDiscoveryPolicyIntersectsAccountAndKey(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := catalog.Open(client)
	require.NoError(t, err)
	definitions := client.Catalog().Definitions()
	var offering catalogs.ProviderOffering
	for _, definition := range definitions {
		offerings, err := client.Catalog().DefinitionOfferings(definition.ID)
		require.NoError(t, err)
		if len(offerings) > 0 {
			offering = offerings[0]
			break
		}
	}
	require.NotEmpty(t, offering.ProviderID)
	viewer := DiscoveryViewer{
		Key:     apikey.APIKey{AllowedModels: []string{string(offering.DefinitionID)}},
		Account: account.Account{Access: []account.ProviderAccess{{Provider: string(offering.ProviderID), Models: []string{string(offering.ProviderModelID)}}}},
	}
	facts, err := plane.Current().Discover(disclosure.New(plane.Current(), viewer.Key, viewer.Account))
	require.NoError(t, err)
	require.Len(t, facts.Models, 1)
	require.Len(t, facts.Models[0].Offerings, 1)
	require.Equal(t, offering.ProviderModelID, facts.Models[0].Offerings[0].ProviderModelID)
	viewer.Key.AllowedModels = []string{"denied-model"}
	facts, err = plane.Current().Discover(disclosure.New(plane.Current(), viewer.Key, viewer.Account))
	require.NoError(t, err)
	require.Empty(t, facts.Models)
}
