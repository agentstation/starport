package app

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCatalogSettingsPreserveConfiguredPermissionClock(t *testing.T) {
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
		"STARPORT_CATALOG_PERMISSION_CLOCK_SOURCE":                       "native",
		"STARPORT_CATALOG_PERMISSION_CLOCK_REFRESH_INTERVAL":             "10s",
		"STARPORT_CATALOG_PERMISSION_CLOCK_MAX_AGE":                      "1m",
		"STARPORT_CATALOG_PERMISSION_CLOCK_MAX_DRIFT_PPM":                "500",
		"STARPORT_CATALOG_PERMISSION_CLOCK_COUNTER_UNCERTAINTY":          "1us",
		"STARPORT_CATALOG_PERMISSION_CLOCK_WINDOWS_MAX_SOURCE_AGE":       "1h",
		"STARPORT_CATALOG_PERMISSION_CLOCK_WINDOWS_MAX_SOURCE_DRIFT_PPM": "750",
		"STARPORT_CATALOG_PERMISSION_CLOCK_WINDOWS_SOURCE_UNCERTAINTY":   "1ms",
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	settings := catalogSettings(cfg)
	require.Equal(t, cfg.Catalog.PermissionClock, settings.PermissionClock)
	require.Equal(t, "native", settings.PermissionClock.Source)
	require.Equal(t, 10*time.Second, settings.PermissionClock.RefreshInterval)
	require.Equal(t, time.Minute, settings.PermissionClock.MaxAge)
	require.EqualValues(t, 500, settings.PermissionClock.MaxDriftPPM)
	require.Equal(t, time.Microsecond, settings.PermissionClock.CounterUncertainty)
	require.Equal(t, time.Hour, settings.PermissionClock.Windows.MaxSourceAge)
	require.EqualValues(t, 750, settings.PermissionClock.Windows.MaxSourceDriftPPM)
	require.Equal(t, time.Millisecond, settings.PermissionClock.Windows.SourceUncertainty)
}
