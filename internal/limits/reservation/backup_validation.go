package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

// StoragePrefix identifies retained atomic-budget records.
const StoragePrefix = "budget:v1:"

// BackupReader reads captured budget records without changing balances or permissions.
type BackupReader interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
}

// BackupRecord describes one validated record without issuing a dispatch permit.
type BackupRecord struct {
	Kind          string
	Attempt       *Record
	Held          bool
	TeamOrigin    *BackupTeamOrigin
	Window        *WindowState
	Contributions []BackupContribution
	Corrections   int64
}

// BackupTeamOrigin binds a completed KV initialization to its consumed SQL grant.
type BackupTeamOrigin struct {
	TeamID    string
	HistoryID string
}

// VerifyBackupRecord checks schema, identity, original expiry, and retained references.
// Deleted holders remain historical identities. No record authorizes a replay or refund.
func VerifyBackupRecord(ctx context.Context, source BackupReader, record storage.TransferRecord) (BackupRecord, error) {
	if source == nil || ctx.Err() != nil {
		return BackupRecord{}, errors.Join(ErrUnavailable, ctx.Err())
	}
	if record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > maxRecordSize {
		return BackupRecord{}, ErrUnavailable
	}
	kind, _, found := strings.Cut(strings.TrimPrefix(record.Key, StoragePrefix), ":")
	if !strings.HasPrefix(record.Key, StoragePrefix) || !found {
		return BackupRecord{}, ErrUnavailable
	}
	result := BackupRecord{Kind: kind}
	var err error
	switch kind {
	case "attempt":
		var value *Record
		value, err = decodeAttemptRecord(record.Key, record.Value)
		if err == nil {
			result.Attempt = value
			result.Held = value.State != Settled && value.State != Canceled
			err = verifyBackupAttemptReferences(ctx, source, value)
			if err == nil {
				result.Corrections, err = verifyBackupCorrectionChain(ctx, source, *value)
			}
			if err == nil {
				result.Contributions, err = backupContributions(value)
			}
		}
	case "meter":
		result.Window, err = verifyBackupMeter(record)
	case "history":
		err = verifyBackupHistory(ctx, source, record)
	case "holder":
		err = verifyBackupHolder(record)
	case "team-origin":
		result.TeamOrigin, err = verifyBackupTeamOrigin(ctx, source, record)
	case "correction":
		err = verifyBackupCorrection(ctx, source, record)
	default:
		err = ErrUnavailable
	}
	return result, err
}

func readBackupValue(ctx context.Context, source BackupReader, key string) ([]byte, error) {
	value, err := source.ReadCaptured(ctx, key, maxRecordSize)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if value.Key != key || value.ExpiresAtMillis != 0 || len(value.Value) == 0 {
		return nil, ErrUnavailable
	}
	return value.Value, nil
}

func verifyBackupAttemptReferences(ctx context.Context, source BackupReader, value *Record) error {
	for _, binding := range value.Bindings {
		data, err := readBackupValue(ctx, source, meterKey(binding.Rule.Meter, binding.Window))
		if err != nil {
			return err
		}
		window, err := decodeWindowRecord(binding.Rule.Meter, binding.Window, data)
		if err != nil || window.HistoryID != binding.Rule.HistoryID {
			return ErrUnavailable
		}
	}

	return nil
}

func verifyBackupHistory(ctx context.Context, source BackupReader, record storage.TransferRecord) error {
	var value historyState
	if json.Unmarshal(record.Value, &value) != nil || value.Version != recordVersion || !value.Meter.valid() || !validID(value.History.ID) || !validID(value.History.Proof) || record.Key != storageKey("history", value.Meter) {
		return ErrUnavailable
	}
	if value.Current == (storage.TimeWindow{}) {
		return nil
	}
	if value.Current != windowFor(value.Meter.Interval, value.Current.Start) {
		return ErrUnavailable
	}
	data, err := readBackupValue(ctx, source, meterKey(value.Meter, value.Current))
	if err != nil {
		return err
	}
	window, err := decodeWindowRecord(value.Meter, value.Current, data)
	if err != nil || window.HistoryID != value.History.ID {
		return ErrUnavailable
	}
	return nil
}

func verifyBackupMeter(record storage.TransferRecord) (*WindowState, error) {
	var value WindowState
	if json.Unmarshal(record.Value, &value) != nil || !value.Meter.valid() || value.Window != windowFor(value.Meter.Interval, value.Window.Start) || record.Key != meterKey(value.Meter, value.Window) {
		return nil, ErrUnavailable
	}
	return decodeWindowRecord(value.Meter, value.Window, record.Value)
}

func verifyBackupHolder(record storage.TransferRecord) error {
	var value holderIdentity
	if json.Unmarshal(record.Value, &value) != nil {
		return ErrUnavailable
	}
	mutation, err := FreshHolderIdentity(value.Scope, value.ID)
	if err != nil || mutation.Key != record.Key {
		return ErrUnavailable
	}
	return nil
}

func verifyBackupTeamOrigin(ctx context.Context, source BackupReader, record storage.TransferRecord) (*BackupTeamOrigin, error) {
	var value teamHistoryReceipt
	if json.Unmarshal(record.Value, &value) != nil || value.Version != recordVersion || !value.Meter.valid() || value.Meter.Scope != limits.ScopeTeam || value.Meter.Dimension != limits.DimensionSpend || !validID(value.History) || record.Key != storageKey("team-origin", value) {
		return nil, ErrUnavailable
	}
	_, err := readBackupValue(ctx, source, storageKey("holder", holderIdentity{Scope: limits.ScopeTeam, ID: value.Meter.Holder}))
	return &BackupTeamOrigin{TeamID: value.Meter.Holder, HistoryID: value.History}, err
}

func verifyBackupCorrection(ctx context.Context, source BackupReader, record storage.TransferRecord) error {
	var value CorrectionReceipt
	if json.Unmarshal(record.Value, &value) != nil || record.Key != correctionKey(value.Before.Attempt.ID, value.Correction.ID) {
		return ErrUnavailable
	}
	_, err := decodeCorrectionReceipt(value.Before.Attempt.ID, value.Correction.ID, record.Value)
	if err != nil {
		return err
	}
	_, err = readBackupValue(ctx, source, storageKey("attempt", value.Before.Attempt.ID))
	if err != nil {
		return err
	}
	if value.Before.CorrectionID != "" {
		data, err := readBackupValue(ctx, source, correctionKey(value.Before.Attempt.ID, value.Before.CorrectionID))
		if err != nil {
			return err
		}
		_, err = decodeCorrectionReceipt(value.Before.Attempt.ID, value.Before.CorrectionID, data)
		return err
	}
	return nil
}

// CheckBackupHistory reports whether captured history matches one current budget policy.
// Missing or different history remains unknown. It never establishes zero consumption.
func CheckBackupHistory(ctx context.Context, source BackupReader, meter Meter, history string) (bool, error) {
	if source == nil || !meter.valid() {
		return false, ErrInvalid
	}
	if !validID(history) {
		return false, nil
	}
	record, err := source.ReadCaptured(ctx, storageKey("history", meter), maxRecordSize)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.Key != storageKey("history", meter) {
		return false, ErrUnavailable
	}
	if _, err := VerifyBackupRecord(ctx, source, record); err != nil {
		return false, err
	}
	var value historyState
	if json.Unmarshal(record.Value, &value) != nil || value.Meter != meter {
		return false, ErrUnavailable
	}
	return value.History.ID == history, nil
}

// CheckBackupLimits counts policy dimensions whose captured accounting history is unknown.
func CheckBackupLimits(ctx context.Context, source BackupReader, scope limits.Scope, id string, policy *limits.Limits) (unknown int64, err error) {
	if policy == nil {
		return 0, nil
	}
	if policy.Validate() != nil {
		return 0, ErrInvalid
	}
	for _, entry := range []struct {
		dimension limits.Dimension
		budget    *limits.Budget
	}{{limits.DimensionSpend, policy.Spend}, {limits.DimensionTokens, policy.Tokens}} {
		if entry.budget == nil {
			continue
		}
		known, err := CheckBackupHistory(ctx, source, Meter{Scope: scope, Holder: id, Dimension: entry.dimension, Interval: entry.budget.Interval}, entry.budget.HistoryID)
		if err != nil {
			return unknown, err
		}
		if !known {
			unknown++
		}
	}
	return unknown, nil
}

// ReadBackupAttempt validates one retained attempt without issuing dispatch permission.
func ReadBackupAttempt(ctx context.Context, source BackupReader, id string) (*Record, error) {
	if !validID(id) || source == nil {
		return nil, ErrInvalid
	}
	key := storageKey("attempt", id)
	data, err := readBackupValue(ctx, source, key)
	if err != nil {
		return nil, err
	}
	return decodeAttemptRecord(key, data)
}

// VerifyBackupCorrectionBinding checks the decision shared with an external audit owner.
// The original digest identifies the exact transaction contents.
func VerifyBackupCorrectionBinding(ctx context.Context, source BackupReader, id string, expected Correction) error {
	if !validID(id) || !expected.valid() || source == nil {
		return ErrInvalid
	}
	data, err := readBackupValue(ctx, source, correctionKey(id, expected.ID))
	if err != nil {
		return err
	}
	receipt, err := decodeCorrectionReceipt(id, expected.ID, data)
	if err != nil {
		return err
	}
	if receipt.PublicationDigest == "" || !sameCorrection(receipt.Correction, expected) {
		return ErrIdentityConflict
	}
	return nil
}
