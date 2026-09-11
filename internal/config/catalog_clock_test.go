package config

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nativeClockEnvironment(prefix string) map[string]string {
	return map[string]string{
		prefix + "SOURCE":                       "native",
		prefix + "REFRESH_INTERVAL":             "10s",
		prefix + "MAX_AGE":                      "1m",
		prefix + "MAX_DRIFT_PPM":                "500",
		prefix + "COUNTER_UNCERTAINTY":          "1us",
		prefix + "WINDOWS_MAX_SOURCE_AGE":       "1h",
		prefix + "WINDOWS_MAX_SOURCE_DRIFT_PPM": "500",
		prefix + "WINDOWS_SOURCE_UNCERTAINTY":   "1ms",
	}
}

func TestCatalogPermissionClockRejectsInvalidConfiguration(t *testing.T) {
	const prefix = "STARPORT_CATALOG_PERMISSION_CLOCK_"
	for _, test := range []struct {
		name    string
		changes map[string]string
	}{
		{name: "unknown source", changes: map[string]string{prefix + "SOURCE": "other"}},
		{name: "empty source", changes: map[string]string{prefix + "SOURCE": ""}},
		{name: "missing uncertainty", changes: map[string]string{prefix + "COUNTER_UNCERTAINTY": "0s"}},
		{name: "invalid duration", changes: map[string]string{prefix + "MAX_AGE": "later"}},
		{name: "unbounded age", changes: map[string]string{prefix + "MAX_AGE": "6m"}},
		{name: "unsafe interval", changes: map[string]string{prefix + "REFRESH_INTERVAL": "30s"}},
		{name: "zero drift", changes: map[string]string{prefix + "MAX_DRIFT_PPM": "0"}},
		{name: "overflow drift", changes: map[string]string{prefix + "MAX_DRIFT_PPM": "4294967296"}},
		{name: "partial Windows bounds", changes: map[string]string{prefix + "WINDOWS_SOURCE_UNCERTAINTY": "0s"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := nativeClockEnvironment(prefix)
			maps.Copy(environment, test.changes)
			_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
		})
	}
}

func TestCatalogPermissionClockNamesAndPrecedence(t *testing.T) {
	for _, prefix := range []string{"STARPORT_CATALOG_PERMISSION_CLOCK_", "STARMAP_CATALOG_PERMISSION_CLOCK_"} {
		t.Run(prefix, func(t *testing.T) {
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(nativeClockEnvironment(prefix)).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
			catalog := Redacted(cfg)["catalog"].(map[string]any)
			require.Contains(t, catalog, "permission_clock")
			clock := catalog["permission_clock"].(map[string]any)
			require.Equal(t, "native", clock["source"])
			require.Equal(t, "1m0s", clock["max_age"])
			require.EqualValues(t, 500, clock["max_drift_ppm"])
		})
	}
	for _, test := range []struct {
		name        string
		environment map[string]string
		file        string
	}{
		{name: "same source product name wins", environment: map[string]string{"STARPORT_CATALOG_PERMISSION_CLOCK_SOURCE": "disabled", "STARMAP_CATALOG_PERMISSION_CLOCK_SOURCE": "native"}},
		{name: "environment alias beats file product name", environment: map[string]string{"STARMAP_CATALOG_PERMISSION_CLOCK_SOURCE": "disabled"}, file: "STARPORT_CATALOG_PERMISSION_CLOCK_SOURCE=native\n"},
		{name: "file alias", file: "STARMAP_CATALOG_PERMISSION_CLOCK_SOURCE=disabled\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			file := filepath.Join(paths.ConfigDir, "clock.env")
			require.NoError(t, os.WriteFile(file, []byte(test.file), 0600))
			cfg, err := NewLoader().WithPaths(paths).WithEnvironment(test.environment).WithEnvFiles(file).Load(t.Context())
			require.NoError(t, err)
			catalog := Redacted(cfg)["catalog"].(map[string]any)
			require.Contains(t, catalog, "permission_clock")
			require.Equal(t, "disabled", catalog["permission_clock"].(map[string]any)["source"])
		})
	}
	_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
		"STARPORT_CATALOG_PERMISSION_CLOCK_SOURCE": "",
		"STARMAP_CATALOG_PERMISSION_CLOCK_SOURCE":  "disabled",
	}).WithEnvFiles().Load(t.Context())
	require.Error(t, err, "an explicit empty value must not fall through to an alias")
}

func TestCatalogPermissionClockIncompleteProfileFails(t *testing.T) {
	for _, source := range []string{"embedded", "public", "starmap"} {
		t.Run(source, func(t *testing.T) {
			_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
				"STARPORT_CATALOG_SOURCE":                  source,
				"STARPORT_CATALOG_SOURCE_URL":              "https://catalog.example/api/v1",
				"STARPORT_CATALOG_PERMISSION_CLOCK_SOURCE": "native",
			}).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), "configuration"))
		})
	}
}
