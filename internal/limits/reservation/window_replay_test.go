package reservation

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type windowReplaySnapshot struct{ replaySnapshot }

func (s windowReplaySnapshot) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	for _, key := range slices.Sorted(maps.Keys(s.replaySnapshot)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(s.replaySnapshot[key]); err != nil {
			return err
		}
	}
	return nil
}
func windowReplayAfter(t *testing.T, before windowReplaySnapshot, changes []storage.CompareAndSwapMutation) windowReplaySnapshot {
	t.Helper()
	next := windowReplaySnapshot{maps.Clone(before.replaySnapshot)}
	for _, change := range changes {
		require.Equal(t, before.replaySnapshot[change.Key].Value, change.ExpectedValue)
		if change.NewValue == nil {
			delete(next.replaySnapshot, change.Key)
		} else {
			next.replaySnapshot[change.Key] = storage.TransferRecord{Key: change.Key, Value: change.NewValue}
		}
	}
	return next
}
func putWindowReplay(t *testing.T, view windowReplaySnapshot, key string, value any) {
	t.Helper()
	data, err := replayBytes(value)
	require.NoError(t, err)
	view.replaySnapshot[key] = storage.TransferRecord{Key: key, Value: data}
}
func windowReplayFixture(t *testing.T, n int) (WindowReplayState, windowReplaySnapshot, []Record) {
	t.Helper()
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	attempt.Rules = attempt.Rules[:1]
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	record, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	state, _, err := f.repository.readWindow(t.Context(), attempt.Rules[0].Meter, record.Bindings[0].Window)
	require.NoError(t, err)
	view := windowReplaySnapshot{replaySnapshot{}}
	state.SeedConsumed = 11
	state.Consumed = 11
	state.Reserved = int64(n) * record.Bindings[0].Amount
	putWindowReplay(t, view, meterKey(state.Meter, state.Window), state)
	putWindowReplay(t, view, storageKey("history", state.Meter), historyState{Version: recordVersion, Meter: state.Meter, History: History{ID: state.HistoryID, Proof: state.HistoryProof}, Current: state.Window})
	records := make([]Record, n)
	for i := range records {
		records[i] = *record
		records[i].Attempt = record.Attempt
		records[i].Attempt.ID = fmt.Sprintf("reconstruction-%04d", i)
		records[i].State = Uncertain
		records[i].Reason = "lost response"
		putWindowReplay(t, view, storageKey("attempt", records[i].Attempt.ID), records[i])
	}
	manifest, err := CaptureWindowReplayState(t.Context(), view, state.Meter, state.Window)
	require.NoError(t, err)
	return manifest, view, records
}
func openWindowReplayTarget(t *testing.T, backend string, before windowReplaySnapshot) (fixture, storage.ImportReconciler, []byte) {
	t.Helper()
	target := openFixture(t, backend)
	identity := ""
	var err error
	if p, ok := target.raw.(storage.IncarnationProvider); ok {
		identity, err = p.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), target.raw, identity)
	require.NoError(t, err)
	claim := []byte("window-reconstruction")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	for _, r := range before.replaySnapshot {
		require.NoError(t, transfer.Import(t.Context(), claim, r))
	}
	return target, transfer.(storage.ImportReconciler), claim
}

func TestWindowReplayStagesCompleteHistory(t *testing.T) {
	manifest, _, records := windowReplayFixture(t, 147)
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			before := windowReplaySnapshot{replaySnapshot{}}
			target, importer, claim := openWindowReplayTarget(t, backend, before)
			var first []storage.CompareAndSwapMutation
			firstReceipt, previous := "", ""
			sequence := int64(1)
			for offset := 0; offset < len(records); offset += 32 {
				chunk := records[offset:min(offset+32, len(records))]
				changes, err := PrepareWindowReplay(t.Context(), before, manifest, chunk, nil)
				require.NoError(t, err)
				reversed := slices.Clone(chunk)
				slices.Reverse(reversed)
				retry, err := PrepareWindowReplay(t.Context(), before, manifest, reversed, nil)
				require.NoError(t, err)
				require.Equal(t, changes, retry)
				receipt, err := importer.ReconcileImport(t.Context(), claim, sequence, previous, strings.Repeat("a", 64), changes)
				require.NoError(t, err)
				if first == nil {
					first, firstReceipt = changes, receipt
				}
				before = windowReplayAfter(t, before, changes)
				previous = receipt
				sequence++
				_, err = target.repository.Window(t.Context(), manifest.Window.Meter, manifest.Window.Window.Start)
				require.ErrorIs(t, err, ErrHistoryUnknown)
				if offset == 0 {
					_, err = FinalizeWindowReplay(t.Context(), before, manifest)
					require.Error(t, err)
				}
			}
			final, err := FinalizeWindowReplay(t.Context(), before, manifest)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, sequence, previous, strings.Repeat("b", 64), final)
			require.NoError(t, err)
			receipt, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), first)
			require.NoError(t, err)
			require.Equal(t, firstReceipt, receipt)
			restored, err := target.repository.Window(t.Context(), manifest.Window.Meter, manifest.Window.Window.Start)
			require.NoError(t, err)
			require.Equal(t, manifest.Window, *restored)
			require.EqualValues(t, 88200, restored.Reserved)
			require.EqualValues(t, 11, restored.SeedConsumed)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.raw), storage.ErrImportRestricted)
			view := windowReplayAfter(t, before, final)
			actual, err := CaptureWindowReplayState(t.Context(), view, manifest.Window.Meter, manifest.Window.Window)
			require.NoError(t, err)
			require.Equal(t, manifest, actual)
		})
	}
}

func TestWindowReplayExplicitEmptyHistory(t *testing.T) {
	manifest, independent, _ := windowReplayFixture(t, 0)
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			empty := windowReplaySnapshot{replaySnapshot{}}
			target, importer, claim := openWindowReplayTarget(t, backend, empty)
			_, err := CaptureWindowReplayState(t.Context(), empty, manifest.Window.Meter, manifest.Window.Window)
			require.ErrorIs(t, err, ErrHistoryUnknown)
			stage, err := PrepareWindowReplay(t.Context(), empty, manifest, nil, nil)
			require.NoError(t, err)
			receipt, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), stage)
			require.NoError(t, err)
			staged := windowReplayAfter(t, empty, stage)
			final, err := FinalizeWindowReplay(t.Context(), staged, manifest)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), final)
			require.NoError(t, err)
			actual, err := target.repository.Window(t.Context(), manifest.Window.Meter, manifest.Window.Window.Start)
			require.NoError(t, err)
			require.EqualValues(t, 11, actual.Consumed)
			raw, err := target.raw.Get(t.Context(), meterKey(actual.Meter, actual.Window))
			require.NoError(t, err)
			require.Equal(t, independent.replaySnapshot[meterKey(actual.Meter, actual.Window)].Value, raw)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.raw), storage.ErrImportRestricted)
		})
	}
}

func TestWindowReplayRefusesIncompleteOrChangedState(t *testing.T) {
	manifest, independent, records := windowReplayFixture(t, 2)
	empty := windowReplaySnapshot{replaySnapshot{}}
	changes, err := PrepareWindowReplay(t.Context(), empty, manifest, records, nil)
	require.NoError(t, err)
	staged := windowReplayAfter(t, empty, changes)
	for _, mode := range []string{"missing-attempt", "extra-attempt", "identity-change", "marker-missing", "marker-expiring", "manifest-conflict", "new-window", "history-conflict", "history-regression", "unknown-field", "expiring-attempt"} {
		t.Run(mode, func(t *testing.T) {
			view := windowReplaySnapshot{maps.Clone(staged.replaySnapshot)}
			state := manifest
			switch mode {
			case "missing-attempt":
				delete(view.replaySnapshot, storageKey("attempt", records[0].Attempt.ID))
			case "extra-attempt":
				extra := records[0]
				extra.Attempt.ID = "extra"
				putWindowReplay(t, view, storageKey("attempt", extra.Attempt.ID), extra)
			case "identity-change":
				changed := records[0]
				changed.Attempt.RequestID = "other"
				putWindowReplay(t, view, storageKey("attempt", changed.Attempt.ID), changed)
			case "marker-missing":
				delete(view.replaySnapshot, windowReplayKey(state))
			case "marker-expiring":
				r := view.replaySnapshot[windowReplayKey(state)]
				r.ExpiresAtMillis = 1
				view.replaySnapshot[r.Key] = r
			case "manifest-conflict":
				state.Attempts++
			case "new-window":
				putWindowReplay(t, view, meterKey(state.Window.Meter, state.Window.Window), state.Window)
			case "history-conflict", "history-regression":
				head := historyState{Version: recordVersion, Meter: state.Window.Meter, History: state.History, Current: state.Current}
				if mode == "history-conflict" {
					head.History.ID = "other"
				} else {
					head.Current = windowFor(head.Meter.Interval, head.Current.End)
				}
				putWindowReplay(t, view, storageKey("history", head.Meter), head)
			case "unknown-field":
				key := storageKey("attempt", records[0].Attempt.ID)
				r := view.replaySnapshot[key]
				r.Value = append(slices.Clone(r.Value[:len(r.Value)-1]), []byte(`,"unknown":true}`)...)
				view.replaySnapshot[key] = r
			case "expiring-attempt":
				key := storageKey("attempt", records[0].Attempt.ID)
				r := view.replaySnapshot[key]
				r.ExpiresAtMillis = 1
				view.replaySnapshot[key] = r
			}
			_, err := FinalizeWindowReplay(t.Context(), view, state)
			require.Error(t, err)
		})
	}
	for _, mode := range []string{"missing-history", "missing-window", "wrong-totals", "no-enumeration"} {
		t.Run("capture-"+mode, func(t *testing.T) {
			view := windowReplaySnapshot{maps.Clone(independent.replaySnapshot)}
			var reader BackupReader = view
			switch mode {
			case "missing-history":
				delete(view.replaySnapshot, storageKey("history", manifest.Window.Meter))
			case "missing-window":
				delete(view.replaySnapshot, meterKey(manifest.Window.Meter, manifest.Window.Window))
			case "wrong-totals":
				state := manifest.Window
				state.Reserved--
				putWindowReplay(t, view, meterKey(state.Meter, state.Window), state)
			case "no-enumeration":
				reader = view.replaySnapshot
			}
			_, err := CaptureWindowReplayState(t.Context(), reader, manifest.Window.Meter, manifest.Window.Window)
			require.Error(t, err)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PrepareWindowReplay(ctx, empty, manifest, records, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = PrepareWindowReplay(t.Context(), empty, manifest, make([]Record, 33), nil)
	require.Error(t, err)
	_, err = PrepareWindowReplay(t.Context(), empty, WindowReplayState{}, nil, nil)
	require.Error(t, err)
	changed := records[0]
	changed.State = Reserved
	_, err = PrepareWindowReplay(t.Context(), staged, manifest, []Record{changed}, nil)
	require.ErrorIs(t, err, ErrTransition)
}

func TestWindowReplayOverflowRequiresCompleteTotals(t *testing.T) {
	manifest, independent, records := windowReplayFixture(t, 1)
	// A stale saturated aggregate can be replaced only from the complete owner census.
	before := windowReplaySnapshot{maps.Clone(independent.replaySnapshot)}
	old := manifest.Window
	old.Consumed = math.MaxInt64
	old.Overflow = true
	putWindowReplay(t, before, meterKey(old.Meter, old.Window), old)
	stage, err := PrepareWindowReplay(t.Context(), before, manifest, records, nil)
	require.NoError(t, err)
	staged := windowReplayAfter(t, before, stage)
	final, err := FinalizeWindowReplay(t.Context(), staged, manifest)
	require.NoError(t, err)
	view := windowReplayAfter(t, staged, final)
	got, err := CaptureWindowReplayState(t.Context(), view, old.Meter, old.Window)
	require.NoError(t, err)
	require.False(t, got.Window.Overflow)
	require.Equal(t, manifest, got)
	// Complete history that still exceeds the integer range retains saturation.
	settled := records[0]
	settled.State = Settled
	settled.SettledAt = settled.AdmittedAt
	settled.Evidence = &Evidence{ID: "confirmed", Quantities: Quantities{"output": math.MaxInt64}}
	amount := int64(math.MaxInt64)
	settled.NanoUSD = &amount
	putWindowReplay(t, independent, storageKey("attempt", settled.Attempt.ID), settled)
	state := manifest.Window
	state.Reserved = 0
	state.Consumed = math.MaxInt64
	state.Overflow = true
	putWindowReplay(t, independent, meterKey(state.Meter, state.Window), state)
	full, err := CaptureWindowReplayState(t.Context(), independent, state.Meter, state.Window)
	require.NoError(t, err)
	require.True(t, full.Window.Overflow)
	empty := windowReplaySnapshot{replaySnapshot{}}
	changes, err := PrepareWindowReplay(t.Context(), empty, full, []Record{settled}, nil)
	require.NoError(t, err)
	_, err = FinalizeWindowReplay(t.Context(), windowReplayAfter(t, empty, changes), full)
	require.NoError(t, err)
	state.Overflow = false
	putWindowReplay(t, independent, meterKey(state.Meter, state.Window), state)
	_, err = CaptureWindowReplayState(t.Context(), independent, state.Meter, state.Window)
	require.Error(t, err)
}
