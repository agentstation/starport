package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
)

// newMembersTestController wires the people plane over a real migrated
// relational store, the same composition the runtime builds. The guards
// under test are about what the repositories actually hold, so a fake
// repository would prove nothing here.
func newMembersTestController(t *testing.T) (*MembersController, identity.Repositories) {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(context.Background()))
	repositories, err := identity.Open(db)
	require.NoError(t, err)
	return NewMembersController(repositories), repositories
}

// membersTestRouter mounts the controller at the same paths routes.go mounts
// it, minus the /api/v1/admin prefix the middleware owns.
func membersTestRouter(controller *MembersController) chi.Router {
	router := chi.NewRouter()
	router.Route("/users", func(r chi.Router) {
		r.Get("/", controller.ListUsers)
		r.Get("/{user_id}/grants", controller.ListUserGrants)
		r.Get("/{user_id}/accounts", controller.ReachableAccounts)
	})
	router.Route("/teams", func(r chi.Router) {
		r.Get("/", controller.ListTeams)
		r.Post("/", controller.CreateTeam)
		r.Delete("/{team_id}", controller.DeleteTeam)
		r.Get("/{team_id}/members", controller.ListTeamMembers)
		r.Put("/{team_id}/members/{user_id}", controller.AddTeamMember)
		r.Delete("/{team_id}/members/{user_id}", controller.RemoveTeamMember)
		r.Get("/{team_id}/grants", controller.ListTeamGrants)
	})
	router.Route("/account-grants", func(r chi.Router) {
		r.Post("/", controller.CreateGrant)
		r.Delete("/", controller.DeleteGrant)
	})
	return router
}

func membersTestCall(router chi.Router, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader([]byte(body))))
	return recorder
}

// TestMembersSurfaceDegradesLoudlyWithoutIdentity states what a deployment
// with no identity configured answers: 503 with the operator's lever named,
// never an empty list that would read as "nobody is here".
func TestMembersSurfaceDegradesLoudlyWithoutIdentity(t *testing.T) {
	router := membersTestRouter(NewMembersController(identity.Repositories{}))

	for _, path := range []string{"/users", "/teams"} {
		recorder := membersTestCall(router, http.MethodGet, path, "")
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, path)
		assert.Contains(t, recorder.Body.String(), "Identity is not configured")
	}
}

// TestListingUsersReportsWhoTheGatewayKnows holds the listing to the stored
// rows and the shared pagination envelope.
func TestListingUsersReportsWhoTheGatewayKnows(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)

	_, err := repositories.Users.Create(context.Background(),
		identity.User{ID: "u-1", Subject: "google:1", DisplayName: "Ada"})
	require.NoError(t, err)
	_, err = repositories.Users.Create(context.Background(),
		identity.User{ID: "u-2", Subject: "google:2", Email: "grace@example.com"})
	require.NoError(t, err)

	recorder := membersTestCall(router, http.MethodGet, "/users", "")
	require.Equal(t, http.StatusOK, recorder.Code)

	var listing struct {
		Users []identity.User `json:"users"`
		Count int             `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listing))
	require.Equal(t, 2, listing.Count)
	assert.Equal(t, "Ada", listing.Users[0].DisplayName)
	assert.Equal(t, "grace@example.com", listing.Users[1].Email)
}

// TestTeamLifecycleThroughTheAdminSurface covers the path an operator
// actually walks: create a team, put a user on it, read the roster, take the
// user off, delete the team.
func TestTeamLifecycleThroughTheAdminSurface(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)

	_, err := repositories.Users.Create(context.Background(),
		identity.User{ID: "u-1", Subject: "google:1"})
	require.NoError(t, err)

	created := membersTestCall(router, http.MethodPost, "/teams", `{"name":"Platform"}`)
	require.Equal(t, http.StatusCreated, created.Code)
	var team identity.Team
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &team))
	require.NotEmpty(t, team.ID)
	assert.Equal(t, "Platform", team.Name)

	// A nameless team is refused before anything is stored.
	require.Equal(t, http.StatusBadRequest,
		membersTestCall(router, http.MethodPost, "/teams", `{}`).Code)

	added := membersTestCall(router, http.MethodPut, "/teams/"+team.ID+"/members/u-1", "")
	require.Equal(t, http.StatusCreated, added.Code)

	// The same fact stated twice is a conflict, and a ghost user is 404.
	require.Equal(t, http.StatusConflict,
		membersTestCall(router, http.MethodPut, "/teams/"+team.ID+"/members/u-1", "").Code)
	require.Equal(t, http.StatusNotFound,
		membersTestCall(router, http.MethodPut, "/teams/"+team.ID+"/members/u-ghost", "").Code)

	roster := membersTestCall(router, http.MethodGet, "/teams/"+team.ID+"/members", "")
	require.Equal(t, http.StatusOK, roster.Code)
	var members struct {
		Members []identity.Membership `json:"members"`
	}
	require.NoError(t, json.Unmarshal(roster.Body.Bytes(), &members))
	require.Len(t, members.Members, 1)
	assert.Equal(t, "u-1", members.Members[0].UserID)

	require.Equal(t, http.StatusOK,
		membersTestCall(router, http.MethodDelete, "/teams/"+team.ID+"/members/u-1", "").Code)
	require.Equal(t, http.StatusNotFound,
		membersTestCall(router, http.MethodDelete, "/teams/"+team.ID+"/members/u-1", "").Code)

	require.Equal(t, http.StatusOK,
		membersTestCall(router, http.MethodDelete, "/teams/"+team.ID, "").Code)
	require.Equal(t, http.StatusNotFound,
		membersTestCall(router, http.MethodGet, "/teams/"+team.ID+"/members", "").Code)
}

// TestGrantingAnAccountThroughTheAdminSurface proves the operator can give
// an account to a user or a team over HTTP, and that the refusals hold: a
// grant naming both grantee kinds, a grantee nobody knows, and the same
// grant stated twice.
func TestGrantingAnAccountThroughTheAdminSurface(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)
	ctx := context.Background()

	_, err := repositories.Users.Create(ctx, identity.User{ID: "u-1", Subject: "google:1"})
	require.NoError(t, err)
	_, err = repositories.Teams.Create(ctx, identity.Team{ID: "t-1", Name: "Platform"})
	require.NoError(t, err)

	direct := membersTestCall(router, http.MethodPost, "/account-grants",
		`{"account_id":"acct-direct","user_id":"u-1"}`)
	require.Equal(t, http.StatusCreated, direct.Code)
	team := membersTestCall(router, http.MethodPost, "/account-grants",
		`{"account_id":"acct-team","team_id":"t-1"}`)
	require.Equal(t, http.StatusCreated, team.Code)

	require.Equal(t, http.StatusBadRequest, membersTestCall(router, http.MethodPost,
		"/account-grants", `{"account_id":"acct-x","user_id":"u-1","team_id":"t-1"}`).Code)
	require.Equal(t, http.StatusNotFound, membersTestCall(router, http.MethodPost,
		"/account-grants", `{"account_id":"acct-x","user_id":"u-ghost"}`).Code)
	require.Equal(t, http.StatusConflict, membersTestCall(router, http.MethodPost,
		"/account-grants", `{"account_id":"acct-direct","user_id":"u-1"}`).Code)

	granted := membersTestCall(router, http.MethodGet, "/users/u-1/grants", "")
	require.Equal(t, http.StatusOK, granted.Code)
	var grants struct {
		Grants []identity.AccountGrant `json:"grants"`
	}
	require.NoError(t, json.Unmarshal(granted.Body.Bytes(), &grants))
	require.Len(t, grants.Grants, 1)
	assert.Equal(t, "acct-direct", grants.Grants[0].AccountID)
}

// TestReachableAccountsFoldDirectAndTeamGrants is the operator's view of the
// same answer the session gate resolves: the direct grant and the one that
// arrives through the team fold into one deduplicated account list.
func TestReachableAccountsFoldDirectAndTeamGrants(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)
	ctx := context.Background()

	_, err := repositories.Users.Create(ctx, identity.User{ID: "u-1", Subject: "google:1"})
	require.NoError(t, err)
	_, err = repositories.Teams.Create(ctx, identity.Team{ID: "t-1", Name: "Platform"})
	require.NoError(t, err)
	_, err = repositories.Memberships.Add(ctx, identity.Membership{UserID: "u-1", TeamID: "t-1"})
	require.NoError(t, err)
	_, err = repositories.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: "acct-direct", UserID: "u-1"})
	require.NoError(t, err)
	_, err = repositories.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: "acct-team", TeamID: "t-1"})
	require.NoError(t, err)

	recorder := membersTestCall(router, http.MethodGet, "/users/u-1/accounts", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	var reachable struct {
		Accounts []string `json:"accounts"`
		Count    int      `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &reachable))
	assert.Equal(t, []string{"acct-direct", "acct-team"}, reachable.Accounts)
	assert.Equal(t, 2, reachable.Count)

	// A user nobody knows is a 404 here: the operator asked about someone
	// specific, and an empty list would hide the typo.
	require.Equal(t, http.StatusNotFound,
		membersTestCall(router, http.MethodGet, "/users/u-ghost/accounts", "").Code)
}

// TestRemovingAGrantNamesTheWholeRow holds the delete to the grant's
// composite identity in the query string, and to 404 for a row that is not
// there.
func TestRemovingAGrantNamesTheWholeRow(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)
	ctx := context.Background()

	_, err := repositories.Users.Create(ctx, identity.User{ID: "u-1", Subject: "google:1"})
	require.NoError(t, err)
	_, err = repositories.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: "acct-direct", UserID: "u-1"})
	require.NoError(t, err)

	query := url.Values{"account_id": {"acct-direct"}, "user_id": {"u-1"}}.Encode()
	require.Equal(t, http.StatusOK,
		membersTestCall(router, http.MethodDelete, "/account-grants?"+query, "").Code)
	require.Equal(t, http.StatusNotFound,
		membersTestCall(router, http.MethodDelete, "/account-grants?"+query, "").Code)

	remaining, err := repositories.AccountGrants.ListByUser(ctx, "u-1")
	require.NoError(t, err)
	assert.Empty(t, remaining)
}
