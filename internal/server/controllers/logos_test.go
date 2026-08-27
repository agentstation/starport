package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/proxy"
)

func logosRouter() chi.Router {
	router := chi.NewRouter()
	router.Get("/api/v1/logos/{kind}/{id}.svg", NewLogosController(nil).Get)
	return router
}

// catalogLogoProxy serves catalog bytes for one provider ID and reports
// not_found for everything else, so tests can prove the controller prefers
// catalog bytes and still falls back to the embedded asset set.
type catalogLogoProxy struct {
	unsupportedMedia
	mockProviders
	id  string
	svg []byte
}

func (m *catalogLogoProxy) GetLogo(_ context.Context, kind view.LogoKind, id string) ([]byte, error) {
	if kind == view.LogoKindProviders && id == m.id {
		return m.svg, nil
	}
	return nil, &proxy.ProviderError{Code: "not_found", Message: "Logo not found"}
}

func TestLogosControllerServesSVG(t *testing.T) {
	recorder := httptest.NewRecorder()
	logosRouter().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/logos/providers/openai.svg", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "image/svg+xml", recorder.Header().Get("Content-Type"))
	require.Equal(t, "public, max-age=86400", recorder.Header().Get("Cache-Control"))
	require.NotEmpty(t, recorder.Header().Get("ETag"))
	require.Contains(t, recorder.Body.String(), "<svg")
}

func TestLogosControllerNotModified(t *testing.T) {
	router := logosRouter()
	first := httptest.NewRecorder()
	router.ServeHTTP(first,
		httptest.NewRequest(http.MethodGet, "/api/v1/logos/authors/qwen.svg", nil))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logos/authors/qwen.svg", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	router.ServeHTTP(second, request)
	require.Equal(t, http.StatusNotModified, second.Code)
	require.Empty(t, second.Body.String())
}

func TestLogosControllerPrefersCatalogBytes(t *testing.T) {
	catalogSVG := `<svg xmlns="http://www.w3.org/2000/svg"><title>catalog-openai</title></svg>`
	controller := NewLogosController(&catalogLogoProxy{id: "openai", svg: []byte(catalogSVG)})
	router := chi.NewRouter()
	router.Get("/api/v1/logos/{kind}/{id}.svg", controller.Get)

	catalog := httptest.NewRecorder()
	router.ServeHTTP(catalog,
		httptest.NewRequest(http.MethodGet, "/api/v1/logos/providers/openai.svg", nil))
	require.Equal(t, http.StatusOK, catalog.Code)
	require.Equal(t, catalogSVG, catalog.Body.String())
	require.NotEmpty(t, catalog.Header().Get("ETag"))

	fallback := httptest.NewRecorder()
	router.ServeHTTP(fallback,
		httptest.NewRequest(http.MethodGet, "/api/v1/logos/providers/groq.svg", nil))
	require.Equal(t, http.StatusOK, fallback.Code)
	require.NotEqual(t, catalogSVG, fallback.Body.String())
	require.Contains(t, fallback.Body.String(), "<svg")
}

func TestLogosControllerUnknownIs404(t *testing.T) {
	for _, path := range []string{
		"/api/v1/logos/providers/no-such-provider.svg",
		"/api/v1/logos/models/openai.svg",
		"/api/v1/logos/providers/%2e%2e.svg",
	} {
		recorder := httptest.NewRecorder()
		logosRouter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, recorder.Code, path)
		require.Contains(t, recorder.Body.String(), "Logo not found", path)
	}
}
