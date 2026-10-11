package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPersistentDevelopmentChangesOnlyHostLogsAndTLS keeps the platform paths
// and the port, and moves only the bind host, the log output, and TLS.
func TestPersistentDevelopmentChangesOnlyHostLogsAndTLS(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	cfg, err := NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SERVER_HOST":            "0.0.0.0",
		"STARPORT_SERVER_PORT":            "18123",
		"STARPORT_LOGGING_OUTPUT":         "file",
		"STARPORT_LOGGING_FILE_PATH":      filepath.Join(paths.DataDir, "starport.log"),
		"STARPORT_SECURITY_ENABLE_TLS":    "true",
		"STARPORT_SECURITY_TLS_CERT_PATH": filepath.Join(paths.ConfigDir, "cert.pem"),
		"STARPORT_SECURITY_TLS_KEY_PATH":  filepath.Join(paths.ConfigDir, "key.pem"),
	}).LoadPersistentDevelopment(t.Context())
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Server.Host)
	require.Equal(t, 18123, cfg.Server.Port)
	require.Equal(t, "stdout", cfg.Logging.Output)
	require.Empty(t, cfg.Logging.FilePath)
	require.False(t, cfg.Security.EnableTLS)
	require.Empty(t, cfg.Security.TLSCertPath)
	require.Empty(t, cfg.Security.TLSKeyPath)
	require.True(t, cfg.UsesPlatformStorage(paths))
	require.Equal(t, ManagementLocal, cfg.ManagementMode())
	require.NoDirExists(t, paths.DataDir)
}

// TestPersistentDevelopmentRefusesBeforeValidation names serve for a recipe
// that validation would also reject, so the operator reads the dev refusal.
func TestPersistentDevelopmentRefusesBeforeValidation(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	cfg, err := NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_STORAGE_MODE":       "valkey",
		"STARPORT_STORAGE_VALKEY_URL": "redis://127.0.0.1:1",
	}).LoadPersistentDevelopment(t.Context())

	require.ErrorIs(t, err, ErrPersistentDevelopment)
	require.Nil(t, cfg)
	message := OperatorError(err).Error()
	require.Contains(t, message, "STARPORT_STORAGE_MODE")
	require.Contains(t, message, "STARPORT_CONFIG_MANAGEMENT")
	require.Contains(t, message, "starport serve")
	require.NotContains(t, message, "redis://")
	require.NoDirExists(t, paths.DataDir)
}
