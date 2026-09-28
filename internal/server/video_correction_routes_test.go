package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestVideoCorrectionRoutesRequireAdministrator(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	caller := storeMediaTestKey(t, server, "video-caller", "videos:write")
	admin := storeMediaTestKey(t, server, "video-admin", "*")
	for _, entry := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/admin/accounts/account/videos/job/reconciliation/corrections"},
		{http.MethodGet, "/api/v1/admin/accounts/account/videos/job/reconciliation/corrections/decision"},
	} {
		t.Run(entry.method, func(t *testing.T) {
			registered := false
			require.NoError(t, chi.Walk(server.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
				if method == entry.method && strings.Contains(route, "/reconciliation/corrections") {
					registered = true
				}
				return nil
			}))
			require.True(t, registered)
			for _, key := range []string{"", caller, admin} {
				request := httptest.NewRequest(entry.method, entry.path, strings.NewReader("{}"))
				request.Header.Set("Content-Type", "application/json")
				if key != "" {
					request.Header.Set("Authorization", "Bearer "+key)
				}
				response := httptest.NewRecorder()
				server.router.ServeHTTP(response, request)
				switch key {
				case "":
					require.Equal(t, http.StatusUnauthorized, response.Code)
				case caller:
					require.Equal(t, http.StatusForbidden, response.Code)
				default:
					require.NotEqual(t, http.StatusUnauthorized, response.Code)
					require.NotEqual(t, http.StatusForbidden, response.Code)
					require.NotEqual(t, http.StatusMethodNotAllowed, response.Code)
				}
			}
		})
	}
}
