package connectors

import (
	"testing"
	"time"
)

func TestProviderConfig_ValidateEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
		check   func(t *testing.T, config *ProviderConfig)
	}{
		{
			name: "base URL with trailing slash",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com/",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "zero timeout sets default",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com",
				Timeout:        0, // Should be set to default
				MaxConnections: 100,
			},
			wantErr: false,
			check: func(t *testing.T, config *ProviderConfig) {
				if config.Timeout == 0 {
					t.Error("Expected timeout to be set to default")
				}
			},
		},
		{
			name: "zero max connections sets default",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com",
				Timeout:        30 * time.Second,
				MaxConnections: 0, // Should be set to default
			},
			wantErr: false,
			check: func(t *testing.T, config *ProviderConfig) {
				if config.MaxConnections == 0 {
					t.Error("Expected max connections to be set to default")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.check != nil {
				tt.check(t, &tt.config)
			}
		})
	}
}
