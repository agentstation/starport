package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
)

// MigrateRuntime executes a stopped runtime move without opening SQL or serving requests.
func MigrateRuntime(ctx context.Context, cfg *config.Config, phase string, migration catalog.RuntimeMigration) (result catalog.RuntimeMigrationResult, err error) {
	if cfg == nil {
		return result, fmt.Errorf("runtime migration requires configuration")
	}
	switch phase {
	case "prepare", "stage", "publish":
		if cfg.EffectivePaths().RuntimeDir != migration.SourceDirectory {
			return result, fmt.Errorf("configuration must select the original runtime before publication")
		}
	case "complete":
		if err := cfg.VerifySavedRuntimeSelection(ctx, migration.TargetDirectory, migration.SourceIdentity); err != nil {
			return result, err
		}
	default:
		return result, fmt.Errorf("unknown runtime migration phase %q", phase)
	}
	selected := cfg.Storage.RuntimeStorage()
	if selected.Type == storage.StorageTypeBadger {
		if selected.Badger.InMemory {
			return result, fmt.Errorf("runtime migration requires persistent catalog storage")
		}
		directory, err := productfiles.ExistingDirectory(selected.Badger.Path)
		if err != nil {
			return result, err
		}
		if _, err := directory.ReadFile("MANIFEST", 64<<20); err != nil {
			return result, fmt.Errorf("runtime migration requires an existing Badger database: %w", err)
		}
		selected.Badger.SyncWrites = true
	}
	store, err := storage.Open(selected)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	settings := catalogSettings(cfg)
	switch phase {
	case "prepare":
		return migration.Prepare(ctx, store, settings)
	case "stage":
		return migration.Stage(ctx, store, settings)
	case "publish":
		return migration.Publish(ctx, store, settings)
	}
	connected, err := migration.OpenReplacement(ctx, store, settings)
	if err != nil {
		return result, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, connected.Close(closeCtx))
	}()
	if err := cfg.VerifySavedRuntimeSelection(ctx, migration.TargetDirectory, migration.SourceIdentity); err != nil {
		return result, err
	}
	return migration.Complete(ctx, connected, settings)
}
