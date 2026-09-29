package reservation

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

func windowReplayContains(record Record, state WindowReplayState) bool {
	for _, binding := range record.Bindings {
		if binding.Rule.Meter == state.Window.Meter && binding.Window == state.Window.Window {
			return binding.Rule.HistoryID == state.Window.HistoryID
		}
	}
	return false
}

func strictWindowReplayAttempt(raw storage.TransferRecord) (*Record, error) {
	if raw.ExpiresAtMillis != 0 || len(raw.Value) == 0 || len(raw.Value) > maxRecordSize {
		return nil, ErrUnavailable
	}
	var value Record
	if json.Unmarshal(raw.Value, &value, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrUnavailable
	}
	return decodeAttemptRecord(raw.Key, raw.Value)
}

func strictWindowReplayCorrection(raw storage.TransferRecord) (*CorrectionReceipt, error) {
	if raw.ExpiresAtMillis != 0 || len(raw.Value) == 0 || len(raw.Value) > maxRecordSize {
		return nil, ErrUnavailable
	}
	var value CorrectionReceipt
	if json.Unmarshal(raw.Value, &value, json.RejectUnknownMembers(true)) != nil || raw.Key != correctionKey(value.Before.Attempt.ID, value.Correction.ID) {
		return nil, ErrUnavailable
	}
	return decodeCorrectionReceipt(value.Before.Attempt.ID, value.Correction.ID, raw.Value)
}

func censusWindowReplay(ctx context.Context, source BackupReader, state WindowReplayState, independent bool) (attempts, corrections int64, digest string, resultErr error) {
	enumerator, ok := source.(WindowReplayEnumerator)
	if !ok {
		return 0, 0, "", ErrHistoryUnknown
	}
	sum := sha256.New()
	_, _ = sum.Write([]byte("starport-budget-window-v1"))
	totals := BackupTotals{}
	previous := ""
	err := enumerator.Enumerate(ctx, func(raw storage.TransferRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if raw.Key <= previous {
			return ErrUnavailable
		}
		previous = raw.Key
		var canonical []byte
		switch {
		case strings.HasPrefix(raw.Key, StoragePrefix+"attempt:"):
			record, err := strictWindowReplayAttempt(raw)
			if err != nil {
				return err
			}
			if !windowReplayContains(*record, state) {
				for _, binding := range record.Bindings {
					if binding.Rule.Meter == state.Window.Meter && binding.Window == state.Window.Window {
						return ErrHistoryUnknown
					}
				}
				return nil
			}
			if independent {
				if err := verifyBackupAttemptReferences(ctx, source, record); err != nil {
					return err
				}
			}
			if _, err := verifyBackupCorrectionChain(ctx, source, *record); err != nil {
				return err
			}
			if attempts == math.MaxInt64 {
				return ErrUnavailable
			}
			attempts++
			contributions, err := backupContributions(record)
			if err != nil {
				return err
			}
			for _, contribution := range contributions {
				if contribution.WindowKey == meterKey(state.Window.Meter, state.Window.Window) {
					totals, err = AddBackupTotals(totals, contribution.Totals)
					if err != nil {
						return err
					}
				}
			}
			canonical, err = replayBytes(record)
			if err != nil {
				return err
			}
		case strings.HasPrefix(raw.Key, StoragePrefix+"correction:"):
			var err error
			canonical, err = censusWindowReplayCorrection(ctx, source, state, raw)
			if err != nil {
				return err
			}
			if canonical == nil {
				return nil
			}
			if corrections == math.MaxInt64 {
				return ErrUnavailable
			}
			corrections++
		default:
			return nil
		}
		_, _ = sum.Write(binary.BigEndian.AppendUint64(nil, uint64(len(raw.Key))))
		_, _ = sum.Write([]byte(raw.Key))
		_, _ = sum.Write(binary.BigEndian.AppendUint64(nil, uint64(len(canonical))))
		_, _ = sum.Write(canonical)
		return nil
	})
	if err != nil {
		return 0, 0, "", err
	}
	if err := VerifyBackupTotals(state.Window, totals); err != nil {
		return 0, 0, "", err
	}
	return attempts, corrections, hex.EncodeToString(sum.Sum(nil)), nil
}

// The caller validates the complete chain before this walk, including cycle checks.
func windowReplayChainContains(ctx context.Context, source BackupReader, record Record, id string) error {
	for record.CorrectionID != "" {
		if record.CorrectionID == id {
			return nil
		}
		raw, err := readWindowReplayRecord(ctx, source, correctionKey(record.Attempt.ID, record.CorrectionID))
		if err != nil || raw == nil {
			return errors.Join(ErrHistoryUnknown, err)
		}
		receipt, err := strictWindowReplayCorrection(*raw)
		if err != nil {
			return err
		}
		record = receipt.Before
	}
	return ErrIdentityConflict
}

func verifyWindowReplayTransition(ctx context.Context, view BackupReader, before, after Record) error {
	if _, err := verifyBackupCorrectionChain(ctx, view, after); err != nil {
		return err
	}
	current := after
	for current.CorrectionID != before.CorrectionID {
		if current.CorrectionID == "" {
			return ErrTransition
		}
		raw, err := readWindowReplayRecord(ctx, view, correctionKey(current.Attempt.ID, current.CorrectionID))
		if err != nil || raw == nil {
			return errors.Join(ErrHistoryUnknown, err)
		}
		receipt, err := strictWindowReplayCorrection(*raw)
		if err != nil {
			return err
		}
		current = receipt.Before
	}
	return verifyReplayProgress(before, current)
}

func censusWindowReplayCorrection(ctx context.Context, source BackupReader, state WindowReplayState, raw storage.TransferRecord) ([]byte, error) {
	receipt, err := strictWindowReplayCorrection(raw)
	if err != nil {
		return nil, err
	}
	if !windowReplayContains(receipt.Before, state) {
		return nil, nil
	}
	rawAttempt, err := readWindowReplayRecord(ctx, source, storageKey("attempt", receipt.Before.Attempt.ID))
	if err != nil || rawAttempt == nil {
		return nil, errors.Join(ErrHistoryUnknown, err)
	}
	attempt, err := strictWindowReplayAttempt(*rawAttempt)
	if err != nil {
		return nil, err
	}
	if !windowReplayContains(*attempt, state) {
		return nil, ErrIdentityConflict
	}
	if _, err := verifyBackupCorrectionChain(ctx, source, *attempt); err != nil {
		return nil, err
	}
	if err := windowReplayChainContains(ctx, source, *attempt, receipt.Correction.ID); err != nil {
		return nil, err
	}
	return replayBytes(receipt)
}
