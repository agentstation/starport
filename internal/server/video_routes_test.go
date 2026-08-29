package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

// videoRoutes are the paths the video job surface publishes in each protocol
// family, with the method a caller reaches each one by.
//
// The list is written out rather than derived from the router, because the
// point of the walk below is to compare what the server built against what this
// gateway promised. A list read out of the router would agree with itself.
var videoRoutes = []struct {
	method string
	path   string
}{
	{method: http.MethodPost, path: "/v1/videos"},
	{method: http.MethodGet, path: "/v1/videos"},
	{method: http.MethodGet, path: "/v1/videos/{video_id}"},
	{method: http.MethodGet, path: "/v1/videos/{video_id}/content"},
	{method: http.MethodPost, path: "/v1/videos/{video_id}/cancel"},
	{method: http.MethodPost, path: "/api/v1/videos"},
	{method: http.MethodGet, path: "/api/v1/videos"},
	{method: http.MethodGet, path: "/api/v1/videos/{video_id}"},
	{method: http.MethodGet, path: "/api/v1/videos/{video_id}/content"},
	{method: http.MethodPost, path: "/api/v1/videos/{video_id}/cancel"},
}

// TestServerRegistersTheVideoPaths walks the router the server builds. A path
// spelled correctly in the route file and mounted under the wrong group reads
// as present to a source scan and answers a caller with 404.
func TestServerRegistersTheVideoPaths(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))

	for _, video := range videoRoutes {
		require.True(t, registered[video.method+" "+video.path],
			"%s %s is not registered", video.method, video.path)
	}
}

// TestVideoRoutesCarryTheVideoScope states the whole access rule for this
// surface. A key holding chat access alone is refused on every path, and a key
// holding videos:write reaches the controller on every path. The second half
// matters as much as the first: a scope no key can satisfy also refuses every
// caller, and a route table that demanded one would pass the first half alone.
func TestVideoRoutesCarryTheVideoScope(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	chatOnly := storeMediaTestKey(t, server, "video-chat-only", "chat:write")
	videoKey := storeMediaTestKey(t, server, "video-writer", "videos:write")

	// A submission with no model reaches the validator, and every other path
	// names a job this account never submitted. Both answers prove the request
	// passed the scope guard and ran the controller.
	reached := map[string]int{
		http.MethodPost + " /v1/videos":                   http.StatusBadRequest,
		http.MethodGet + " /v1/videos":                    http.StatusOK,
		http.MethodGet + " /v1/videos/absent":             http.StatusNotFound,
		http.MethodGet + " /v1/videos/absent/content":     http.StatusNotFound,
		http.MethodPost + " /v1/videos/absent/cancel":     http.StatusNotFound,
		http.MethodPost + " /api/v1/videos":               http.StatusBadRequest,
		http.MethodGet + " /api/v1/videos":                http.StatusOK,
		http.MethodGet + " /api/v1/videos/absent":         http.StatusNotFound,
		http.MethodGet + " /api/v1/videos/absent/content": http.StatusNotFound,
		http.MethodPost + " /api/v1/videos/absent/cancel": http.StatusNotFound,
	}

	for call, expected := range reached {
		method, path, found := strings.Cut(call, " ")
		require.True(t, found, call)
		t.Run(call, func(t *testing.T) {
			refused := videoRequest(server, method, path, chatOnly)
			require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

			allowed := videoRequest(server, method, path, videoKey)
			require.Equal(t, expected, allowed.Code, allowed.Body.String())
		})
	}
}

// TestAnonymousDeploymentReachesTheVideoRoutes covers the operator running with
// authentication disabled. The anonymous key has to carry videos:write, or
// the mode that exists to make a first request work would refuse this surface
// while serving every other one.
func TestAnonymousDeploymentReachesTheVideoRoutes(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	recorder := videoRequest(server, http.MethodGet, "/v1/videos", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

// TestVideoJobOfAnotherAccountIsNotFound is the disclosure rule for a job
// identifier. A caller holding a valid key and a real identifier from another
// account reads not found, not forbidden: a refusal would confirm the
// identifier exists, and the identifier is the only thing such a caller has to
// guess.
func TestVideoJobOfAnotherAccountIsNotFound(t *testing.T) {
	store := storage.NewMockStore()
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store))

	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	owned, err := jobs.New("job-owned-by-acme", "acme", "mock", "mock/video-1",
		routing.OperationVideosGenerations, time.Now())
	require.NoError(t, err)
	require.NoError(t, owned.AdoptProviderJob("provider-side-identifier"))
	require.NoError(t, records.Create(t.Context(), owned))

	// The record itself is unreadable from the other account, and the route
	// answers with the same fact rather than a softer one.
	_, err = records.Get(t.Context(), "globex", owned.ID)
	require.ErrorIs(t, err, jobs.ErrJobNotFound)

	stranger := storeFileTestKeyForAccount(t, server, "video-stranger", "globex", "videos:write")
	for _, path := range []string{
		"/v1/videos/" + owned.ID,
		"/v1/videos/" + owned.ID + "/content",
		"/api/v1/videos/" + owned.ID,
	} {
		recorder := videoRequest(server, http.MethodGet, path, stranger)
		require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
		require.NotContains(t, recorder.Body.String(), "provider-side-identifier")
	}

	cancelled := videoRequest(server, http.MethodPost,
		"/v1/videos/"+owned.ID+"/cancel", stranger)
	require.Equal(t, http.StatusNotFound, cancelled.Code, cancelled.Body.String())

	// The owner still reads its own job, so the answer above is about the
	// account and not about a record no one can reach.
	owner := storeFileTestKeyForAccount(t, server, "video-owner", "acme", "videos:write")
	listed := videoRequest(server, http.MethodGet, "/v1/videos", owner)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), owned.ID)
}

func videoRequest(server *Server, method, path, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}
