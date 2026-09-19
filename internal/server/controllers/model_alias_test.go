package controllers

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

type aliasControllerSource struct{ state starmap.CatalogState }

func (s aliasControllerSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func TestModelDetailsResolveRetainedAliasInBothProtocols(t *testing.T) {
	for _, state := range []catalogs.CanonicalAliasState{catalogs.CanonicalAliasActive, catalogs.CanonicalAliasRemoved} {
		t.Run(string(state), func(t *testing.T) {
			builder := catalogs.NewEmpty()
			require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
			require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current", Name: "Current", Authors: []catalogs.Author{{ID: "author", Name: "Author"}}}))
			require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{{ID: "author/old", TargetID: "author/current", PublisherID: "publisher", State: state}}))
			accepted, err := builder.Build()
			require.NoError(t, err)
			plane, err := runtimecatalog.Open(aliasControllerSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "alias-" + string(state)}})
			require.NoError(t, err)
			reg, err := registry.Open(plane, nil)
			require.NoError(t, err)
			lease, err := reg.AcquireRuntime()
			require.NoError(t, err)
			defer lease.Release()
			for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolOpenRouter} {
				t.Run(string(protocol), func(t *testing.T) {
					service := &mockModels{models: &proxy.ModelsResponse{Object: "list", Data: []proxy.ModelInfo{{ID: "author/current", Object: "model", Name: "Current"}}}}
					controller := NewModelsController(service)
					if protocol == ProtocolOpenRouter {
						controller = NewOpenRouterModelsController(service)
					}
					routeContext := chi.NewRouteContext()
					routeContext.URLParams.Add("model", url.QueryEscape("author/old"))
					ctx := context.WithValue(t.Context(), chi.RouteCtxKey, routeContext)
					ctx = connectors.ContextWithRuntimeLease(ctx, lease)
					request := httptest.NewRequest(http.MethodGet, "/models/author%2Fold", nil).WithContext(ctx)
					response := httptest.NewRecorder()
					controller.Get(response, request)
					if state == catalogs.CanonicalAliasActive {
						require.Equal(t, http.StatusOK, response.Code, response.Body.String())
						require.Contains(t, response.Body.String(), "author/current")
					} else {
						require.Equal(t, http.StatusNotFound, response.Code)
						require.NotContains(t, response.Body.String(), "author/current")
						var envelope struct {
							Error struct {
								Type     string         `json:"type"`
								Code     int            `json:"code"`
								Metadata map[string]any `json:"metadata"`
							} `json:"error"`
						}
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
						if protocol == ProtocolOpenAI {
							require.Equal(t, "not_found_error", envelope.Error.Type)
						} else {
							require.Equal(t, 404, envelope.Error.Code)
							require.Equal(t, "not_found_error", envelope.Error.Metadata["error_type"])
						}
					}
				})
			}
		})
	}
}

func TestModelDetailsPreserveLiteralPlusInPath(t *testing.T) {
	service := &mockModels{models: &proxy.ModelsResponse{Object: "list", Data: []proxy.ModelInfo{{ID: "author/model+variant", Object: "model"}}}}
	for _, controller := range []*ModelsController{NewModelsController(service), NewOpenRouterModelsController(service)} {
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("model", "author%2Fmodel+variant")
		request := httptest.NewRequest(http.MethodGet, "/models/author%2Fmodel+variant", nil).WithContext(context.WithValue(t.Context(), chi.RouteCtxKey, routeContext))
		response := httptest.NewRecorder()
		controller.Get(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "model+variant")
		endpoints := httptest.NewRecorder()
		controller.GetEndpoints(endpoints, request)
		require.Equal(t, http.StatusOK, endpoints.Code)
		require.Contains(t, endpoints.Body.String(), "model+variant")
	}
}
