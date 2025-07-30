package models

import (
	"testing"
	"time"
)

func TestPreset_Validate(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		preset  *Preset
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid preset",
			preset: &Preset{
				ID:          "test-id",
				Name:        "test-preset",
				Description: "Test preset",
				Config:      map[string]any{"model": "gpt-4"},
				Version:     1,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			wantErr: false,
		},
		{
			name: "missing id",
			preset: &Preset{
				Name:      "test-preset",
				Config:    map[string]any{"model": "gpt-4"},
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: true,
			errMsg:  "missing id",
		},
		{
			name: "invalid version",
			preset: &Preset{
				ID:        "test-id",
				Name:      "test-preset",
				Config:    map[string]any{"model": "gpt-4"},
				Version:   0,
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: true,
			errMsg:  "invalid version",
		},
		{
			name: "empty config",
			preset: &Preset{
				ID:        "test-id",
				Name:      "test-preset",
				Config:    map[string]any{},
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: true,
			errMsg:  "empty config",
		},
		{
			name: "updated before created",
			preset: &Preset{
				ID:        "test-id",
				Name:      "test-preset",
				Config:    map[string]any{"model": "gpt-4"},
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now.Add(-time.Hour),
			},
			wantErr: true,
			errMsg:  "updated_at must be after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.preset.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Preset.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("Preset.Validate() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestProviderKey_Validate(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		key     *ProviderKey
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid user-scoped key",
			key: &ProviderKey{
				Scope:               "user:123",
				Provider:            "openai",
				EncryptedCredential: "encrypted-data",
				CreatedAt:           now,
				UpdatedAt:           now,
			},
			wantErr: false,
		},
		{
			name: "valid global key",
			key: &ProviderKey{
				Scope:               "*",
				Provider:            "openai",
				EncryptedCredential: "encrypted-data",
				CreatedAt:           now,
				UpdatedAt:           now,
			},
			wantErr: false,
		},
		{
			name: "empty scope defaults to global",
			key: &ProviderKey{
				Scope:               "",
				Provider:            "openai",
				EncryptedCredential: "encrypted-data",
				CreatedAt:           now,
				UpdatedAt:           now,
			},
			wantErr: false,
		},
		{
			name: "missing provider",
			key: &ProviderKey{
				Scope:               "user:123",
				EncryptedCredential: "encrypted-data",
				CreatedAt:           now,
				UpdatedAt:           now,
			},
			wantErr: true,
			errMsg:  "invalid provider",
		},
		{
			name: "missing encrypted credential",
			key: &ProviderKey{
				Scope:     "user:123",
				Provider:  "openai",
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: true,
			errMsg:  "missing encrypted credential",
		},
		{
			name: "updated before created",
			key: &ProviderKey{
				Scope:               "user:123",
				Provider:            "openai",
				EncryptedCredential: "encrypted-data",
				CreatedAt:           now,
				UpdatedAt:           now.Add(-time.Hour),
			},
			wantErr: true,
			errMsg:  "updated_at must be after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ProviderKey.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("ProviderKey.Validate() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestTokenBucket_Validate(t *testing.T) {
	tests := []struct {
		name    string
		bucket  *TokenBucket
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid bucket",
			bucket: &TokenBucket{
				Tokens:     50,
				Capacity:   100,
				RefillRate: 10,
				LastRefill: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "invalid capacity",
			bucket: &TokenBucket{
				Tokens:     50,
				Capacity:   0,
				RefillRate: 10,
				LastRefill: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid capacity",
		},
		{
			name: "invalid refill rate",
			bucket: &TokenBucket{
				Tokens:     50,
				Capacity:   100,
				RefillRate: 0,
				LastRefill: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid refill rate",
		},
		{
			name: "negative tokens",
			bucket: &TokenBucket{
				Tokens:     -10,
				Capacity:   100,
				RefillRate: 10,
				LastRefill: time.Now(),
			},
			wantErr: true,
			errMsg:  "tokens cannot be negative",
		},
		{
			name: "tokens exceed capacity",
			bucket: &TokenBucket{
				Tokens:     150,
				Capacity:   100,
				RefillRate: 10,
				LastRefill: time.Now(),
			},
			wantErr: true,
			errMsg:  "tokens cannot exceed capacity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bucket.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("TokenBucket.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("TokenBucket.Validate() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	now := time.Now()
	bucket := &TokenBucket{
		Tokens:     50,
		Capacity:   100,
		RefillRate: 10,                        // 10 tokens per second
		LastRefill: now.Add(-5 * time.Second), // 5 seconds ago
	}

	bucket.Refill()

	// Should have added 50 tokens (10 tokens/sec * 5 sec)
	expectedTokens := 100.0 // capped at capacity
	if bucket.Tokens != expectedTokens {
		t.Errorf("TokenBucket.Refill() tokens = %v, want %v", bucket.Tokens, expectedTokens)
	}

	// Test partial refill
	bucket.Tokens = 50
	bucket.LastRefill = now.Add(-2 * time.Second)
	bucket.Refill()

	// Should have added 20 tokens (10 tokens/sec * 2 sec)
	expectedTokens = 70.0
	tolerance := 1.0 // Allow some tolerance for timing
	if bucket.Tokens < expectedTokens-tolerance || bucket.Tokens > expectedTokens+tolerance {
		t.Errorf("TokenBucket.Refill() tokens = %v, want approximately %v", bucket.Tokens, expectedTokens)
	}
}

func TestTokenBucket_TryConsume(t *testing.T) {
	bucket := &TokenBucket{
		Tokens:     50,
		Capacity:   100,
		RefillRate: 10,
		LastRefill: time.Now(),
	}

	tests := []struct {
		name   string
		tokens float64
		want   bool
	}{
		{"consume 10", 10, true},
		{"consume 40", 40, true},
		{"consume 1", 1, false}, // only 0 left after previous consumes
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bucket.TryConsume(tt.tokens); got != tt.want {
				t.Errorf("TokenBucket.TryConsume(%v) = %v, want %v", tt.tokens, got, tt.want)
			}
		})
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s[:len(substr)] == substr || containsSubstring(s[1:], substr))
}
