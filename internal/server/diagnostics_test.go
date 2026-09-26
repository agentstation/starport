package server

import (
	"github.com/agentstation/starport/internal/localauth"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOperatorDiagnosticAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/api/v1/admin/info", true},
		{http.MethodGet, "/api/v1/admin/catalog/status", true},
		{http.MethodPost, "/api/v1/admin/info", false},
		{http.MethodGet, "/api/v1/admin/keys", false},
		{http.MethodGet, "/api/v1/admin/info/", false},
		{http.MethodGet, "/api/v1/models", false},
	} {
		if got := isOperatorDiagnostic(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.allowed {
			t.Fatalf("%s %s = %v", tc.method, tc.path, got)
		}
	}
}

func TestOperatorDiagnosticsVerifySessionAndCredentialPriority(t *testing.T) {
	token := sessionToken(t, 1)
	middleware := sessionHarness(t, localauth.NewGate(token, "127.0.0.1"))
	valid := openSession(t, token)
	expired, _, err := localauth.IssueSession(token, localauth.GrantTicket, time.Now().Add(-localauth.SessionTTL-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cookie, bearer string
		allowed              bool
	}{
		{"valid", valid, "", true},
		{"expired", expired, "", false},
		{"invalid", "invalid", "", false},
		{"missing", "", "", false},
		{"explicit_bearer", valid, "chosen-key", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/info", nil)
			r.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: tc.cookie})
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if _, allowed := middleware.operatorDiagnosticContext(r); allowed != tc.allowed {
				t.Fatalf("allowed = %v", allowed)
			}
		})
	}
	middleware.AcceptSessions(localauth.NewGate(sessionToken(t, 2), "127.0.0.1"))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/info", nil)
	r.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: valid})
	if _, allowed := middleware.operatorDiagnosticContext(r); allowed {
		t.Fatal("rotated token retained diagnostic access")
	}
}
