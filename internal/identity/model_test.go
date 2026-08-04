package identity

import (
	"testing"
	"time"
)

func TestAPIKey_Validate(t *testing.T) {
	tests := []struct {
		name    string
		key     *APIKey
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid api key",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key",
				Hash:      "hash123",
				Scopes:    []string{"read", "write"},
				Active:    true,
				CreatedAt: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "missing id",
			key: &APIKey{
				Name:      "test-key",
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "missing id",
		},
		{
			name: "empty name",
			key: &APIKey{
				ID:        "test-id",
				Name:      "",
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid name",
		},
		{
			name: "name too long",
			key: &APIKey{
				ID:        "test-id",
				Name:      string(make([]byte, 256)),
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid name",
		},
		{
			name: "invalid name characters",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test key with spaces",
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "must contain only alphanumeric",
		},
		{
			name: "missing hash",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key",
				Hash:      "",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "missing hash",
		},
		{
			name: "no scopes",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key",
				Hash:      "hash123",
				Scopes:    []string{},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "missing scopes",
		},
		{
			name: "empty scope",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key",
				Hash:      "hash123",
				Scopes:    []string{"read", ""},
				CreatedAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid scope",
		},
		{
			name: "empty allowed model",
			key: &APIKey{
				ID:            "test-id",
				Name:          "test-key",
				Hash:          "hash123",
				Scopes:        []string{"read"},
				AllowedModels: []string{"gpt-4", ""},
				CreatedAt:     time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid model",
		},
		{
			name: "expires before created",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key",
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
				ExpiresAt: func() *time.Time { t := time.Now().Add(-time.Hour); return &t }(),
			},
			wantErr: true,
			errMsg:  "expires_at must be after created_at",
		},
		{
			name: "valid with expiry",
			key: &APIKey{
				ID:        "test-id",
				Name:      "test-key_123",
				Hash:      "hash123",
				Scopes:    []string{"read"},
				CreatedAt: time.Now(),
				ExpiresAt: func() *time.Time { t := time.Now().Add(time.Hour); return &t }(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("APIKey.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("APIKey.Validate() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestAPIKey_IsExpired(t *testing.T) {
	tests := []struct {
		name string
		key  *APIKey
		want bool
	}{
		{
			name: "no expiry",
			key: &APIKey{
				ExpiresAt: nil,
			},
			want: false,
		},
		{
			name: "not expired",
			key: &APIKey{
				ExpiresAt: func() *time.Time { t := time.Now().Add(time.Hour); return &t }(),
			},
			want: false,
		},
		{
			name: "expired",
			key: &APIKey{
				ExpiresAt: func() *time.Time { t := time.Now().Add(-time.Hour); return &t }(),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.IsExpired(); got != tt.want {
				t.Errorf("APIKey.IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKey_HasScope(t *testing.T) {
	key := &APIKey{
		Scopes: []string{"read", "write", "*"},
	}

	tests := []struct {
		name  string
		scope string
		want  bool
	}{
		{"has read", "read", true},
		{"has write", "write", true},
		{"has wildcard", "anything", true},
		{"missing scope", "admin", true}, // wildcard matches
		{"empty scope", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := key.HasScope(tt.scope); got != tt.want {
				t.Errorf("APIKey.HasScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestAPIKey_CanUseModel(t *testing.T) {
	tests := []struct {
		name          string
		allowedModels []string
		model         string
		want          bool
	}{
		{"no restrictions", nil, "gpt-4", true},
		{"empty restrictions", []string{}, "gpt-4", true},
		{"allowed model", []string{"gpt-4", "claude-3"}, "gpt-4", true},
		{"not allowed model", []string{"gpt-4", "claude-3"}, "gpt-3.5", false},
		{"wildcard allowed", []string{"*"}, "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := &APIKey{AllowedModels: tt.allowedModels}
			if got := key.CanUseModel(tt.model); got != tt.want {
				t.Errorf("APIKey.CanUseModel(%q) = %v, want %v", tt.model, got, tt.want)
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
