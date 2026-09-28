package account

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryReader reads captured account records without changing their state.
type RecoveryReader interface {
	GetBounded(context.Context, string, int) ([]byte, error)
}

// VerifyRecoveryRecord validates the account and its canonical storage identity.
func VerifyRecoveryRecord(key string, data []byte) (Record, error) {
	if len(data) > policyrecord.MaxBytes {
		return Record{}, policyrecord.ErrTooLarge
	}
	stored, err := decodeAccount(data)
	if err != nil || key != accountStorageKey(stored.Account.ID) {
		return Record{}, ErrCorruptRecord
	}
	return Record{Revision: stored.Revision, Account: stored.Account}, nil
}

// ReadRecoveryAccount validates one captured account without granting access.
func ReadRecoveryAccount(ctx context.Context, reader RecoveryReader, id string) (Record, error) {
	if reader == nil || ValidateID(id) != nil {
		return Record{}, ErrCorruptRecord
	}
	key := accountStorageKey(id)
	data, err := reader.GetBounded(ctx, key, policyrecord.MaxBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	return VerifyRecoveryRecord(key, data)
}

// RecoveryTemplateMaxBytes applies the portable record bound to template verification.
// Templates do not use the smaller authorization-record limit.
const RecoveryTemplateMaxBytes = storage.TransferMaxValueBytes

// VerifyRecoveryTemplate checks the duplicated SQL identity and revision fields.
func VerifyRecoveryTemplate(id string, revision uint64, data string) error {
	if len(data) > RecoveryTemplateMaxBytes {
		return storage.ErrValueTooLarge
	}
	stored, err := decodeTemplate(data)
	if err != nil || stored.Template.ID != id || stored.Revision != revision {
		return ErrCorruptTemplate
	}
	return nil
}
