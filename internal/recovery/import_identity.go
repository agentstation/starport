package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"math"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
)

// PreparedImportIdentity binds native owners to the same verified backup and operation.
// It identifies prepared state without proving later history or permitting activation.
type PreparedImportIdentity struct {
	ComponentOperation string                            `json:"component_operation"`
	Boundary           Record                            `json:"boundary"`
	KVClaim            []byte                            `json:"kv_claim"`
	KVOriginal         KVSnapshot                        `json:"kv_original"`
	SQL                sqlstore.RelationalImportIdentity `json:"sql"`
	SQLOriginal        sqlstore.SQLiteSnapshot           `json:"sql_original"`
	BlobOriginal       blob.Snapshot                     `json:"blob_original"`
}

// ImportIdentity derives native claims without reopening targets or repeating preparation.
// The caller retains these identities through replay, inspection, and activation retries.
func (s *RestoreSource) ImportIdentity(operation RestoreOperation) (PreparedImportIdentity, error) {
	if s == nil || s.manifest.Format != bundleFormat {
		return PreparedImportIdentity{}, ErrConflict
	}
	if err := operation.Validate(); err != nil {
		return PreparedImportIdentity{}, err
	}
	identity, err := json.Marshal(struct {
		Version             int
		Operation, Manifest string
		FencingEvidence     string `json:",omitempty"`
	}{1, operation.ID, s.request.ManifestSHA256, operation.FencingEvidence})
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	digest := sha256.Sum256(identity)
	component := hex.EncodeToString(digest[:])
	sql, boundary, err := sqlRestoreIdentity(component, s.request.ManifestSHA256, s.manifest.Request.Boundary)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: component, Snapshot: s.manifest.KV})
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	return PreparedImportIdentity{ComponentOperation: component, Boundary: boundary, KVClaim: claim,
		KVOriginal: s.manifest.KV, SQL: sql, SQLOriginal: s.manifest.SQL, BlobOriginal: s.manifest.Blobs}, nil
}

func sqlRestoreIdentity(operation, manifest string, original Record) (sqlstore.RelationalImportIdentity, Record, error) {
	if original.Epoch == math.MaxInt64 {
		return sqlstore.RelationalImportIdentity{}, Record{}, ErrConflict
	}
	policy, err := json.Marshal(struct {
		Version   string
		Operation string
		Manifest  string
		Boundary  Record
	}{"sql-restore-closed-v1", operation, manifest, original})
	if err != nil {
		return sqlstore.RelationalImportIdentity{}, Record{}, err
	}
	digest := sha256.Sum256(policy)
	policyID := hex.EncodeToString(digest[:])
	expected := Record{DeploymentID: original.DeploymentID, Epoch: original.Epoch + 1, Evidence: "restore-prepared:" + policyID}
	return sqlstore.RelationalImportIdentity{OperationID: operation, RestrictionID: policyID}, expected, nil
}
