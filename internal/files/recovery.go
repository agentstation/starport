package files

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryRecordReader supplies bounded reads from a captured record image.
type RecoveryRecordReader interface {
	GetBounded(context.Context, string, int) ([]byte, error)
}

// ReadRecoveryFile checks the account and canonical identity of a referenced file.
// It does not make expired or pending files readable to callers.
func ReadRecoveryFile(ctx context.Context, records RecoveryRecordReader, account, id string) (File, error) {
	data, err := records.GetBounded(ctx, storageKey(account, id), 16384)
	if errors.Is(err, storage.ErrNotFound) {
		return File{}, ErrFileNotFound
	}
	if err != nil {
		return File{}, err
	}
	file, err := decodeFile(data)
	if err != nil || file.Account != account || file.ID != id {
		return File{}, ErrCorruptRecord
	}
	return file, nil
}

// RecoveryOutputMatches permits an interrupted digest bind only for unfinished output.
func (f File) RecoveryOutputMatches(size int64, digest string, ready bool) bool {
	if f.Purpose != PurposeBatchOutput || f.outputIdentity == "" {
		return false
	}
	if f.State == FileStatePending {
		if ready {
			return false
		}
		if f.outputDigest == "" {
			return true
		}
	}
	return f.Bytes == size && f.outputDigest == digest && digest != ""
}

// VerifyRecoveryRecord checks canonical identity and retained bytes at the capture time.
// Pending, deleting, and expired records can legitimately have no live bytes.
// It preserves those states and never publishes, deletes, or grants access.
func VerifyRecoveryRecord(ctx context.Context, key string, data []byte, source blob.PublicationReader, capturedAt time.Time) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	file, err := decodeFile(data)
	if err != nil || key != storageKey(file.Account, file.ID) || source == nil || capturedAt.IsZero() {
		return File{}, ErrCorruptRecord
	}
	required := file.State == FileStateReady && !file.Expired(capturedAt)
	info, err := source.StatPublished(ctx, file.blobKey)
	if errors.Is(err, blob.ErrNotFound) && !required {
		return file, nil
	}
	if err != nil {
		return File{}, errors.Join(ErrCorruptRecord, err)
	}
	// Ordinary pending uploads have no accepted size yet. An output digest
	// binds a pending result before its ready transition.
	size := file.Bytes
	if file.State == FileStatePending && file.outputDigest == "" {
		size = info.Size
	}
	if info.Size != size {
		return File{}, ErrCorruptRecord
	}
	if err := blob.VerifyPublished(ctx, source, file.blobKey, size, file.outputDigest); err != nil {
		return File{}, errors.Join(ErrCorruptRecord, err)
	}
	return file, nil
}
