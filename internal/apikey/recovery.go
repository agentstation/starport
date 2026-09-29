package apikey

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/storage"
)

// RecoverySource reads captured records with their original expiration metadata.
type RecoverySource interface {
	storage.RecordSource
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
}

// RecoveryReport counts retained keys and unavailable owner references.
// A missing initial key can follow deletion. Its marker still prevents implicit initialization.
type RecoveryReport struct {
	UnknownBudgetHistories int64  `json:"unknown_budget_histories"`
	Keys                   uint64 `json:"keys"`
	HashIndexes            uint64 `json:"hash_indexes"`
	MissingAccounts        uint64 `json:"missing_accounts"`
	MissingTeams           uint64 `json:"missing_teams"`
	MissingInitialKeys     uint64 `json:"missing_initial_keys"`
}

// VerifyRecoverySnapshot checks both index directions and the collection count.
// It preserves revocation and expiration fields without granting authentication.
func VerifyRecoverySnapshot(ctx context.Context, source RecoverySource, checkOwner func(context.Context, APIKey) (bool, bool, error)) (report RecoveryReport, err error) {
	if source == nil || checkOwner == nil {
		return report, ErrRepositoryRequired
	}
	var collection *apiKeyCollectionRecord
	var initial bool
	err = source.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !strings.HasPrefix(record.Key, StoragePrefix) {
			return nil
		}
		if record.ExpiresAtMillis != 0 || len(record.Value) > policyrecord.MaxBytes {
			return ErrCorruptRecord
		}
		if err := validateRecoverySchema(record); err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(record.Key, apiKeyPrefix):
			key, err := verifyRecoveryKeyIndex(ctx, source, record)
			if err != nil {
				return err
			}
			missingAccount, missingTeam, err := checkOwner(ctx, key)
			if err != nil {
				return err
			}
			if missingAccount {
				report.MissingAccounts++
			}
			if missingTeam {
				report.MissingTeams++
			}
			unknown, err := reservation.CheckBackupLimits(ctx, source, limits.ScopeKey, key.ID, key.Limits)
			if err != nil {
				return err
			}
			report.UnknownBudgetHistories += unknown
			report.Keys++
		case strings.HasPrefix(record.Key, hashKeyPrefix):
			index, err := decodeHashRecord(record.Value)
			if err != nil {
				return ErrCorruptRecord
			}
			key, err := readRecoveryKey(ctx, source, index.APIKeyID)
			if err != nil {
				return err
			}
			if record.Key != hashStorageKey(key.Hash) {
				return ErrCorruptRecord
			}
			report.HashIndexes++
		case record.Key == collectionKey:
			value, err := decodeAPIKeyCollectionRecord(record.Value)
			if err != nil {
				return ErrCorruptRecord
			}
			collection = &value
		case record.Key == initialKey:
			initial = true
			marker, err := decodeInitialAPIKeyRecord(record.Value)
			if err != nil {
				return ErrCorruptRecord
			}
			_, err = readRecoveryKey(ctx, source, marker.APIKeyID)
			if errors.Is(err, storage.ErrNotFound) {
				report.MissingInitialKeys++
				return nil
			}
			return err
		default:
			return ErrCorruptRecord
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	if collection == nil {
		if report.Keys != 0 || report.HashIndexes != 0 || initial {
			return report, ErrCorruptRecord
		}
	} else if collection.Count != report.Keys || report.HashIndexes != report.Keys {
		return report, ErrCorruptRecord
	}
	return report, nil
}

func readRecoveryValue(ctx context.Context, source RecoverySource, key string) ([]byte, error) {
	record, err := source.ReadCaptured(ctx, key, policyrecord.MaxBytes)
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 {
		return nil, ErrCorruptRecord
	}
	return record.Value, nil
}

func readRecoveryKey(ctx context.Context, source RecoverySource, id string) (APIKey, error) {
	key := apiKeyStorageKey(id)
	data, err := readRecoveryValue(ctx, source, key)
	if err != nil {
		return APIKey{}, err
	}
	return decodeRecoveryKey(key, data)
}

func decodeRecoveryKey(key string, data []byte) (APIKey, error) {
	stored, err := decodeAPIKey(data)
	if err != nil || key != apiKeyStorageKey(stored.APIKey.ID) {
		return APIKey{}, ErrCorruptRecord
	}
	return stored.APIKey, nil
}

func verifyRecoveryKeyIndex(ctx context.Context, source RecoverySource, record storage.TransferRecord) (APIKey, error) {
	key, err := decodeRecoveryKey(record.Key, record.Value)
	if err != nil {
		return APIKey{}, err
	}
	data, err := readRecoveryValue(ctx, source, hashStorageKey(key.Hash))
	if err != nil {
		return APIKey{}, err
	}
	index, err := decodeHashRecord(data)
	if err != nil || index.APIKeyID != key.ID {
		return APIKey{}, ErrCorruptRecord
	}
	return key, nil
}

// validateRecoverySchema rejects fields whose permission meaning is unknown to this binary.
func validateRecoverySchema(record storage.TransferRecord) error {
	var target any
	switch {
	case strings.HasPrefix(record.Key, apiKeyPrefix):
		target = new(apiKeyRecord)
	case strings.HasPrefix(record.Key, hashKeyPrefix):
		target = new(hashRecord)
	case record.Key == collectionKey:
		target = new(apiKeyCollectionRecord)
	case record.Key == initialKey:
		target = new(initialAPIKeyRecord)
	default:
		return ErrCorruptRecord
	}
	if json.Unmarshal(record.Value, target, json.RejectUnknownMembers(true)) != nil {
		return ErrCorruptRecord
	}
	return nil
}
