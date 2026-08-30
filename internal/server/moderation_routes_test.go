package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// moderationsPath is the one path the operation publishes. OpenRouter lists no
// moderation route, so the OpenAI family serves it alone.
const moderationsPath = "/v1/moderations"

// TestServerRegistersTheModerationsPath walks the router the server builds. A
// path spelled correctly in the source and mounted under the wrong group reads
// as present to a source scan and answers 404 to a caller.
func TestServerRegistersTheModerationsPath(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))
	require.True(t, registered["POST "+moderationsPath], "POST %s is not registered", moderationsPath)
}

// TestModerationsCarriesItsOwnScope states the access rule. The scope stands
// alone for the reason the rerank scope does: a moderation request reads the
// caller's own text, so a key that writes chat holds no moderation access
// until an operator grants it.
func TestModerationsCarriesItsOwnScope(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	chatOnly := storeMediaTestKey(t, server, "moderations-chat-only", "chat:write", "embeddings:write")
	moderationKey := storeMediaTestKey(t, server, "moderations-writer", "moderations:write")

	refused := postMediaRequest(server, moderationsPath, chatOnly)
	require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

	// An empty JSON body reaches the handler and fails at the codec, which
	// names no model and no input. That answer proves the controller ran
	// rather than the scope guard.
	allowed := postMediaRequest(server, moderationsPath, moderationKey)
	require.Equal(t, http.StatusBadRequest, allowed.Code, allowed.Body.String())
}

// TestAnonymousDeploymentReachesTheModerationsRoute covers the operator who
// runs with authentication disabled. The anonymous key has to carry the scope,
// or the mode that exists to make the first request work would refuse this one.
func TestAnonymousDeploymentReachesTheModerationsRoute(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	recorder := postMediaRequest(server, moderationsPath, "")
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}

// TestModerationsRefusesAnUncataloguedModel covers the caller who misspells a
// model. The answer is 404 and not the 503 every other routing failure
// answers, because no wait produces a model the catalog never held.
func TestModerationsRefusesAnUncataloguedModel(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	key := storeMediaTestKey(t, server, "moderations-unknown-model", "moderations:write")
	body := `{"model":"no-such-moderation-model","input":"some text"}`

	request := httptest.NewRequest(http.MethodPost, moderationsPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "no-such-moderation-model")
}

// TestModerationsAcceptsACataloguedModelName is the other half of the guard.
// The test server holds no provider credential, so the request still fails.
// It fails as a route the gateway could not reach, which is the answer an
// operator can act on.
func TestModerationsAcceptsACataloguedModelName(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withRoutableCatalog())
	key := storeMediaTestKey(t, server, "moderations-known-model", "moderations:write")
	body := `{"model":"openai/omni-moderation-latest","input":"some text"}`

	request := httptest.NewRequest(http.MethodPost, moderationsPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
}
