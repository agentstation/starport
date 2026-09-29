package jobslots

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"strings"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

// ClaimStoragePrefix identifies claims and their retained account history markers.
const ClaimStoragePrefix = claimPrefix

// RecoveryRecord describes one validated slot record from a closed recovery view.
type RecoveryRecord struct {
	Account string
	Claim   *Claim
	Total   *int64
	History bool
}

// RecoveryAttachment identifies work that its owner validates in the same view.
// Finished means that no execution remains. A terminal batch can still have active lines.
type RecoveryAttachment struct {
	Account  string
	ClaimID  string
	JobID    string
	Kind     string
	Released bool
	Finished bool
}

// RecoveryReport counts retained ownership. Pending claims still consume capacity.
// These checks do not establish complete history after a backup or permit activation.
type RecoveryReport struct {
	Accounts    int64 `json:"accounts"`
	Claims      int64 `json:"claims"`
	Held        int64 `json:"held"`
	Pending     int64 `json:"pending"`
	Attachments int64 `json:"attachments"`
}

// VerifyRecoveryRecord validates persistent slot schemas and exact storage identities.
func VerifyRecoveryRecord(record storage.TransferRecord) (RecoveryRecord, error) {
	if record.ExpiresAtMillis != 0 || len(record.Value) > maxRecordBytes {
		return RecoveryRecord{}, ErrHistoryUnknown
	}
	if account, ok := strings.CutPrefix(record.Key, limits.OutstandingJobsPrefix); ok {
		var count counter
		if !validID(account) || json.Unmarshal(record.Value, &count) != nil || count.Version != recordVersion || count.Total < 0 {
			return RecoveryRecord{}, ErrHistoryUnknown
		}
		return RecoveryRecord{Account: account, Total: &count.Total}, nil
	}
	suffix, ok := strings.CutPrefix(record.Key, claimPrefix)
	if !ok {
		return RecoveryRecord{}, ErrInvalid
	}
	encoded, name, ok := strings.Cut(suffix, ":")
	account, err := base64.RawURLEncoding.DecodeString(encoded)
	if !ok || err != nil || !validID(string(account)) || base64.RawURLEncoding.EncodeToString(account) != encoded {
		return RecoveryRecord{}, ErrHistoryUnknown
	}
	if name == "history" {
		if !bytes.Equal(record.Value, []byte("3")) {
			return RecoveryRecord{}, ErrHistoryUnknown
		}
		return RecoveryRecord{Account: string(account), History: true}, nil
	}
	var claim Claim
	if json.Unmarshal(record.Value, &claim) != nil || !claim.valid() || claimKey(claim.Account, claim.ID) != record.Key {
		return RecoveryRecord{}, ErrHistoryUnknown
	}
	return RecoveryRecord{Account: claim.Account, Claim: &claim}, nil
}

// VerifyRecoveryTotal refuses missing history and counts that differ from retained claims.
// An untouched account needs no record. Call this for each account with retained slot state.
func VerifyRecoveryTotal(total *int64, history bool, held int64) error {
	if total == nil || !history || held < 0 || *total != held {
		return ErrHistoryUnknown
	}
	return nil
}

// VerifyRecoveryAttachment checks both directions without releasing uncertain capacity.
// Released claims can outlive deleted work. A release acknowledgement can remain pending.
func VerifyRecoveryAttachment(claim *Claim, work *RecoveryAttachment) error {
	if claim == nil || !claim.valid() {
		return ErrHistoryUnknown
	}
	if work == nil {
		if claim.Attached && !claim.Released {
			return ErrHistoryUnknown
		}
		return nil
	}
	if !claim.Attached || claim.Account != work.Account || claim.ID != work.ClaimID || claim.JobID != work.JobID || claim.Kind != work.Kind {
		return ErrHistoryUnknown
	}
	if work.Released && !claim.Released || (work.Released || claim.Released) && !work.Finished {
		return ErrHistoryUnknown
	}
	return nil
}
