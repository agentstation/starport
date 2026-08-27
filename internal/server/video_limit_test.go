package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/tenant"
)

// submitVideo posts one submission naming a model. The scope tests post an
// empty body because they only need to reach the controller; this file needs to
// reach the job service, which happens after the body validates.
func submitVideo(server *Server, key string) *httptest.ResponseRecorder {
	body := `{"model":"mock/video-1","prompt":"a lighthouse at dusk"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}

// TestAnAccountAtItsOutstandingJobLimitReadsARefusal is the whole limit as a
// caller meets it.
//
// The three halves are here because none of them proves the surface on its own.
// A meter test proves the arithmetic and says nothing about which status a
// caller reads. A controller that mapped the refusal to 500 would still pass
// every test in internal/limits. And a refusal that arrived after the gateway
// had already resolved a route and a credential would bound what this account
// reads rather than what it spends, which is the opposite of the point.
//
// The routing failure is the marker for that last half. This test server has no
// video model to route to, so a submission that gets past the limit reads 503.
// Reading 503 therefore proves the submission was admitted and reached the
// router; reading 429 before it proves the limit answered first.
func TestAnAccountAtItsOutstandingJobLimitReadsARefusal(t *testing.T) {
	store := storage.NewMockStore()
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store))

	bound := int64(1)
	key := storeFileTestKeyWithLimits(t, server, "video-bounded",
		&limits.Limits{OutstandingJobs: &bound}, "videos:write")

	// Hold the account's one slot the way a queued job holds it. Submitting a
	// real job first is not available here: nothing in this server routes a
	// video, so no submission ever reaches a record.
	meter, err := limits.NewJobMeter(store)
	require.NoError(t, err)
	require.NoError(t, meter.Reserve(t.Context(), tenant.DefaultID, 1, bound))

	refused := submitVideo(server, key)
	require.Equal(t, http.StatusTooManyRequests, refused.Code, refused.Body.String())

	// An operator raising the limit needs the number that refused, and a caller
	// backing off needs to know it is holding work rather than being throttled.
	body := refused.Body.String()
	require.Contains(t, body, "outstanding job")
	require.Contains(t, body, strconv.FormatInt(bound, 10))
	require.Contains(t, body, "rate_limit_error")

	// The slot comes back when a job ends. The same request is then admitted
	// and fails further along, at the router.
	require.NoError(t, meter.Release(t.Context(), tenant.DefaultID, 1))
	admitted := submitVideo(server, key)
	require.Equal(t, http.StatusServiceUnavailable, admitted.Code, admitted.Body.String())
	require.NotContains(t, admitted.Body.String(), "outstanding job")
}

// TestAnAccountThatStatesNoOutstandingJobLimitStillHasOne keeps the default off
// the request path. A deployment whose operator never set a limit is not
// unbounded: one key could otherwise hold an unbounded spend commitment open.
func TestAnAccountThatStatesNoOutstandingJobLimitStillHasOne(t *testing.T) {
	store := storage.NewMockStore()
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store))
	key := storeFileTestKey(t, server, "video-default-bound", "videos:write")

	meter, err := limits.NewJobMeter(store)
	require.NoError(t, err)
	// Fill the default. The number itself belongs to internal/tenant; what this
	// asserts is that the route reads it rather than treating an absent limit
	// as no limit.
	filled := tenant.DefaultOutstandingJobs
	require.NoError(t, meter.Reserve(t.Context(), tenant.DefaultID, filled, filled))

	refused := submitVideo(server, key)
	require.Equal(t, http.StatusTooManyRequests, refused.Code, refused.Body.String())
}
