package config

import (
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestLoaderDerivesCloudAuthModesFromCatalogProfiles(t *testing.T) {
	cfg := loadTestConfig(t, map[string]string{
		"GOOGLE_CLOUD_PROJECT":  "vertex-project",
		"AZURE_OPENAI_ENDPOINT": "https://azure.example",
		"AZURE_OPENAI_API_KEY":  "azure-key",
	})
	resolveTestProviders(t, cfg)
	if cfg.Providers[catalogs.ProviderIDGoogleVertex].AuthMode != providerauth.ModeDefault {
		t.Errorf("Vertex auth mode = %q", cfg.Providers[catalogs.ProviderIDGoogleVertex].AuthMode)
	}
	if cfg.Providers[catalogs.ProviderIDAzureOpenAI].AuthMode != providerauth.ModeStatic {
		t.Errorf("Azure auth mode = %q", cfg.Providers[catalogs.ProviderIDAzureOpenAI].AuthMode)
	}
}

func TestProviderConfigRejectsAmbiguousCloudCredentials(t *testing.T) {
	tests := []struct {
		name   string
		config ProviderConfig
		match  string
	}{
		{
			name:   "default with API key",
			config: ProviderConfig{AuthMode: providerauth.ModeDefault, APIKey: "secret"},
			match:  "cannot be combined",
		},
		{
			name:   "static without API key",
			config: ProviderConfig{AuthMode: providerauth.ModeStatic},
			match:  "is required",
		},
		{
			name:   "unknown mode",
			config: ProviderConfig{AuthMode: "ambient"},
			match:  "is invalid",
		},
		{
			name:   "mode with whitespace",
			config: ProviderConfig{AuthMode: " default "},
			match:  "is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.config.Timeout = time.Second
			test.config.MaxConnections = 1
			err := test.config.Validate()
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("Validate() error = %v, want text %q", err, test.match)
			}
		})
	}
}

func TestProvidersConfigDoesNotOwnProviderAuthenticationRoster(t *testing.T) {
	providers := ProvidersConfig{"yaml-only": {
		AuthMode: providerauth.ModeDefault, Timeout: time.Second, MaxConnections: 1,
	}}
	if err := providers.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestProvidersConfigRequiresStaticModeForStaticMaterial(t *testing.T) {
	providers := ProvidersConfig{"yaml-only": {
		APIKey: "token", Timeout: time.Second, MaxConnections: 1,
	}}
	if err := providers.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
