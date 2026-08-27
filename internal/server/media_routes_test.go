package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
)

// mediaRoutes are the eight paths the two protocol families publish for the
// dedicated media operations, with the scope each one demands. OpenRouter
// publishes no image edit path and no translation path, so its list is shorter
// rather than padded with paths its own clients cannot call.
var mediaRoutes = []struct {
	path  string
	scope string
}{
	{path: "/v1/images/generations", scope: "images:write"},
	{path: "/v1/images/edits", scope: "images:write"},
	{path: "/v1/audio/speech", scope: "audio:write"},
	{path: "/v1/audio/transcriptions", scope: "audio:write"},
	{path: "/v1/audio/translations", scope: "audio:write"},
	{path: "/api/v1/images", scope: "images:write"},
	{path: "/api/v1/audio/speech", scope: "audio:write"},
	{path: "/api/v1/audio/transcriptions", scope: "audio:write"},
}

// TestServerRegistersTheMediaPaths walks the router the server builds, rather
// than reading the file that builds it. A path registered under the wrong
// group, or spelled correctly in one place and mounted somewhere else, reads
// as present to a source scan and returns 404 to a caller.
func TestServerRegistersTheMediaPaths(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))

	for _, media := range mediaRoutes {
		require.True(t, registered["POST "+media.path], "POST %s is not registered", media.path)
	}
}

// TestMediaRoutesCarryTheirScopes states the whole access rule for the media
// surface: a key holding chat access alone is refused, and a key holding the
// media scopes reaches the controller. The second half matters as much as the
// first, because a scope no key can satisfy also refuses every caller.
func TestMediaRoutesCarryTheirScopes(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	chatOnly := storeMediaTestKey(t, server, "media-chat-only", "chat:write")
	mediaKey := storeMediaTestKey(t, server, "media-writer", "images:write", "audio:write")

	for _, media := range mediaRoutes {
		t.Run(media.path, func(t *testing.T) {
			refused := postMediaRequest(server, media.path, chatOnly)
			require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

			// An empty JSON body reaches every media handler and fails there:
			// the two JSON paths find no model, and the multipart paths find
			// no form. Both answers prove the controller ran.
			allowed := postMediaRequest(server, media.path, mediaKey)
			require.Equal(t, http.StatusBadRequest, allowed.Code, allowed.Body.String())
		})
	}
}

// TestAnonymousDeploymentReachesTheMediaRoutes covers the operator who runs
// with authentication disabled. The anonymous identity has to carry the two
// media scopes, or the mode that exists to make the first request work would
// refuse half the surface.
func TestAnonymousDeploymentReachesTheMediaRoutes(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	for _, media := range mediaRoutes {
		t.Run(media.path, func(t *testing.T) {
			recorder := postMediaRequest(server, media.path, "")
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func storeMediaTestKey(t *testing.T, server *Server, id string, scopes ...string) string {
	t.Helper()
	secret := "sk-starport-" + id
	hash := sha256.Sum256([]byte(secret))
	_, err := server.identities.Create(context.Background(), identity.APIKey{
		ID:        id,
		Name:      id,
		Hash:      hex.EncodeToString(hash[:]),
		Scopes:    scopes,
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return secret
}

func postMediaRequest(server *Server, path, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}
