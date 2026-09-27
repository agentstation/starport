package config

import "errors"

// validateStorageRecipe rejects combinations that leave fleet state on one replica.
// Backend configuration and runtime recovery approval have separate checks.
func (c *Config) validateStorageRecipe() error {
	if !c.Storage.Distributed() {
		return nil
	}
	if c.Storage.SQL.Mode != sqlModePostgres {
		return storageRecipeFailure("shared storage requires PostgreSQL for relational state and recovery approval")
	}
	if c.Files.SelectedBackend() != BlobBackendObjectStore {
		return storageRecipeFailure("shared storage requires object storage for file bytes")
	}
	if c.Storage.Valkey.ClusterMode {
		return storageRecipeFailure("shared storage requires a controlled single-primary Valkey service. Cluster recovery is unqualified")
	}
	return nil
}

// storageRecipeFailure publishes only a fixed operator instruction.
func storageRecipeFailure(message string) error {
	return newLoadFailure(message, errors.New(message))
}
