package models

import (
	"testing"
)

func TestStorageKeys(t *testing.T) {
	tests := []struct {
		name     string
		function func() string
		expected string
	}{
		{
			name:     "api key storage key",
			function: func() string { return APIKeyStorageKey("hash123") },
			expected: "apikey:hash123",
		},
		{
			name:     "preset storage key",
			function: func() string { return PresetStorageKey("my-preset") },
			expected: "preset:my-preset",
		},
		{
			name:     "byok credential storage key",
			function: func() string { return BYOKCredentialStorageKey("key123", "openai") },
			expected: "provider_key:key123:openai",
		},
		{
			name:     "rate limit storage key",
			function: func() string { return RateLimitStorageKey("user123", "1m") },
			expected: "ratelimit:user123:1m",
		},
		{
			name:     "filter storage key",
			function: func() string { return FilterStorageKey("pii-filter") },
			expected: "filter:pii-filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.function(); got != tt.expected {
				t.Errorf("Storage key = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseStorageKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantPrefix string
		wantParts  []string
	}{
		{
			name:       "api key",
			key:        "apikey:hash123",
			wantPrefix: "apikey",
			wantParts:  []string{"hash123"},
		},
		{
			name:       "preset",
			key:        "preset:my-preset",
			wantPrefix: "preset",
			wantParts:  []string{"my-preset"},
		},
		{
			name:       "byok credential",
			key:        "provider_key:key123:openai",
			wantPrefix: "provider_key",
			wantParts:  []string{"key123", "openai"},
		},
		{
			name:       "rate limit",
			key:        "ratelimit:user123:1m",
			wantPrefix: "ratelimit",
			wantParts:  []string{"user123", "1m"},
		},
		{
			name:       "empty key",
			key:        "",
			wantPrefix: "",
			wantParts:  nil,
		},
		{
			name:       "no separator",
			key:        "invalidkey",
			wantPrefix: "invalidkey",
			wantParts:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, parts := ParseStorageKey(tt.key)
			if prefix != tt.wantPrefix {
				t.Errorf("ParseStorageKey() prefix = %v, want %v", prefix, tt.wantPrefix)
			}
			if !sliceEqual(parts, tt.wantParts) {
				t.Errorf("ParseStorageKey() parts = %v, want %v", parts, tt.wantParts)
			}
		})
	}
}

func TestIsStorageKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		checkAPI bool
		checkPre bool
		checkBYO bool
		checkRL  bool
	}{
		{
			name:     "api key",
			key:      "apikey:hash123",
			checkAPI: true,
		},
		{
			name:     "preset",
			key:      "preset:my-preset",
			checkPre: true,
		},
		{
			name:     "byok credential",
			key:      "provider_key:key123:openai",
			checkBYO: true,
		},
		{
			name:    "rate limit",
			key:     "ratelimit:user123:1m",
			checkRL: true,
		},
		{
			name: "invalid key",
			key:  "invalid:key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAPIKeyStorageKey(tt.key); got != tt.checkAPI {
				t.Errorf("IsAPIKeyStorageKey(%q) = %v, want %v", tt.key, got, tt.checkAPI)
			}
			if got := IsPresetStorageKey(tt.key); got != tt.checkPre {
				t.Errorf("IsPresetStorageKey(%q) = %v, want %v", tt.key, got, tt.checkPre)
			}
			if got := IsBYOKCredentialStorageKey(tt.key); got != tt.checkBYO {
				t.Errorf("IsBYOKCredentialStorageKey(%q) = %v, want %v", tt.key, got, tt.checkBYO)
			}
			if got := IsRateLimitStorageKey(tt.key); got != tt.checkRL {
				t.Errorf("IsRateLimitStorageKey(%q) = %v, want %v", tt.key, got, tt.checkRL)
			}
		})
	}
}

func TestExtractAPIKeyHash(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{
			name: "valid api key",
			key:  "apikey:hash123",
			want: "hash123",
		},
		{
			name:    "not api key",
			key:     "preset:my-preset",
			wantErr: true,
		},
		{
			name:    "invalid format",
			key:     "apikey",
			wantErr: true,
		},
		{
			name:    "empty key",
			key:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractAPIKeyHash(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractAPIKeyHash() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractAPIKeyHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractPresetName(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{
			name: "valid preset",
			key:  "preset:my-preset",
			want: "my-preset",
		},
		{
			name: "preset with special chars",
			key:  "preset:my_preset-123",
			want: "my_preset-123",
		},
		{
			name:    "not preset key",
			key:     "apikey:hash123",
			wantErr: true,
		},
		{
			name:    "invalid format",
			key:     "preset",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractPresetName(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractPresetName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractPresetName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractBYOKCredentialParts(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		wantAPIKeyID string
		wantProvider string
		wantErr      bool
	}{
		{
			name:         "valid credential",
			key:          "provider_key:key123:openai",
			wantAPIKeyID: "key123",
			wantProvider: "openai",
		},
		{
			name:         "different provider",
			key:          "provider_key:apikey456:anthropic",
			wantAPIKeyID: "apikey456",
			wantProvider: "anthropic",
		},
		{
			name:    "not credential key",
			key:     "apikey:hash123",
			wantErr: true,
		},
		{
			name:    "missing provider",
			key:     "provider_key:key123",
			wantErr: true,
		},
		{
			name:    "too many parts",
			key:     "provider_key:key123:openai:extra",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKeyID, provider, err := ExtractBYOKCredentialParts(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractBYOKCredentialParts() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if apiKeyID != tt.wantAPIKeyID {
				t.Errorf("ExtractBYOKCredentialParts() apiKeyID = %v, want %v", apiKeyID, tt.wantAPIKeyID)
			}
			if provider != tt.wantProvider {
				t.Errorf("ExtractBYOKCredentialParts() provider = %v, want %v", provider, tt.wantProvider)
			}
		})
	}
}

// Helper function
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
