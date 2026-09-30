package storage

import (
	"bytes"
	"context"
	"encoding/json/v2"
)

func copyExpiringImportRecords(ctx context.Context, records []TransferRecord) ([]TransferRecord, error) {
	if ctx == nil || len(records) == 0 || len(records) > reconciliationMaxMutations {
		return nil, ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	total := 0
	for _, record := range records {
		if record.Validate() != nil || record.ExpiresAtMillis <= 0 {
			return nil, ErrInvalidMutation
		}
		total += len(record.Key) + len(record.Value) + 8
		if total > ImportReplayMaxBytes {
			return nil, ErrValueTooLarge
		}
	}
	sealed := make([]TransferRecord, len(records))
	for index, record := range records {
		record.Value = bytes.Clone(record.Value)
		sealed[index] = record
	}
	return sealed, nil
}

// ImportExpiringRetirer deletes exact captured expiring records under the original import barrier.
// The native owner must prove that original absolute expiry explains absence. The operation never renews or creates data.
// Exact durable retries do not delete later state. Callers must validate domain ownership and fence writers.
type ImportExpiringRetirer interface {
	ReconcileExpiringImport(context.Context, []byte, int64, string, string, []TransferRecord) (string, error)
}

// ImportExpiringReconciliationSHA256 derives the exact deletion receipt without native effects.
// Original absolute expiry remains in the fingerprint even when native expiry uses lower precision.
func ImportExpiringReconciliationSHA256(claim []byte, sequence int64, previous, evidence string, records []TransferRecord) (string, error) {
	sealed, err := copyExpiringImportRecords(context.Background(), records)
	if err != nil {
		return "", err
	}
	_, encoded, _, err := expiringReconciliationReceipt(claim, sequence, previous, evidence, sealed)
	if err != nil {
		return "", err
	}
	return reconciliationDigest(encoded), nil
}

type expiringReceiptRecord struct {
	Key             string `json:"key"`
	Value           []byte `json:"value"`
	ValuePresent    bool   `json:"value_present"`
	ExpiresAtMillis int64  `json:"expires_at_millis"`
}

func expiringReconciliationReceipt(claim []byte, sequence int64, previous, evidence string, records []TransferRecord) (reconciliationReceipt, []byte, []CompareAndSwapMutation, error) {
	mutations := make([]CompareAndSwapMutation, len(records))
	for index, record := range records {
		if record.Validate() != nil || record.ExpiresAtMillis <= 0 {
			return reconciliationReceipt{}, nil, nil, ErrInvalidMutation
		}
		mutations[index] = CompareAndSwapMutation{Key: record.Key, ExpectedValue: record.Value}
	}
	receipt, _, err := newReconciliationReceipt(claim, sequence, previous, evidence, mutations)
	if err != nil {
		return receipt, nil, nil, err
	}
	originals := make([]expiringReceiptRecord, len(records))
	for index, record := range records {
		originals[index] = expiringReceiptRecord{record.Key, record.Value, record.Value != nil, record.ExpiresAtMillis}
	}
	original, err := json.Marshal(struct {
		Kind    string                  `json:"kind"`
		Records []expiringReceiptRecord `json:"records"`
	}{"expiring-record-retirement", originals})
	if err != nil {
		return receipt, nil, nil, err
	}
	receipt.MutationsSHA256 = reconciliationDigest(original)
	encoded, err := json.Marshal(receipt)
	return receipt, encoded, mutations, err
}
