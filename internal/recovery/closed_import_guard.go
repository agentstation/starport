package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/sqlstore"
)

// ClosedImportGuardRequest binds a closed recovery record to its exact native SQL import position.
type ClosedImportGuardRequest struct {
	Snapshot sqlstore.SQLiteSnapshot
	Import   sqlstore.RelationalImportIdentity
	Position sqlstore.RelationalReplayPosition
	Boundary Record
}

// GuardClosedImport locks the selected closed record on the native import transaction connection.
// The callback must use conn for SQL work and must not call ordinary Witness methods or start another transaction.
// The context must have a deadline. The caller must bound independent work and retain its native receipts.
// Failure can follow an independent KV commit. Retry must verify that receipt rather than repeat its effects.
//
// Success grants no permission and leaves the recovery and import barriers closed.
// External writer fencing must span the whole recovery operation between guards.
func (w *Witness) GuardClosedImport(ctx context.Context, request ClosedImportGuardRequest, callback func(context.Context, *sql.Conn) error) error {
	if w == nil || w.db == nil || callback == nil {
		return ErrClosed
	}
	if err := validDeployment(request.Boundary.DeploymentID); err != nil {
		return err
	}
	if request.Boundary.Open || request.Boundary.Epoch <= 0 {
		return ErrConflict
	}
	return w.db.GuardRelationalImport(ctx, request.Snapshot, request.Import, request.Position, func(ctx context.Context, conn *sql.Conn) error {
		if err := w.checkLockedClosedBoundary(ctx, conn, request.Boundary); err != nil {
			return err
		}
		if err := callback(ctx, conn); err != nil {
			return err
		}
		return w.checkLockedClosedBoundary(ctx, conn, request.Boundary)
	})
}

func (w *Witness) checkLockedClosedBoundary(ctx context.Context, conn *sql.Conn, expected Record) error {
	query := `SELECT epoch, gate_open, backend_id, evidence, bootstrap_allowed
FROM catalog_recovery WHERE deployment_id = ?`
	if w.db.Dialect() != sqlstore.TypeSQLite {
		query += " FOR UPDATE"
	}
	current := Record{DeploymentID: expected.DeploymentID}
	var opened, bootstrap int
	err := conn.QueryRowContext(ctx, w.db.Bind(query), expected.DeploymentID).Scan(&current.Epoch, &opened, &current.BackendID, &current.Evidence, &bootstrap)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrClosed
	}
	if err != nil {
		return fmt.Errorf("lock closed recovery record: %w", err)
	}
	current.Open = opened != 0
	if opened != 0 || bootstrap != 0 || current != expected {
		return ErrConflict
	}
	return ctx.Err()
}
