package config

import "github.com/agentstation/starport/internal/storage"

// RuntimeStorage projects external storage settings into the storage adapter contract.
func (c StorageConfig) RuntimeStorage() storage.Config {
	return storage.Config{
		Type: c.Mode,
		Badger: storage.BadgerConfig{
			Path: c.Badger.Path, SyncWrites: c.Badger.SyncWrites,
			Compression: c.Badger.Compression != "none", NumVersions: 1,
			NumLevelZero: 5, MemTableSize: 64 << 20,
		},
		Valkey: storage.ValkeyConfig{
			URL: c.Valkey.URL, Password: c.Valkey.Password,
			MaxRetries: 3, MinIdleConns: c.Valkey.MinIdleConns,
			ReadTimeout: c.Valkey.ReadTimeout, WriteTimeout: c.Valkey.WriteTimeout,
			ClusterMode: c.Valkey.ClusterMode,
		},
	}
}
