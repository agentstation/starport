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
	}
}

func newSPARouter(t *testing.T, dist fstest.MapFS) *chi.Mux {
	t.Helper()
	logger := zerolog.Nop()
	handler := newSPAHandler(&logger, dist)
	router := chi.NewRouter()
	handler.Register(router)
	return router
}

func TestSPAHandlerServesIndexForEveryPagePath(t *testing.T) {
	router := newSPARouter(t, builtDist())
	for _, path := range []string{
		"/", "/auth", "/chat", "/docs", "/documents", "/files", "/jobs",
		"/keys", "/models", "/presets", "/providers", "/settings",
		"/tenants", "/usage",
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
