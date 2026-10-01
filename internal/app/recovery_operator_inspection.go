package app

import (
	"bytes"
	"context"
	"encoding/json/v2"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
)

// InspectRecoveryOperatorInputs reopens the original decision without publication or repair.
// It checks current selected inputs and native targets without granting activation permission.
func InspectRecoveryOperatorInputs(ctx context.Context, cfg *config.Config, db *sqlstore.DB, blobs blob.RestoreTarget, request RecoveryOperatorInputsRequest) (*VerifiedOperatorInputs, error) {
	if err := validateOperatorInputRequest(ctx, request); err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(request.JournalDirectory)
	if err != nil {
		return nil, err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return nil, err
	}
	retained, err := directory.ReadFile(recoveryOperatorInputsFile, 8<<20)
	if err != nil {
		return nil, err
	}
	record, files, err := configuredOperatorInputRecord(ctx, cfg, db, blobs, request, directory)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(retained, body) {
		return nil, recovery.ErrConflict
	}
	verified := &VerifiedOperatorInputs{state: &verifiedOperatorInputState{request: request, directory: directory, body: bytes.Clone(retained), files: files}}
	if err := verified.Check(ctx, cfg, db, blobs, request); err != nil {
		return nil, err
	}
	return verified, nil
}
