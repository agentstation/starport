package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type signedTestIdentity struct{}

func (signedTestIdentity) Authenticate(string) (string, error) { return "test:person", nil }

func identityAdmissionFixture(t *testing.T) (*AuthMiddleware, identity.Repositories, string) {
	t.Helper()
	var repositories identity.Repositories
	middleware, _, accounts := cachedAuthFixture(t, storage.NewMockStore(), func() (time.Time, bool) { return time.Now(), true }, func(value identity.Repositories) { repositories = value })
	_, err := repositories.Users.Create(t.Context(), identity.User{ID: "person", Subject: "test:person"})
	require.NoError(t, err)
	for _, id := range []string{"account-one", "account-two"} {
		_, err := accounts.Create(t.Context(), account.Account{ID: id, Name: id, Active: true})
		require.NoError(t, err)
	}
	gate := localauth.NewGate(sessionToken(t, 1), "127.0.0.1")
	gate.UseIdentityProvider(signedTestIdentity{})
	cookie, _, err := gate.MintSession(localauth.GrantIdentity, localauth.GrantRequest{Claim: "verified-test-claim"}, time.Now())
	require.NoError(t, err)
	middleware.AcceptSessions(gate)
	return middleware, repositories, cookie
}

func identityRequest(middleware *AuthMiddleware, cookie, selected string, admin bool) (int, context.Context) {
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
	if selected != "" {
		request.Header.Set("X-Starport-Account-ID", selected)
	}
	var current context.Context
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current = r.Context(); w.WriteHeader(http.StatusOK) })
	if admin {
		handler = middleware.RequireAdmin(handler)
	}
	response := httptest.NewRecorder()
	middleware.RequireAPIKey(handler).ServeHTTP(response, request)
	return response.Code, current
}

func TestIdentityAdmissionRequiresGrantedAccountSelection(t *testing.T) {
	middleware, repositories, cookie := identityAdmissionFixture(t)
	status, _ := identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusForbidden, status, "no grant must not select the default account")
	grant := identity.AccountGrant{AccountID: "account-one", UserID: "person"}
	_, err := repositories.AccountGrants.Add(t.Context(), grant)
	require.NoError(t, err)
	status, first := identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "account-one", requestctx.AccountIDOrDefault(first))
	key, ok := requestctx.GetAPIKeyModel(first)
	require.True(t, ok)
	require.False(t, key.HasScope("admin"))
	require.True(t, key.HasScope("chat:write"))
	status, _ = identityRequest(middleware, cookie, "", true)
	require.Equal(t, http.StatusForbidden, status)
	status, _ = identityRequest(middleware, cookie, "default", false)
	require.Equal(t, http.StatusForbidden, status)
	_, err = repositories.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "account-two", UserID: "person"})
	require.NoError(t, err)
	require.ErrorIs(t, inference.CheckPermission(first), authorization.ErrWithdrawn)
	status, _ = identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusConflict, status)
	status, selected := identityRequest(middleware, cookie, "account-one", false)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "account-one", requestctx.AccountIDOrDefault(selected))
	require.NoError(t, repositories.AccountGrants.Remove(t.Context(), grant))
	require.ErrorIs(t, inference.CheckPermission(selected), authorization.ErrWithdrawn)
	status, _ = identityRequest(middleware, cookie, "account-one", false)
	require.Equal(t, http.StatusForbidden, status)
	status, _ = identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusOK, status, "one remaining grant can be selected automatically")
	user, err := repositories.Users.GetByID(t.Context(), "person")
	require.NoError(t, err)
	require.NoError(t, repositories.Users.Delete(t.Context(), user.User.ID, user.Revision))
	status, _ = identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusForbidden, status)
}

func TestIdentityAdmissionTracksMembershipAndDeduplicatesGrants(t *testing.T) {
	middleware, repositories, cookie := identityAdmissionFixture(t)
	_, err := repositories.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
	require.NoError(t, err)
	_, err = repositories.Memberships.Add(t.Context(), identity.Membership{UserID: "person", TeamID: "team"})
	require.NoError(t, err)
	_, err = repositories.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "account-one", TeamID: "team"})
	require.NoError(t, err)
	direct := identity.AccountGrant{AccountID: "account-one", UserID: "person"}
	_, err = repositories.AccountGrants.Add(t.Context(), direct)
	require.NoError(t, err)
	status, _ := identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, repositories.AccountGrants.Remove(t.Context(), direct))
	status, current := identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, repositories.Memberships.Remove(t.Context(), "person", "team"))
	require.ErrorIs(t, inference.CheckPermission(current), authorization.ErrWithdrawn)
	status, _ = identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusForbidden, status)
}

func TestIdentityAdmissionWithoutCacheRefuses(t *testing.T) {
	middleware, _, cookie := identityAdmissionFixture(t)
	middleware.authorization = nil
	status, _ := identityRequest(middleware, cookie, "", false)
	require.Equal(t, http.StatusServiceUnavailable, status)
}
