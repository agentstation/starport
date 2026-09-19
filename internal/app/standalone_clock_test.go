package app

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

func TestStandaloneAuthorizationNeedsNoNativeClock(t *testing.T) {
	factories := explicitTestFactories()
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return newBlockingHTTPRuntime(), nil
	}
	application, err := New(validProductionConfig(t), withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	now, healthy := dependencies.PermissionClock()
	require.True(t, healthy)
	require.False(t, now.IsZero())
	require.NotEqual(t, now.Round(0), now, "standalone permission time must retain Go's monotonic reading")
	bundle, err := dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: testAPIKey().Hash})
	require.NoError(t, err)
	require.NotEqual(t, bundle.Permit().Deadline().Round(0), bundle.Permit().Deadline())
	require.True(t, dependencies.Readiness())
}

func TestStandaloneAuthorizationCannotReplaceCatalogAuthority(t *testing.T) {
	loaded, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CATALOG_NETWORK_MODE":          "offline",
		"STARPORT_CATALOG_SOURCE":                "starmap",
		"STARPORT_CATALOG_SOURCE_URL":            "https://catalog.example.com/api/v1",
		"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_authority",
		"STARPORT_CATALOG_SOURCE_AUTHORITY_ID":   "enterprise",
		"STARPORT_CATALOG_SOURCE_POLICY_ID":      "production",
		"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
	}).Load(t.Context())
	require.NoError(t, err)
	cfg := validProductionConfig(t)
	cfg.Catalog = loaded.Catalog
	factories := explicitTestFactories()
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return newBlockingHTTPRuntime(), nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	_, err = dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: testAPIKey().Hash})
	require.NoError(t, err, "local policy does not require native time")
	require.False(t, dependencies.Readiness(), "local policy cannot replace missing catalog authority")
	lease, err := application.registry.AcquireRuntime()
	require.NoError(t, err)
	defer lease.Release()
	require.False(t, lease.Snapshot().AllowsNewAttempt())
	require.False(t, application.catalogRuntime.PermissionClock().Known)
}
