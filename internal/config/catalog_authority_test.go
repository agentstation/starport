package config

import (
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogAuthoritySettingsLoadAndValidate(t *testing.T) {
	base := map[string]string{
		"STARPORT_CATALOG_SOURCE":                "starmap",
		"STARPORT_CATALOG_SOURCE_URL":            "https://catalog.example/api/v1",
		"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_authority",
		"STARPORT_CATALOG_SOURCE_AUTHORITY_ID":   "enterprise",
		"STARPORT_CATALOG_SOURCE_POLICY_ID":      "production",
		"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
	}
	for _, test := range []struct {
		name      string
		changes   map[string]string
		wantError bool
	}{
		{name: "valid"},
		{name: "missing authority", changes: map[string]string{"STARPORT_CATALOG_SOURCE_AUTHORITY_ID": ""}, wantError: true},
		{name: "missing policy", changes: map[string]string{"STARPORT_CATALOG_SOURCE_POLICY_ID": ""}, wantError: true},
		{name: "authority whitespace", changes: map[string]string{"STARPORT_CATALOG_SOURCE_AUTHORITY_ID": " enterprise "}, wantError: true},
		{name: "policy control character", changes: map[string]string{"STARPORT_CATALOG_SOURCE_POLICY_ID": "production\nother"}, wantError: true},
		{name: "authority too long", changes: map[string]string{"STARPORT_CATALOG_SOURCE_AUTHORITY_ID": strings.Repeat("a", 257)}, wantError: true},
		{name: "wrong source", changes: map[string]string{"STARPORT_CATALOG_SOURCE": "public"}, wantError: true},
		{name: "pins with ordinary startup", changes: map[string]string{"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "prefer_source"}, wantError: true},
		{name: "pins with strict online startup", changes: map[string]string{"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_source"}, wantError: true},
		{name: "local acquisition", changes: map[string]string{"STARPORT_CATALOG_ACQUISITION_ENABLED": "true"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := maps.Clone(base)
			maps.Copy(environment, test.changes)
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "enterprise", cfg.Catalog.SourceAuthorityID)
			require.Equal(t, "production", cfg.Catalog.SourcePolicyID)
		})
	}
}
