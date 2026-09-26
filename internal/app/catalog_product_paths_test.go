package app

import (
	"encoding/json"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogPersistsProductBaselineAndOwnershipOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gateway")
	cfg, err := config.NewLoader().WithEnvironment(map[string]string{
		"STARPORT_HOME":                        home,
		"STARPORT_INSTANCE_ID":                 "gateway-two",
		"STARPORT_DEPLOYMENT_ID":               "team-local",
		"STARPORT_CATALOG_NETWORK_MODE":        "offline",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(cfg.EffectivePaths().BadgerDir, 0700))
	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	require.NoError(t, err)
	connected, err := runtimecatalog.OpenRuntime(t.Context(), store, catalogSettings(cfg), nil)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	_, acceptedErr := connected.AcceptedGeneration(t.Context())
	require.ErrorIs(t, acceptedErr, starmaperrors.ErrNotFound)
	scheduler := connected.Status().InstanceIdentity
	require.NoError(t, connected.Close(t.Context()))
	require.NoError(t, store.Close())
	manifests, err := filepath.Glob(filepath.Join(home, "data", "catalog", "baseline", "*", "manifest.json"))
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(manifests[0]), "catalog.json"))
	require.NoError(t, err)
	require.True(t, json.Valid(payload))
	owner, err := os.ReadFile(filepath.Join(home, "state", "catalog", "runtime", "gateway-two", "owner.json"))
	require.NoError(t, err)
	var identity struct{ Product, Deployment, Instance string }
	require.NoError(t, json.Unmarshal(owner, &identity))
	require.Equal(t, "starport", identity.Product)
	require.Equal(t, "team-local", identity.Deployment)
	require.Equal(t, "gateway-two", identity.Instance)
	// An unchanged restart must preserve and verify the same export.
	store, err = storage.Open(cfg.Storage.RuntimeStorage())
	require.NoError(t, err)
	defer store.Close()
	again, err := runtimecatalog.OpenRuntime(t.Context(), store, catalogSettings(cfg), nil)
	require.NoError(t, err)
	require.Equal(t, scheduler, again.Status().InstanceIdentity)
	require.Equal(t, candidate.State.GenerationID, again.ControlPlane().Current().GenerationID())
	_, acceptedErr = again.AcceptedGeneration(t.Context())
	require.ErrorIs(t, acceptedErr, starmaperrors.ErrNotFound)
	require.NoError(t, again.Close(t.Context()))
	retained, err := os.ReadFile(filepath.Join(filepath.Dir(manifests[0]), "catalog.json"))
	require.NoError(t, err)
	require.Equal(t, payload, retained)
}

func TestCatalogOwnerConflictRefusesBeforeGatewayStores(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gateway")
	cfg, err := config.NewLoader().WithEnvironment(map[string]string{
		"STARPORT_HOME": home, "STARPORT_SECURITY_MASTER_KEY": strings.Repeat("m", 32),
		"STARPORT_CATALOG_NETWORK_MODE": "offline", "STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	foreign := catalogSettings(cfg)
	foreign.DeploymentID = "another-deployment"
	foreign.BaselineDirectory = ""
	connected, err := runtimecatalog.OpenRuntime(t.Context(), storage.NewMockStore(), foreign, nil)
	require.NoError(t, err)
	require.NoError(t, connected.Close(t.Context()))
	application, err := New(cfg)
	if application != nil {
		require.NoError(t, application.Close(t.Context()))
	}
	require.Error(t, err)
	require.NoDirExists(t, cfg.EffectivePaths().BaselineDir)
	require.NoDirExists(t, cfg.Storage.Badger.Path)
	require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
}
