package server

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoReconciliationRequiresAdministrator(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	reader := createServerAPIKey(t, server, "video-reader", []string{"videos:write"})
	admin := createServerAPIKey(t, server, "video-admin", []string{"admin"})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, tc := range []struct {
			name, token string
			status      int
		}{
			{"unauthenticated", "", http.StatusUnauthorized},
			{"job owner", reader, http.StatusForbidden},
			{"administrator", admin, http.StatusNotFound},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				response := httptest.NewRecorder()
				request := templateJSONRequest(method, "/api/v1/admin/accounts/default/videos/job/reconciliation", tc.token, `{}`)
				server.router.ServeHTTP(response, request)
				require.Equal(t, tc.status, response.Code, response.Body.String())
			})
		}
	}
}

func TestVideoReconciliationRefusesAnonymousAdministrator(t *testing.T) {
	server := newTestServer(t, unauthenticatedConfig("admin"))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := httptest.NewRecorder()
		request := templateJSONRequest(method, "/api/v1/admin/accounts/default/videos/job/reconciliation", "", `{}`)
		server.router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	}
}

func TestVideoReconciliationRejectsClientAuditFields(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	admin := createServerAPIKey(t, server, "video-admin", []string{"admin"})
	for _, body := range []string{`{"actor":"other-admin"}`, `{"decided_at":"2026-09-27T00:00:00Z"}`, `{"decision_id":"a","decision_id":"b"}`, `{} {}`} {
		response := httptest.NewRecorder()
		request := templateJSONRequest(http.MethodPost, "/api/v1/admin/accounts/default/videos/job/reconciliation", admin, body)
		server.router.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}
