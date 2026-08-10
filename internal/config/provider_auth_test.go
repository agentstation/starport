package config

import (
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestLoaderUsesExplicitCloudAuthModes(t *testing.T) {
	cfg := loadTestConfig(t, map[string]string{
		"STARPORT_PROVIDERS_GOOGLE_VERTEX_AUTH_MODE": "default",
		"STARPORT_PROVIDERS_AZURE_OPENAI_AUTH_MODE":  "static",
		"STARPORT_PROVIDERS_AZURE_OPENAI_API_KEY":    "azure-key",
	})
	if cfg.Providers.GoogleVertexAI.AuthMode != providerauth.ModeDefault {
		t.Errorf("Vertex auth mode = %q", cfg.Providers.GoogleVertexAI.AuthMode)
	}
	if cfg.Providers.Azure.AuthMode != providerauth.ModeStatic {
		t.Errorf("Azure auth mode = %q", cfg.Providers.Azure.AuthMode)
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

func TestProvidersConfigLimitsDefaultCredentialsToCloudAdapters(t *testing.T) {
	providers := ProvidersConfig{OpenAI: ProviderConfig{
		AuthMode: providerauth.ModeDefault, Timeout: time.Second, MaxConnections: 1,
	}}
	if err := providers.Validate(); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Validate() error = %v", err)
	}
}
