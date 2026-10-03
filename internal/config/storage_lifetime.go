package config

// The storage lifetimes that the effective report names. A record lives as
// long as the store that the configuration selected for it.
const (
	// LifetimeProcess records end when the process exits.
	LifetimeProcess = "process"
	// LifetimeLocal records stay on the disk of this node.
	LifetimeLocal = "local"
	// LifetimeService records stay in the selected external service.
	LifetimeService = "service"
)

// selectionProcessMemory names a store that keeps its records in memory or
// in owned development scratch.
const selectionProcessMemory = "process-memory"

// StorageLifetime reports one selected store and how long it keeps records.
// ID is kv, sql, or blobs. Location is a local path and is empty for a
// process or service store. An endpoint or a credential is never reported.
type StorageLifetime struct {
	ID        string `json:"id"`
	Selection string `json:"selection"`
	Lifetime  string `json:"lifetime"`
	Location  string `json:"location,omitempty"`
}

// StorageLifetimes reports the stores that this configuration selects. It
// reads process memory only and opens no store.
func (c *Config) StorageLifetimes() []StorageLifetime {
	if c == nil {
		return []StorageLifetime{}
	}
	var stores []StorageLifetime
	switch {
	case c.Storage.Mode == storageModeValkey:
		stores = append(stores, StorageLifetime{ID: "kv", Selection: storageModeValkey, Lifetime: LifetimeService})
	case c.Storage.Badger.inMemory:
		stores = append(stores, StorageLifetime{ID: "kv", Selection: selectionProcessMemory, Lifetime: LifetimeProcess})
	default:
		stores = append(stores, StorageLifetime{ID: "kv", Selection: storageModeBadger, Lifetime: LifetimeLocal, Location: c.paths.BadgerDir})
	}
	switch {
	case c.Storage.SQL.Mode != sqlModeSQLite:
		stores = append(stores, StorageLifetime{ID: "sql", Selection: c.Storage.SQL.Mode, Lifetime: LifetimeService})
	case c.Storage.Badger.inMemory:
		stores = append(stores, StorageLifetime{ID: "sql", Selection: selectionProcessMemory, Lifetime: LifetimeProcess})
	default:
		stores = append(stores, StorageLifetime{ID: "sql", Selection: sqlModeSQLite, Lifetime: LifetimeLocal, Location: c.paths.SQLiteFile})
	}
	switch {
	case c.Files.SelectedBackend() == BlobBackendObjectStore:
		stores = append(stores, StorageLifetime{ID: "blobs", Selection: BlobBackendObjectStore, Lifetime: LifetimeService})
	case c.Catalog.StateDirectoryIsScratch():
		// Development binds file bytes to scratch that the session removes.
		stores = append(stores, StorageLifetime{ID: "blobs", Selection: BlobBackendFilesystem, Lifetime: LifetimeProcess, Location: c.paths.FilesDir})
	default:
		stores = append(stores, StorageLifetime{ID: "blobs", Selection: BlobBackendFilesystem, Lifetime: LifetimeLocal, Location: c.paths.FilesDir})
	}
	return stores
}
