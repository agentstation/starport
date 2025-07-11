package storage

import (
	"errors"
	"testing"
)

func TestOpen(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid badger config",
			config: Config{
				Type: "badger",
				Badger: BadgerConfig{
					Path:         "./data/badger",
					NumVersions:  1,
					MemTableSize: 64 * 1024 * 1024,
				},
			},
			wantErr: false, // Badger is now implemented
		},
		{
			name: "valid valkey config",
			config: Config{
				Type: "valkey",
				Valkey: ValkeyConfig{
					URL: "redis://localhost:6379",
				},
			},
			wantErr: true, // Connection will fail in test environment
			errMsg:  "failed to create valkey client",
		},
		{
			name: "invalid storage type",
			config: Config{
				Type: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid storage type",
		},
		{
			name: "empty storage type",
			config: Config{
				Type: "",
			},
			wantErr: true,
			errMsg:  "invalid storage type",
		},
		{
			name: "invalid config",
			config: Config{
				Type: "badger",
				Badger: BadgerConfig{
					Path: "", // Invalid
				},
			},
			wantErr: true,
			errMsg:  "badger path cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := Open(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Open() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" {
				if !errors.Is(err, errors.New(tt.errMsg)) && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("Open() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
			if !tt.wantErr && store != nil {
				defer store.Close()
			}
		})
	}
}

func TestNewMockStore(t *testing.T) {
	store := NewMockStore()
	if store == nil {
		t.Fatal("NewMockStore() returned nil")
	}

	// Verify initialization
	if store.data == nil {
		t.Error("MockStore data map not initialized")
	}
	if store.ttl == nil {
		t.Error("MockStore ttl map not initialized")
	}
	if store.transactions == nil {
		t.Error("MockStore transactions map not initialized")
	}
	if store.closed {
		t.Error("MockStore should not be closed on creation")
	}

	// Verify it implements KVStore interface
	var _ KVStore = store
}

// Helper function to check if error message contains substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		len(s) >= len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && containsSubstring(s[1:], substr)
}

func containsSubstring(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
