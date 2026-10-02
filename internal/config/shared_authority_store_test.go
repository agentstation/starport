package config_test

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/sqlstore"
)

// TestSharedAuthorityNamespaceMismatchRefusesStartup stores a head under the
// deployment ID with a namespace that the bootstrap identity does not derive.
// The read and the apply each refuse and name the stored namespace and
// deployment ID.
func TestSharedAuthorityNamespaceMismatchRefusesStartup(t *testing.T) {
	directory := t.TempDir()
	deploymentID := "namespace-test-" + rand.Text()
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(directory)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CONFIG_MANAGEMENT": config.ManagementShared,
		"STARPORT_DEPLOYMENT_ID":     deploymentID,
	}).Load(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, cfg.ConfigNamespace())

	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(directory, "starport.db")}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(t.Context()))
	trail, err := audit.Open(db, 0)
	require.NoError(t, err)

	const stored = "starport:v1:other-namespace:"
	writer, err := configrevision.New(db, trail, nil, deploymentID, stored)
	require.NoError(t, err)
	written, err := writer.Initialize(t.Context(), map[string]string{catalogconfig.AcquisitionInterval: "5m"}, "operator", "initialize-"+rand.Text())
	require.NoError(t, err)

	reader, err := configrevision.New(db, trail, nil, cfg.EffectivePaths().DeploymentID, cfg.ConfigNamespace())
	require.NoError(t, err)
	want := config.AuthorityMismatchError{Setting: config.NamespaceSetting, Configured: cfg.ConfigNamespace(), Stored: stored, DeploymentID: deploymentID}
	for _, read := range []func() error{
		func() error { _, err := reader.Head(t.Context()); return err },
		func() error { _, err := reader.Current(t.Context()); return err },
	} {
		err := read()
		var mismatch *config.AuthorityMismatchError
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, want, *mismatch)
		require.ErrorContains(t, err, stored)
		require.ErrorContains(t, err, deploymentID)
		require.NotErrorIs(t, err, configrevision.ErrNotInitialized)
	}

	err = cfg.ApplySharedRevision(written.Shared())
	var mismatch *config.AuthorityMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, want, *mismatch)
	require.False(t, cfg.SharedRevisionApplied(), "a refused revision applies nothing")
}
