package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// ImportedReferenceSources selects native owners whose import barriers remain closed.
// The caller must fence all writers before inspection and retain that fence afterward.
type ImportedReferenceSources struct {
	KV    storage.ImportInspector
	SQL   *sqlstore.DB
	Blobs blob.ImportInspector
}

// ImportedReferenceRequest binds inspection to the exact imported state and replay positions.
// The coordinator selects CapturedAt as the validation time. Evidence must not choose it.
// It evaluates retained blob expiry and is not an authorization deadline.
// These identities do not prove fencing or complete independent recovery history.
type ImportedReferenceRequest struct {
	Boundary      Record                            `json:"boundary"`
	CapturedAt    time.Time                         `json:"captured_at"`
	KVClaim       []byte                            `json:"kv_claim"`
	KVPosition    storage.ImportReplayPosition      `json:"kv_position"`
	SQLOriginal   sqlstore.SQLiteSnapshot           `json:"sql_original"`
	SQLIdentity   sqlstore.RelationalImportIdentity `json:"sql_identity"`
	SQLPosition   sqlstore.RelationalReplayPosition `json:"sql_position"`
	BlobOperation string                            `json:"blob_operation"`
	BlobOriginal  blob.Snapshot                     `json:"blob_original"`
}

// ImportedReferences binds a checked graph to native snapshots and its exact input identities.
// It grants no permission to activate stores or admit requests. Retained uncertain work stays held.
type ImportedReferences struct {
	RequestSHA256 string                  `json:"request_sha256"`
	KV            KVSnapshot              `json:"kv"`
	SQL           sqlstore.SQLiteSnapshot `json:"sql"`
	Blobs         blob.Snapshot           `json:"blobs"`
	References    ReferenceReport         `json:"references"`
}

// InspectImportedReferences captures and checks a closed imported graph without changing authority.
// Destination must be new and its parent private. Scratch must be an existing private directory.
// Failed captures remain diagnostic artifacts. Failure returns no checked graph receipt.
//
// Native owner guards run again after graph validation. External fencing remains required.
func InspectImportedReferences(ctx context.Context, sources ImportedReferenceSources, request ImportedReferenceRequest, destination, scratch string, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (result ImportedReferences, resultErr error) {
	if ctx == nil {
		return result, errors.New("imported reference context is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if sources.KV == nil || sources.SQL == nil || sources.Blobs == nil || encryption == nil || request.Boundary.Open || validDeployment(request.Boundary.DeploymentID) != nil || request.Boundary.Epoch <= 0 || strings.TrimSpace(request.Boundary.Evidence) == "" || request.CapturedAt.IsZero() || len(request.KVClaim) == 0 || len(request.KVClaim) > 4096 || len(request.Boundary.BackendID) > 256 || len(request.Boundary.Evidence) > 4096 {
		return result, errors.New("closed imported reference identities and owners are required")
	}
	// Copy mutable claim bytes before invoking any owner callback.
	request.KVClaim = append([]byte(nil), request.KVClaim...)
	body, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	binding := sha256.Sum256(body)
	_, root, err := newKVSnapshotDirectory(destination)
	if err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
		if resultErr != nil {
			result = ImportedReferences{}
		}
	}()
	kvPath, sqlPath, blobPath := filepath.Join(destination, "kv"), filepath.Join(destination, "sql"), filepath.Join(destination, "blobs.tar")
	result.KV, err = SnapshotKV(ctx, importedKVSource{sources.KV, request.KVClaim, request.KVPosition}, kvPath)
	if err != nil {
		return result, err
	}
	sqlResult, err := sources.SQL.SnapshotRelationalImport(ctx, sqlPath, request.SQLOriginal, request.SQLIdentity, request.SQLPosition)
	if err != nil {
		return result, err
	}
	result.SQL = sqlResult.Snapshot
	result.Blobs, err = sources.Blobs.SnapshotImport(ctx, blobPath, request.BlobOperation, request.BlobOriginal)
	if err != nil {
		return result, err
	}
	result.References, err = inspectImportedCopies(ctx, kvPath, sqlPath, blobPath, scratch, request, result, encryption, inspectors...)
	if err != nil {
		return result, err
	}
	if err := sources.SQL.CheckRelationalImportPosition(ctx, request.SQLOriginal, request.SQLIdentity, request.SQLPosition); err != nil {
		return result, err
	}
	if err := inspectReferenceBoundary(ctx, sources.SQL, sources.SQL.Bind(referenceBoundaryQuery), request.Boundary); err != nil {
		return result, err
	}
	if err := sources.Blobs.CheckImport(ctx, request.BlobOperation, request.BlobOriginal); err != nil {
		return result, err
	}
	if err := sources.KV.InspectImport(ctx, request.KVClaim, request.KVPosition, func(storage.TransferRecord) error { return nil }); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.RequestSHA256 = hex.EncodeToString(binding[:])
	return result, nil
}

func inspectImportedCopies(ctx context.Context, kvPath, sqlPath, blobPath, scratch string, request ImportedReferenceRequest, snapshots ImportedReferences, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (report ReferenceReport, resultErr error) {
	records, err := OpenKVSnapshot(ctx, KVSnapshotPath(kvPath), scratch, snapshots.KV)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, records.Close()) }()
	image, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(sqlPath, "starport.db"), snapshots.SQL, scratch)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	blobs, err := blob.OpenSnapshot(ctx, blobPath, scratch, snapshots.Blobs)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, blobs.Close()) }()
	report, err = inspectCapturedReferences(ctx, records, image, blobs, request.Boundary, request.CapturedAt, encryption, inspectors...)
	if err == nil && (report.UnknownAccountBudgetHistories != 0 || report.GatewayKeys.UnknownBudgetHistories != 0 || report.Identity.UnknownBudgetHistories != 0) {
		err = reservation.ErrHistoryUnknown
	}
	return report, err
}

type importedKVSource struct {
	owner    storage.ImportInspector
	claim    []byte
	position storage.ImportReplayPosition
}

func (s importedKVSource) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	return s.owner.InspectImport(ctx, s.claim, s.position, visit)
}
