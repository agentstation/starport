package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
)

func TestManagedMaterialEnvironmentBounds(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
		"STARPORT_CREDENTIAL_SOURCES_MANAGED_ENTRIES":  "32",
		"STARPORT_CREDENTIAL_SOURCES_MANAGED_VALIDITY": "10s",
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, 32, cfg.CredentialSources.Managed.Limits().Entries)
	require.Equal(t, 10*time.Second, cfg.CredentialSources.Managed.Limits().Validity)
}

// TestManagedMaterialEnvironmentDefaultsMatchCredentialDefaults keeps the
// environment defaults on the credential-owned profile, so an operator who
// sets one variable does not move the others away from it.
func TestManagedMaterialEnvironmentDefaultsMatchCredentialDefaults(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.NotZero(t, cfg.CredentialSources.Managed, "the environment defaults fill the profile")
	require.Equal(t, credentials.DefaultMaterialLimits(), cfg.CredentialSources.Managed.Limits())
}

func TestManagedMaterialEnvironmentRejectsUnsafeBounds(t *testing.T) {
	for field, value := range map[string]string{"ENTRIES": "0", "SECRET_BYTES": "-1", "TENANT_CONCURRENT_LOADS": "100", "VALIDITY": "6m", "LOAD_TIMEOUT": "5s", "REFRESH_INTERVAL": "5s", "IDLE": "1s"} {
		t.Run(field, func(t *testing.T) {
			_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{"STARPORT_CREDENTIAL_SOURCES_MANAGED_" + field: value}).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
		})
	}
}

func TestManagedMaterialExplicitZeroProfileDoesNotRestoreDefaults(t *testing.T) {
	values := map[string]string{}
	for _, field := range []string{"ENTRIES", "SECRET_BYTES", "CONCURRENT_LOADS", "TENANT_CONCURRENT_LOADS", "VALIDITY", "LOAD_TIMEOUT", "IDLE", "REFRESH_INTERVAL"} {
		values["STARPORT_CREDENTIAL_SOURCES_MANAGED_"+field] = "0"
	}
	_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(values).WithEnvFiles().Load(t.Context())
	require.Error(t, err)
}
