package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/console"
)

// docsTestServer is a gateway that requires a key and serves a console
// build with a documentation site.
func docsTestServer(t *testing.T) *Server {
	t.Helper()
	logger := zerolog.Nop()
	dist := fstest.MapFS{
		"index.html":                            {Data: []byte("<!doctype html><title>console</title>")},
		"docs/index.html":                       {Data: []byte("<!doctype html><title>Starport documentation</title>")},
		"docs/troubleshoot/recovery/index.html": {Data: []byte("<!doctype html><title>Recover a gateway</title>")},
		"docs/search-index.json":                {Data: []byte(`{"documents":[]}`)},
		"docs/manifest.json":                    {Data: []byte(`{"starport_release":"dev"}`)},
	}
	return newTestServer(t, &Config{Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20},
		withTestConsole(console.NewSPAHandlerFS(&logger, dist)))
}

func getDocsTestRoute(t *testing.T, server *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

// TestDocsServeWithoutASession is the CSP18 contract: an operator whose
// console does not open can still read the documentation, and serving it
// opens nothing else.
func TestDocsServeWithoutASession(t *testing.T) {
	server := docsTestServer(t)

	for _, target := range []string{"/docs/", "/docs/troubleshoot/recovery/", "/docs/search-index.json", "/docs/manifest.json"} {
		response := getDocsTestRoute(t, server, target)
		require.Equal(t, http.StatusOK, response.Code, target)
	}
	require.Contains(t, getDocsTestRoute(t, server, "/docs/troubleshoot/recovery/").Body.String(), "Recover a gateway")

	root := getDocsTestRoute(t, server, "/docs")
	require.Equal(t, http.StatusMovedPermanently, root.Code)
	require.Equal(t, "/docs/", root.Header().Get("Location"))

	// The documentation is a static site, not a console page: an unknown
	// path is a 404, not the console shell.
	require.Equal(t, http.StatusNotFound, getDocsTestRoute(t, server, "/docs/no-such-topic/").Code)
}

func TestDocsDoNotOpenTheDynamicEndpoints(t *testing.T) {
	server := docsTestServer(t)

	for _, target := range []string{
		"/api/v1/admin/config/effective",
		"/api/v1/admin/audit",
		"/api/v1/admin/info",
		"/v1/models",
	} {
		response := getDocsTestRoute(t, server, target)
		require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.Code,
			"%s answered %d without credentials", target, response.Code)
	}
}
