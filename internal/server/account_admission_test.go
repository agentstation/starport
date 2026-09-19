package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/storage"
)

type admissionAccountReader struct {
	record account.Record
	err    error
}

func (r admissionAccountReader) GetByID(context.Context, string) (account.Record, error) {
	return r.record, r.err
}

func TestAccountAdmissionAcrossCallerModes(t *testing.T) {
	for _, mode := range []string{"bearer", "anonymous", "session"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				reader AccountReader
				status int
			}{
				{name: "unconfigured", status: http.StatusServiceUnavailable},
				{name: "unavailable", reader: admissionAccountReader{err: storage.ErrStorageClosed}, status: http.StatusServiceUnavailable},
				{name: "missing", reader: admissionAccountReader{err: account.ErrNotFound}, status: http.StatusForbidden},
				{name: "inactive", reader: admissionAccountReader{record: account.Record{Revision: 1, Account: account.Account{ID: account.DefaultID}}}, status: http.StatusForbidden},
				{name: "foreign", reader: admissionAccountReader{record: account.Record{Revision: 1, Account: account.Account{ID: "foreign", Active: true}}}, status: http.StatusServiceUnavailable},
				{name: "unversioned", reader: admissionAccountReader{record: account.Record{Account: account.Account{ID: account.DefaultID, Active: true}}}, status: http.StatusServiceUnavailable},
				{name: "valid", reader: admissionAccountReader{record: account.Record{Revision: 1, Account: account.Account{ID: account.DefaultID, Active: true}}}, status: http.StatusNoContent},
			} {
				t.Run(tc.name, func(t *testing.T) {
					keys, err := apikey.Open(storage.NewMockStore())
					require.NoError(t, err)
					const secret = "test-admission-secret"
					_, err = keys.Create(t.Context(), apikey.APIKey{ID: "STARPORT_admission", Name: "Admission", Hash: hashSecret(secret), AccountID: account.DefaultID, Active: true, Scopes: []string{"chat:write"}})
					require.NoError(t, err)
					middleware := NewAuthMiddleware(keys, tc.reader)
					request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					switch mode {
					case "bearer":
						request.Header.Set("Authorization", "Bearer "+secret)
					case "anonymous":
						middleware.Govern(authmode.NewPolicy(authmode.Setting{Mode: authmode.Disabled}), nil)
					case "session":
						token := sessionToken(t, 1)
						middleware.AcceptSessions(localauth.NewGate(token, "127.0.0.1"))
						request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: openSession(t, token)})
					}
					admitted := false
					handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						admitted = true
						w.WriteHeader(http.StatusNoContent)
					}))
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					require.Equal(t, tc.status, response.Code, response.Body.String())
					require.Equal(t, tc.status == http.StatusNoContent, admitted)
					if tc.status == http.StatusServiceUnavailable {
						require.Equal(t, "1", response.Header().Get("Retry-After"))
					}
					require.NotContains(t, response.Body.String(), secret)
				})
			}
		})
	}
}

func TestAccountAuthorityFailureKeepsLivenessAvailable(t *testing.T) {
	server := newTestServer(t, &Config{Port: 8080})
	server.auth.accounts = admissionAccountReader{err: storage.ErrStorageClosed}
	response := doRequest(t, server, http.MethodGet, "/health/live")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}
