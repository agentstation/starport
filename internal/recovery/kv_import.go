package recovery

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
)

// KVImportResult reports processed records, including records already expired.
// The import barrier remains present. This result never approves startup.
type KVImportResult struct {
	ProcessedRecords   int64 `json:"processed_records"`
	RoundedExpirations int64 `json:"rounded_expirations"`
}

type kvImportClaim struct {
	Version     int        `json:"version"`
	OperationID string     `json:"operation_id"`
	Snapshot    KVSnapshot `json:"snapshot"`
}

// ImportKV verifies a private copy before claiming and filling an empty target.
// Exact retries preserve the original expirations and import identity.
// The caller must fence target writers and retain the barrier through complete recovery.
func ImportKV(ctx context.Context, target storage.RecordTransfer, operationID, source, scratch string, expected KVSnapshot) (result KVImportResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if target == nil || strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return result, errors.New("KV import requires a target and a bounded operation identifier")
	}
	if err := expected.validate(); err != nil {
		return result, err
	}
	resolution := target.ExpiryResolution().Milliseconds()
	if resolution != 1 && resolution != 1000 {
		return result, errors.New("unsupported KV expiry precision")
	}
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return result, err
	}
	parentRoot, err := parent.Open()
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, parentRoot.Close()) }()
	name := ".kv-import-" + rand.Text()
	_, root, err := newKVSnapshotDirectory(filepath.Join(scratch, name))
	if err != nil {
		return result, err
	}
	identity, err := parentRoot.Lstat(name)
	if err != nil {
		return result, errors.Join(err, root.Close())
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
		current, err := parentRoot.Lstat(name)
		if err == nil && os.SameFile(identity, current) {
			resultErr = errors.Join(resultErr, parentRoot.RemoveAll(name), productfiles.SyncDirectory(parentRoot))
		}
	}()
	if err := copyKVSnapshot(ctx, root, source, expected); err != nil {
		return result, err
	}
	path := KVSnapshotPath(filepath.Join(scratch, name))
	if err := validateKVSnapshot(ctx, path, expected.Records); err != nil {
		return result, err
	}
	// Validate the complete record contract before any target mutation.
	if err := visitKVSnapshot(ctx, path, func(storage.TransferRecord) error { return nil }); err != nil {
		return result, err
	}
	claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: operationID, Snapshot: expected})
	if err != nil {
		return result, err
	}
	if err := target.Claim(ctx, claim); err != nil {
		return result, err
	}
	err = visitKVSnapshot(ctx, path, func(record storage.TransferRecord) error {
		if err := target.Import(ctx, claim, record); err != nil {
			return err
		}
		result.ProcessedRecords++
		if record.ExpiresAtMillis%resolution != 0 {
			result.RoundedExpirations++
		}
		return nil
	})
	return result, err
}
