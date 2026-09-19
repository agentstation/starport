package server

import (
	"context"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/requestctx"
	"net/http"
	"time"
)

// isOperatorDiagnostic limits storage-independent local-session access to these reads.
// Inference, mutations, bearer keys, and identity grants keep normal policy checks.
func isOperatorDiagnostic(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/admin/info", "/api/v1/admin/catalog/status":
		return true
	default:
		return false
	}
}

// operatorDiagnosticContext authenticates recovery reads without policy storage.
// An explicit bearer key retains priority over the ambient console cookie.
func (m *AuthMiddleware) operatorDiagnosticContext(r *http.Request) (context.Context, bool) {
	if !isOperatorDiagnostic(r) || extractAPIKey(r) != "" {
		return nil, false
	}
	cookie, err := r.Cookie(localauth.SessionCookie)
	if err != nil {
		return nil, false
	}
	session, err := m.sessions.Verify(cookie.Value, time.Now())
	if err != nil || (session.Grant != localauth.GrantTicket && session.Grant != localauth.GrantLocalToken) {
		return nil, false
	}
	operator := apikey.LocalOperator()
	ctx := requestctx.WithConsoleSession(r.Context(), string(session.Grant), session.Subject)
	ctx = requestctx.WithAPIKeyID(ctx, operator.ID)
	return requestctx.WithAPIKeyModel(ctx, &operator), true
}
