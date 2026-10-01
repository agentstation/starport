package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func inspectRecoverySQL(ctx context.Context, settings config.StorageConfig, deployment string) (sqlstore.RecoveryStartupState, error) {
	return sqlstore.InspectRecoveryStartup(ctx, settings.RuntimeSQL(), deployment)
}

// inspectRecoveryStartup runs before setup can create local installation state.
// Native read failures stop construction. They never establish first boot.
func (b *runtimeBuilder) inspectRecoveryStartup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := b.factories.inspectRecoverySQL(ctx, b.config.Storage, b.config.EffectivePaths().DeploymentID)
	if err != nil {
		return fmt.Errorf("inspect SQL recovery startup: %w", err)
	}
	b.recoveryStartup = state
	return nil
}

// checkRecoveryStartup joins native controls before maintenance or policy writes.
// This consistency check grants no recovery activation or catalog permission.
// The recovery coordinator must establish the complete retained activation proof.
func (b *runtimeBuilder) checkRecoveryStartup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kv, err := storage.InspectRecoveryStartup(ctx, b.application.store)
	if err != nil {
		return fmt.Errorf("inspect KV recovery startup: %w", err)
	}
	relational, err := b.factories.inspectRecoverySQL(ctx, b.config.Storage, b.config.EffectivePaths().DeploymentID)
	if err != nil {
		return fmt.Errorf("inspect SQL recovery startup: %w", err)
	}
	if relational != b.recoveryStartup || kv.Activated != relational.Activated || kv.DecisionSHA256 != relational.DecisionSHA256 {
		return fmt.Errorf("recovery owners have incomplete activation: %w", storage.ErrImportRestricted)
	}
	if maintenance, ok := b.application.store.(interface{ StartMaintenance() error }); ok {
		if err := maintenance.StartMaintenance(); err != nil {
			return fmt.Errorf("start storage maintenance: %w", err)
		}
	}
	return nil
}
