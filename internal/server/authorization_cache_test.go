package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func cachedAuthFixture(t *testing.T, store storage.KVStore, clock authorization.Clock, configure ...func(identity.Repositories)) (*AuthMiddleware, apikey.Repository, account.Repository) {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "policy.db")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	kv, sql := revision.NewKV(store, nil), revision.NewSQL(db, nil)
	ks, err := kv.Initialize(t.Context())
	require.NoError(t, err)
	ss, err := sql.Initialize(t.Context())
	require.NoError(t, err)
	kf, sf := authorization.NewFence("kv", ks.Epoch), authorization.NewFence("sql", ss.Epoch)
	set, err := authorization.NewAuthoritySet(kf, sf)
	require.NoError(t, err)
	keys, err := apikey.Open(store, apikey.WithAuthorizationFence(kf.BeginMutation))
	require.NoError(t, err)
	accounts, err := account.Open(store, account.WithAuthorizationFence(kf.BeginMutation))
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(t.Context())
	require.NoError(t, err)
	repos, err := identity.Open(db, identity.WithAuthorizationFence(sf.BeginMutation))
	require.NoError(t, err)
	for _, setup := range configure {
		setup(repos)
	}
	source, err := authorization.NewRepositorySource(authorization.RepositorySources{Keys: authorization.LocalKeys{Keys: keys, Anonymous: apikey.Anonymous(nil)}, Accounts: accounts, Teams: repos.Teams, KV: kv, SQL: sql, KVAuthority: "kv", SQLAuthority: "sql"}, set, clock, 5*time.Minute)
	require.NoError(t, err)
	cache, err := authorization.NewCache(source, set, authorization.CacheLimits{Entries: 16, Bytes: 1 << 20, BundleBytes: 64 << 10, ConcurrentLoads: 4, TenantLoads: 2, LoadTimeout: time.Second, PermissionLifetime: 5 * time.Minute, ClockUncertainty: 30 * time.Second}, clock)
	require.NoError(t, err)
	t.Cleanup(cache.Close)
	middleware := NewAuthMiddleware(keys, accounts)
	middleware.UseAuthorization(cache, clock)
	return middleware, keys, accounts
}

func TestCachedAuthorizationRetainsRevocationThroughDiscoveryAndAttempts(t *testing.T) {
	store := &authorizationReadStore{KVStore: storage.NewMockStore()}
	_, _, secret := authorizationFixture(t, store)
	now := time.Now()
	middleware, keys, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return now, true })
	var retained *http.Request
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { retained = r; w.WriteHeader(http.StatusOK) }))
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	store.reads.Store(0)
	for range 10 {
		_, err := middleware.discoveryViewer(retained)
		require.NoError(t, err)
		require.NoError(t, inference.CheckPermission(retained.Context()))
	}
	require.Zero(t, store.reads.Load())
	record, err := keys.GetByHash(t.Context(), hashSecret(secret))
	require.NoError(t, err)
	record.APIKey.Active = false
	_, err = keys.Update(t.Context(), record.APIKey, record.Revision)
	require.NoError(t, err)
	store.reads.Store(0)
	_, err = middleware.discoveryViewer(retained)
	require.Error(t, err)
	require.ErrorIs(t, inference.CheckPermission(retained.Context()), authorization.ErrWithdrawn)
	require.Zero(t, store.reads.Load(), "permission recheck must not reload storage")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestCachedAuthorizationUnknownClockRefuses(t *testing.T) {
	store := storage.NewMockStore()
	_, _, secret := authorizationFixture(t, store)
	middleware, _, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return time.Now(), false })
	_, status := resolveStrategy(t, middleware, secret)
	require.Equal(t, http.StatusServiceUnavailable, status)
}

func TestCachedAnonymousPermissionTracksMode(t *testing.T) {
	middleware, _, _ := cachedAuthFixture(t, storage.NewMockStore(), func() (time.Time, bool) { return time.Now(), true })
	policy := authmode.NewPolicy(authmode.Setting{Mode: authmode.Disabled})
	middleware.Govern(policy, nil)
	ctx, err := middleware.anonymousContext(t.Context())
	require.NoError(t, err)
	require.NoError(t, inference.CheckPermission(ctx))
	policy.Set(authmode.Setting{Mode: authmode.Required})
	require.ErrorIs(t, inference.CheckPermission(ctx), authorization.ErrWithdrawn)
}

func TestCachedPermissionKeepsOriginalDeadline(t *testing.T) {
	store := storage.NewMockStore()
	_, _, secret := authorizationFixture(t, store)
	now := time.Now()
	middleware, _, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return now, true })
	ctx, err := middleware.cachedBearer(context.Background(), secret, hashSecret(secret))
	require.NoError(t, err)
	now = now.Add(270 * time.Second)
	require.ErrorIs(t, inference.CheckPermission(ctx), authorization.ErrExpired)
	_, err = middleware.cachedBearer(t.Context(), secret, hashSecret(secret))
	require.NoError(t, err)
	require.ErrorIs(t, inference.CheckPermission(ctx), authorization.ErrExpired, "new load cannot renew old request")
}

func TestCachedTeamBudgetUsesAdmittedPolicy(t *testing.T) {
	store := &authorizationReadStore{KVStore: storage.NewMockStore()}
	_, _, secret := authorizationFixture(t, store)
	now := time.Now()
	var teams identity.TeamRepository
	middleware, keys, _ := cachedAuthFixture(t, store, func() (time.Time, bool) { return now, true }, func(repositories identity.Repositories) {
		teams = repositories.Teams
		_, err := teams.Create(t.Context(), identity.Team{ID: "platform", Name: "Platform", Budget: &limits.TeamBudget{Limit: 10, Interval: limits.IntervalDay}})
		require.NoError(t, err)
	})
	key, err := keys.GetByHash(t.Context(), hashSecret(secret))
	require.NoError(t, err)
	key.APIKey.TeamID = "platform"
	_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	var retained *http.Request
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { retained = r; w.WriteHeader(http.StatusOK) }))
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	server := &Server{auth: middleware, teamBudgets: func(context.Context, string) (*limits.TeamBudget, error) {
		t.Fatal("warm team policy must not read storage")
		return nil, nil
	}}
	store.reads.Store(0)
	budget, err := server.readTeamBudget(retained.Context(), "platform")
	require.NoError(t, err)
	require.Equal(t, int64(10), budget.Limit)
	require.Zero(t, store.reads.Load())
	_, err = server.readTeamBudget(retained.Context(), "other-team")
	require.Error(t, err)
	team, err := teams.GetByID(t.Context(), "platform")
	require.NoError(t, err)
	team.Team.Budget.Limit = 5
	_, err = teams.Update(t.Context(), team.Team, team.Revision)
	require.NoError(t, err)
	_, err = server.readTeamBudget(retained.Context(), "platform")
	require.ErrorIs(t, err, authorization.ErrWithdrawn)
}
