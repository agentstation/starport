package recovery

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/storage"
)

type historyKVDomain struct {
	Version     int                             `json:"version"`
	Accounts    []account.RecoveryChange        `json:"accounts,omitempty"`
	APIKeys     *apikey.RecoveryReplay          `json:"api_keys,omitempty"`
	Credentials []credentials.RecoveryChange    `json:"credentials,omitempty"`
	Attempts    []reservation.Record            `json:"attempts,omitempty"`
	Corrections []reservation.CorrectionReceipt `json:"corrections,omitempty"`
	Slots       []jobslots.Claim                `json:"slots,omitempty"`
	Files       []files.RecoveryChange          `json:"files,omitempty"`
	Batches     []jobs.BatchReplay              `json:"batches,omitempty"`
	Videos      []historyVideoReplay            `json:"videos,omitempty"`
}
type historyVideoReplay struct {
	Final       jobs.RecoveryJob             `json:"final"`
	Corrections []jobs.RecoveryJobCorrection `json:"corrections,omitempty"`
	Publish     bool                         `json:"publish"`
}
type historyWindowStage struct {
	Version     int                             `json:"version"`
	State       reservation.WindowReplayState   `json:"state"`
	Attempts    []reservation.Record            `json:"attempts"`
	Corrections []reservation.CorrectionReceipt `json:"corrections"`
}
type historyWindowFinal struct {
	Version int                           `json:"version"`
	State   reservation.WindowReplayState `json:"state"`
}
type historySlotStage struct {
	Version int                         `json:"version"`
	State   jobslots.AccountReplayState `json:"state"`
	Claims  []jobslots.Claim            `json:"claims"`
}
type historySlotFinal struct {
	Version int                         `json:"version"`
	State   jobslots.AccountReplayState `json:"state"`
}
type historyByteStage struct {
	Version int                            `json:"version"`
	State   storedbytes.AccountReplayState `json:"state"`
	Claims  []storedbytes.RecoveryClaim    `json:"claims"`
}
type historyByteFinal struct {
	Version int                            `json:"version"`
	State   storedbytes.AccountReplayState `json:"state"`
}

// prepareHistoryKV decodes private owner evidence against one immutable pre-step snapshot.
// It grants no replay capability. The coordinator retains source snapshots and native receipts.
// Assets must come from a position-guarded import snapshot, never the live imported store.
func prepareHistoryKV(ctx context.Context, kind string, payload []byte, before *KVSnapshotView, assets blob.PublicationReader, at time.Time, encryption *credentials.EncryptionService, accepted revision.RecoveryAuthority) (preparedHistoryKV, error) {
	if ctx == nil || before == nil || at.IsZero() {
		return preparedHistoryKV{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return preparedHistoryKV{}, err
	}
	proposal := newHistoryKVProposal(before)
	digest, changes, err := prepareHistoryKVKind(ctx, kind, payload, before, assets, at, encryption, accepted, proposal)
	if err != nil {
		return preparedHistoryKV{}, err
	}
	if err := proposal.add(changes); err != nil {
		return preparedHistoryKV{}, err
	}
	return proposal.finish(digest)
}
func prepareHistoryKVKind(ctx context.Context, kind string, payload []byte, before *KVSnapshotView, assets blob.PublicationReader, at time.Time, encryption *credentials.EncryptionService, accepted revision.RecoveryAuthority, proposal *historyKVProposal) (string, []storage.CompareAndSwapMutation, error) {
	switch kind {
	case "kv_domain":
		var value historyKVDomain
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 || !explicitDomainChanges(payload) {
			return "", nil, ErrConflict
		}
		err = prepareHistoryDomains(ctx, before, assets, at, encryption, value, proposal)
		return digest, nil, err
	case "window_stage":
		var value historyWindowStage
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := reservation.PrepareWindowReplay(ctx, before, value.State, value.Attempts, value.Corrections)
		return digest, changes, err
	case "window_finalize":
		var value historyWindowFinal
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := reservation.FinalizeWindowReplay(ctx, before, value.State)
		return digest, changes, err
	case "slot_stage":
		var value historySlotStage
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := jobslots.PrepareAccountReplay(ctx, before, value.State, value.Claims)
		return digest, changes, err
	case "slot_finalize":
		var value historySlotFinal
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := jobslots.FinalizeAccountReplay(ctx, before, value.State)
		return digest, changes, err
	case "storedbytes_stage":
		var value historyByteStage
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := storedbytes.PrepareAccountReplay(ctx, before, value.State, value.Claims)
		return digest, changes, err
	case "storedbytes_finalize":
		var value historyByteFinal
		digest, err := decodeHistoryPayload(kind, payload, &value)
		if err != nil || value.Version != 1 {
			return "", nil, ErrConflict
		}
		changes, err := storedbytes.FinalizeAccountReplay(ctx, before, value.State)
		return digest, changes, err
	case historyKVAuthorityFinal:
		return prepareHistoryKVAuthority(ctx, payload, before, accepted)
	default:
		return "", nil, ErrConflict
	}
}
func prepareHistoryKVAuthority(ctx context.Context, payload []byte, before *KVSnapshotView, accepted revision.RecoveryAuthority) (string, []storage.CompareAndSwapMutation, error) {
	var input historyKVAuthorityPayload
	digest, err := decodeHistoryPayload(historyKVAuthorityFinal, payload, &input)
	if err != nil || input.Version != 1 || !explicitHistoryMembers(payload, "expected_sha256") {
		return "", nil, ErrConflict
	}
	transition, err := revision.NewKVRecoveryTransition(input.ExpectedSHA256, accepted)
	if err != nil {
		return "", nil, err
	}
	change, err := revision.PrepareKVRecovery(ctx, before, transition)
	return digest, []storage.CompareAndSwapMutation{change}, err
}
func explicitDomainChanges(data []byte) bool {
	var object map[string]jsontext.Value
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	for _, group := range []string{"accounts", "credentials", "files"} {
		field, ok := object[group]
		if !ok {
			continue
		}
		after := "next"
		if group == "files" {
			after = "after"
		}
		if !explicitHistoryArray(field, "expected_sha256", after) {
			return false
		}
	}
	if raw, ok := object["api_keys"]; ok && string(raw) != "null" {
		var key map[string]jsontext.Value
		if json.Unmarshal(raw, &key) != nil || !explicitHistoryMembers(raw, "expected_indexes_sha256", "indexes", "changes") || !explicitHistoryArray(key["changes"], "expected_sha256", "next") {
			return false
		}
	}
	return true
}
func explicitHistoryArray(data []byte, members ...string) bool {
	var values []jsontext.Value
	if json.Unmarshal(data, &values) != nil {
		return false
	}
	for _, value := range values {
		if !explicitHistoryMembers(value, members...) {
			return false
		}
	}
	return true
}

type historyKVAuthorityPayload struct {
	Version        int    `json:"version"`
	ExpectedSHA256 string `json:"expected_sha256"`
}
