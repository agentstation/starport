package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"slices"

	"github.com/agentstation/starport/internal/storage"
)

// replayView describes one immutable accounting step, including its proposed records.
// It never reads a provider or issues dispatch permission.
type replayView struct {
	source  BackupReader
	records map[string]storage.TransferRecord
}

func (v replayView) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	if record, ok := v.records[key]; ok {
		if len(record.Value) > maximum {
			return storage.TransferRecord{}, storage.ErrValueTooLarge
		}
		return record, nil
	}
	return v.source.ReadCaptured(ctx, key, maximum)
}

type replayWindow struct {
	before    storage.TransferRecord
	state     *WindowState
	old, next BackupTotals
}

// PrepareAccountingReplay derives conditional writes from independent later attempt records.
// source must retain the immutable pre-step snapshot for every exact retry.
// The coordinator must verify its interval coverage and combine linked execution changes.
// Missing original windows remain restricted. This operation does not initialize history or write storage.
func PrepareAccountingReplay(ctx context.Context, source BackupReader, attempts []Record, corrections []CorrectionReceipt) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(attempts) == 0 || len(attempts) > 16 || len(corrections) > 64 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view := replayView{source: source, records: map[string]storage.TransferRecord{}}
	mutations := map[string]storage.CompareAndSwapMutation{}
	windows := map[string]*replayWindow{}
	for _, after := range attempts {
		key := storageKey("attempt", after.Attempt.ID)
		if _, exists := view.records[key]; exists {
			return nil, ErrInvalid
		}
		data, err := replayBytes(after)
		if err != nil {
			return nil, err
		}
		decoded, err := decodeAttemptRecord(key, data)
		if err != nil {
			return nil, err
		}
		view.records[key] = storage.TransferRecord{Key: key, Value: data}
		before, err := source.ReadCaptured(ctx, key, maxRecordSize)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			checked, err := VerifyBackupRecord(ctx, source, before)
			if err != nil {
				return nil, err
			}
			if checked.Attempt == nil || before.Key != key {
				return nil, ErrUnavailable
			}
			if err := addReplayContributions(ctx, source, windows, checked.Attempt, false); err != nil {
				return nil, err
			}
		} else {
			before = storage.TransferRecord{Key: key}
		}
		mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: before.Value, NewValue: data}
		if err := addReplayContributions(ctx, source, windows, decoded, true); err != nil {
			return nil, err
		}
	}
	if err := addReplayCorrections(ctx, view, mutations, corrections); err != nil {
		return nil, err
	}
	for key, window := range windows {
		if err := applyReplayTotals(window); err != nil {
			return nil, err
		}
		data, err := replayBytes(window.state)
		if err != nil {
			return nil, err
		}
		view.records[key] = storage.TransferRecord{Key: key, Value: data}
		mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: window.before.Value, NewValue: data}
	}
	if err := verifyReplayRecords(ctx, view, mutations, attempts, corrections); err != nil {
		return nil, err
	}
	if len(mutations) > 128 {
		return nil, ErrInvalid
	}
	keys := make([]string, 0, len(mutations))
	for key := range mutations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	for _, key := range keys {
		result = append(result, mutations[key])
	}
	return result, nil
}

func replayBytes(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err == nil && len(data) > maxRecordSize {
		return nil, ErrInvalid
	}
	return data, err
}

func addReplayContributions(ctx context.Context, source BackupReader, windows map[string]*replayWindow, record *Record, after bool) error {
	contributions, err := backupContributions(record)
	if err != nil {
		return err
	}
	for index, contribution := range contributions {
		binding := record.Bindings[index]
		window := windows[contribution.WindowKey]
		if window == nil {
			captured, err := source.ReadCaptured(ctx, contribution.WindowKey, maxRecordSize)
			if errors.Is(err, storage.ErrNotFound) {
				return ErrHistoryUnknown
			}
			if err != nil {
				return err
			}
			if captured.Key != contribution.WindowKey || captured.ExpiresAtMillis != 0 {
				return ErrUnavailable
			}
			state, err := decodeWindowRecord(binding.Rule.Meter, binding.Window, captured.Value)
			if err != nil {
				return err
			}
			window = &replayWindow{before: captured, state: state}
			windows[contribution.WindowKey] = window
		}
		if window.state.HistoryID != binding.Rule.HistoryID {
			return ErrHistoryUnknown
		}
		if after {
			window.next, err = AddBackupTotals(window.next, contribution.Totals)
		} else {
			window.old, err = AddBackupTotals(window.old, contribution.Totals)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyReplayTotals(window *replayWindow) error {
	state, old := window.state, window.old
	if state.Overflow || old.Overflow || old.Consumed > state.Consumed-state.SeedConsumed || old.Reserved > state.Reserved || old.Disputes > state.ActiveDisputes {
		return ErrUnavailable
	}
	next, err := AddBackupTotals(BackupTotals{Consumed: state.Consumed - old.Consumed, Reserved: state.Reserved - old.Reserved, Disputes: state.ActiveDisputes - old.Disputes}, window.next)
	if err != nil {
		return err
	}
	state.Consumed, state.Reserved, state.ActiveDisputes, state.Overflow = next.Consumed, next.Reserved, next.Disputes, next.Overflow
	state.ReconciliationRequired = state.ActiveDisputes > 0
	return nil
}

func verifyReplayTransition(ctx context.Context, view BackupReader, before, after Record) error {
	if !reflect.DeepEqual(before.Attempt, after.Attempt) || !before.AdmittedAt.Equal(after.AdmittedAt) || !reflect.DeepEqual(before.Bindings, after.Bindings) || (before.JobID != "" && before.JobID != after.JobID) {
		return ErrIdentityConflict
	}
	current := after
	// At most 64 new correction receipts fit this bounded replay step.
	for steps := 0; current.CorrectionID != before.CorrectionID; steps++ {
		if current.CorrectionID == "" || steps >= 64 {
			return ErrTransition
		}
		data, err := readBackupValue(ctx, view, correctionKey(current.Attempt.ID, current.CorrectionID))
		if err != nil {
			return err
		}
		receipt, err := decodeCorrectionReceipt(current.Attempt.ID, current.CorrectionID, data)
		if err != nil {
			return err
		}
		if err := verifyCorrectionRecord(current, *receipt); err != nil {
			return err
		}
		current = receipt.Before
	}
	return verifyReplayProgress(before, current)
}

func verifyReplayProgress(before, after Record) error {
	if !reflect.DeepEqual(before.Attempt, after.Attempt) || !before.AdmittedAt.Equal(after.AdmittedAt) || !reflect.DeepEqual(before.Bindings, after.Bindings) || (before.JobID != "" && before.JobID != after.JobID) {
		return ErrIdentityConflict
	}
	if before.DisputeID != "" && before.DisputeID != after.DisputeID {
		return ErrTransition
	}
	pending, unresolved := after.Pending, after.Unresolved
	if after.State == Settled {
		pending, unresolved = after.Evidence, after.Evidence
	}
	if before.Pending != nil && !sameEvidence(before.Pending, pending) || before.Unresolved != nil && !sameEvidence(before.Unresolved, unresolved) {
		return ErrTransition
	}
	switch before.State {
	case Canceled:
		if CorrectionBinding(before) != CorrectionBinding(after) {
			return ErrTransition
		}
	case Settled:
		if after.State != Settled || !before.SettledAt.Equal(after.SettledAt) || !sameEvidence(before.Evidence, after.Evidence) || before.ResolvedDisputeID != after.ResolvedDisputeID {
			return ErrTransition
		}
	case Dispatched, Uncertain:
		if after.State == Reserved || after.State == Canceled || (before.State == Uncertain && after.State == Dispatched) {
			return ErrTransition
		}
	case Reserved:
	default:
		return ErrTransition
	}
	return nil
}

func replayChainContains(ctx context.Context, view BackupReader, record Record, id string) (bool, error) {
	for steps := 0; record.CorrectionID != ""; steps++ {
		if record.CorrectionID == id {
			return true, nil
		}
		if steps >= 64 {
			return false, ErrInvalid
		}
		data, err := readBackupValue(ctx, view, correctionKey(record.Attempt.ID, record.CorrectionID))
		if err != nil {
			return false, err
		}
		receipt, err := decodeCorrectionReceipt(record.Attempt.ID, record.CorrectionID, data)
		if err != nil {
			return false, err
		}
		record = receipt.Before
	}
	return false, nil
}

func verifyReplayRecords(ctx context.Context, view replayView, mutations map[string]storage.CompareAndSwapMutation, attempts []Record, corrections []CorrectionReceipt) error {
	for _, after := range attempts {
		key := storageKey("attempt", after.Attempt.ID)
		checked, err := VerifyBackupRecord(ctx, view, view.records[key])
		if err != nil {
			return err
		}
		if before := mutations[key].ExpectedValue; before != nil {
			prior, err := decodeAttemptRecord(key, before)
			if err != nil {
				return err
			}
			if err := verifyReplayTransition(ctx, view, *prior, *checked.Attempt); err != nil {
				return err
			}
		}
	}
	// A supplied correction must belong to a final attempt's selected ancestry.
	for _, receipt := range corrections {
		selected := false
		for _, after := range attempts {
			if after.Attempt.ID == receipt.Before.Attempt.ID {
				var err error
				selected, err = replayChainContains(ctx, view, after, receipt.Correction.ID)
				if err != nil {
					return err
				}
			}
		}
		if !selected {
			return ErrInvalid
		}
	}
	return nil
}

func addReplayCorrections(ctx context.Context, view replayView, mutations map[string]storage.CompareAndSwapMutation, corrections []CorrectionReceipt) error {
	for _, receipt := range corrections {
		if _, ok := view.records[storageKey("attempt", receipt.Before.Attempt.ID)]; !ok {
			return ErrInvalid
		}
		key := correctionKey(receipt.Before.Attempt.ID, receipt.Correction.ID)
		if _, ok := view.records[key]; ok {
			return ErrInvalid
		}
		data, err := replayBytes(receipt)
		if err != nil {
			return err
		}
		if _, err := decodeCorrectionReceipt(receipt.Before.Attempt.ID, receipt.Correction.ID, data); err != nil {
			return err
		}
		if _, err := view.source.ReadCaptured(ctx, key, maxRecordSize); !errors.Is(err, storage.ErrNotFound) {
			return errors.Join(ErrIdentityConflict, err)
		}
		view.records[key] = storage.TransferRecord{Key: key, Value: data}
		mutations[key] = storage.CompareAndSwapMutation{Key: key, NewValue: data}
	}
	return nil
}
