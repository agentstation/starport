package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs/permission"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReadinessTracksAdmissionWithoutBlockingLiveness(t *testing.T) {
	factories := explicitTestFactories()
	openCatalog := factories.openCatalog
	sampled := &sampledCatalogRuntime{}
	factories.openCatalog = func(ctx context.Context, store storage.KVStore, settings runtimecatalog.Settings, lookup runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		runtime, err := openCatalog(ctx, store, settings, lookup)
		sampled.catalogRuntime = runtime
		return sampled, err
	}
	token, err := localauth.Mint(1, time.Now())
	require.NoError(t, err)
	var httpServer *server.Server
	factories.newServer = func(cfg *server.Config, deps server.Dependencies) (httpRuntime, error) {
		var err error
		deps.LocalGate = localauth.NewGate(token, "127.0.0.1")
		httpServer, err = server.New(cfg, deps)
		return httpServer, err
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = "redis://127.0.0.1:6379"
	cfg.Storage.Valkey.MaxConnections = 10
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	check := func(want int) {
		t.Helper()
		for path, status := range map[string]int{"/health/ready": want, "/health/live": http.StatusOK} {
			response := httptest.NewRecorder()
			httpServer.Router().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, status, response.Code, path)
			if status == http.StatusServiceUnavailable {
				require.Contains(t, response.Body.String(), `"status":"not_ready"`)
				require.Equal(t, "1", response.Header().Get("Retry-After"))
			}
		}
	}
	check(http.StatusOK)
	sampled.sample.Store(&permission.ClockReading{Time: time.Now(), Known: true, Uncertainty: time.Second})
	check(http.StatusOK)
	sampled.sample.Store(&permission.ClockReading{})
	check(http.StatusOK)
	sampled.sample.Store(&permission.ClockReading{Time: time.Now(), Known: true, Uncertainty: time.Second})
	check(http.StatusOK)
	application.authorization.sql.Withdraw()
	check(http.StatusServiceUnavailable)
	cookie, _, err := localauth.IssueSession(token, localauth.GrantTicket, time.Now())
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/info", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
	response := httptest.NewRecorder()
	httpServer.Router().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"authorization"`)
	require.Contains(t, response.Body.String(), `"authority":"identity-policy"`)
	require.Contains(t, response.Body.String(), `"state":"withdrawn"`)
	require.Contains(t, response.Body.String(), `"recovery":"reinitialize_authority_epoch"`)
	response = httptest.NewRecorder()
	httpServer.Router().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/info", nil))
	require.NotEqual(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), `"authorization"`)
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/chat/completions"},
		{http.MethodGet, "/api/v1/admin/keys/"},
		{http.MethodPost, "/api/v1/admin/keys/"},
	} {
		request := httptest.NewRequest(target.method, target.path, nil)
		request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
		response := httptest.NewRecorder()
		httpServer.Router().ServeHTTP(response, request)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, target.path)
	}

}
