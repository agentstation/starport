package config

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrPersistentDevelopment rejects a configuration that persistent
// development cannot own: shared or external management, or storage away from
// the platform roots.
var ErrPersistentDevelopment = errors.New("starport dev requires local management and platform storage")

// LoadPersistentDevelopment loads the configuration through the serve loader
// and applies the persistent development profile.
func LoadPersistentDevelopment(ctx context.Context, overrides ...Override) (*Config, error) {
	return NewLoader().LoadPersistentDevelopment(ctx, overrides...)
}

// LoadPersistentDevelopment reads the same sources as Load and keeps the
// platform paths. The profile changes only the bind host, the log output, and
// TLS. A configuration that selects shared or external management or storage
// away from the platform roots fails here, before validation and before any
// store opens.
func (l *Loader) LoadPersistentDevelopment(ctx context.Context, overrides ...Override) (*Config, error) {
	platform, err := l.environmentPaths()
	if err != nil {
		return nil, newLoadFailure("configuration paths could not be resolved", err)
	}
	// The profile runs after the caller overrides, so no flag can widen it.
	// An Override returns no error, so the profile records the refusal and the
	// refusal takes precedence over a later validation error.
	var refusal error
	profile := func(cfg *Config) {
		cfg.Server.Host = loopbackHost
		cfg.Security.EnableTLS = false
		cfg.Security.TLSCertPath, cfg.Security.TLSKeyPath = "", ""
		cfg.Logging.Output, cfg.Logging.FilePath = logOutputStdout, ""
		refusal = cfg.persistentDevelopmentConflicts(platform)
	}
	cfg, err := l.Load(ctx, append(slices.Clone(overrides), profile)...)
	if refusal != nil {
		return nil, newLoadFailure(refusal.Error(), refusal)
	}
	return cfg, err
}

// persistentDevelopmentConflicts names each setting that moves management or
// storage away from the local platform layout.
func (c *Config) persistentDevelopmentConflicts(platform Paths) error {
	var conflicts []string
	for _, check := range []struct {
		name  string
		moved bool
	}{
		{managementEnvironment, c.ManagementMode() != ManagementLocal},
		{storageModeEnvironment, c.Storage.Mode != storageModeBadger},
		{badgerPathEnvironment, c.Storage.Badger.Path != platform.BadgerDir},
		{sqlModeEnvironment, c.Storage.SQL.Mode != sqlModeSQLite},
		{sqlitePathEnvironment, c.Storage.SQL.SQLite.Path != platform.SQLiteFile},
		{filesBackendEnvironment, c.Files.SelectedBackend() != BlobBackendFilesystem},
		{filesPathEnvironment, c.Files.Path != platform.FilesDir},
		{cacheBackendEnvironment, c.Cache.Backend != "" && c.Cache.Backend != cacheBackendLocal},
	} {
		if check.moved {
			conflicts = append(conflicts, check.name)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("%w: remove %s, run starport serve for this configuration, or run starport dev --ephemeral for a scratch gateway",
		ErrPersistentDevelopment, strings.Join(conflicts, ", "))
}
