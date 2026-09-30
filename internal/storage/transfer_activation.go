package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"strings"
)

const (
	transferActivationPrefix = "storage:activation:v1:"
	// This native marker never enters a portable backup. Historical receipts alone
	// cannot authorize an activation retry on a different imported deployment.
	transferActivationCurrent = "storage:activation-current:v1"
)

// ImportActivator releases one exact native import claim and retains its receipt.
// The recovery coordinator must first verify history, fencing, and other stores.
// A successful activation does not establish those cross-store prerequisites.
type ImportActivator interface {
	ActivateImport(ctx context.Context, claim []byte, decisionSHA256 string) error
}

// ImportReplayActivator releases an import at its exact final replay position.
// The native transaction checks the cursor and retained history before release.
// Exact retries retain that position and cannot apply earlier domain changes.
type ImportReplayActivator interface {
	ActivateImportAt(context.Context, []byte, ImportReplayPosition, string) error
}

// ImportActivationInspector checks an exact native activation without releasing a barrier.
// Historical completion does not establish current permission or erase a withdrawal.
type ImportActivationInspector interface {
	CheckActivatedImportAt(context.Context, []byte, ImportReplayPosition, string) error
}

func activationPositionGuards(claim []byte, position ImportReplayPosition, read func(string) ([]byte, error)) ([]CompareAndSwapMutation, error) {
	if validateTransferClaim(claim) != nil || position.Sequence < 0 ||
		position.Sequence == 0 && position.ReceiptSHA256 != "" ||
		position.Sequence > 0 && !transferDigest(position.ReceiptSHA256) {
		return nil, ErrInvalidMutation
	}
	var guards []CompareAndSwapMutation
	guarded := func(key string) ([]byte, error) {
		value, err := read(key)
		if err == nil {
			guards = append(guards, CompareAndSwapMutation{Key: key, ExpectedValue: bytes.Clone(value)})
		}
		return value, err
	}
	data, err := guarded(transferReconciliationCurrent)
	if err != nil {
		return nil, err
	}
	cursor, _, err := readReconciliationCursor(data, reconciliationDigest(claim), guarded)
	if err != nil {
		return nil, err
	}
	if cursor.Sequence != position.Sequence || cursor.ReceiptSHA256 != position.ReceiptSHA256 {
		return nil, ErrConflict
	}
	return guards, nil
}

type transferActivationReceipt struct {
	Version        int                   `json:"version"`
	ClaimSHA256    string                `json:"claim_sha256"`
	DecisionSHA256 string                `json:"decision_sha256"`
	Position       *ImportReplayPosition `json:"position,omitempty"`
}

func transferDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func activationReceiptAt(claim []byte, position *ImportReplayPosition, decision string) (key string, encoded []byte, err error) {
	if err = validateTransferClaim(claim); err != nil {
		return
	}
	if !transferDigest(decision) {
		return "", nil, ErrInvalidMutation
	}
	digest := sha256.Sum256(claim)
	receipt := transferActivationReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(digest[:]), DecisionSHA256: decision}
	if position != nil {
		if !validActivationPosition(*position) {
			return "", nil, ErrInvalidMutation
		}
		receipt.Version = 2
		receipt.Position = new(*position)
	}
	encoded, err = json.Marshal(receipt)
	return transferActivationPrefix + receipt.ClaimSHA256, encoded, err
}

func validateActivationHistory(record TransferRecord) error {
	if !strings.HasPrefix(record.Key, transferActivationPrefix) {
		return nil
	}
	var receipt transferActivationReceipt
	if record.ExpiresAtMillis != 0 || len(record.Value) > 512 || json.Unmarshal(record.Value, &receipt) != nil ||
		!validActivationReceiptPosition(receipt) || !transferDigest(receipt.ClaimSHA256) || !transferDigest(receipt.DecisionSHA256) ||
		record.Key != transferActivationPrefix+receipt.ClaimSHA256 {
		return ErrInvalidMutation
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, record.Value) {
		return ErrInvalidMutation
	}
	return nil
}

func validActivationPosition(position ImportReplayPosition) bool {
	return position.Sequence >= 0 && (position.Sequence == 0 && position.ReceiptSHA256 == "" || position.Sequence > 0 && transferDigest(position.ReceiptSHA256))
}

func validActivationReceiptPosition(receipt transferActivationReceipt) bool {
	switch receipt.Version {
	case 1:
		return receipt.Position == nil
	case 2:
		return receipt.Position != nil && validActivationPosition(*receipt.Position)
	default:
		return false
	}
}
