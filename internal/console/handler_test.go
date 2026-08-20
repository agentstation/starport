package console

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	logger := zerolog.Nop()
	handler, err := NewHandler(&logger, Config{Title: "Test Console", Theme: "dark"})
	require.NoError(t, err)
	return handler
}

func TestNewHandler(t *testing.T) {
	handler := newTestHandler(t)
	assert.Equal(t, "Test Console", handler.config.Title)
	assert.Len(t, handler.AssetVersion(), 16, "asset version is a 64-bit hex fingerprint")
}

func TestIndexServesShell(t *testing.T) {
	handler := newTestHandler(t)
	rec := httptest.NewRecorder()
	handler.Index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Test Console")
	assert.Contains(t, body, `data-theme="dark"`)
	assert.Contains(t, body, handler.AssetVersion(), "asset links carry the version query")
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}

func TestIndexCSPNonceMatchesInlineScript(t *testing.T) {
	handler := newTestHandler(t)
	rec := httptest.NewRecorder()
	handler.Index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.NotContains(t, csp, "unsafe-inline")

	nonceMatch := regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`).FindStringSubmatch(csp)
	require.Len(t, nonceMatch, 2, "CSP carries a script nonce")
	assert.Contains(t, rec.Body.String(), `nonce="`+nonceMatch[1]+`"`,
		"the inline theme script uses the CSP nonce")
}

func TestIndexNoncesAreUnique(t *testing.T) {
	handler := newTestHandler(t)
	first := httptest.NewRecorder()
	second := httptest.NewRecorder()
	handler.Index(first, httptest.NewRequest(http.MethodGet, "/", nil))
	handler.Index(second, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.NotEqual(t,
		first.Header().Get("Content-Security-Policy"),
		second.Header().Get("Content-Security-Policy"))
}

func TestRegisterServesEveryPagePath(t *testing.T) {
	handler := newTestHandler(t)
	router := chi.NewRouter()
	handler.Register(router)

	for _, path := range PagePaths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, "page path %s", path)
		assert.Contains(t, rec.Body.String(), "Test Console", "page path %s serves the shell", path)
	}
}

func TestStaticServesAssetsWithETag(t *testing.T) {
	handler := newTestHandler(t)
	router := chi.NewRouter()
	handler.Register(router)

	cases := map[string]string{
		"/static/js/app.js":           "application/javascript; charset=utf-8",
		"/static/css/tokens.css":      "text/css; charset=utf-8",
		"/static/favicon.svg":         "image/svg+xml",
		"/static/vendor/prism.min.js": "application/javascript; charset=utf-8",
	}
	for path, contentType := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, "asset %s", path)
		assert.Equal(t, contentType, rec.Header().Get("Content-Type"), "asset %s", path)
		assert.Equal(t, `"`+handler.AssetVersion()+`"`, rec.Header().Get("ETag"))
	}
}

func TestStaticRevalidatesWithIfNoneMatch(t *testing.T) {
	handler := newTestHandler(t)
	router := chi.NewRouter()
	handler.Register(router)

	req := httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	req.Header.Set("If-None-Match", `"`+handler.AssetVersion()+`"`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotModified, rec.Code)
	assert.Zero(t, rec.Body.Len())
}

func TestStaticRejectsTraversalAndMissing(t *testing.T) {
	handler := newTestHandler(t)
	router := chi.NewRouter()
	handler.Register(router)

	for _, path := range []string{
		"/static/../handler.go",
		"/static/js/../../templates/index.html",
		"/static/does-not-exist.js",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, "path %s", path)
	}
}

func TestNoRemoteAssetReferences(t *testing.T) {
	// The console must work fully offline: no CDN scripts, styles, or fonts.
	for _, fragment := range []string{"https://cdn.", "https://unpkg.", "https://fonts.googleapis"} {
		assert.False(t, strings.Contains(indexHTML, fragment),
			"shell must not reference remote assets: %s", fragment)
	}
}
