package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/deployment"
)

func loadAuthorityConfig(t *testing.T, environment map[string]string, file string) (*Config, string) {
	t.Helper()
	paths := PathsForConfigDir(t.TempDir())
	selected := filepath.Join(paths.ConfigDir, "selected.env")
	require.NoError(t, os.WriteFile(selected, []byte(file), 0o600))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles(selected).Load(t.Context())
	require.NoError(t, err)
	return cfg, selected
}

func sharedRevisionFor(cfg *Config, sequence int64, values map[string]string) SharedRevision {
	return SharedRevision{
		DeploymentID: cfg.EffectivePaths().DeploymentID, Namespace: cfg.ConfigNamespace(),
		Sequence: sequence, RevisionID: "revision", Checksum: SharedValuesChecksum(values), Values: values,
	}
}

func effectiveSetting(t *testing.T, report EffectiveReport, name string) EffectiveSetting {
	t.Helper()
	for _, setting := range report.Settings {
		if setting.Name == name {
			return setting
		}
	}
	t.Fatalf("effective report omits %s", name)
	return EffectiveSetting{}
}

func TestLocalManagementReadsFileAuthority(t *testing.T) {
	cfg, file := loadAuthorityConfig(t, nil, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	require.Equal(t, ManagementLocal, cfg.ManagementMode(), "embedded storage selects local management")
	require.Equal(t, 7*time.Minute, cfg.Catalog.AcquisitionInterval)
	require.True(t, cfg.SharedRevisionApplied(), "local management needs no shared revision")
	require.Equal(t, AppliedRevision{Authority: ManagementLocal, FileChecksum: fileRevision([]byte("STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"))}, cfg.AppliedRevision())

	report := cfg.EffectiveReport()
	require.Equal(t, ManagementLocal, report.Management)
	require.Empty(t, report.Namespace)
	setting := effectiveSetting(t, report, catalogconfig.AcquisitionInterval)
	require.Equal(t, ManagementLocal, setting.Authority)
	require.Equal(t, "file:"+file, setting.Origin)
	require.Equal(t, "7m", setting.Value)
	require.Zero(t, setting.DesiredRevision)
	require.Zero(t, setting.AppliedRevision)

	// Local management has no shared path, so no revision can replace the file value.
	err := cfg.ApplySharedRevision(sharedRevisionFor(cfg, 1, map[string]string{catalogconfig.AcquisitionInterval: "5m"}))
	require.ErrorContains(t, err, "requires shared management")
	require.Equal(t, 7*time.Minute, cfg.Catalog.AcquisitionInterval)

	external, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementExternal}, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	require.Equal(t, 7*time.Minute, external.Catalog.AcquisitionInterval)
	require.Equal(t, ManagementExternal, external.EffectiveReport().Controller)
	require.Equal(t, ManagementExternal, effectiveSetting(t, external.EffectiveReport(), catalogconfig.AcquisitionInterval).Authority)
}

func TestSharedAuthorityIgnoresLocalDeploymentValues(t *testing.T) {
	environment := map[string]string{managementEnvironment: ManagementShared, "STARPORT_CATALOG_SOURCE_POLL_INTERVAL": "2m"}
	firstWorkspace, secondWorkspace := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	first, firstFile := loadAuthorityConfig(t, environment, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\nSTARPORT_CATALOG_WORKSPACE_PATH="+firstWorkspace+"\n")
	second, secondFile := loadAuthorityConfig(t, environment, "STARPORT_CATALOG_ACQUISITION_INTERVAL=9m\nSTARPORT_CATALOG_WORKSPACE_PATH="+secondWorkspace+"\n")
	require.False(t, first.SharedRevisionApplied(), "shared management waits for a revision")

	values := map[string]string{catalogconfig.AcquisitionInterval: "5m", catalogconfig.SourcePollInterval: "30s"}
	for _, replica := range []struct {
		cfg       *Config
		file      string
		workspace string
	}{{first, firstFile, firstWorkspace}, {second, secondFile, secondWorkspace}} {
		require.NoError(t, replica.cfg.ApplySharedRevision(sharedRevisionFor(replica.cfg, 3, values)))
		require.True(t, replica.cfg.SharedRevisionApplied())
		require.Equal(t, 5*time.Minute, replica.cfg.Catalog.AcquisitionInterval)
		require.Equal(t, 30*time.Second, replica.cfg.Catalog.SourcePollInterval)
		require.Equal(t, "5m", replica.cfg.Catalog.CatalogValues()[catalogconfig.AcquisitionInterval])
		require.Equal(t, replica.workspace, replica.cfg.Catalog.WorkspacePath, "node-scope values stay local")
		require.Equal(t, AppliedRevision{
			Authority: ManagementShared, Namespace: keyPrefix(t, replica.cfg.EffectivePaths().DeploymentID),
			Desired: 3, Applied: 3, Checksum: SharedValuesChecksum(values),
		}, replica.cfg.AppliedRevision())

		report := replica.cfg.EffectiveReport()
		interval := effectiveSetting(t, report, catalogconfig.AcquisitionInterval)
		require.Equal(t, ManagementShared, interval.Authority)
		require.Equal(t, sharedRevisionLayer, interval.Origin)
		require.Equal(t, []IgnoredValue{{Origin: "file:" + replica.file, Reason: catalogconfig.IgnoredSharedAuthority}}, interval.Ignored)
		require.Equal(t, int64(3), interval.DesiredRevision)
		require.Equal(t, int64(3), interval.AppliedRevision)
		poll := effectiveSetting(t, report, catalogconfig.SourcePollInterval)
		require.Equal(t, []IgnoredValue{{Origin: "environment", Reason: catalogconfig.IgnoredSharedAuthority}}, poll.Ignored)
		workspace := effectiveSetting(t, report, catalogconfig.WorkspacePath)
		require.Equal(t, ManagementLocal, workspace.Authority)
		require.Equal(t, "file:"+replica.file, workspace.Origin)
		require.Empty(t, workspace.Ignored)
	}
	require.Equal(t, first.Catalog.CatalogValues()[catalogconfig.AcquisitionInterval], second.Catalog.CatalogValues()[catalogconfig.AcquisitionInterval])

	first.ObserveDesiredRevision(4)
	first.RetainAppliedRevision()
	require.Equal(t, int64(4), first.AppliedRevision().Desired)
	require.Equal(t, int64(3), first.AppliedRevision().Applied)
	require.True(t, first.AppliedRevision().Retained)
	require.ErrorContains(t, first.ApplySharedRevision(sharedRevisionFor(first, 4, values)), "already applied")
}

func keyPrefix(t *testing.T, deploymentID string) string {
	t.Helper()
	prefix, err := deployment.KeyPrefix(deploymentID)
	require.NoError(t, err)
	return prefix
}

func TestApplySharedRevisionRefusesOtherIdentity(t *testing.T) {
	cfg, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementShared}, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	namespace := keyPrefix(t, cfg.EffectivePaths().DeploymentID)
	require.Equal(t, namespace, cfg.ConfigNamespace())
	revision := sharedRevisionFor(cfg, 1, map[string]string{catalogconfig.AcquisitionInterval: "5m"})

	revision.Namespace = "starport:v1:green:"
	err := cfg.ApplySharedRevision(revision)
	var mismatch *AuthorityMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, AuthorityMismatchError{Setting: NamespaceSetting, Configured: namespace, Stored: "starport:v1:green:", DeploymentID: revision.DeploymentID}, *mismatch)
	require.ErrorContains(t, err, `"starport:v1:green:"`)
	require.ErrorContains(t, err, revision.DeploymentID)

	revision.Namespace, revision.DeploymentID = namespace, "other-deployment"
	err = cfg.ApplySharedRevision(revision)
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, "STARPORT_DEPLOYMENT_ID", mismatch.Setting)
	require.ErrorContains(t, err, `"other-deployment"`)

	require.False(t, cfg.SharedRevisionApplied())
	require.Equal(t, 7*time.Minute, cfg.Catalog.AcquisitionInterval, "a refused revision changes nothing")
}

func TestSharedRecordCannotSupplyBootstrapValues(t *testing.T) {
	for _, name := range []string{
		"STARPORT_STORAGE_MODE",
		"STARPORT_STORAGE_SQL_POSTGRES_URL",
		managementEnvironment,
		"STARPORT_SECURITY_MASTER_KEY",
		"STARPORT_DEPLOYMENT_ID",
		catalogconfig.StateDirectory,
		catalogconfig.WorkspacePath,
		catalogconfig.SchedulerIdentity,
		catalogconfig.PermissionClockSource,
		catalogconfig.AuthorityOrigin,
		"STARMAP_CATALOG_UNKNOWN_SETTING",
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]string{catalogconfig.AcquisitionInterval: "5m", name: "value"}
			err := ValidateSharedValues(values)
			var refused *SharedSettingError
			require.ErrorAs(t, err, &refused)
			require.Equal(t, name, refused.Name)
			require.ErrorContains(t, err, name)

			cfg, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementShared}, "")
			before := cfg.Catalog.AcquisitionInterval
			require.ErrorAs(t, cfg.ApplySharedRevision(sharedRevisionFor(cfg, 1, values)), &refused)
			require.Equal(t, name, refused.Name)
			require.False(t, cfg.SharedRevisionApplied())
			require.Equal(t, before, cfg.Catalog.AcquisitionInterval)
		})
	}
	err := ValidateSharedValues(map[string]string{catalogconfig.Source: CatalogSourceFile, catalogconfig.SourceURL: "relative/catalog.json"})
	require.ErrorContains(t, err, catalogconfig.SourceURL)
	require.NoError(t, ValidateSharedValues(map[string]string{catalogconfig.AcquisitionInterval: "5m"}))
}

func TestSharedChecksumExcludesCredentials(t *testing.T) {
	values := map[string]string{catalogconfig.Source: CatalogSourceStarmap, catalogconfig.SourceURL: "https://catalog.example/api/v1", catalogconfig.SourceAPIKey: "first"}
	rotated := map[string]string{catalogconfig.Source: CatalogSourceStarmap, catalogconfig.SourceURL: "https://catalog.example/api/v1", catalogconfig.SourceAPIKey: "second"}
	require.Equal(t, SharedValuesChecksum(values), SharedValuesChecksum(rotated))
	require.NotEqual(t, SharedValuesChecksum(values), SharedValuesChecksum(map[string]string{catalogconfig.Source: CatalogSourceEmbedded}))

	cfg, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementShared}, "")
	require.NoError(t, cfg.ApplySharedRevision(sharedRevisionFor(cfg, 1, values)))
	require.Equal(t, "first", cfg.Catalog.SourceAPIKey)
	key := effectiveSetting(t, cfg.EffectiveReport(), catalogconfig.SourceAPIKey)
	require.Equal(t, redactedValue, key.Value)
	require.Equal(t, redactedValue, effectiveSetting(t, cfg.EffectiveReport(), catalogconfig.SourceURL).Value)
}

func TestManagementDefaultFollowsStorageRecipe(t *testing.T) {
	cfg, _ := loadAuthorityConfig(t, nil, "")
	require.Equal(t, ManagementLocal, cfg.Management.Mode)
	require.Equal(t, keyPrefix(t, cfg.EffectivePaths().DeploymentID), cfg.ConfigNamespace(), "the namespace is the deployment key prefix")
	require.Equal(t, ManagementLocal, (&Config{}).ManagementMode())
	require.Empty(t, (&Config{}).ConfigNamespace(), "an invalid deployment ID derives no namespace")
	require.ErrorContains(t, ManagementConfig{Mode: "remote"}.Validate(), managementEnvironment)
	recipe := &Config{}
	recipe.Storage.Mode = storageModeValkey
	recipe.selectDefaultManagement()
	require.Equal(t, ManagementShared, recipe.ManagementMode())
}
