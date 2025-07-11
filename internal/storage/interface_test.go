package storage

import (
	"context"
	"testing"
	"time"
)

func TestKVStoreInterface(t *testing.T) {
	// This test ensures the interface is properly defined
	var _ KVStore = (*MockStore)(nil)
	var _ Transaction = (*MockTransaction)(nil)
}

func TestStorageErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ErrNotFound",
			err:  ErrNotFound,
			want: "key not found",
		},
		{
			name: "ErrConflict",
			err:  ErrConflict,
			want: "write conflict",
		},
		{
			name: "ErrInvalidKey",
			err:  ErrInvalidKey,
			want: "invalid key",
		},
		{
			name: "ErrStorageClosed",
			err:  ErrStorageClosed,
			want: "storage closed",
		},
		{
			name: "ErrTimeout",
			err:  ErrTimeout,
			want: "operation timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
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
			wantErr: false,
		},
		{
			name: "valid valkey config",
			config: Config{
				Type: "valkey",
				Valkey: ValkeyConfig{
					URL:        "redis://localhost:6379",
					MaxRetries: 3,
					DB:         0,
				},
			},
			wantErr: false,
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
			name: "badger with empty path",
			config: Config{
				Type: "badger",
				Badger: BadgerConfig{
					Path:         "",
					NumVersions:  1,
					MemTableSize: 64 * 1024 * 1024,
				},
			},
			wantErr: true,
			errMsg:  "badger path cannot be empty",
		},
		{
			name: "badger with invalid num_versions",
			config: Config{
				Type: "badger",
				Badger: BadgerConfig{
					Path:         "./data/badger",
					NumVersions:  0,
					MemTableSize: 64 * 1024 * 1024,
				},
			},
			wantErr: true,
			errMsg:  "badger num_versions must be at least 1",
		},
		{
			name: "badger with small mem_table_size",
			config: Config{
				Type: "badger",
				Badger: BadgerConfig{
					Path:         "./data/badger",
					NumVersions:  1,
					MemTableSize: 1024, // Too small
				},
			},
			wantErr: true,
			errMsg:  "badger mem_table_size must be at least 1MB",
		},
		{
			name: "valkey with empty URL",
			config: Config{
				Type: "valkey",
				Valkey: ValkeyConfig{
					URL: "",
				},
			},
			wantErr: true,
			errMsg:  "valkey URL cannot be empty",
		},
		{
			name: "valkey with negative max_retries",
			config: Config{
				Type: "valkey",
				Valkey: ValkeyConfig{
					URL:        "redis://localhost:6379",
					MaxRetries: -1,
				},
			},
			wantErr: true,
			errMsg:  "valkey max_retries cannot be negative",
		},
		{
			name: "valkey with negative DB",
			config: Config{
				Type: "valkey",
				Valkey: ValkeyConfig{
					URL: "redis://localhost:6379",
					DB:  -1,
				},
			},
			wantErr: true,
			errMsg:  "valkey DB index cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" {
				if !containsString(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestBadgerConfig_Defaults(t *testing.T) {
	// Test that the struct tags define proper defaults
	config := BadgerConfig{}

	// These would be set by envconfig in production
	if config.Path != "" {
		t.Errorf("Path should be empty by default, got %s", config.Path)
	}
	if config.SyncWrites != false {
		t.Errorf("SyncWrites should be false by default")
	}
	if config.Compression != false {
		t.Errorf("Compression should be false by default")
	}
}

func TestValkeyConfig_Defaults(t *testing.T) {
	// Test that the struct tags define proper defaults
	config := ValkeyConfig{}

	// These would be set by envconfig in production
	if config.URL != "" {
		t.Errorf("URL should be empty by default, got %s", config.URL)
	}
	if config.ClusterMode != false {
		t.Errorf("ClusterMode should be false by default")
	}
}

func TestTransactionInterface(t *testing.T) {
	// Create a mock store first
	store := NewMockStore()
	defer store.Close()

	// Begin a transaction
	ctx := context.Background()
	tx, err := store.BeginTransaction(ctx)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Ensure all required methods are defined
	var _ Transaction = tx

	// Test that interface methods exist (these calls ensure the interface is properly implemented)
	_, _ = tx.Get("key")
	_ = tx.Set("key", []byte("value"))
	_ = tx.Delete("key")
	_ = tx.SetWithTTL("key", []byte("value"), time.Second)
	_, _ = tx.Increment("key", 1)
	_ = tx.CompareAndSwap("key", nil, []byte("new"))
	_ = tx.Rollback() // Use rollback to clean up without committing
}
