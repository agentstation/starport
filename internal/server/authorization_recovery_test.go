package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCachedCallerModesRetainMemoryRecoveryWithdrawal(t *testing.T) {
	for _, mode := range []string{"bearer", "anonymous", "operator", "session"} {
		t.Run(mode, func(t *testing.T) {
			store := &authorizationReadStore{KVStore: storage.NewMockStore()}
			_, _, secret := authorizationFixture(t, store)
			middleware, _, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return time.Now(), true })
			var closed atomic.Bool
			privateDiagnostic := errors.New("private recovery diagnostic")
			middleware.recoveryAdmission = func() error {
				if closed.Load() {
					return privateDiagnostic
				}
				return nil
			}
			token := sessionToken(t, 1)
			middleware.AcceptSessions(localauth.NewGate(token, "127.0.0.1"))
			cookie := openSession(t, token)
			resolve := func() (context.Context, error) {
				switch mode {
				case "bearer":
					return middleware.cachedBearer(t.Context(), secret, hashSecret(secret))
				case "anonymous":
					middleware.Govern(authmode.NewPolicy(authmode.Setting{Mode: authmode.Disabled}), nil)
					return middleware.anonymousContext(t.Context())
				case "session":
					request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
					return middleware.sessionContext(request)
				default:
					return middleware.cachedLocal(t.Context(), authorization.OperatorSubject, nil)
				}
			}
			ctx, err := resolve()
			require.NoError(t, err)
			store.reads.Store(0)
			for range 10 {
				require.NoError(t, inference.CheckPermission(ctx))
			}
			require.Zero(t, store.reads.Load())
			closed.Store(true)
			require.ErrorIs(t, inference.CheckPermission(ctx), authorization.ErrUnavailable)
			require.Zero(t, store.reads.Load(), "known withdrawal rechecks cannot read storage")
			_, err = resolve()
			if mode == "session" {
				require.ErrorIs(t, err, errAccountUnavailable)
			} else {
				require.ErrorIs(t, err, authorization.ErrUnavailable)
			}
			require.Zero(t, store.reads.Load(), "a warmed caller cannot reload permission after known withdrawal")
			response := httptest.NewRecorder()
			writeAuthorizationRefusal(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), err)
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Equal(t, "1", response.Header().Get("Retry-After"))
			require.NotContains(t, response.Body.String(), "private recovery diagnostic")
			diagnostic := httptest.NewRequest(http.MethodGet, "/api/v1/admin/info", nil)
			diagnostic.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
			_, allowed := middleware.operatorDiagnosticContext(diagnostic)
			require.True(t, allowed, "verified local recovery diagnostics remain available")
		})
	}
}
