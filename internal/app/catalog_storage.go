package app

import (
	"context"
	"fmt"

	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

// validateCatalogStorage checks catalog runtime ownership. Under shared
// management it waits for openConfigurationAuthority, which applies the stored
// revision and runs it again, so no local deployment value takes part.
func (b *runtimeBuilder) validateCatalogStorage() error {
	if !b.config.SharedRevisionApplied() {
		return nil
	}
	return catalogSettings(b.config).ValidateStorageSelection(context.Background())
}

func (b *runtimeBuilder) guardLocalSetup() error {
	selected := b.config.RuntimeStorage()
	if selected.Type == storage.StorageTypeValkey || selected.Badger.InMemory {
		return nil
	}
	paths := b.config.EffectivePaths()
	paths.BadgerDir = b.config.Storage.Badger.Path
	guard, err := setup.GuardLocalStorage(context.Background(), paths)
	if err != nil {
		return fmt.Errorf("open storage: guard local setup: %w", err)
	}
	b.application.own("local setup guard", func(context.Context) error { return guard.Close() })
	return nil
}

func (b *runtimeBuilder) prepareInferencePolicy() error {
	ctx := context.Background()
	legacy := false
	if b.config.InferenceCredentialPolicyDirectory() != "" {
		retained, err := catalogSettings(b.config).HasRetainedInstallation(ctx, b.application.store)
		if err != nil {
			return fmt.Errorf("inspect inference policy installation: %w", err)
		}
		legacy = retained
	}
	return b.config.InitializeInferencePolicy(ctx, legacy)
}
