package apikey

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryIndexes retains collection metadata and the one-time initialization marker.
// InitialKeyID can name a deleted key. Recovery never removes or replaces that marker.
type RecoveryIndexes struct {
	Revision     uint64 `json:"revision"`
	Count        uint64 `json:"count"`
	InitialKeyID string `json:"initial_key_id,omitempty"`
}

// RecoveryReplay binds a bounded key step and its complete index metadata.
// ExpectedIndexesSHA256 comes from CaptureRecoveryIndexes on the immutable pre-step snapshot.
type RecoveryReplay struct {
	ExpectedIndexesSHA256 string           `json:"expected_indexes_sha256"`
	Indexes               RecoveryIndexes  `json:"indexes"`
	Changes               []RecoveryChange `json:"changes"`
}

type replayIndexRecords struct {
	value               RecoveryIndexes
	collection, initial []byte
}

// CaptureRecoveryIndexes binds both present and absent index metadata.
func CaptureRecoveryIndexes(ctx context.Context, source reservation.BackupReader) (RecoveryIndexes, string, error) {
	if ctx == nil || source == nil {
		return RecoveryIndexes{}, "", ErrCorruptRecord
	}
	current, err := readReplayIndexes(ctx, source)
	if err != nil {
		return RecoveryIndexes{}, "", err
	}
	return current.value, current.digest(), nil
}
func (r replayIndexRecords) digest() string {
	data, _ := json.Marshal(struct {
		Collection []byte
		Initial    []byte
	}{r.collection, r.initial}, json.Deterministic(true))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func readReplayIndexes(ctx context.Context, source reservation.BackupReader) (replayIndexRecords, error) {
	var result replayIndexRecords
	var err error
	result.collection, err = readReplayIndex(ctx, source, collectionKey)
	if err != nil {
		return result, err
	}
	if result.collection != nil {
		var collection apiKeyCollectionRecord
		if json.Unmarshal(result.collection, &collection, json.RejectUnknownMembers(true)) != nil {
			return result, ErrCorruptRecord
		}
		if _, err := decodeAPIKeyCollectionRecord(result.collection); err != nil {
			return result, err
		}
		result.value.Revision, result.value.Count = collection.Revision, collection.Count
	}
	result.initial, err = readReplayIndex(ctx, source, initialKey)
	if err != nil {
		return result, err
	}
	if result.initial != nil {
		var initial initialAPIKeyRecord
		if json.Unmarshal(result.initial, &initial, json.RejectUnknownMembers(true)) != nil {
			return result, ErrCorruptRecord
		}
		if _, err := decodeInitialAPIKeyRecord(result.initial); err != nil {
			return result, err
		}
		if result.collection == nil {
			return result, ErrCorruptRecord
		}
		result.value.InitialKeyID = initial.APIKeyID
	}
	return result, nil
}
func readReplayIndex(ctx context.Context, source reservation.BackupReader, key string) ([]byte, error) {
	record, err := source.ReadCaptured(ctx, key, policyrecord.MaxBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > policyrecord.MaxBytes {
		return nil, ErrCorruptRecord
	}
	return bytes.Clone(record.Value), nil
}
func replayMetadataMutations(before replayIndexRecords, next RecoveryIndexes, count uint64, changed bool) ([]storage.CompareAndSwapMutation, error) {
	if next.Count != count || next.Revision == 0 || next.Revision < before.value.Revision || changed && next.Revision == before.value.Revision || before.value.InitialKeyID != "" && next.InitialKeyID != before.value.InitialKeyID {
		return nil, ErrCorruptRecord
	}
	collection, err := policyrecord.Marshal(apiKeyCollectionRecord{SchemaVersion: StorageSchemaVersion, Revision: next.Revision, Count: next.Count})
	if err != nil {
		return nil, err
	}
	var initial []byte
	if next.InitialKeyID != "" {
		initial, err = policyrecord.Marshal(initialAPIKeyRecord{SchemaVersion: StorageSchemaVersion, APIKeyID: next.InitialKeyID})
		if err != nil {
			return nil, err
		}
	}
	if before.value.Revision == next.Revision && before.value.Count == next.Count {
		collection = bytes.Clone(before.collection)
	}
	if before.value.InitialKeyID == next.InitialKeyID {
		initial = bytes.Clone(before.initial)
	}
	return []storage.CompareAndSwapMutation{{Key: collectionKey, ExpectedValue: before.collection, NewValue: collection}, {Key: initialKey, ExpectedValue: before.initial, NewValue: initial}}, nil
}
