package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
)

func TestInferencePolicyUsesCheckedConfigurationAndPersists(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gateway")
	values := map[string]string{"STARPORT_HOME": home, "OPENAI_API_KEY": "valid-old", "STARPORT_OPENAI_API_KEY": "valid-product"}
	load := func() *Config {
		t.Helper()
		cfg, err := NewLoader().WithEnvironment(values).WithEnvFiles().Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	provider := testCredentialProvider("openai", "OPENAI_API_KEY", `^valid-`)
	providers := catalogs.NewProviders()
	if err := providers.Set(provider.ID, &provider); err != nil {
		t.Fatal(err)
	}
	cfg := load()
	if err := cfg.InitializeInferencePolicy(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	var conflict *credentials.PolicyConflictError
	if err := cfg.ResolveProviders(t.Context(), providers); !errors.As(err, &conflict) {
		t.Fatalf("retained installation accepted changed credentials: %v", err)
	}
	values["OPENAI_API_KEY"] = "valid-product"
	cfg = load()
	if err := cfg.InitializeInferencePolicy(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
		t.Fatal(err)
	}
	values["OPENAI_API_KEY"] = "valid-old"
	cfg = load()
	if err := cfg.InitializeInferencePolicy(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
		t.Fatal(err)
	}
	if value, _ := cfg.Providers[provider.ID].Material.Value("api-key"); value != "valid-product" {
		t.Fatal("persisted selection changed")
	}
}

func TestInferenceStarmapFallbackConfiguration(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			cfg, err := NewLoader().WithEnvironment(map[string]string{
				"STARPORT_HOME": filepath.Join(t.TempDir(), "gateway"),
				"STARPORT_CREDENTIAL_SOURCES_ALLOW_STARMAP_FALLBACK": enabled,
				"STARMAP_OPENAI_API_KEY":                             "valid-catalog",
			}).WithEnvFiles().Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.InitializeInferencePolicy(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			provider := testCredentialProvider("openai", "OPENAI_API_KEY", `^valid-`)
			providers := catalogs.NewProviders()
			if err := providers.Set(provider.ID, &provider); err != nil {
				t.Fatal(err)
			}
			if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
				t.Fatal(err)
			}
			_, configured := cfg.Providers[provider.ID]
			if configured != (enabled == "true") {
				t.Fatal("configuration did not control Starmap fallback")
			}
		})
	}
}
