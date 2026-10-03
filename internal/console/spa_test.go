package console

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

func builtDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><div id=\"root\"></div>"),
		},
		"assets/index-abc123.js": &fstest.MapFile{
			Data: []byte("console.log(\"starport\")"),
		},
		"docs/index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><h1>Starport documentation</h1>"),
		},
		"docs/start/index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><h1>Start</h1>"),
		},
		"docs/start/keys-and-roles/index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><h1>Keys and roles</h1>"),
		},
		"docs/manifest.json": &fstest.MapFile{
			Data: []byte(`{"starport_release":"dev"}`),
		},
		"docs/search-index.json": &fstest.MapFile{Data: []byte(`{}`)},
		"docs/search-index.js":   &fstest.MapFile{Data: []byte(`window.__STARPORT_DOCS_SEARCH__ = "{}";`)},
		"docs/assets/docs-abc123.css": &fstest.MapFile{
			Data: []byte("body{}"),
		},
	}
}

func serve(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func newSPARouter(t *testing.T, dist fstest.MapFS) *chi.Mux {
	t.Helper()
	logger := zerolog.Nop()
	handler := NewSPAHandlerFS(&logger, dist)
	router := chi.NewRouter()
	handler.Register(router)
	return router
}

func TestSPAHandlerServesIndexForEveryPagePath(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for _, path := range []string{
		"/", "/accounts", "/auth", "/chat", "/documents",
		"/files", "/jobs", "/keys", "/models", "/presets", "/providers",
		"/settings", "/usage",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		body, _ := io.ReadAll(recorder.Body)
		if !strings.Contains(string(body), "id=\"root\"") {
			t.Fatalf("GET %s did not serve the SPA index", path)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache", path, cache)
		}
		if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Fatalf("GET %s is missing the same-origin CSP", path)
		}
	}
}

func TestSPAHandlerServesIndexForNestedPagePaths(t *testing.T) {
	// Model detail paths carry an encoded slash in the id segment.
	paths := []string{
		"/providers/groq",
		"/models/meta%2Fllama-3.1-8b-instruct",
		"/authors",
		"/authors/openai",
	}
	for _, path := range paths {
		router := newSPARouter(t, builtDist())
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		body, _ := io.ReadAll(recorder.Body)
		if !strings.Contains(string(body), "id=\"root\"") {
			t.Fatalf("GET %s did not serve the SPA index", path)
		}
	}
}

// TestSPAPagePathsCoverClientRoutes derives the page paths from the
// console route files and requires spaPagePaths to match them exactly.
// A route missing from the allowlist breaks only direct loads and
// reloads — client-side navigation still works — so the gap does not
// show up in normal console use.
func TestSPAPagePathsCoverClientRoutes(t *testing.T) {
	routesDir := filepath.Join("..", "..", "console", "src", "routes")
	entries, err := os.ReadDir(routesDir)
	if err != nil {
		t.Skipf("console route sources unavailable: %v", err)
	}
	want := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tsx") ||
			strings.Contains(name, ".test.") ||
			strings.HasPrefix(name, "__") {
			continue
		}
		name = strings.TrimSuffix(name, ".tsx")
		if name == "index" {
			want["/"] = true
			continue
		}
		// TanStack Router file names: dots nest path segments, a
		// trailing underscore detaches a segment from its layout, and a
		// $param segment is a wildcard to the server.
		segments := strings.Split(name, ".")
		for i, segment := range segments {
			segment = strings.TrimSuffix(segment, "_")
			if strings.HasPrefix(segment, "$") {
				segment = "*"
			}
			segments[i] = segment
		}
		want["/"+strings.Join(segments, "/")] = true
	}
	registered := map[string]bool{}
	for _, path := range spaPagePaths {
		registered[path] = true
	}
	for path := range want {
		if !registered[path] {
			t.Errorf("client route %s is missing from spaPagePaths; a direct load of it gets the API 404", path)
		}
	}
	for path := range registered {
		if !want[path] {
			t.Errorf("spaPagePaths lists %s but no console route file serves it", path)
		}
	}
}

func TestSPAHandlerServesHashedAssetsImmutable(t *testing.T) {
	router := newSPARouter(t, builtDist())
	request := httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want %d", recorder.Code, http.StatusOK)
	}
	cache := recorder.Header().Get("Cache-Control")
	if !strings.Contains(cache, "immutable") {
		t.Fatalf("asset Cache-Control = %q, want immutable", cache)
	}
}

func TestSPAHandlerRejectsMissingAndTraversalAssets(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for _, path := range []string{"/assets/missing.js", "/assets/../index.html"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			t.Fatalf("GET %s status = 200, want a non-200 rejection", path)
		}
	}
}

func TestSPAHandlerServesDocsPages(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for path, want := range map[string]string{
		"/docs/":                      "Starport documentation",
		"/docs/start/":                "<h1>Start</h1>",
		"/docs/start/keys-and-roles/": "<h1>Keys and roles</h1>",
	} {
		recorder := serve(router, path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		if body := recorder.Body.String(); !strings.Contains(body, want) {
			t.Fatalf("GET %s body = %q, want it to contain %q", path, body, want)
		}
		if strings.Contains(recorder.Body.String(), "id=\"root\"") {
			t.Fatalf("GET %s served the SPA shell, want the static page", path)
		}
		if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("GET %s Content-Type = %q, want text/html", path, got)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache", path, cache)
		}
		if csp := recorder.Header().Get("Content-Security-Policy"); csp != spaContentSecurityPolicy {
			t.Fatalf("GET %s CSP = %q, want the console CSP", path, csp)
		}
	}
}

func TestSPAHandlerServesDocsDataNoCacheAndAssetsImmutable(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for path, wantCache := range map[string]string{
		"/docs/manifest.json":          "no-cache",
		"/docs/search-index.json":      "no-cache",
		"/docs/search-index.js":        "no-cache",
		"/docs/assets/docs-abc123.css": "public, max-age=31536000, immutable",
	} {
		recorder := serve(router, path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != wantCache {
			t.Fatalf("GET %s Cache-Control = %q, want %q", path, cache, wantCache)
		}
	}
}

func TestSPAHandlerRedirectsDocsDirectories(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for path, wantLocation := range map[string]string{
		"/docs":               "/docs/",
		"/docs?q=health":      "/docs/?q=health",
		"/docs/start":         "/docs/start/",
		"/docs/start?q=roles": "/docs/start/?q=roles",
	} {
		recorder := serve(router, path)
		if recorder.Code != http.StatusMovedPermanently {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusMovedPermanently)
		}
		if location := recorder.Header().Get("Location"); location != wantLocation {
			t.Fatalf("GET %s Location = %q, want %q", path, location, wantLocation)
		}
	}
}

func TestSPAHandlerRejectsMissingAndTraversalDocs(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for _, path := range []string{
		"/docs/missing/",
		"/docs/start/missing",
		"/docs/assets/missing.css",
		"/docs/../index.html",
		"/docs/start/../../index.html",
		"/docs//start/",
	} {
		recorder := serve(router, path)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestSPAHandlerWithoutDocsServesNotice(t *testing.T) {
	dist := builtDist()
	for name := range dist {
		if strings.HasPrefix(name, "docs/") {
			delete(dist, name)
		}
	}
	router := newSPARouter(t, dist)
	recorder := serve(router, "/docs/")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "documentation was not built") {
		t.Fatalf("body %q does not explain the missing documentation build", body)
	}
	// The console itself still serves.
	if recorder := serve(router, "/"); recorder.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestSPAHandlerWithoutBuildServesNotice(t *testing.T) {
	router := newSPARouter(t, fstest.MapFS{
		".gitkeep": &fstest.MapFile{Data: []byte("")},
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	body, _ := io.ReadAll(recorder.Body)
	if !strings.Contains(string(body), "not built") {
		t.Fatalf("body %q does not explain the missing build", string(body))
	}
}

func TestNewSPAHandlerUsesEmbeddedDist(t *testing.T) {
	logger := zerolog.Nop()
	handler, err := NewSPAHandler(&logger)
	if err != nil {
		t.Fatalf("NewSPAHandler error: %v", err)
	}
	// The embedded dist may or may not contain a build in this checkout;
	// the handler must exist either way and report the state coherently.
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	router := chi.NewRouter()
	handler.Register(router)
	router.ServeHTTP(recorder, request)
	if handler.Built() && recorder.Code != http.StatusOK {
		t.Fatalf("built handler status = %d, want 200", recorder.Code)
	}
	if !handler.Built() && recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unbuilt handler status = %d, want 503", recorder.Code)
	}
}
