package credentials

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/storage"
)

// ErrRecoveryCredential reports invalid stored identity, ciphertext, or secret fields.
// It deliberately omits credential values and decoder details.
var ErrRecoveryCredential = errors.New("stored credential cannot be verified for recovery")

// VerifyRecoveryRecord validates one retained record and decrypts every credential.
// It does not consult the current catalog, reach providers, or grant permission.
func VerifyRecoveryRecord(ctx context.Context, key string, data []byte, encryption *EncryptionService) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if encryption == nil || len(data) > storage.TransferMaxValueBytes {
		return 0, ErrRecoveryCredential
	}
	record, err := decodeProviderCredential(data)
	if err != nil || key != StorageKey(record.Key.Scope, record.Key.Provider) {
		return 0, ErrRecoveryCredential
	}
	var values []string
	if record.Key.IsShared() {
		for _, shared := range record.Key.Shared {
			values = append(values, shared.EncryptedCredential)
		}
	} else {
		values = []string{record.Key.EncryptedCredential}
	}
	for _, encrypted := range values {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		plaintext, err := encryption.DecryptCredential(encrypted)
		if err != nil {
			return 0, ErrRecoveryCredential
		}
		var fields map[string]string
		if err := json.Unmarshal([]byte(plaintext), &fields); err != nil || len(fields) == 0 {
			return 0, ErrRecoveryCredential
		}
		if _, empty := fields[""]; empty {
			return 0, ErrRecoveryCredential
		}
	}
	return len(values), nil
}
