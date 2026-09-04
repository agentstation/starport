package config

import (
	"strings"

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
func (c StorageConfig) RuntimeStorage() storage.Config {
	return storage.Config{
		Type: c.Mode,
		Badger: storage.BadgerConfig{
			Path: c.Badger.Path, InMemory: c.Badger.inMemory,
			SyncWrites:  c.Badger.SyncWrites,
			Compression: c.Badger.Compression != compressionNone, NumVersions: 1,
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

// ConfigureDevelopmentRuntime selects process-local settings that cannot expose
// a development gateway or create persistent state.
func (c *Config) ConfigureDevelopmentRuntime() {
	if c == nil {
		return
	}
	c.Server.Host = "127.0.0.1"
	c.Server.EnableProfiling = false
	// Catalog acquisition stays on. A development gateway that reads no
	// catalog routes nothing, and an operator who wants a quiet gateway
	// sets STARPORT_CATALOG_ACQUISITION_ENABLED=false.
	c.Storage = StorageConfig{
		Mode: storageModeBadger,
		Badger: BadgerConfig{
			Compression: compressionNone, inMemory: true,
		},
		// An empty SQLite path is the in-memory database: real schema,
		// no file, gone with the process — the relational twin of the
		// in-memory Badger above.
		SQL: SQLConfig{Mode: sqlModeSQLite},
	}
	// The local admin token file is read but never written. The path stays
	// so a machine that already holds a token keeps `starport auth token`
	// and the console paste path in agreement with a development gateway;
	// the read-only mark keeps a machine that holds none exactly as it was.
	c.Security.localTokenReadOnly = true
	// The default catalog state directory is session scratch. The development
	// composition creates it and removes it on close. The runtime then retains
	// no layer, no identity seed, and no discovery record on the machine. An
	// operator value stays. An operator who names a directory asks the
	// session to retain its catalog state there.
	c.Catalog.stateDirectoryScratch = strings.TrimSpace(c.Catalog.StateDirectory) == ""
	c.Security.MasterKey = ""
	c.Security.EnableTLS = false
	c.Security.TLSCertPath = ""
	c.Security.TLSKeyPath = ""
	c.Security.EnableCORS = false
	c.Security.AllowedOrigins = ""
	c.Security.JWTSecret = ""
	c.Logging.Output = "stdout"
	c.Logging.FilePath = ""
}
