package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrBootstrapConsumed requires controlled recovery when an initial publication is uncertain or missing.
var ErrBootstrapConsumed = errors.New("catalog bootstrap permission is consumed; recovery is required")

// CheckBootstrap requires unused first-publication permission from fresh initialization.
// The independent SQL record survives loss of all catalog keys in the KV backend.
func (w *Witness) CheckBootstrap(ctx context.Context, expected Record) error {
	if err := validDeployment(expected.DeploymentID); err != nil {
		return err
	}
	if !expected.Open || expected.Epoch <= 0 {
		return ErrClosed
	}
	var allowed int
	err := w.db.QueryRowContext(ctx, w.db.Bind(`SELECT bootstrap_allowed FROM catalog_recovery
WHERE deployment_id = ? AND epoch = ? AND gate_open = 1 AND backend_id = ? AND evidence = ?`),
		expected.DeploymentID, expected.Epoch, expected.BackendID, expected.Evidence).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("read catalog bootstrap permission: %w", err)
	}
	if allowed != 1 {
		return ErrBootstrapConsumed
	}
	return nil
}

// ConsumeBootstrap durably closes first-use permission before the native KV publication.
// An uncertain result must stop publication. This operation does not prove a KV commit.
func (w *Witness) ConsumeBootstrap(ctx context.Context, expected Record) error {
	if err := validDeployment(expected.DeploymentID); err != nil {
		return err
	}
	if !expected.Open || expected.Epoch <= 0 {
		return ErrClosed
	}
	result, err := w.db.ExecContext(ctx, w.db.Bind(`UPDATE catalog_recovery SET bootstrap_allowed = 0
WHERE deployment_id = ? AND epoch = ? AND gate_open = 1 AND backend_id = ? AND evidence = ? AND bootstrap_allowed = 1`),
		expected.DeploymentID, expected.Epoch, expected.BackendID, expected.Evidence)
	if err != nil {
		return fmt.Errorf("consume catalog bootstrap permission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
