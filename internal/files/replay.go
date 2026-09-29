package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryFile retains private file identity, metering, output lineage, and expiry.
type RecoveryFile struct{ state *recoveryFileState }
type recoveryFileState struct {
	file File
	raw  []byte
}

// Format hides private recovery evidence for every formatting verb.
func (RecoveryFile) Format(s fmt.State, _ rune) {
	_, _ = s.Write([]byte("<private file recovery evidence>"))
}

// MarshalJSON encodes private operator recovery evidence.
func (r RecoveryFile) MarshalJSON() ([]byte, error) {
	if r.state == nil {
		return nil, ErrCorruptRecord
	}
	return append([]byte(nil), r.state.raw...), nil
}

// UnmarshalJSON rejects unsupported private record fields and schemas.
func (r *RecoveryFile) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > 16384 {
		return ErrCorruptRecord
	}
	var schema fileRecord
	if json.Unmarshal(data, &schema, json.RejectUnknownMembers(true)) != nil {
		return ErrCorruptRecord
	}
	file, err := decodeFile(data)
	if err != nil {
		return err
	}
	if blob.ValidateKey(file.blobKey) != nil {
		return ErrCorruptRecord
	}
	r.state = &recoveryFileState{file: file, raw: append([]byte(nil), data...)}
	return nil
}

// SHA256 binds exact retained bytes, including the private durable fields.
func (r RecoveryFile) SHA256() string {
	if r.state == nil {
		return ""
	}
	sum := sha256.Sum256(r.state.raw)
	return hex.EncodeToString(sum[:])
}

// RecoveryReader supplies immutable complete owner bytes and original TTLs.
type RecoveryReader interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
}

// CaptureRecoveryFile copies and validates the complete private durable record.
func CaptureRecoveryFile(ctx context.Context, source RecoveryReader, account, id string) (RecoveryFile, error) {
	if ctx == nil || source == nil {
		return RecoveryFile{}, ErrCorruptRecord
	}
	raw, err := source.ReadCaptured(ctx, storageKey(account, id), 16384)
	if err != nil {
		return RecoveryFile{}, err
	}
	if raw.Key != storageKey(account, id) || raw.ExpiresAtMillis != 0 {
		return RecoveryFile{}, ErrCorruptRecord
	}
	var result RecoveryFile
	if err := result.UnmarshalJSON(raw.Value); err != nil {
		return RecoveryFile{}, err
	}
	if result.state.file.Account != account || result.state.file.ID != id {
		return RecoveryFile{}, ErrCorruptRecord
	}
	return result, nil
}

// RecoveryChange names an exact expected file and a later record or explicit deletion.
type RecoveryChange struct {
	Account        string        `json:"account"`
	ID             string        `json:"id"`
	ExpectedSHA256 string        `json:"expected_sha256"`
	After          *RecoveryFile `json:"after"`
}

// RecoveryByteAttachment projects only the facts owned by the stored-byte meter.
func (f File) RecoveryByteAttachment() storedbytes.RecoveryAttachment {
	return storedbytes.RecoveryAttachment{Holder: f.Account, FileID: f.ID, Metered: f.metered, Deleting: f.State == FileStateDeleting, Ready: f.State == FileStateReady, Bytes: f.Bytes}
}

// VerifyRecoveryByteAttachment includes positive retirement evidence for a deleting file.
func (f File) VerifyRecoveryByteAttachment(ctx context.Context, source blob.RecoveryPublicationReader) (storedbytes.RecoveryAttachment, error) {
	result := f.RecoveryByteAttachment()
	if f.metered && f.State == FileStateDeleting {
		if source == nil {
			return result, ErrCorruptRecord
		}
		state, err := source.InspectPublication(ctx, f.blobKey)
		if err != nil {
			return result, err
		}
		result.Retired = state.Kind == "retired"
	}
	return result, nil
}

// PrepareRecoveryReplay preserves retained identity, expiry, and cleanup history.
// The coordinator combines file and byte-account owner mutations under the import barrier.
// Deletion requires durable retirement. Missing bytes do not prove retirement.
func PrepareRecoveryReplay(ctx context.Context, source RecoveryReader, assets blob.PublicationReader, at time.Time, changes []RecoveryChange) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || assets == nil || at.IsZero() || len(changes) == 0 || len(changes) > 64 {
		return nil, ErrCorruptRecord
	}
	result := make([]storage.CompareAndSwapMutation, 0, len(changes))
	seen := map[string]bool{}
	for _, change := range changes {
		key := storageKey(change.Account, change.ID)
		if seen[key] || change.Account == "" || change.ID == "" {
			return nil, ErrCorruptRecord
		}
		seen[key] = true
		before, err := CaptureRecoveryFile(ctx, source, change.Account, change.ID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		if before.SHA256() != change.ExpectedSHA256 || before.state == nil && change.After == nil {
			return nil, storage.ErrConflict
		}
		mutation := storage.CompareAndSwapMutation{Key: key}
		if before.state != nil {
			mutation.ExpectedValue = before.state.raw
		}
		if change.After == nil {
			inspector, ok := assets.(blob.RecoveryPublicationReader)
			if !ok {
				return nil, ErrCorruptRecord
			}
			retained, err := inspector.InspectPublication(ctx, before.state.file.blobKey)
			if err != nil || retained.Kind != "retired" {
				return nil, errors.Join(ErrCorruptRecord, err)
			}
		} else {
			after := change.After
			if after.state == nil || after.state.file.Account != change.Account || after.state.file.ID != change.ID {
				return nil, ErrCorruptRecord
			}
			if before.state != nil {
				if err := verifyFileRecoveryProgress(before.state.file, after.state.file); err != nil {
					return nil, err
				}
			}
			if _, err := VerifyRecoveryRecord(ctx, key, after.state.raw, assets, at); err != nil {
				return nil, err
			}
			mutation.NewValue = after.state.raw
		}
		result = append(result, mutation)
	}
	return result, nil
}

func verifyFileRecoveryProgress(before, after File) error {
	if !sameFileIdentity(before, after) || before.outputPublished && !after.outputPublished || before.State == FileStateDeleting && after.State != FileStateDeleting || before.State == FileStateReady && after.State == FileStatePending || before.outputDigest != "" && (before.outputDigest != after.outputDigest || before.Bytes != after.Bytes) {
		return storage.ErrConflict
	}
	if before.Bytes != after.Bytes && before.State != FileStatePending {
		return storage.ErrConflict
	}
	return nil
}
