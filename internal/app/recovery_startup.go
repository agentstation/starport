package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// relationalStartupTimeout bounds each construction step that opens and reads
// the relational database: the two recovery inspections and the migration.
// One bound for the three steps, because the inspections read the database
// that the migration opens a few steps later, and a bound shorter than the
// migration's fails a slow disk at the preflight that was meant to be the
// cheap read. A loaded CI runner took more than 5 s to open a SQLite file
// that the previous process had just closed and to read its schema.
const relationalStartupTimeout = 30 * time.Second

func inspectRecoverySQL(ctx context.Context, settings config.StorageConfig, deployment string) (sqlstore.RecoveryStartupState, error) {
	return sqlstore.InspectRecoveryStartup(ctx, settings.RuntimeSQL(), deployment)
}

// inspectRecoveryStartup runs before setup can create local installation state.
// Native read failures stop construction. They never establish first boot.
func (b *runtimeBuilder) inspectRecoveryStartup() error {
	ctx, cancel := context.WithTimeout(context.Background(), relationalStartupTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), relationalStartupTimeout)
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
