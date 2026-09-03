package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRemovedCatalogVariableFailsStartup proves that every removed catalog
// variable fails startup and names its replacement. A deployment that still
// sets one must learn that the setting no longer applies.
func TestRemovedCatalogVariableFailsStartup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		variable        string
		value           string
		wantReplacement string
	}{
		{
			name:            "refresh on start",
			variable:        "STARPORT_CATALOG_REFRESH_ON_START",
			value:           "true",
			wantReplacement: "STARPORT_CATALOG_ACQUISITION_ENABLED",
		},
		{
			name:            "refresh interval",
			variable:        "STARPORT_CATALOG_REFRESH_INTERVAL",
			value:           "1m",
			wantReplacement: "STARPORT_CATALOG_ACQUISITION_INTERVAL",
		},
		{
			name:            "remote URL",
			variable:        "STARPORT_CATALOG_REMOTE_URL",
			value:           "https://catalog.example/api/v1",
			wantReplacement: "STARPORT_CATALOG_SOURCE_URL",
		},
		{
			name:            "remote API key",
			variable:        "STARPORT_CATALOG_REMOTE_API_KEY",
			value:           "catalog-secret",
			wantReplacement: "STARPORT_CATALOG_SOURCE_API_KEY",
		},
		{
			name:            "remote activation interval",
			variable:        "STARPORT_CATALOG_REMOTE_ACTIVATION_INTERVAL",
			value:           "750ms",
			wantReplacement: "STARPORT_CATALOG_SOURCE_POLL_INTERVAL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewLoader().
				WithPaths(PathsForConfigDir(t.TempDir())).
				WithEnvironment(map[string]string{test.variable: test.value}).
				WithEnvFiles().
				Load(t.Context())
			if err == nil {
				t.Fatalf("%s was accepted", test.variable)
			}
			var removed *RemovedSettingError
			if !errors.As(err, &removed) {
				t.Fatalf("error type = %T, want *RemovedSettingError", err)
			}
			if removed.Name != test.variable {
				t.Fatalf("removed name = %q, want %q", removed.Name, test.variable)
			}
			if removed.Replacement != test.wantReplacement {
				t.Fatalf(
					"replacement = %q, want %q",
					removed.Replacement, test.wantReplacement,
				)
			}
			if !strings.Contains(err.Error(), test.wantReplacement) {
				t.Fatalf("startup error does not name %q", test.wantReplacement)
			}
		})
	}
}

// TestCanonicalCatalogSettingsLoad proves that the canonical catalog settings
// reach the configuration with the gateway prefix and the Starmap suffixes.
func TestCanonicalCatalogSettingsLoad(t *testing.T) {
	t.Parallel()

	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CATALOG_SOURCE":                "starmap",
			"STARPORT_CATALOG_SOURCE_URL":            "https://catalog.example/api/v1",
			"STARPORT_CATALOG_SOURCE_API_KEY":        "catalog-secret",
			"STARPORT_CATALOG_SOURCE_POLL_INTERVAL":  "30m",
			"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_source",
			"STARPORT_CATALOG_SOURCE_MAX_AGE":        "2h",
			"STARPORT_CATALOG_SOURCE_MAX_HOPS":       "3",
			"STARPORT_CATALOG_ACQUISITION_INTERVAL":  "45m",
			"STARPORT_CATALOG_STARTUP_SPREAD":        "5m",
			"STARPORT_CATALOG_TRANSFER_IDLE_TIMEOUT": "90s",
			"STARPORT_CATALOG_TRANSFER_MAX_DURATION": "30m",
			"STARPORT_CATALOG_REFRESH_TIMEOUT":       "3m",
		}).
		WithEnvFiles().
		Load(t.Context())
	if err != nil {
		t.Fatalf("load canonical catalog settings: %v", err)
	}
	if cfg.Catalog.Source != CatalogSourceStarmap {
		t.Fatalf("catalog source = %q", cfg.Catalog.Source)
	}
	if cfg.Catalog.SourceURL != "https://catalog.example/api/v1" {
		t.Fatalf("catalog source URL = %q", cfg.Catalog.SourceURL)
	}
	if cfg.Catalog.SourceAPIKey != "catalog-secret" {
		t.Fatal("catalog source API key was not loaded")
	}
	if cfg.Catalog.SourcePollInterval != 30*time.Minute {
		t.Fatalf("catalog source poll interval = %s", cfg.Catalog.SourcePollInterval)
	}
	if cfg.Catalog.SourceStartupPolicy != CatalogStartupRequireSource {
		t.Fatalf("catalog startup policy = %q", cfg.Catalog.SourceStartupPolicy)
	}
	if cfg.Catalog.SourceMaxAge != 2*time.Hour || cfg.Catalog.SourceMaxHops != 3 {
		t.Fatalf("catalog source bounds = %#v", cfg.Catalog)
	}
	if !cfg.Catalog.AcquisitionEnabled || cfg.Catalog.AcquisitionInterval != 45*time.Minute {
		t.Fatalf("catalog acquisition = %#v", cfg.Catalog)
	}
	if cfg.Catalog.StartupSpread != 5*time.Minute {
		t.Fatalf("catalog startup spread = %s", cfg.Catalog.StartupSpread)
	}
	if cfg.Catalog.TransferIdleTimeout != 90*time.Second ||
		cfg.Catalog.TransferMaxDuration != 30*time.Minute {
		t.Fatalf("catalog transfer bounds = %#v", cfg.Catalog)
	}
	if cfg.Catalog.RefreshTimeout != 3*time.Minute {
		t.Fatalf("catalog refresh timeout = %s", cfg.Catalog.RefreshTimeout)
	}
}

// TestCatalogConfigRefusesUnusableSettings proves the validator refuses a
// setting the runtime cannot honor.
func TestCatalogConfigRefusesUnusableSettings(t *testing.T) {
	t.Parallel()

	valid := func() CatalogConfig {
		return CatalogConfig{
			Source:              CatalogSourcePublic,
			SourceRepository:    DefaultCatalogSourceRepository,
			SourceChannel:       DefaultCatalogSourceChannel,
			SourcePollInterval:  time.Hour,
			SourceStartupPolicy: CatalogStartupPreferSource,
			SourceMaxAge:        6 * time.Hour,
			SourceMaxHops:       8,
			AcquisitionEnabled:  true,
			AcquisitionInterval: 4 * time.Hour,
			StartupSpread:       15 * time.Minute,
			TransferIdleTimeout: 2 * time.Minute,
			TransferMaxDuration: time.Hour,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*CatalogConfig)
		wantErr bool
	}{
		{name: "defaults", mutate: func(*CatalogConfig) {}},
		{
			name:    "unknown source",
			mutate:  func(c *CatalogConfig) { c.Source = "elsewhere" },
			wantErr: true,
		},
		{
			name:    "unknown startup policy",
			mutate:  func(c *CatalogConfig) { c.SourceStartupPolicy = "always" },
			wantErr: true,
		},
		{
			name:    "starmap source without a URL",
			mutate:  func(c *CatalogConfig) { c.Source = CatalogSourceStarmap },
			wantErr: true,
		},
		{
			name:    "zero transfer max duration",
			mutate:  func(c *CatalogConfig) { c.TransferMaxDuration = 0 },
			wantErr: true,
		},
		{
			name:    "zero transfer idle timeout",
			mutate:  func(c *CatalogConfig) { c.TransferIdleTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "negative acquisition interval",
			mutate:  func(c *CatalogConfig) { c.AcquisitionInterval = -time.Second },
			wantErr: true,
		},
		{
			name:    "zero source max hops",
			mutate:  func(c *CatalogConfig) { c.SourceMaxHops = 0 },
			wantErr: true,
		},
		{
			name:   "zero refresh timeout adds no cap",
			mutate: func(c *CatalogConfig) { c.RefreshTimeout = 0 },
		},
		{
			name:   "zero acquisition interval means startup only",
			mutate: func(c *CatalogConfig) { c.AcquisitionInterval = 0 },
		},
		{
			name: "the state directory is the workspace path",
			mutate: func(c *CatalogConfig) {
				c.WorkspacePath = "/srv/shared/catalog"
				c.StateDirectory = "/srv/shared/catalog"
			},
			wantErr: true,
		},
		{
			name: "the state directory stands beside the workspace path",
			mutate: func(c *CatalogConfig) {
				c.WorkspacePath = "/srv/shared/catalog"
				c.StateDirectory = "/var/lib/starport/catalog"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := valid()
			test.mutate(&catalog)
			err := catalog.Validate()
			if test.wantErr && err == nil {
				t.Fatal("validation accepted an unusable catalog setting")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validation refused a usable catalog setting: %v", err)
			}
		})
	}
}

// TestDevelopmentRuntimeKeepsCatalogAcquisitionEnabled proves that development
// mode leaves automatic catalog work on. Only the operator turns it off.
func TestDevelopmentRuntimeKeepsCatalogAcquisitionEnabled(t *testing.T) {
	t.Parallel()

	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{}).
		WithEnvFiles().
		LoadDevelopment(t.Context())
	if err != nil {
		t.Fatalf("load development configuration: %v", err)
	}
	if !cfg.Catalog.AcquisitionEnabled {
		t.Fatal("development mode disabled catalog acquisition")
	}
	if cfg.Catalog.AcquisitionInterval <= 0 {
		t.Fatalf("development acquisition interval = %s", cfg.Catalog.AcquisitionInterval)
	}

	disabled, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
		}).
		WithEnvFiles().
		LoadDevelopment(t.Context())
	if err != nil {
		t.Fatalf("load development configuration: %v", err)
	}
	if disabled.Catalog.AcquisitionEnabled {
		t.Fatal("the operator could not disable catalog acquisition")
	}
}

// TestResolveStateDirectoryIsProcessLocal proves the catalog state directory
// resolves to a process-local path. An operator value wins, the user state
// root supplies the default, and no answer is empty.
func TestResolveStateDirectoryIsProcessLocal(t *testing.T) {
	stateRoot := t.TempDir()
	home := t.TempDir()
	operator := t.TempDir()

	tests := []struct {
		name       string
		configured string
		stateHome  string
		home       string
		want       string
	}{
		{
			name:       "an operator value wins",
			configured: operator,
			stateHome:  stateRoot,
			home:       home,
			want:       operator,
		},
		{
			name:      "the user state root supplies the default",
			stateHome: stateRoot,
			home:      home,
			want:      filepath.Join(stateRoot, "starport", "catalog"),
		},
		{
			name: "the home directory supplies the default",
			home: home,
			want: filepath.Join(home, ".local", "state", "starport", "catalog"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(stateHomeEnvironment, test.stateHome)
			t.Setenv("HOME", test.home)
			directory, err := ResolveStateDirectory(test.configured)
			if err != nil {
				t.Fatalf("resolve the state directory: %v", err)
			}
			if directory == "" {
				t.Fatal("the resolved state directory is empty")
			}
			if directory != test.want {
				t.Fatalf("state directory = %q, want %q", directory, test.want)
			}
		})
	}
}
