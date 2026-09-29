package jobslots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// ReplayStoragePrefix identifies incomplete account recovery. Final validation must refuse it.
const ReplayStoragePrefix = "limits:v2:job_replay:"

// AccountReplayState binds a complete independently captured account slot state.
// Counts and a digest establish consistency, not provenance or interval completeness.
// The coordinator must establish those facts before it accepts this owner state.
type AccountReplayState struct {
	Version int    `json:"version"`
	Account string `json:"account"`
	Claims  int64  `json:"claims"`
	Held    int64  `json:"held"`
	SHA256  string `json:"sha256"`
}

func replayAccountKey(account string) string {
	return ReplayStoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(account))
}

func (s AccountReplayState) bytes() ([]byte, error) {
	digest, err := hex.DecodeString(s.SHA256)
	if s.Version != recordVersion || !validID(s.Account) || s.Claims < 0 || s.Held < 0 || s.Held > s.Claims || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != s.SHA256 {
		return nil, ErrInvalid
	}
	if s.Claims == 0 {
		empty := sha256.Sum256([]byte("starport-jobslots-account-v1"))
		if s.SHA256 != hex.EncodeToString(empty[:]) {
			return nil, ErrInvalid
		}
	}
	return json.Marshal(s, json.Deterministic(true))
}

// CaptureAccountReplayState validates complete independent claims, counter, and history.
// This method streams enumeration. The number of historical claims does not limit recovery.
func CaptureAccountReplayState(ctx context.Context, source RecoveryReader, account string) (AccountReplayState, error) {
	if ctx == nil || source == nil || !validID(account) {
		return AccountReplayState{}, ErrInvalid
	}
	total, _, err := readReplayAccount(ctx, source, account)
	if err != nil {
		return AccountReplayState{}, err
	}
	marker, err := readReplayRecord(ctx, source, replayAccountKey(account))
	if err != nil || marker != nil {
		return AccountReplayState{}, errors.Join(ErrHistoryUnknown, err)
	}
	state, err := summarizeReplayClaims(ctx, source, account)
	if err != nil {
		return state, err
	}
	item, err := verifyReplayRecord(total)
	if err != nil || item.Total == nil || *item.Total != state.Held {
		return AccountReplayState{}, ErrHistoryUnknown
	}
	if _, err := state.bytes(); err != nil {
		return AccountReplayState{}, err
	}
	return state, nil
}

// PrepareAccountReplay stages at most 64 claims under one exact account-state marker.
// The first step refuses all existing account state, including orphan claims.
// An explicitly empty state stages only its marker and absent-history guards.
// Later steps retain the marker and cannot change an earlier staged claim.
// Counter and history remain absent until FinalizeAccountReplay validates the complete census.
func PrepareAccountReplay(ctx context.Context, source RecoveryReader, state AccountReplayState, claims []Claim) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(claims) > replayClaimLimit || len(claims) == 0 && state.Claims != 0 {
		return nil, ErrInvalid
	}
	marker, err := state.bytes()
	if err != nil {
		return nil, err
	}
	before, err := readReplayRecord(ctx, source, replayAccountKey(state.Account))
	if err != nil {
		return nil, err
	}
	if err := requireAbsentReplayHistory(ctx, source, state.Account); err != nil {
		return nil, err
	}
	if before == nil {
		if err := requireEmptyReplayAccount(ctx, source, state.Account); err != nil {
			return nil, err
		}
	} else if !bytes.Equal(before.Value, marker) {
		return nil, ErrClaimConflict
	}
	mutations := map[string]storage.CompareAndSwapMutation{
		countKey(state.Account):         {Key: countKey(state.Account)},
		historyKey(state.Account):       {Key: historyKey(state.Account)},
		replayAccountKey(state.Account): {Key: replayAccountKey(state.Account), NewValue: marker},
	}
	if before != nil {
		mutation := mutations[before.Key]
		mutation.ExpectedValue = before.Value
		mutations[before.Key] = mutation
	}
	var held int64
	for _, claim := range claims {
		if claim.Account != state.Account {
			return nil, ErrClaimConflict
		}
		data, err := replayClaimBytes(claim)
		if err != nil {
			return nil, err
		}
		key := claimKey(claim.Account, claim.ID)
		if _, ok := mutations[key]; ok {
			return nil, ErrInvalid
		}
		existing, err := readReplayRecord(ctx, source, key)
		if err != nil {
			return nil, err
		}
		mutation := storage.CompareAndSwapMutation{Key: key, NewValue: data}
		if existing != nil {
			if !bytes.Equal(existing.Value, data) {
				return nil, ErrClaimConflict
			}
			mutation.ExpectedValue = existing.Value
		}
		mutations[key] = mutation
		if !claim.Released {
			held++
		}
	}
	if int64(len(claims)) > state.Claims || held > state.Held {
		return nil, ErrHistoryUnknown
	}
	return orderedReplayMutations(mutations)
}

// FinalizeAccountReplay publishes counts and history only after every claim matches.
// It removes the owner marker in the same native step. The import barrier remains.
// Linked execution references still require the coordinator's closed-view validation.
func FinalizeAccountReplay(ctx context.Context, source RecoveryReader, state AccountReplayState) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil {
		return nil, ErrInvalid
	}
	marker, err := state.bytes()
	if err != nil {
		return nil, err
	}
	before, err := readReplayRecord(ctx, source, replayAccountKey(state.Account))
	if err != nil || before == nil {
		return nil, errors.Join(ErrHistoryUnknown, err)
	}
	if !bytes.Equal(before.Value, marker) {
		return nil, ErrClaimConflict
	}
	if err := requireAbsentReplayHistory(ctx, source, state.Account); err != nil {
		return nil, err
	}
	actual, err := summarizeReplayClaims(ctx, source, state.Account)
	if err != nil {
		return nil, err
	}
	if actual != state {
		return nil, ErrHistoryUnknown
	}
	total, err := json.Marshal(counter{Version: recordVersion, Total: state.Held})
	if err != nil {
		return nil, err
	}
	return orderedReplayMutations(map[string]storage.CompareAndSwapMutation{
		countKey(state.Account):   {Key: countKey(state.Account), NewValue: total},
		historyKey(state.Account): {Key: historyKey(state.Account), NewValue: []byte("3")},
		before.Key:                {Key: before.Key, ExpectedValue: before.Value},
	})
}

func requireAbsentReplayHistory(ctx context.Context, source RecoveryReader, account string) error {
	for _, key := range []string{countKey(account), historyKey(account)} {
		value, err := readReplayRecord(ctx, source, key)
		if err != nil || value != nil {
			return errors.Join(ErrHistoryUnknown, err)
		}
	}
	return nil
}

func enumerateReplay(ctx context.Context, source RecoveryReader, visit func(storage.TransferRecord) error) error {
	enumerator, ok := source.(RecoveryEnumerator)
	if !ok {
		return ErrHistoryUnknown
	}
	previous := ""
	return enumerator.Enumerate(ctx, func(record storage.TransferRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.Key <= previous {
			return ErrHistoryUnknown
		}
		previous = record.Key
		return visit(record)
	})
}

func requireEmptyReplayAccount(ctx context.Context, source RecoveryReader, account string) error {
	return enumerateReplay(ctx, source, func(record storage.TransferRecord) error {
		if record.Key == countKey(account) || record.Key == replayAccountKey(account) || strings.HasPrefix(record.Key, accountClaimPrefix(account)) {
			return ErrHistoryUnknown
		}
		return nil
	})
}

func summarizeReplayClaims(ctx context.Context, source RecoveryReader, account string) (AccountReplayState, error) {
	state := AccountReplayState{Version: recordVersion, Account: account}
	hash := sha256.New()
	_, _ = hash.Write([]byte("starport-jobslots-account-v1"))
	err := enumerateReplay(ctx, source, func(record storage.TransferRecord) error {
		if !strings.HasPrefix(record.Key, accountClaimPrefix(account)) || record.Key == historyKey(account) {
			return nil
		}
		item, err := verifyReplayRecord(record)
		if err != nil || item.Claim == nil || item.Account != account {
			return errors.Join(ErrHistoryUnknown, err)
		}
		if state.Claims == math.MaxInt64 {
			return ErrHistoryUnknown
		}
		state.Claims++
		if !item.Claim.Released {
			state.Held++
		}
		data, err := replayClaimBytes(*item.Claim)
		if err != nil {
			return err
		}
		_, _ = hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		_, _ = hash.Write(data)
		return nil
	})
	state.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return state, err
}
