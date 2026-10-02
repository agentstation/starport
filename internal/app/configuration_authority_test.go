package app

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/audit"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// sharedConfigurationDeployment is one shared-management deployment: its
// replicas share one relational store and keep their own key-value stores.
type sharedConfigurationDeployment struct {
	environment map[string]string
}

// configurationReplica is one started process of the deployment.
type configurationReplica struct {
	app      *App
	cfg      *config.Config
	db       *sqlstore.DB
	file     string
	settings runtimecatalog.Settings
}

// observedCatalogRuntime reports an observation time, so the catalog status
// states the next source read from the applied poll interval.
type observedCatalogRuntime struct {
	*lifecycleCatalogRuntime
	observedAt time.Time
}

func (r observedCatalogRuntime) Status() runtime.Status {
	return runtime.Status{ObservedAt: r.observedAt}
}

func newSharedConfigurationDeployment(t *testing.T) sharedConfigurationDeployment {
	t.Helper()
	return sharedConfigurationDeployment{environment: map[string]string{
		"STARPORT_CONFIG_MANAGEMENT":           config.ManagementShared,
		"STARPORT_DEPLOYMENT_ID":               "configuration-test-" + rand.Text(),
		"STARPORT_STORAGE_SQL_SQLITE_PATH":     filepath.Join(t.TempDir(), "starport.db"),
		"STARPORT_SECURITY_MASTER_KEY":         strings.Repeat("k", 32),
		"STARPORT_CATALOG_SOURCE":              config.CatalogSourceEmbedded,
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}}
}

// load reads one process configuration. The product file holds the given
// local deployment values.
func (d sharedConfigurationDeployment) load(t *testing.T, fileValues string) (*config.Config, string) {
	t.Helper()
	directory := t.TempDir()
	file := filepath.Join(directory, "starport.env")
	require.NoError(t, os.WriteFile(file, []byte(fileValues), 0o600))
	environment := map[string]string{"STARPORT_CATALOG_STATE_DIR": filepath.Join(directory, "catalog-state")}
	for name, value := range d.environment {
		environment[name] = value
	}
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(directory)).WithEnvironment(environment).WithEnvFiles(file).Load(t.Context())
	require.NoError(t, err)
	return cfg, file
}

// initialize writes revision 1 from an operator configuration whose file
// selects a 30 second source poll interval.
func (d sharedConfigurationDeployment) initialize(t *testing.T) configrevision.Revision {
	t.Helper()
	operator, _ := d.load(t, "STARPORT_CATALOG_SOURCE_POLL_INTERVAL=30s\n")
	result, err := InitializeSharedConfiguration(t.Context(), operator, configrevision.Request{OperationID: "initialize-" + rand.Text()})
	require.NoError(t, err)
	require.True(t, result.Written)
	require.Equal(t, int64(1), result.Revision.Sequence)
	return result.Revision
}

// write stores revision 1 through the store API with the given sealer and
// namespace. An empty namespace selects the one that the deployment derives.
func (d sharedConfigurationDeployment) write(t *testing.T, sealer configrevision.Sealer, namespace string, values map[string]string) {
	t.Helper()
	cfg, _ := d.load(t, "")
	if namespace == "" {
		namespace = cfg.ConfigNamespace()
	}
	db, err := openSQL(cfg.Storage)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.Migrate(t.Context()))
	trail, err := audit.Open(db, 0)
	require.NoError(t, err)
	store, err := configrevision.New(db, trail, sealer, cfg.EffectivePaths().DeploymentID, namespace)
	require.NoError(t, err)
	_, err = store.Initialize(t.Context(), values, "operator:test", "initialize-"+rand.Text())
	require.NoError(t, err)
}

// start builds one replica whose own file selects a 2 minute poll interval.
func (d sharedConfigurationDeployment) start(t *testing.T) (*configurationReplica, error) {
	t.Helper()
	cfg, file := d.load(t, "STARPORT_CATALOG_SOURCE_POLL_INTERVAL=2m\n")
	replica := &configurationReplica{cfg: cfg, file: file}
	factories := explicitTestFactories(t)
	factories.openSQL = func(selected config.StorageConfig) (*sqlstore.DB, error) {
		db, err := openSQL(selected)
		replica.db = db
		return db, err
	}
	factories.openCatalog = func(ctx context.Context, store storage.KVStore, _ *sqlstore.DB, settings runtimecatalog.Settings, _ runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		replica.settings = settings
		catalog, err := newLifecycleCatalogRuntime(ctx, store)
		if err != nil {
			return nil, err
		}
		return observedCatalogRuntime{lifecycleCatalogRuntime: catalog, observedAt: time.Now()}, nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	replica.app = application
	return replica, nil
}

func (d sharedConfigurationDeployment) mustStart(t *testing.T) *configurationReplica {
	t.Helper()
	replica, err := d.start(t)
	require.NoError(t, err)
	return replica
}

// requireAppliedFirstRevision proves that the replica serves revision 1 and
// none of its own file values.
func requireAppliedFirstRevision(t *testing.T, replica *configurationReplica, desired int64, retained bool) {
	t.Helper()
	applied := replica.cfg.AppliedRevision()
	require.Equal(t, config.ManagementShared, applied.Authority)
	require.Equal(t, desired, applied.Desired)
	require.Equal(t, int64(1), applied.Applied)
	require.Equal(t, retained, applied.Retained)
	require.Equal(t, 30*time.Second, replica.cfg.Catalog.SourcePollInterval)
	status, err := replica.app.CatalogStatus(context.Background())
	require.NoError(t, err)
	observed := replica.app.catalogRuntime.Status().ObservedAt
	require.Equal(t, observed.Add(30*time.Second), status.NextUpdateAt, "the request reads the applied poll interval")
	require.False(t, status.Acquisition.Enabled)
}

func TestSharedStartupRefusesWithoutUsableHead(t *testing.T) {
	t.Run("absent head", func(t *testing.T) {
		deployment := newSharedConfigurationDeployment(t)
		_, err := deployment.start(t)
		require.ErrorIs(t, err, configrevision.ErrNotInitialized)
		require.ErrorContains(t, err, "starport config init --shared")
	})
	t.Run("namespace mismatch", func(t *testing.T) {
		deployment := newSharedConfigurationDeployment(t)
		const stored = "starport:v1:other-namespace:"
		deployment.write(t, nil, stored, map[string]string{catalogconfig.AcquisitionInterval: "5m"})
		_, err := deployment.start(t)
		var mismatch *config.AuthorityMismatchError
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, stored, mismatch.Stored)
		require.Equal(t, deployment.environment["STARPORT_DEPLOYMENT_ID"], mismatch.DeploymentID)
	})
	t.Run("other deployment", func(t *testing.T) {
		first := newSharedConfigurationDeployment(t)
		first.initialize(t)
		second := newSharedConfigurationDeployment(t)
		second.environment["STARPORT_STORAGE_SQL_SQLITE_PATH"] = first.environment["STARPORT_STORAGE_SQL_SQLITE_PATH"]
		_, err := second.start(t)
		require.ErrorIs(t, err, configrevision.ErrNotInitialized)
		require.ErrorContains(t, err, first.environment["STARPORT_DEPLOYMENT_ID"], "the error names the stored deployment")
	})
	t.Run("sealed credential", func(t *testing.T) {
		deployment := newSharedConfigurationDeployment(t)
		other, err := credentials.NewEncryptionService([]byte(strings.Repeat("o", 32)))
		require.NoError(t, err)
		const secret = "source-key-value"
		deployment.write(t, other, "", map[string]string{
			catalogconfig.Source:       config.CatalogSourceStarmap,
			catalogconfig.SourceURL:    "https://catalog.example/api/v1",
			catalogconfig.SourceAPIKey: secret,
		})
		_, err = deployment.start(t)
		var sealed *configrevision.UnsealError
		require.ErrorAs(t, err, &sealed)
		require.Equal(t, catalogconfig.SourceAPIKey, sealed.Setting)
		require.NotErrorIs(t, err, configrevision.ErrCorrupt)
		require.NotContains(t, err.Error(), secret)
	})
}

func TestDesiredAndAppliedRevisionDrift(t *testing.T) {
	deployment := newSharedConfigurationDeployment(t)
	deployment.initialize(t)
	replicaA, replicaB := deployment.mustStart(t), deployment.mustStart(t)
	for _, replica := range []*configurationReplica{replicaA, replicaB} {
		requireAppliedFirstRevision(t, replica, 1, false)
		require.Equal(t, 30*time.Second, replica.settings.SourcePollInterval, "the catalog runtime opens with the applied revision")
	}

	// Replica A's operator commits revision 2 from a file that selects 5 minutes.
	operator, _ := deployment.load(t, "STARPORT_CATALOG_SOURCE_POLL_INTERVAL=5m\n")
	result, err := ApplyConfiguration(t.Context(), operator, configrevision.ApplyRequest{OperationID: "apply-two"})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Revision.Sequence)
	require.Equal(t, configrevision.FenceNotShared, result.Fence)

	// Observation reads the head only. No replica reads its file again.
	for _, replica := range []*configurationReplica{replicaA, replicaB} {
		require.NoError(t, os.Remove(replica.file))
		require.NoError(t, replica.app.observeConfigurationRevision(t.Context()))
		requireAppliedFirstRevision(t, replica, 2, false)
	}

	restartedA := deployment.mustStart(t)
	require.Equal(t, int64(2), restartedA.cfg.AppliedRevision().Desired)
	require.Equal(t, int64(2), restartedA.cfg.AppliedRevision().Applied)
	require.Equal(t, 5*time.Minute, restartedA.cfg.Catalog.SourcePollInterval)
	require.Equal(t, int64(1), replicaB.cfg.AppliedRevision().Applied, "replica B keeps its applied revision until it restarts")
	require.Equal(t, int64(2), replicaB.cfg.AppliedRevision().Desired)
}

func TestStoreOutageRetainsAppliedRevision(t *testing.T) {
	deployment := newSharedConfigurationDeployment(t)
	deployment.initialize(t)
	replica := deployment.mustStart(t)
	require.NoError(t, replica.db.Close())

	err := replica.app.observeConfigurationRevision(t.Context())
	var unavailable *configrevision.UnavailableError
	require.ErrorAs(t, err, &unavailable)
	requireAppliedFirstRevision(t, replica, 1, true)
	report := replica.cfg.EffectiveReport()
	require.True(t, report.Revision.Retained)
	require.Equal(t, int64(1), report.Revision.Applied)
}

func TestStoreOutageNeverAppliesFileValues(t *testing.T) {
	deployment := newSharedConfigurationDeployment(t)
	deployment.initialize(t)
	replica := deployment.mustStart(t)
	require.NoError(t, replica.db.Close())
	require.NoError(t, os.WriteFile(replica.file, []byte("STARPORT_CATALOG_SOURCE_POLL_INTERVAL=9m\nSTARPORT_CATALOG_ACQUISITION_ENABLED=true\n"), 0o600))

	for range 2 {
		require.Error(t, replica.app.observeConfigurationRevision(t.Context()))
		requireAppliedFirstRevision(t, replica, 1, true)
	}
	require.Equal(t, 30*time.Second, replica.settings.SourcePollInterval)

	// A new process cannot start from the changed file while the store is gone.
	require.NoError(t, os.Remove(deployment.environment["STARPORT_STORAGE_SQL_SQLITE_PATH"]))
	cfg, _ := deployment.load(t, "STARPORT_CATALOG_SOURCE_POLL_INTERVAL=9m\n")
	_, err := EffectiveConfiguration(t.Context(), cfg)
	var unavailable *configrevision.UnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.False(t, cfg.SharedRevisionApplied())
	require.NoFileExists(t, deployment.environment["STARPORT_STORAGE_SQL_SQLITE_PATH"], "an unavailable store is never seeded")
}

func TestRequestsReadAppliedRevisionInMemory(t *testing.T) {
	deployment := newSharedConfigurationDeployment(t)
	deployment.initialize(t)
	replica := deployment.mustStart(t)
	require.NoError(t, replica.db.Close())
	require.NoError(t, os.Remove(replica.file))

	requireAppliedFirstRevision(t, replica, 1, false)
	effective := replica.cfg.EffectiveReport()
	require.Equal(t, config.ManagementShared, effective.Management)
	require.Equal(t, int64(1), effective.Revision.Applied)
}
