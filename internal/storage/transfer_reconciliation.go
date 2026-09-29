package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"strings"
)

const (
	transferReconciliationPrefix  = "storage:reconciliation:v1:"
	transferReconciliationCurrent = "storage:reconciliation-current:v1"
	// ImportReplayMaxBytes bounds total keys, preimages, and new values in one native replay step.
	ImportReplayMaxBytes       = 4 << 20
	reconciliationMaxMutations = 128
)

// ImportReconciler commits ordered owner mutations and their receipt under an import barrier.
// Callers must validate domain transitions and fence all writers. This method grants no admission.
// Mutations address persistent records only. Exact retries never apply earlier mutations again.
type ImportReconciler interface {
	ReconcileImport(context.Context, []byte, int64, string, string, []CompareAndSwapMutation) (string, error)
}

type reconciliationReceipt struct {
	Version         int    `json:"version"`
	ClaimSHA256     string `json:"claim_sha256"`
	Sequence        int64  `json:"sequence"`
	PreviousSHA256  string `json:"previous_sha256"`
	EvidenceSHA256  string `json:"evidence_sha256"`
	MutationsSHA256 string `json:"mutations_sha256"`
}

type reconciliationCursor struct {
	Version       int    `json:"version"`
	ClaimSHA256   string `json:"claim_sha256"`
	Sequence      int64  `json:"sequence"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

type reconciliationPlan struct {
	digest string
	guards []CompareAndSwapMutation
	writes []CompareAndSwapMutation
}

func reconciliationDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func reconciliationKey(claim string, sequence int64) string {
	return fmt.Sprintf("%s%s:%d", transferReconciliationPrefix, claim, sequence)
}

func newReconciliationReceipt(claim []byte, sequence int64, previous, evidence string, mutations []CompareAndSwapMutation) (reconciliationReceipt, []byte, error) {
	if validateTransferClaim(claim) != nil || sequence <= 0 ||
		(sequence == 1 && previous != "") || (sequence > 1 && !transferDigest(previous)) ||
		!transferDigest(evidence) || len(mutations) == 0 || len(mutations) > reconciliationMaxMutations || validateCompareAndSwapMutations(mutations) != nil {
		return reconciliationReceipt{}, nil, ErrInvalidMutation
	}
	total := 0
	for _, mutation := range mutations {
		if len(mutation.Key) > TransferMaxKeyBytes || mutation.TTL != 0 ||
			strings.HasPrefix(mutation.Key, "storage:") || strings.HasPrefix(mutation.Key, "!badger!") {
			return reconciliationReceipt{}, nil, ErrInvalidMutation
		}
		total += len(mutation.Key) + len(mutation.ExpectedValue) + len(mutation.NewValue)
		if total > ImportReplayMaxBytes {
			return reconciliationReceipt{}, nil, ErrValueTooLarge
		}
	}
	type boundMutation struct {
		Key             string
		ExpectedPresent bool
		Expected        []byte
		NewPresent      bool
		New             []byte
	}
	bound := make([]boundMutation, len(mutations))
	for i, m := range mutations {
		bound[i] = boundMutation{m.Key, m.ExpectedValue != nil, m.ExpectedValue, m.NewValue != nil, m.NewValue}
	}
	encoded, err := json.Marshal(bound)
	if err != nil {
		return reconciliationReceipt{}, nil, err
	}
	receipt := reconciliationReceipt{1, reconciliationDigest(claim), sequence, previous, evidence, reconciliationDigest(encoded)}
	encoded, err = json.Marshal(receipt)
	return receipt, encoded, err
}

// read returns nil for absence and refuses expiring or oversized control records.
func prepareImportReconciliation(claim []byte, receipt reconciliationReceipt, encoded []byte, mutations []CompareAndSwapMutation, read func(string) ([]byte, error)) (reconciliationPlan, error) {
	plan := reconciliationPlan{digest: reconciliationDigest(encoded)}
	guarded := func(key string) ([]byte, error) {
		data, err := read(key)
		if err == nil {
			plan.guards = append(plan.guards, CompareAndSwapMutation{Key: key, ExpectedValue: data})
		}
		return data, err
	}
	barrier, err := guarded(TransferBarrierKey)
	if err != nil {
		return plan, err
	}
	if !bytes.Equal(barrier, claim) {
		return plan, ErrImportRestricted
	}
	active, err := guarded(transferActivationCurrent)
	if err != nil {
		return plan, err
	}
	if active != nil {
		return plan, ErrConflict
	}
	cursorData, err := guarded(transferReconciliationCurrent)
	if err != nil {
		return plan, err
	}
	cursor, currentHistory, err := readReconciliationCursor(cursorData, receipt.ClaimSHA256, guarded)
	if err != nil {
		return plan, err
	}
	key := reconciliationKey(receipt.ClaimSHA256, receipt.Sequence)
	// The requested receipt may already be the current history guard.
	var history []byte
	if cursorData != nil && cursor.Sequence == receipt.Sequence {
		history = currentHistory
	} else {
		history, err = guarded(key)
		if err != nil {
			return plan, err
		}
	}
	if history != nil {
		if cursorData == nil || cursor.Sequence < receipt.Sequence || !bytes.Equal(history, encoded) {
			return plan, ErrConflict
		}
		return plan, nil
	}
	if receipt.Sequence == 1 {
		if cursorData != nil {
			return plan, ErrConflict
		}
	} else if cursorData == nil || cursor.Sequence != receipt.Sequence-1 || cursor.ReceiptSHA256 != receipt.PreviousSHA256 {
		return plan, ErrConflict
	}
	next, err := json.Marshal(reconciliationCursor{1, receipt.ClaimSHA256, receipt.Sequence, plan.digest})
	if err != nil {
		return plan, err
	}
	plan.writes = append(plan.writes, mutations...)
	plan.writes = append(plan.writes, CompareAndSwapMutation{Key: key, NewValue: encoded}, CompareAndSwapMutation{Key: transferReconciliationCurrent, NewValue: next})
	return plan, nil
}

func readReconciliationCursor(data []byte, claim string, read func(string) ([]byte, error)) (reconciliationCursor, []byte, error) {
	var cursor reconciliationCursor
	if data == nil {
		return cursor, nil, nil
	}
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.ClaimSHA256 != claim || cursor.Sequence <= 0 || !transferDigest(cursor.ReceiptSHA256) {
		return cursor, nil, ErrConflict
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, data) {
		return cursor, nil, ErrConflict
	}
	key := reconciliationKey(cursor.ClaimSHA256, cursor.Sequence)
	history, err := read(key)
	if err != nil {
		return cursor, nil, err
	}
	if history == nil || reconciliationDigest(history) != cursor.ReceiptSHA256 || validateReconciliationHistory(TransferRecord{Key: key, Value: history}) != nil {
		return cursor, nil, ErrConflict
	}
	return cursor, history, nil
}

func validateReconciliationHistory(record TransferRecord) error {
	if !strings.HasPrefix(record.Key, transferReconciliationPrefix) {
		return nil
	}
	var receipt reconciliationReceipt
	if record.ExpiresAtMillis != 0 || len(record.Value) > 1024 || json.Unmarshal(record.Value, &receipt) != nil ||
		receipt.Version != 1 || !transferDigest(receipt.ClaimSHA256) || receipt.Sequence <= 0 ||
		(receipt.Sequence == 1 && receipt.PreviousSHA256 != "") || (receipt.Sequence > 1 && !transferDigest(receipt.PreviousSHA256)) ||
		!transferDigest(receipt.EvidenceSHA256) || !transferDigest(receipt.MutationsSHA256) || record.Key != reconciliationKey(receipt.ClaimSHA256, receipt.Sequence) {
		return ErrInvalidMutation
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, record.Value) {
		return ErrInvalidMutation
	}
	return nil
}
