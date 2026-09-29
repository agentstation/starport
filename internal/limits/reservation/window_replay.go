package reservation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// WindowReplayPrefix identifies unfinished accounting reconstruction.
// The final recovery view must reject every record under this prefix.
const WindowReplayPrefix = "budget-replay:v1:window:"

// WindowReplayEnumerator streams a complete immutable view in strict key order.
type WindowReplayEnumerator interface {
	Enumerate(context.Context, func(storage.TransferRecord) error) error
}

// WindowReplayState binds independently captured original-window accounting.
// Its digest proves consistency. The coordinator must prove source and interval coverage.
type WindowReplayState struct {
	Version     int                `json:"version"`
	Window      WindowState        `json:"window"`
	History     History            `json:"history"`
	Current     storage.TimeWindow `json:"current"`
	Attempts    int64              `json:"attempts"`
	Corrections int64              `json:"corrections"`
	SHA256      string             `json:"sha256"`
}

func windowReplayKey(state WindowReplayState) string {
	return WindowReplayPrefix + strings.TrimPrefix(meterKey(state.Window.Meter, state.Window.Window), StoragePrefix+"meter:")
}

func (s WindowReplayState) bytes() ([]byte, error) {
	data, err := replayBytes(s.Window)
	if err != nil {
		return nil, err
	}
	if _, err := verifyBackupMeter(storage.TransferRecord{Key: meterKey(s.Window.Meter, s.Window.Window), Value: data}); err != nil {
		return nil, err
	}
	digest, err := hex.DecodeString(s.SHA256)
	if s.Version != 1 || s.Attempts < 0 || s.Corrections < 0 || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != s.SHA256 || s.History.ID != s.Window.HistoryID || s.History.Proof != s.Window.HistoryProof || s.Current != windowFor(s.Window.Meter.Interval, s.Current.Start) || s.Current.Start.Before(s.Window.Window.Start) {
		return nil, ErrInvalid
	}
	return replayBytes(s)
}

// CaptureWindowReplayState validates a complete independent window and its retained history.
// An explicit empty census is valid. An absent window or history remains unknown.
func CaptureWindowReplayState(ctx context.Context, source BackupReader, meter Meter, window storage.TimeWindow) (WindowReplayState, error) {
	if ctx == nil || source == nil || !meter.valid() || window != windowFor(meter.Interval, window.Start) {
		return WindowReplayState{}, ErrInvalid
	}
	raw, err := readWindowReplayRecord(ctx, source, meterKey(meter, window))
	if err != nil || raw == nil {
		return WindowReplayState{}, errors.Join(ErrHistoryUnknown, err)
	}
	var state WindowState
	if json.Unmarshal(raw.Value, &state, json.RejectUnknownMembers(true)) != nil {
		return WindowReplayState{}, ErrUnavailable
	}
	if _, err := verifyBackupMeter(*raw); err != nil {
		return WindowReplayState{}, err
	}
	head, _, err := readWindowReplayHistory(ctx, source, meter)
	if err != nil || head == nil {
		return WindowReplayState{}, errors.Join(ErrHistoryUnknown, err)
	}
	manifest := WindowReplayState{Version: 1, Window: state, History: head.History, Current: head.Current}
	if err := verifyBackupHistory(ctx, source, storage.TransferRecord{Key: storageKey("history", meter), Value: mustReplayHistory(*head)}); err != nil {
		return WindowReplayState{}, err
	}
	marker, err := readWindowReplayRecord(ctx, source, windowReplayKey(manifest))
	if err != nil || marker != nil {
		return WindowReplayState{}, errors.Join(ErrUnavailable, err)
	}
	manifest.Attempts, manifest.Corrections, manifest.SHA256, err = censusWindowReplay(ctx, source, manifest, true)
	if err != nil {
		return WindowReplayState{}, err
	}
	_, err = manifest.bytes()
	return manifest, err
}

func mustReplayHistory(head historyState) []byte { data, _ := replayBytes(head); return data }

// PrepareWindowReplay stages typed records without publishing a replacement balance.
// Each retry must use the exact immutable pre-step source. Staging grants no permission.
func PrepareWindowReplay(ctx context.Context, source BackupReader, state WindowReplayState, attempts []Record, corrections []CorrectionReceipt) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(attempts) > 32 || len(corrections) > 64 {
		return nil, ErrInvalid
	}
	original, err := checkWindowReplayBase(ctx, source, state)
	if err != nil {
		return nil, err
	}
	marker, err := windowReplayMarker(state, original)
	if err != nil {
		return nil, err
	}
	before, err := readWindowReplayRecord(ctx, source, windowReplayKey(state))
	if err != nil {
		return nil, err
	}
	if before != nil && !bytes.Equal(before.Value, marker) {
		return nil, ErrIdentityConflict
	}
	changes := map[string]storage.CompareAndSwapMutation{}
	markerChange := storage.CompareAndSwapMutation{Key: windowReplayKey(state), NewValue: marker}
	if before != nil {
		markerChange.ExpectedValue = before.Value
	}
	changes[markerChange.Key] = markerChange
	windowKey := meterKey(state.Window.Meter, state.Window.Window)
	changes[windowKey] = storage.CompareAndSwapMutation{Key: windowKey}
	if original != nil {
		changes[windowKey] = storage.CompareAndSwapMutation{Key: windowKey, ExpectedValue: original.Value, NewValue: original.Value}
	}
	view := replayView{source: source, records: map[string]storage.TransferRecord{}}
	for _, receipt := range corrections {
		if err := prepareWindowReplayCorrection(ctx, source, state, view, changes, receipt); err != nil {
			return nil, err
		}
	}
	for _, record := range attempts {
		key := storageKey("attempt", record.Attempt.ID)
		if _, ok := changes[key]; ok {
			return nil, ErrInvalid
		}
		data, err := replayBytes(record)
		if err != nil {
			return nil, err
		}
		next, err := decodeAttemptRecord(key, data)
		if err != nil {
			return nil, err
		}
		if !windowReplayContains(*next, state) {
			return nil, ErrIdentityConflict
		}
		old, err := readWindowReplayRecord(ctx, source, key)
		if err != nil {
			return nil, err
		}
		mutation := storage.CompareAndSwapMutation{Key: key, NewValue: data}
		if old != nil {
			prior, err := strictWindowReplayAttempt(*old)
			if err != nil {
				return nil, err
			}
			if err := verifyWindowReplayTransition(ctx, view, *prior, *next); err != nil {
				return nil, err
			}
			mutation.ExpectedValue = old.Value
		}
		if _, err := verifyBackupCorrectionChain(ctx, view, *next); err != nil {
			return nil, err
		}
		changes[key] = mutation
		view.records[key] = storage.TransferRecord{Key: key, Value: data}
	}
	return sortedWindowReplayChanges(changes), nil
}

// FinalizeWindowReplay publishes a complete original window and its history head.
// Other windows, execution references and independent coverage still need final validation.
// The native import barrier remains after this owner marker disappears.
func FinalizeWindowReplay(ctx context.Context, source BackupReader, state WindowReplayState) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil {
		return nil, ErrInvalid
	}
	original, err := checkWindowReplayBase(ctx, source, state)
	if err != nil {
		return nil, err
	}
	marker, err := windowReplayMarker(state, original)
	if err != nil {
		return nil, err
	}
	retained, err := readWindowReplayRecord(ctx, source, windowReplayKey(state))
	if err != nil || retained == nil {
		return nil, errors.Join(ErrHistoryUnknown, err)
	}
	if !bytes.Equal(marker, retained.Value) {
		return nil, ErrIdentityConflict
	}
	attempts, corrections, digest, err := censusWindowReplay(ctx, source, state, false)
	if err != nil {
		return nil, err
	}
	if attempts != state.Attempts || corrections != state.Corrections || digest != state.SHA256 {
		return nil, ErrHistoryUnknown
	}
	data, err := replayBytes(state.Window)
	if err != nil {
		return nil, err
	}
	key := meterKey(state.Window.Meter, state.Window.Window)
	head, headRaw, err := readWindowReplayHistory(ctx, source, state.Window.Meter)
	if err != nil {
		return nil, err
	}
	if head != nil && (head.History != state.History || head.Current.Start.After(state.Current.Start)) {
		return nil, ErrHistoryUnknown
	}
	nextHead := historyState{Version: recordVersion, Meter: state.Window.Meter, History: state.History, Current: state.Current}
	view := replayView{source: source, records: map[string]storage.TransferRecord{key: {Key: key, Value: data}}}
	headKey := storageKey("history", state.Window.Meter)
	headData := mustReplayHistory(nextHead)
	if err := verifyBackupHistory(ctx, view, storage.TransferRecord{Key: headKey, Value: headData}); err != nil {
		return nil, err
	}
	changes := map[string]storage.CompareAndSwapMutation{
		key: {Key: key, NewValue: data}, headKey: {Key: headKey, NewValue: headData}, retained.Key: {Key: retained.Key, ExpectedValue: retained.Value},
	}
	if original != nil {
		m := changes[key]
		m.ExpectedValue = original.Value
		changes[key] = m
	}
	if headRaw != nil {
		m := changes[headKey]
		m.ExpectedValue = headRaw.Value
		changes[headKey] = m
	}
	return sortedWindowReplayChanges(changes), nil
}

func checkWindowReplayBase(ctx context.Context, source BackupReader, state WindowReplayState) (*storage.TransferRecord, error) {
	if _, err := state.bytes(); err != nil {
		return nil, err
	}
	head, _, err := readWindowReplayHistory(ctx, source, state.Window.Meter)
	if err != nil {
		return nil, err
	}
	if head != nil && (head.History != state.History || head.Current.Start.After(state.Current.Start)) {
		return nil, ErrHistoryUnknown
	}
	original, err := readWindowReplayRecord(ctx, source, meterKey(state.Window.Meter, state.Window.Window))
	if err != nil || original == nil {
		return original, err
	}
	var old WindowState
	if json.Unmarshal(original.Value, &old, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrUnavailable
	}
	if _, err := verifyBackupMeter(*original); err != nil {
		return nil, err
	}
	if old.Meter != state.Window.Meter || old.Window != state.Window.Window || old.HistoryID != state.Window.HistoryID || old.HistoryProof != state.Window.HistoryProof || old.SeedConsumed != state.Window.SeedConsumed {
		return nil, ErrIdentityConflict
	}
	return original, nil
}

func readWindowReplayRecord(ctx context.Context, source BackupReader, key string) (*storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := source.ReadCaptured(ctx, key, maxRecordSize)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > maxRecordSize {
		return nil, ErrUnavailable
	}
	record.Value = bytes.Clone(record.Value)
	return &record, nil
}

func readWindowReplayHistory(ctx context.Context, source BackupReader, meter Meter) (*historyState, *storage.TransferRecord, error) {
	raw, err := readWindowReplayRecord(ctx, source, storageKey("history", meter))
	if err != nil || raw == nil {
		return nil, raw, err
	}
	var head historyState
	if json.Unmarshal(raw.Value, &head, json.RejectUnknownMembers(true)) != nil || head.Version != recordVersion || head.Meter != meter || !validID(head.History.ID) || !validID(head.History.Proof) || head.Current != (storage.TimeWindow{}) && head.Current != windowFor(meter.Interval, head.Current.Start) {
		return nil, nil, ErrUnavailable
	}
	return &head, raw, nil
}

func sortedWindowReplayChanges(changes map[string]storage.CompareAndSwapMutation) []storage.CompareAndSwapMutation {
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	for _, key := range keys {
		result = append(result, changes[key])
	}
	return result
}

func windowReplayMarker(state WindowReplayState, original *storage.TransferRecord) ([]byte, error) {
	if _, err := state.bytes(); err != nil {
		return nil, err
	}
	binding := "absent"
	if original != nil {
		digest := sha256.Sum256(original.Value)
		binding = hex.EncodeToString(digest[:])
	}
	return replayBytes(struct {
		State    WindowReplayState `json:"state"`
		Original string            `json:"original"`
	}{state, binding})
}

func prepareWindowReplayCorrection(ctx context.Context, source BackupReader, state WindowReplayState, view replayView, changes map[string]storage.CompareAndSwapMutation, receipt CorrectionReceipt) error {
	key := correctionKey(receipt.Before.Attempt.ID, receipt.Correction.ID)
	data, err := replayBytes(receipt)
	if err != nil {
		return err
	}
	decoded, err := decodeCorrectionReceipt(receipt.Before.Attempt.ID, receipt.Correction.ID, data)
	if err != nil {
		return err
	}
	if !windowReplayContains(decoded.Before, state) {
		return ErrIdentityConflict
	}
	if _, ok := changes[key]; ok {
		return ErrInvalid
	}
	old, err := readWindowReplayRecord(ctx, source, key)
	if err != nil {
		return err
	}
	mutation := storage.CompareAndSwapMutation{Key: key, NewValue: data}
	if old != nil {
		retained, err := strictWindowReplayCorrection(*old)
		if err != nil {
			return err
		}
		canonical, err := replayBytes(retained)
		if err != nil || !bytes.Equal(canonical, data) {
			return ErrIdentityConflict
		}
		mutation.ExpectedValue = old.Value
		mutation.NewValue = old.Value
	}
	changes[key] = mutation
	view.records[key] = storage.TransferRecord{Key: key, Value: data}
	return nil
}
