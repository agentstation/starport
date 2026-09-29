package storedbytes

import (
	"encoding/base64"
	"encoding/json/v2"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// RecoveryClaim preserves the complete durable byte claim during operator recovery.
type RecoveryClaim struct {
	Version   int       `json:"version"`
	Holder    string    `json:"holder"`
	ID        string    `json:"id"`
	Initial   int64     `json:"initial"`
	Bytes     int64     `json:"bytes"`
	Bound     int64     `json:"bound"`
	Measured  bool      `json:"measured"`
	Attached  bool      `json:"attached"`
	Released  bool      `json:"released"`
	CreatedAt time.Time `json:"created_at"`
}

// RecoveryRecord identifies one strictly decoded owner record.
type RecoveryRecord struct {
	Holder string
	Total  *int64
	Claim  *RecoveryClaim
}

// VerifyRecoveryRecord rejects unsupported fields, transient records, and identity mismatches.
func VerifyRecoveryRecord(record storage.TransferRecord) (RecoveryRecord, error) {
	if record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > 8192 {
		return RecoveryRecord{}, ErrStorageHistoryUnknown
	}
	if strings.HasSuffix(record.Key, ":total") {
		var total byteTotal
		if json.Unmarshal(record.Value, &total, json.RejectUnknownMembers(true)) != nil || total.Version != StoredBytesSchemaVersion || total.Bytes < 0 {
			return RecoveryRecord{}, ErrStorageHistoryUnknown
		}
		holder, ok := recoveryHolder(record.Key, ":total")
		if !ok || record.Key != byteTotalKey(holder) {
			return RecoveryRecord{}, ErrStorageHistoryUnknown
		}
		return RecoveryRecord{Holder: holder, Total: &total.Bytes}, nil
	}
	var claim RecoveryClaim
	if json.Unmarshal(record.Value, &claim, json.RejectUnknownMembers(true)) != nil || !validRecoveryClaim(claim) || record.Key != byteClaimKey(claim.Holder, claim.ID) {
		return RecoveryRecord{}, ErrStorageHistoryUnknown
	}
	return RecoveryRecord{Holder: claim.Holder, Claim: &claim}, nil
}

func validRecoveryClaim(c RecoveryClaim) bool {
	return c.Version == StoredBytesSchemaVersion && strings.TrimSpace(c.Holder) != "" && c.ID != "" && len(c.Holder) <= 512 && len(c.ID) <= 512 && c.Initial >= 0 && c.Bytes >= 0 && c.Bound >= 0 && !c.CreatedAt.IsZero() && (!c.Measured || c.Attached) && (c.Measured || c.Bytes == c.Initial)
}

// VerifyRecoveryTotal requires retained total evidence and the exact held-claim sum.
func VerifyRecoveryTotal(total *int64, held int64) error {
	if total == nil || held < 0 || *total != held {
		return ErrStorageHistoryUnknown
	}
	return nil
}

// RecoveryAttachment describes the file facts needed to check its byte claim.
type RecoveryAttachment struct {
	Holder, FileID                    string
	Metered, Deleting, Ready, Retired bool
	Bytes                             int64
}

// VerifyRecoveryAttachment checks both claim-to-file and file-to-claim references.
// Released attached claims may retain deleting metadata until cleanup completes.
func VerifyRecoveryAttachment(claim *RecoveryClaim, file *RecoveryAttachment) error {
	if file == nil {
		if claim != nil && claim.Attached && !claim.Released {
			return ErrStorageHistoryUnknown
		}
		return nil
	}
	if !file.Metered {
		if claim != nil {
			return ErrStorageHistoryUnknown
		}
		return nil
	}
	if claim == nil || !validRecoveryClaim(*claim) || claim.Holder != file.Holder || claim.ID != file.FileID || !claim.Attached || claim.Released && (!file.Deleting || !file.Retired) || file.Ready && (!claim.Measured || claim.Bytes != file.Bytes) {
		return ErrStorageHistoryUnknown
	}
	return nil
}

func recoveryHolder(key, suffix string) (string, bool) {
	encoded, ok := strings.CutPrefix(key, StoredBytesPrefix)
	if !ok {
		return "", false
	}
	encoded, ok = strings.CutSuffix(encoded, suffix)
	if !ok {
		return "", false
	}
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	return string(value), err == nil && strings.TrimSpace(string(value)) != "" && len(value) <= 512
}
