// Package recovery owns independent approval for the deployment KV incarnation.
package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/sqlstore"
)

var (
	// ErrClosed refuses use of a missing or closed recovery approval.
	ErrClosed = errors.New("catalog recovery gate is closed")
	// ErrConflict refuses a transition against a different recovery record.
	ErrConflict = errors.New("catalog recovery record changed")
)

// Record identifies the backend an independent authority permits this deployment to use.
// Evidence names the external reconciliation record, without storing its secrets.
type Record struct {
	DeploymentID string
	Epoch        int64
	Open         bool
	BackendID    string
	Evidence     string
}

// Witness stores recovery approval outside the KV backend that it approves.
// The host must supply an independent SQL database and enforce the recovery procedure.
type Witness struct {
	db *sqlstore.DB
}

// New retains an open SQL handle without reading or creating approval.
func New(db *sqlstore.DB) (*Witness, error) {
	if db == nil || db.DB == nil {
		return nil, errors.New("catalog recovery SQL database is required")
	}
	return &Witness{db: db}, nil
}

// Current reads the recovery record. Missing records confer no permission.
func (w *Witness) Current(ctx context.Context, deployment string) (Record, error) {
	if err := validDeployment(deployment); err != nil {
		return Record{}, err
	}
	r := Record{DeploymentID: deployment}
	var opened int
	err := w.db.QueryRowContext(ctx, w.db.Bind(`SELECT epoch, gate_open, backend_id, evidence
FROM catalog_recovery WHERE deployment_id = ?`), deployment).Scan(&r.Epoch, &opened, &r.BackendID, &r.Evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrClosed
	}
	if err != nil {
		return Record{}, fmt.Errorf("read catalog recovery record: %w", err)
	}
	if r.Epoch <= 0 || (opened != 0 && opened != 1) {
		return Record{}, errors.New("invalid catalog recovery record")
	}
	r.Open = opened == 1
	if r.Open && (r.BackendID == "" || r.Evidence == "") {
		return Record{}, errors.New("catalog recovery approval has no backend or evidence")
	}
	return r, nil
}

// Approved requires a complete open record. It does not verify the live KV process.
func (w *Witness) Approved(ctx context.Context, deployment string) (Record, error) {
	r, err := w.Current(ctx, deployment)
	if err != nil {
		return Record{}, err
	}
	if !r.Open {
		return Record{}, ErrClosed
	}
	return r, nil
}

// Initialize creates a closed record. Startup must not call it to approve an unknown backend.
func (w *Witness) Initialize(ctx context.Context, deployment string) (Record, error) {
	if err := validDeployment(deployment); err != nil {
		return Record{}, err
	}
	_, err := w.db.ExecContext(ctx, w.db.Bind(`INSERT INTO catalog_recovery
(deployment_id, epoch, gate_open, backend_id, evidence) VALUES (?, 1, 0, '', '')`), deployment)
	if err != nil {
		return Record{}, fmt.Errorf("initialize catalog recovery record: %w", err)
	}
	return Record{DeploymentID: deployment, Epoch: 1}, nil
}

// Close invalidates the prior epoch before recovery starts.
// External process or network controls must also fence unreachable gateways and old primaries.
func (w *Witness) Close(ctx context.Context, expected Record) (Record, error) {
	if expected.Epoch <= 0 || expected.Epoch == math.MaxInt64 {
		return Record{}, errors.New("catalog recovery epoch is invalid or exhausted")
	}
	next := expected
	next.Epoch++
	next.Open = false
	return w.replace(ctx, expected, next)
}

// Approve opens a closed epoch after external fencing and reconciliation.
// The evidence must identify the verified history boundary and recovery procedure result.
// This method cannot prove those external actions or substitute for them.
func (w *Witness) Approve(ctx context.Context, expected Record, backend, evidence string) (Record, error) {
	if expected.Open || expected.Epoch <= 0 {
		return Record{}, ErrConflict
	}
	if strings.TrimSpace(backend) == "" || len(backend) > 256 || strings.TrimSpace(evidence) == "" || len(evidence) > 4096 {
		return Record{}, errors.New("catalog recovery backend and bounded evidence reference are required")
	}
	next := expected
	next.Open, next.BackendID, next.Evidence = true, backend, evidence
	return w.replace(ctx, expected, next)
}

func (w *Witness) replace(ctx context.Context, expected, next Record) (Record, error) {
	if err := validDeployment(expected.DeploymentID); err != nil {
		return Record{}, err
	}
	result, err := w.db.ExecContext(ctx, w.db.Bind(`UPDATE catalog_recovery
SET epoch = ?, gate_open = ?, backend_id = ?, evidence = ?, bootstrap_allowed = 0
WHERE deployment_id = ? AND epoch = ? AND gate_open = ? AND backend_id = ? AND evidence = ?`),
		next.Epoch, gateValue(next.Open), next.BackendID, next.Evidence,
		expected.DeploymentID, expected.Epoch, gateValue(expected.Open), expected.BackendID, expected.Evidence)
	if err != nil {
		return Record{}, fmt.Errorf("change catalog recovery record: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Record{}, err
	}
	if count != 1 {
		return Record{}, ErrConflict
	}
	return next, nil
}

func validDeployment(deployment string) error {
	if strings.TrimSpace(deployment) == "" || len(deployment) > 128 {
		return errors.New("catalog recovery deployment ID must contain 1 through 128 bytes")
	}
	return nil
}

func gateValue(open bool) int {
	if open {
		return 1
	}
	return 0
}
