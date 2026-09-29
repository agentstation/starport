package recovery

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// ImportedAuthorityRequest binds final approval to one prepared relational import.
// DecisionSHA256 identifies the coordinator's complete accepted recovery decision.
type ImportedAuthorityRequest struct {
	Closed         Record
	BackendID      string
	Evidence       string
	OperationID    string
	Snapshot       sqlstore.SQLiteSnapshot
	Import         sqlstore.RelationalImportIdentity
	DecisionSHA256 string
}

// ApproveImportedAuthority installs native authority, then commits SQL approval
// and import activation together. Earlier component releases must already be safe.
// The coordinator owns history reconciliation, catalog preparation, and fencing.
func (w *Witness) ApproveImportedAuthority(ctx context.Context, backend storage.IncarnationProvider, request ImportedAuthorityRequest) (Record, error) {
	if ctx == nil || w == nil || w.db == nil || request.Import.OperationID != request.OperationID ||
		request.Closed.Open || request.Closed.Epoch <= 0 ||
		(FreshRequest{OperationID: request.OperationID, Evidence: request.Evidence}).Validate() != nil ||
		validDeployment(request.Closed.DeploymentID) != nil || strings.TrimSpace(request.BackendID) == "" || len(request.BackendID) > 256 {
		return Record{}, ErrConflict
	}
	next := request.Closed
	next.Open, next.BackendID, next.Evidence = true, request.BackendID, request.Evidence
	err := w.db.ActivateRelationalImport(ctx, request.Snapshot, request.Import, request.DecisionSHA256, func(ctx context.Context, conn *sql.Conn) error {
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
