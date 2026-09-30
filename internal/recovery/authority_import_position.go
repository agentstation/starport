package recovery

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// ApproveImportedAuthorityAt commits approval at the exact final SQL replay position.
// The coordinator must first verify component releases, complete history, and fencing.
// Native work requires a context deadline. SQL approval uses the locked transaction connection.
// Completed retries preserve later withdrawals. Historical receipts grant no current permission.
func (w *Witness) ApproveImportedAuthorityAt(ctx context.Context, backend storage.IncarnationProvider, request ImportedAuthorityRequest, position sqlstore.RelationalReplayPosition) (Record, error) {
	return w.ApproveImportedAuthorityCheckedAt(ctx, backend, request, position, nil)
}

// ApproveImportedAuthorityCheckedAt repeats current application approval inside the final SQL transaction.
// The check must use no second connection from the same SQL pool.
func (w *Witness) ApproveImportedAuthorityCheckedAt(ctx context.Context, backend storage.IncarnationProvider, request ImportedAuthorityRequest, position sqlstore.RelationalReplayPosition, check func(context.Context) error) (Record, error) {
	if ctx == nil || w == nil || w.db == nil || backend == nil {
		return Record{}, ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded || !validImportedAuthorityRequest(request) {
		return Record{}, ErrConflict
	}
	next := request.Closed
	next.Open, next.BackendID, next.Evidence = true, request.BackendID, request.Evidence
	err := w.db.ActivateRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256, func(ctx context.Context, conn *sql.Conn) error {
		if err := w.checkLockedClosedBoundary(ctx, conn, request.Closed); err != nil {
			return err
		}
		if check != nil {
			if err := check(ctx); err != nil {
				return err
			}
		}
		if _, err := w.prepareAuthorityWith(ctx, conn, backend, request.Closed, request.BackendID, request.Evidence, request.OperationID); err != nil {
			return err
		}
		_, err := w.replaceWith(ctx, conn, request.Closed, next)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	current, err := w.Current(ctx, next.DeploymentID)
	if err != nil || current != next {
		return Record{}, errors.Join(ErrConflict, err)
	}
	if _, err := w.OpenAuthority(ctx, backend, next.DeploymentID); err != nil {
		return Record{}, err
	}
	return next, nil
}

func validImportedAuthorityRequest(request ImportedAuthorityRequest) bool {
	return request.Import.OperationID == request.OperationID && !request.Closed.Open && request.Closed.Epoch > 0 &&
		(FreshRequest{OperationID: request.OperationID, Evidence: request.Evidence}).Validate() == nil &&
		validDeployment(request.Closed.DeploymentID) == nil && strings.TrimSpace(request.BackendID) != "" && len(request.BackendID) <= 256
}
