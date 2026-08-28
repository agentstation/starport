package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// rerankRoutes are the two paths the protocol families publish for the rerank
// operation. Both plan one route and reach one provider, so they appear here as
// one list rather than as two tests that drift apart.
var rerankRoutes = []string{"/v1/rerank", "/api/v1/rerank"}

// TestServerRegistersTheRerankPaths walks the router the server builds. A path
// spelled correctly in the source and mounted under the wrong group reads as
// present to a source scan and answers 404 to a caller.
func TestServerRegistersTheRerankPaths(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))

	for _, path := range rerankRoutes {
		require.True(t, registered["POST "+path], "POST %s is not registered", path)
	}
}

// TestRerankRoutesCarryTheRerankScope states the access rule. The scope stands
// alone: a key that writes chat and embeddings holds no rerank access, because
// a rerank request reads the caller's own documents. The second half matters as
// much as the first, because a scope no key can satisfy refuses every caller.
func TestRerankRoutesCarryTheRerankScope(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	chatOnly := storeMediaTestKey(t, server, "rerank-chat-only", "chat:write", "embeddings:write")
	rerankKey := storeMediaTestKey(t, server, "rerank-writer", "rerank:write")

	for _, path := range rerankRoutes {
		t.Run(path, func(t *testing.T) {
			refused := postMediaRequest(server, path, chatOnly)
			require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

			// An empty JSON body reaches the handler and fails at the codec,
			// which names no model, no query, and no documents. That answer
			// proves the controller ran rather than the scope guard.
			allowed := postMediaRequest(server, path, rerankKey)
			require.Equal(t, http.StatusBadRequest, allowed.Code, allowed.Body.String())
		})
	}
}

// TestAnonymousDeploymentReachesTheRerankRoutes covers the operator who runs
// with authentication disabled. The anonymous identity has to carry the scope,
// or the mode that exists to make the first request work would refuse this one.
func TestAnonymousDeploymentReachesTheRerankRoutes(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	for _, path := range rerankRoutes {
		t.Run(path, func(t *testing.T) {
			recorder := postMediaRequest(server, path, "")
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

// TestRerankRefusesAnUncataloguedModel covers the caller who misspells a model.
// The answer is 404 and not the 503 every other routing failure answers,
// because no wait produces a model the catalog never held. Both families answer
// alike, which is the observable half of sharing one route plan.
func TestRerankRefusesAnUncataloguedModel(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	key := storeMediaTestKey(t, server, "rerank-unknown-model", "rerank:write")
	body := `{"model":"no-such-rerank-model","query":"q","documents":["a","b"]}`

	for _, path := range rerankRoutes {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "no-such-rerank-model")
		})
	}
}

// TestRerankAcceptsACataloguedModelName is the other half of the guard. A check
// that refused every name would pass the test above and break every real
// request, so this one sends a rerank model the catalog holds and asserts the
// gateway does not call it unknown. The test server registers no Cohere
// adapter, so the request still fails. It fails as a route the gateway could
// not reach, which is the answer an operator can act on.
func TestRerankAcceptsACataloguedModelName(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	key := storeMediaTestKey(t, server, "rerank-known-model", "rerank:write")
	body := `{"model":"cohere/rerank-v3.5","query":"q","documents":["a","b"]}`

	for _, path := range rerankRoutes {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
		})
	}
}
