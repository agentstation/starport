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
			name: "retry delay of zero sets default",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				MaxRetries:     3,
				RetryDelay:     0, // Should be set to default
			},
			wantErr: false,
			check: func(t *testing.T, config *ProviderConfig) {
				if config.RetryDelay == 0 {
					t.Error("Expected retry delay to be set to default")
				}
			},
		},
		{
			name: "base URL with trailing slash",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com/",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "zero timeout sets default",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com",
				APIKey:         "test-key",
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
				APIKey:         "test-key",
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
		{
			name: "zero max retries is valid",
			config: ProviderConfig{
				BaseURL:        "https://api.example.com",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				MaxRetries:     0, // Valid - no retries
			},
			wantErr: false,
		},
		{
			name: "backoff multiplier zero sets default",
			config: ProviderConfig{
				BaseURL:           "https://api.example.com",
				APIKey:            "test-key",
				Timeout:           30 * time.Second,
				MaxConnections:    100,
				MaxRetries:        3,
				BackoffMultiplier: 0, // Should be set to default
			},
			wantErr: false,
			check: func(t *testing.T, config *ProviderConfig) {
				if config.BackoffMultiplier == 0 {
					t.Error("Expected backoff multiplier to be set to default")
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
