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

type transferActivationReceipt struct {
	Version        int    `json:"version"`
	ClaimSHA256    string `json:"claim_sha256"`
	DecisionSHA256 string `json:"decision_sha256"`
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

func activationReceipt(claim []byte, decision string) (key string, encoded []byte, err error) {
	if err = validateTransferClaim(claim); err != nil {
		return
	}
	if !transferDigest(decision) {
		return "", nil, ErrInvalidMutation
	}
	digest := sha256.Sum256(claim)
	receipt := transferActivationReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(digest[:]), DecisionSHA256: decision}
	encoded, err = json.Marshal(receipt)
	return transferActivationPrefix + receipt.ClaimSHA256, encoded, err
}

func validateActivationHistory(record TransferRecord) error {
	if !strings.HasPrefix(record.Key, transferActivationPrefix) {
		return nil
	}
	var receipt transferActivationReceipt
	if record.ExpiresAtMillis != 0 || len(record.Value) > 256 || json.Unmarshal(record.Value, &receipt) != nil ||
		receipt.Version != 1 || !transferDigest(receipt.ClaimSHA256) || !transferDigest(receipt.DecisionSHA256) ||
		record.Key != transferActivationPrefix+receipt.ClaimSHA256 {
		return ErrInvalidMutation
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, record.Value) {
		return ErrInvalidMutation
	}
	return nil
}
