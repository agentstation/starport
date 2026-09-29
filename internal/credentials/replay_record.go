package credentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/storage"
)

// RecoveryRecord holds complete private permission evidence for operator recovery.
// JSON contains durable fields. Diagnostic formatting never exposes those fields.
type RecoveryRecord struct{ *recoveryRecord }

type recoveryRecord struct {
	data   []byte
	stored providerCredentialRecord
}

func (RecoveryRecord) String() string { return "<private credentials recovery evidence>" }

// GoString excludes private permission evidence from formatted diagnostics.
func (RecoveryRecord) GoString() string { return "<private credentials recovery evidence>" }

// Format excludes private evidence from diagnostic formatting.
func (RecoveryRecord) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private credentials recovery evidence>"))
}

// MarshalJSON returns the complete retained durable record.
func (r RecoveryRecord) MarshalJSON() ([]byte, error) {
	if r.recoveryRecord == nil || len(r.data) == 0 {
		return nil, ErrRecoveryCredential
	}
	return bytes.Clone(r.data), nil
}

// UnmarshalJSON refuses unknown fields, unsupported schemas, and oversized records.
func (r *RecoveryRecord) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) == 0 || len(data) > storage.TransferMaxValueBytes {
		return ErrRecoveryCredential
	}
	var stored providerCredentialRecord
	if json.Unmarshal(data, &stored, json.RejectUnknownMembers(true)) != nil {
		return ErrRecoveryCredential
	}
	if _, err := decodeProviderCredential(data); err != nil {
		return ErrRecoveryCredential
	}
	*r = RecoveryRecord{recoveryRecord: &recoveryRecord{data: bytes.Clone(data), stored: stored}}
	return nil
}

// SHA256 binds the complete record to an expected replay preimage.
func (r RecoveryRecord) SHA256() string {
	if r.recoveryRecord == nil || len(r.data) == 0 {
		return ""
	}
	sum := sha256.Sum256(r.data)
	return hex.EncodeToString(sum[:])
}

// RecoveryChange names an exact expected record and a later record or deletion.
// An empty ExpectedSHA256 requires absence. A nil Next means explicit deletion.
type RecoveryChange struct {
	Scope          string          `json:"scope"`
	Provider       string          `json:"provider"`
	ExpectedSHA256 string          `json:"expected_sha256"`
	Next           *RecoveryRecord `json:"next"`
}

func readReplayRecord(ctx context.Context, source RecoveryReader, key string) (storage.TransferRecord, *RecoveryRecord, error) {
	original, err := source.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.TransferRecord{Key: key}, nil, nil
	}
	if err != nil {
		return storage.TransferRecord{}, nil, err
	}
	var record RecoveryRecord
	if original.Key != key || original.ExpiresAtMillis != 0 || record.UnmarshalJSON(original.Value) != nil {
		return storage.TransferRecord{}, nil, ErrRecoveryCredential
	}
	return original, &record, nil
}

func verifyReplayExpected(before *RecoveryRecord, expected string) error {
	if before == nil {
		if expected != "" {
			return ErrConflict
		}
		return nil
	}
	if expected == "" || expected != before.SHA256() {
		return ErrConflict
	}
	return nil
}
