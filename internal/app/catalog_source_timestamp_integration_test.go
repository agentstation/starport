package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/server"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDevelopmentCatalogAcceptsUnchangedDerivedPublication(t *testing.T) {
	source, err := starmap.New()
	require.NoError(t, err)
	upstream, err := source.CurrentGeneration(t.Context())
	require.NoError(t, err)
	handler, err := server.New(source, server.DefaultConfig())
	require.NoError(t, err)
	catalogServer := httptest.NewServer(handler.Handler())
	t.Cleanup(catalogServer.Close)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
		"STARPORT_CATALOG_SOURCE":                "starmap",
		"STARPORT_CATALOG_SOURCE_URL":            catalogServer.URL + "/api/v1",
		"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_source",
		"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
		"STARPORT_CATALOG_ACQUISITION_SOURCES":   "",
	}).LoadDevelopment(t.Context())
	require.NoError(t, err)
	require.NoError(t, cfg.BindDevelopmentScratch(t.TempDir()))
	cfg.Security.MasterKey = strings.Repeat("d", 32)
	cfg.Security.AuthMode = config.AuthModeDisabled
	application, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	candidate, err := application.catalogRuntime.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, upstream.Manifest.GenerationID, candidate.State.GenerationID)
	require.Equal(t, upstream.Manifest.Payload.Checksum, candidate.State.PayloadChecksum)
	require.NoError(t, application.activateRuntimeState(t.Context(), candidate))
	accepted, err := application.catalogRuntime.AcceptedStore().Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, candidate.State.GenerationID, accepted.Manifest.GenerationID)
	require.Equal(t, candidate.State.GeneratedAt, accepted.Manifest.GeneratedAt)
	require.Equal(t, runtimecatalog.RouteValidationAccepted, application.catalogRuntime.RouteValidation().State)
	require.Equal(t, accepted.Manifest.GenerationID, application.catalog.Current().GenerationID())
	require.Equal(t, upstream.Manifest.Payload.Checksum, application.catalog.Current().PayloadChecksum())
}
