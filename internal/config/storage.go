package config

import (
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const (
	storageModeBadger = storage.StorageTypeBadger
	storageModeValkey = storage.StorageTypeValkey
	sqlModeSQLite     = sqlstore.TypeSQLite
	sqlModePostgres   = sqlstore.TypePostgres
	sqlModeMySQL      = sqlstore.TypeMySQL
	compressionNone   = "none"
)

// RuntimeSQL projects the relational settings into the sqlstore contract,
// the way RuntimeStorage projects the key-value ones.
func (c StorageConfig) RuntimeSQL() sqlstore.Config {
	return sqlstore.Config{
		Type:     c.SQL.Mode,
		SQLite:   sqlstore.SQLiteConfig{Path: c.SQL.SQLite.Path},
		Postgres: sqlstore.PostgresConfig{URL: c.SQL.Postgres.URL},
		MySQL:    sqlstore.MySQLConfig{DSN: c.SQL.MySQL.DSN},
	}
}

// Distributed reports whether the runtime key-value store is one that
// replicas share. Shared provider health publication turns on with it.
func (c StorageConfig) Distributed() bool {
	return c.Mode == storageModeValkey
}

// RuntimeStorage projects external storage settings into the storage adapter contract.
func (c *Config) RuntimeStorage() storage.Config {
	selected := c.Storage
	connection := selected.Valkey.RuntimeConnection()
	connection.DeploymentID = c.EffectivePaths().DeploymentID
	return storage.Config{
		Type: selected.Mode,
		Badger: storage.BadgerConfig{
			Path: selected.Badger.Path, InMemory: selected.Badger.inMemory,
			SyncWrites:  selected.Badger.SyncWrites,
			Compression: selected.Badger.Compression, NumVersions: 1,
			GCInterval: selected.Badger.GCInterval, GCDiscardRatio: selected.Badger.GCDiscardRatio,
			NumLevelZero: 5, MemTableSize: 64 << 20,
		},
		Valkey: connection,
	}
}

// ConfigureDevelopmentRuntime selects process-local settings that cannot expose
// a development gateway or create persistent state.
func (c *Config) ConfigureDevelopmentRuntime() error {
	if c == nil {
		return nil
	}
	if err := c.validateDevelopmentStorage(); err != nil {
		return err
	}
	c.Server.Host = "127.0.0.1"
	c.Server.EnableProfiling = false
	// Catalog acquisition stays on. A development gateway that reads no
	// catalog routes nothing, and an operator who wants a quiet gateway
	// sets STARPORT_CATALOG_ACQUISITION_ENABLED=false.
	c.Storage.Mode = storageModeBadger
	// A development gateway reads no shared revision.
	c.Management = ManagementConfig{Mode: ManagementLocal}
	c.Storage.Badger.inMemory = true
	c.Storage.Badger.SyncWrites = false
	c.Storage.SQL.Mode = sqlModeSQLite
	c.Files.Backend = BlobBackendFilesystem
	// Development can read an existing machine token for local authentication.
	// It never creates or rotates that persistent token.
	c.Security.localTokenReadOnly = true
	// The composition supplies every development storage path from owned scratch.
	c.Catalog.stateDirectoryScratch = true
	c.Security.MasterKey = ""
	c.Security.EnableTLS = false
	c.Security.TLSCertPath = ""
	c.Security.TLSKeyPath = ""
	c.Security.EnableCORS = false
	c.Security.AllowedOrigins = ""
	c.Security.JWTSecret = ""
	c.Logging.Output = "stdout"
	c.Logging.FilePath = ""
	return nil
}

const valkeyCAFileRole = "valkey-ca"
const valkeyCAFileEnvironment = "STARPORT_STORAGE_VALKEY_CA_FILE"

// RuntimeConnection projects endpoint settings without opening storage.
func (c ValkeyConfig) RuntimeConnection() storage.ValkeyConfig {
	return storage.ValkeyConfig{URL: c.URL, Username: c.Username, Password: c.Password,
		CAFile: c.CAFile, AllowInsecure: c.AllowInsecure, ClusterMode: c.ClusterMode,
		DialTimeout: c.DialTimeout, MaxConnections: c.MaxConnections, MinIdleConns: c.MinIdleConns,
		IdleTimeout: c.IdleTimeout, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout}
}
