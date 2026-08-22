package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func logosRouter() chi.Router {
	router := chi.NewRouter()
	router.Get("/api/v1/logos/{kind}/{id}.svg", NewLogosController().Get)
	return router
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
