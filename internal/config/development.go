package config

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/sethvargo/go-envconfig"
)

const (
	badgerPathEnvironment = "STARPORT_STORAGE_BADGER_PATH"
	sqlitePathEnvironment = "STARPORT_STORAGE_SQL_SQLITE_PATH"
	filesPathEnvironment  = "STARPORT_FILES_PATH"
)

// ErrDevelopmentStorage rejects persistent storage before development access.
var ErrDevelopmentStorage = errors.New("development requires isolated storage")

func developmentStorageError(names []string) error {
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return fmt.Errorf("%w: remove %s or use starport init and starport serve for persistent storage. See docs/OPERATOR-GUIDE.md#initialize-persistent-state", ErrDevelopmentStorage, strings.Join(slices.Compact(names), ", "))
}

func rejectDevelopmentEnvironment(source envconfig.Lookuper) error {
	var conflicts []string
	for _, key := range []string{
		"STARPORT_STORAGE_MODE", badgerPathEnvironment,
		"STARPORT_STORAGE_VALKEY_URL", "STARPORT_STORAGE_VALKEY_PASSWORD", "STARPORT_STORAGE_VALKEY_CLUSTER_MODE",
		"STARPORT_CACHE_BACKEND", "STARPORT_CACHE_URL", "STARPORT_CACHE_ALLOW_INSECURE", cacheCAFileEnvironment,
		"STARPORT_STORAGE_SQL_MODE", sqlitePathEnvironment,
		"STARPORT_STORAGE_SQL_POSTGRES_URL", "STARPORT_STORAGE_SQL_MYSQL_DSN",
		"STARPORT_FILES_BACKEND", filesPathEnvironment,
		"STARPORT_FILES_OBJECT_STORE_BUCKET", "STARPORT_FILES_OBJECT_STORE_REGION",
		"STARPORT_FILES_OBJECT_STORE_ENDPOINT", "STARPORT_FILES_OBJECT_STORE_PREFIX",
		"STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID", "STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY",
		stateDirectoryEnvironment,
	} {
		if _, present := source.Lookup(key); present {
			conflicts = append(conflicts, key)
		}
	}
	return developmentStorageError(conflicts)
}

func (c *Config) validateDevelopmentStorage() error {
	var conflicts []string
	for _, check := range []struct {
		name     string
		selected bool
	}{
		{"STARPORT_CACHE_BACKEND", c.Cache.Backend != "" && c.Cache.Backend != cacheBackendLocal},
		{cacheCAFileEnvironment, c.Cache.CAFile != ""},
		{"STARPORT_CACHE_URL", c.Cache.URL != ""},
		{"STARPORT_CACHE_ALLOW_INSECURE", c.Cache.AllowInsecure},
		{"STARPORT_STORAGE_MODE", c.Storage.Mode != "" && c.Storage.Mode != storageModeBadger},
		{badgerPathEnvironment, c.Storage.Badger.Path != ""},
		{"STARPORT_STORAGE_VALKEY_URL", c.Storage.Valkey.URL != ""},
		{"STARPORT_STORAGE_VALKEY_PASSWORD", c.Storage.Valkey.Password != ""},
		{"STARPORT_STORAGE_VALKEY_CLUSTER_MODE", c.Storage.Valkey.ClusterMode},
		{"STARPORT_STORAGE_SQL_MODE", c.Storage.SQL.Mode != "" && c.Storage.SQL.Mode != sqlModeSQLite},
		{sqlitePathEnvironment, c.Storage.SQL.SQLite.Path != ""},
		{"STARPORT_STORAGE_SQL_POSTGRES_URL", c.Storage.SQL.Postgres.URL != ""},
		{"STARPORT_STORAGE_SQL_MYSQL_DSN", c.Storage.SQL.MySQL.DSN != ""},
		{"STARPORT_FILES_BACKEND", c.Files.SelectedBackend() != BlobBackendFilesystem},
		{filesPathEnvironment, c.Files.Path != ""},
		{"STARPORT_FILES_OBJECT_STORE_BUCKET", c.Files.ObjectStore.Bucket != ""},
		{"STARPORT_FILES_OBJECT_STORE_REGION", c.Files.ObjectStore.Region != ""},
		{"STARPORT_FILES_OBJECT_STORE_ENDPOINT", c.Files.ObjectStore.Endpoint != ""},
		{"STARPORT_FILES_OBJECT_STORE_PREFIX", c.Files.ObjectStore.Prefix != ""},
		{"STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID", c.Files.ObjectStore.AccessKeyID != ""},
		{"STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY", c.Files.ObjectStore.SecretAccessKey != ""},
		{stateDirectoryEnvironment, c.Catalog.StateDirectory != ""},
	} {
		if check.selected {
			conflicts = append(conflicts, check.name)
		}
	}
	return developmentStorageError(conflicts)
}

// BindDevelopmentScratch assigns storage paths under a session-owned directory.
// The caller must create and own that private directory before application startup.
func (c *Config) BindDevelopmentScratch(root string) error {
	if c == nil || !c.Catalog.StateDirectoryIsScratch() || !c.Storage.Badger.inMemory {
		return errors.New("development storage must be configured before binding scratch")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("development scratch requires a clean absolute directory")
	}
	if err := c.validateDevelopmentStorage(); err != nil {
		return err
	}
	c.Files.Path = filepath.Join(root, "files")
	c.Catalog.StateDirectory = filepath.Join(root, "catalog", "runtime")
	c.paths.DataDir, c.paths.StateDir, c.paths.CacheDir = root, root, filepath.Join(root, "cache")
	c.paths.BadgerDir, c.paths.SQLiteFile = "", ""
	c.paths.FilesDir, c.paths.RuntimeDir = c.Files.Path, c.Catalog.StateDirectory
	c.paths.BaselineDir = filepath.Join(root, "catalog", pathRoleBaseline)
	c.paths.WelcomeStampFile = ""
	c.paths.Origins = maps.Clone(c.paths.Origins)
	if c.paths.Origins == nil {
		c.paths.Origins = make(map[string]productpaths.Path)
	}
	for _, name := range []string{pathRoleBadger, pathRoleSQLite} {
		delete(c.paths.Origins, name)
	}
	for name, path := range map[string]string{"data": root, "state": root, "cache": c.paths.CacheDir, pathRoleFiles: c.Files.Path, pathRoleRuntime: c.Catalog.StateDirectory, pathRoleBaseline: c.paths.BaselineDir} {
		c.paths.Origins[name] = productpaths.Path{Path: path, Origin: "development-scratch"}
	}
	return nil
}
