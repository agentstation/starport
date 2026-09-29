package storedbytes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// ReplayStoragePrefix marks unfinished byte-account reconstruction.
const ReplayStoragePrefix = "limits:v2:stored_bytes_replay:"

// RecoveryReader exposes an immutable complete captured view, in strict key order.
type RecoveryReader interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
	Enumerate(context.Context, func(storage.TransferRecord) error) error
}

// AccountReplayState binds a complete independently retained account byte census.
// The coordinator must establish its provenance and complete interval coverage.
type AccountReplayState struct {
	Version int    `json:"version"`
	Holder  string `json:"holder"`
	Claims  int64  `json:"claims"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type byteReplayMarker struct {
	Version  int                `json:"version"`
	State    AccountReplayState `json:"state"`
	Original []byte             `json:"original"`
}

func replayKey(holder string) string {
	return ReplayStoragePrefix + strings.TrimPrefix(byteTotalKey(holder), StoredBytesPrefix)
}

func validateReplayState(state AccountReplayState) error {
	digest, err := hex.DecodeString(state.SHA256)
	if state.Version != 1 || strings.TrimSpace(state.Holder) == "" || len(state.Holder) > 512 || state.Claims < 0 || state.Bytes < 0 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != state.SHA256 {
		return ErrStorageHistoryUnknown
	}
	return nil
}

func readRecovery(ctx context.Context, source RecoveryReader, key string, maximum int) (storage.TransferRecord, error) {
	record, err := source.ReadCaptured(ctx, key, maximum)
	if err != nil {
		return record, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > maximum {
		return record, ErrStorageHistoryUnknown
	}
	return record, nil
}

func recoveryCensus(ctx context.Context, source RecoveryReader, holder string) (AccountReplayState, error) {
	state := AccountReplayState{Version: 1, Holder: holder}
	h := sha256.New()
	_, _ = h.Write([]byte("starport-stored-byte-census-v1\n"))
	last := ""
	err := source.Enumerate(ctx, func(raw storage.TransferRecord) error {
		if last != "" && raw.Key <= last {
			return ErrStorageHistoryUnknown
		}
		last = raw.Key
		if !strings.HasPrefix(raw.Key, byteClaimPrefix(holder)) {
			return nil
		}
		item, err := VerifyRecoveryRecord(raw)
		if err != nil || item.Claim == nil || item.Holder != holder {
			return ErrStorageHistoryUnknown
		}
		state.Claims++
		claim := *item.Claim
		if !claim.Released {
			if claim.Bytes > math.MaxInt64-state.Bytes {
				return ErrStorageHistoryUnknown
			}
			state.Bytes += claim.Bytes
		}
		encoded, err := json.Marshal(claim)
		if err != nil {
			return err
		}
		_, _ = h.Write(encoded)
		_, _ = h.Write([]byte("\n"))
		return nil
	})
	state.SHA256 = hex.EncodeToString(h.Sum(nil))
	return state, err
}

// CaptureAccountReplayState verifies the independent total and every retained claim.
// Missing total evidence never means zero consumption.
func CaptureAccountReplayState(ctx context.Context, source RecoveryReader, holder string) (AccountReplayState, error) {
	if ctx == nil || source == nil {
		return AccountReplayState{}, ErrStorageHistoryUnknown
	}
	marker, err := source.ReadCaptured(ctx, replayKey(holder), 16384)
	if err == nil || !errors.Is(err, storage.ErrNotFound) || len(marker.Value) != 0 {
		return AccountReplayState{}, ErrStorageHistoryUnknown
	}
	raw, err := readRecovery(ctx, source, byteTotalKey(holder), 1024)
	if err != nil {
		return AccountReplayState{}, err
	}
	item, err := VerifyRecoveryRecord(raw)
	if err != nil || item.Total == nil {
		return AccountReplayState{}, ErrStorageHistoryUnknown
	}
	state, err := recoveryCensus(ctx, source, holder)
	if err != nil {
		return AccountReplayState{}, err
	}
	if err := VerifyRecoveryTotal(item.Total, state.Bytes); err != nil {
		return AccountReplayState{}, err
	}
	return state, validateReplayState(state)
}

// PrepareAccountReplay stages at most 64 typed claims while preserving original total bytes.
// The final census must include every old claim. Omitted records never disappear.
func PrepareAccountReplay(ctx context.Context, source RecoveryReader, state AccountReplayState, claims []RecoveryClaim) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || validateReplayState(state) != nil || len(claims) > 64 || len(claims) == 0 && state.Claims != 0 {
		return nil, ErrStorageHistoryUnknown
	}
	marker, oldMarker, err := readReplayMarker(ctx, source, state)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	mutations := []storage.CompareAndSwapMutation{{Key: byteTotalKey(state.Holder), ExpectedValue: marker.Original, NewValue: marker.Original}, {Key: replayKey(state.Holder), ExpectedValue: oldMarker, NewValue: encoded}}
	seen := map[string]bool{}
	for _, claim := range claims {
		if !validRecoveryClaim(claim) || claim.Holder != state.Holder || seen[claim.ID] {
			return nil, ErrStorageClaimConflict
		}
		seen[claim.ID] = true
		key := byteClaimKey(claim.Holder, claim.ID)
		before, err := readRecovery(ctx, source, key, 8192)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			item, err := VerifyRecoveryRecord(before)
			if err != nil || item.Claim == nil {
				return nil, ErrStorageHistoryUnknown
			}
			if err := verifyRecoveryProgress(*item.Claim, claim); err != nil {
				return nil, err
			}
		}
		data, err := json.Marshal(claim)
		if err != nil {
			return nil, err
		}
		if err == nil && len(before.Value) > 0 {
			var old RecoveryClaim
			if json.Unmarshal(before.Value, &old) == nil && old == claim {
				data = before.Value
			}
		}
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: key, ExpectedValue: before.Value, NewValue: data})
	}
	return mutations, nil
}

// FinalizeAccountReplay publishes the exact independently accepted total after a complete census.
// It removes only this owner's matching staging marker. It never creates fresh budget history.
func FinalizeAccountReplay(ctx context.Context, source RecoveryReader, state AccountReplayState) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || validateReplayState(state) != nil {
		return nil, ErrStorageHistoryUnknown
	}
	marker, old, err := readReplayMarker(ctx, source, state)
	if err != nil || old == nil {
		return nil, ErrStorageHistoryUnknown
	}
	actual, err := recoveryCensus(ctx, source, state.Holder)
	if err != nil || actual != state {
		return nil, ErrStorageHistoryUnknown
	}
	value, err := json.Marshal(byteTotal{Version: StoredBytesSchemaVersion, Bytes: state.Bytes})
	if err != nil {
		return nil, err
	}
	return []storage.CompareAndSwapMutation{{Key: byteTotalKey(state.Holder), ExpectedValue: marker.Original, NewValue: value}, {Key: replayKey(state.Holder), ExpectedValue: old}}, nil
}

func readReplayMarker(ctx context.Context, source RecoveryReader, state AccountReplayState) (byteReplayMarker, []byte, error) {
	total, err := readRecovery(ctx, source, byteTotalKey(state.Holder), 1024)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return byteReplayMarker{}, nil, err
	}
	if err == nil {
		if _, err := VerifyRecoveryRecord(total); err != nil {
			return byteReplayMarker{}, nil, err
		}
	}
	raw, err := readRecovery(ctx, source, replayKey(state.Holder), 16384)
	if errors.Is(err, storage.ErrNotFound) {
		return byteReplayMarker{Version: 1, State: state, Original: total.Value}, nil, nil
	}
	if err != nil {
		return byteReplayMarker{}, nil, err
	}
	var marker byteReplayMarker
	if json.Unmarshal(raw.Value, &marker, json.RejectUnknownMembers(true)) != nil || marker.Version != 1 || marker.State != state || !bytes.Equal(marker.Original, total.Value) {
		return byteReplayMarker{}, nil, ErrStorageHistoryUnknown
	}
	return marker, raw.Value, nil
}

func verifyRecoveryProgress(before, after RecoveryClaim) error {
	if before.Holder != after.Holder || before.ID != after.ID || before.Initial != after.Initial || before.Bound != after.Bound || !before.CreatedAt.Equal(after.CreatedAt) || before.Attached && !after.Attached || before.Released && !after.Released || before.Measured && (!after.Measured || before.Bytes != after.Bytes) || before.Released && before != after {
		return ErrStorageClaimConflict
	}
	return nil
}
