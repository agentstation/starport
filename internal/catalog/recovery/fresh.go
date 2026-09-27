package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// FreshRequest names one explicit initialization operation and its audit reference.
type FreshRequest struct {
	OperationID string `json:"operation_id"`
	Evidence    string `json:"evidence"`
}

// Validate checks request bounds before any storage connection or mutation.
func (r FreshRequest) Validate() error {
	if strings.TrimSpace(r.OperationID) == "" || len(r.OperationID) > 128 ||
		strings.TrimSpace(r.Evidence) == "" || len(r.Evidence) > 2048 {
		return errors.New("fresh initialization requires an operation ID of 1–128 bytes and evidence of 1–2048 bytes")
	}
	return nil
}

// InitializeFresh approves only empty SQL and KV stores for one deployment.
// All gateway processes and schema writers must remain stopped until it finishes.
// An interrupted KV claim permits retry only for the same operation and backend.
func (w *Witness) InitializeFresh(ctx context.Context, backend storage.FreshDatabase, deployment string, request FreshRequest) (Record, error) {
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	if err := validDeployment(deployment); err != nil {
		return Record{}, err
	}
	if backend == nil {
		return Record{}, errors.New("fresh initialization requires a shared KV backend")
	}
	identity, err := backend.ObserveIncarnation(ctx)
	if err != nil {
		return Record{}, err
	}
	claim, err := json.Marshal(struct {
		Deployment string       `json:"deployment"`
		Backend    string       `json:"backend"`
		Request    FreshRequest `json:"request"`
	}{deployment, identity, request})
	if err != nil {
		return Record{}, err
	}
	record := Record{DeploymentID: deployment, Epoch: 1, Open: true, BackendID: identity, Evidence: request.Evidence}
	err = w.db.WithFreshSchema(ctx, func(tx *sql.Tx) error {
		if err := backend.ClaimEmptyDatabase(ctx, identity, "catalog:bootstrap:v1", claim); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, w.db.Bind(`INSERT INTO catalog_recovery (deployment_id, epoch, gate_open, backend_id, evidence, bootstrap_allowed) VALUES (?, 1, 1, ?, ?, 1)`), deployment, identity, request.Evidence)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}
