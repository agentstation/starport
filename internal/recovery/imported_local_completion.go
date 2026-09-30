package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const localRecoveryBackendPrefix = "badger-target-v1:"

// ImportedLocalCompletionRequest binds complete local recovery to original native component positions.
// LocalTargetSHA256 identifies the accepted Badger directory. DecisionSHA256 identifies the complete coordinator decision.
// The coordinator separately verifies history, blobs, catalog permission, current inputs, and external fencing.
type ImportedLocalCompletionRequest struct {
	Closed                                                   Record
	Snapshot                                                 sqlstore.SQLiteSnapshot
	Import                                                   sqlstore.RelationalImportIdentity
	OperationID, DecisionSHA256, Evidence, LocalTargetSHA256 string
	KVClaim                                                  []byte
	KVPosition                                               storage.ImportReplayPosition
}

// Format excludes claims and evidence from supported value diagnostics.
// This caller-owned request is not a sealed capability. Log the checked target instead.
func (ImportedLocalCompletionRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private local import completion>"))
}

// CompleteImportedLocalAt atomically opens the exact local recovery record and releases its SQLite import barrier.
// It requires completed Badger activation and nonzero final KV and SQL positions.
// It creates no KV authority, remote incarnation, permission renewal, or time qualification.
// Exact completed retries preserve later changes and refuse a later withdrawal.
func (w *Witness) CompleteImportedLocalAt(ctx context.Context, target *storage.LocalRecoveryTarget, request ImportedLocalCompletionRequest, position sqlstore.RelationalReplayPosition) (Record, error) {
	return w.CompleteImportedLocalCheckedAt(ctx, target, request, position, nil)
}

// CompleteImportedLocalCheckedAt checks current application approval inside the final SQL transaction.
// The check must use no second connection from the same SQL pool.
func (w *Witness) CompleteImportedLocalCheckedAt(ctx context.Context, target *storage.LocalRecoveryTarget, request ImportedLocalCompletionRequest, position sqlstore.RelationalReplayPosition, check func(context.Context) error) (Record, error) {
	if err := w.validateImportedLocalCompletion(ctx, target, request, position); err != nil {
		return Record{}, err
	}
	request.KVClaim = bytes.Clone(request.KVClaim)
	if err := target.CheckActivatedImportAt(ctx, request.KVClaim, request.KVPosition, request.DecisionSHA256); err != nil {
		return Record{}, err
	}
	next := importedLocalCompletedRecord(request)
	err := w.db.ActivateRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256, func(ctx context.Context, conn *sql.Conn) error {
		if err := w.checkLockedClosedBoundary(ctx, conn, request.Closed); err != nil {
			return err
		}
		if err := target.CheckActivatedImportAt(ctx, request.KVClaim, request.KVPosition, request.DecisionSHA256); err != nil {
			return err
		}
		if check != nil {
			if err := check(ctx); err != nil {
				return err
			}
		}
		if _, err := w.replaceWith(ctx, conn, request.Closed, next); err != nil {
			return err
		}
		return target.CheckActivatedImportAt(ctx, request.KVClaim, request.KVPosition, request.DecisionSHA256)
	})
	if err != nil {
		return Record{}, err
	}
	if err := w.checkCurrentLocalCompletion(ctx, target, request, next); err != nil {
		return Record{}, err
	}
	return next, nil
}

// CheckImportedLocalCompletionAt verifies current local recovery approval and exact historical native completion.
// It preserves later withdrawals and grants no catalog permission or admission capability.
// Use the SQL owner's receipt check separately to report historical completion after a withdrawal.
func (w *Witness) CheckImportedLocalCompletionAt(ctx context.Context, target *storage.LocalRecoveryTarget, request ImportedLocalCompletionRequest, position sqlstore.RelationalReplayPosition) error {
	if err := w.validateImportedLocalCompletion(ctx, target, request, position); err != nil {
		return err
	}
	request.KVClaim = bytes.Clone(request.KVClaim)
	if err := target.CheckActivatedImportAt(ctx, request.KVClaim, request.KVPosition, request.DecisionSHA256); err != nil {
		return err
	}
	if err := w.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256); err != nil {
		return err
	}
	return w.checkCurrentLocalCompletion(ctx, target, request, importedLocalCompletedRecord(request))
}

func (w *Witness) validateImportedLocalCompletion(ctx context.Context, target *storage.LocalRecoveryTarget, request ImportedLocalCompletionRequest, position sqlstore.RelationalReplayPosition) error {
	if ctx == nil || w == nil || w.db == nil || w.db.Dialect() != sqlstore.TypeSQLite || target == nil {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrConflict
	}
	if request.Closed.Open || request.Closed.Epoch <= 0 || validDeployment(request.Closed.DeploymentID) != nil ||
		request.Import.OperationID != request.OperationID || (FreshRequest{OperationID: request.OperationID, Evidence: request.Evidence}).Validate() != nil ||
		!historyDigest(request.DecisionSHA256) || !historyDigest(request.LocalTargetSHA256) || target.TargetSHA256() != request.LocalTargetSHA256 ||
		len(request.KVClaim) == 0 || len(request.KVClaim) > 4096 || request.KVPosition.Sequence <= 0 || !historyDigest(request.KVPosition.ReceiptSHA256) ||
		position.Sequence <= 0 || !historyDigest(position.ReceiptSHA256) {
		return ErrConflict
	}
	return target.Check(ctx)
}

func importedLocalCompletedRecord(request ImportedLocalCompletionRequest) Record {
	next := request.Closed
	next.Open, next.BackendID, next.Evidence = true, localRecoveryBackendPrefix+request.LocalTargetSHA256, request.Evidence
	return next
}

func (w *Witness) checkCurrentLocalCompletion(ctx context.Context, target *storage.LocalRecoveryTarget, request ImportedLocalCompletionRequest, expected Record) error {
	current, err := w.Current(ctx, expected.DeploymentID)
	if err != nil || current != expected {
		return errors.Join(ErrConflict, err)
	}
	return target.CheckActivatedImportAt(ctx, request.KVClaim, request.KVPosition, request.DecisionSHA256)
}
