package revision

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

type kvRecoveryPayload struct {
	Version        int               `json:"version"`
	ExpectedSHA256 string            `json:"expected_sha256"`
	Authority      RecoveryAuthority `json:"authority"`
}

// KVRecoveryTransition binds an exact retained marker to independently accepted replacement authority.
// An empty preimage digest requires absence. Replacement always starts at sequence one.
type KVRecoveryTransition struct{ payload *kvRecoveryPayload }

// Format excludes recovery evidence from diagnostic formatting.
func (KVRecoveryTransition) Format(state fmt.State, _ rune) { redactRecovery(state) }

// MarshalJSON returns private recovery evidence for a retained replay receipt.
func (r KVRecoveryTransition) MarshalJSON() ([]byte, error) {
	return encodeRecovery(r.payload)
}

// UnmarshalJSON refuses unknown fields, duplicate fields, and unsupported versions.
func (r *KVRecoveryTransition) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > maxRecoveryTransitionBytes || !explicitRecoveryMember(data, "expected_sha256") {
		return ErrRecoveryConflict
	}
	var payload kvRecoveryPayload
	if json.Unmarshal(data, &payload, json.RejectUnknownMembers(true)) != nil || payload.Version != 1 || !payload.Authority.valid() || payload.ExpectedSHA256 != "" && !validRecoveryDigest(payload.ExpectedSHA256) {
		return ErrRecoveryConflict
	}
	r.payload = &payload
	return nil
}

// NewKVRecoveryTransition copies explicit accepted authority and its exact expected preimage digest.
func NewKVRecoveryTransition(expectedSHA256 string, authority RecoveryAuthority) (KVRecoveryTransition, error) {
	data, err := encodeRecovery(kvRecoveryPayload{Version: 1, ExpectedSHA256: expectedSHA256, Authority: authority})
	if err != nil {
		return KVRecoveryTransition{}, err
	}
	var result KVRecoveryTransition
	err = result.UnmarshalJSON(data)
	return result, err
}

// Digest binds complete canonical recovery input. It does not prove provenance or continuity.
func (r KVRecoveryTransition) Digest() (string, error) {
	data, err := r.MarshalJSON()
	var checked KVRecoveryTransition
	if err != nil || checked.UnmarshalJSON(data) != nil {
		return "", ErrRecoveryConflict
	}
	return recoveryDigest(data), nil
}

// CaptureKVRecovery reads a complete marker and its exact byte digest without initializing authority.
// A nil stamp and empty digest mean that the immutable source contains no marker.
func CaptureKVRecovery(ctx context.Context, source reservation.BackupReader) (*Stamp, string, error) {
	stamp, raw, err := readKVRecovery(ctx, source)
	if err != nil || stamp == nil {
		return stamp, "", err
	}
	return stamp, recoveryDigest(raw), nil
}

func readKVRecovery(ctx context.Context, source reservation.BackupReader) (*Stamp, []byte, error) {
	if ctx == nil || source == nil {
		return nil, nil, ErrRecoveryConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	record, err := source.ReadCaptured(ctx, StorageKey, 1024)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var stamp Stamp
	if record.Key != StorageKey || record.ExpiresAtMillis != 0 || len(record.Value) > 1024 || json.Unmarshal(record.Value, &stamp, json.RejectUnknownMembers(true)) != nil || !validRecoveryStamp(stamp) {
		return nil, nil, ErrRecoveryConflict
	}
	return &stamp, bytes.Clone(record.Value), nil
}

// PrepareKVRecovery returns one exact CAS mutation for a native import replay step.
// The coordinator combines it with domain mutations under its barrier and evidence receipt.
// It must use the same immutable pre-step source for retries and keep admission closed.
// Run this final replacement once before activation, after all policy replay.
func PrepareKVRecovery(ctx context.Context, source reservation.BackupReader, transition KVRecoveryTransition) (storage.CompareAndSwapMutation, error) {
	if _, err := transition.Digest(); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	before, raw, err := readKVRecovery(ctx, source)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	digest := ""
	if before != nil {
		digest = recoveryDigest(raw)
		if transition.payload.Authority.Epoch == before.Epoch {
			return storage.CompareAndSwapMutation{}, ErrRecoveryConflict
		}
	}
	if digest != transition.payload.ExpectedSHA256 {
		return storage.CompareAndSwapMutation{}, ErrRecoveryConflict
	}
	next, err := json.Marshal(Stamp{Epoch: transition.payload.Authority.Epoch, Sequence: 1})
	if err != nil {
		return storage.CompareAndSwapMutation{}, ErrRecoveryConflict
	}
	return storage.CompareAndSwapMutation{Key: StorageKey, ExpectedValue: raw, NewValue: next}, nil
}
