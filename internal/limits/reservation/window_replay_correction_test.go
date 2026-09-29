package reservation

import (
	"maps"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestWindowReplayNativeCorrectionReconstructsSaturation(t *testing.T) {
	f := openFixture(t, "badger")
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	var err error
	f.repository, err = Open(clock)
	require.NoError(t, err)
	f.store, f.now = clock, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	attempt.Rules = attempt.Rules[:1]
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "first", Quantities: Quantities{"output": 40}})
	first, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	before := windowReplaySnapshot{captureReplayFixture(t, f)}
	// Later independent evidence corrects the charge. Its original settlement time remains fixed.
	clock.now = clock.now.Add(time.Hour)
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Quantities: Quantities{"output": 80}})
	receipt, err := f.repository.Correct(t.Context(), attempt.ID, correction)
	require.NoError(t, err)
	after, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	independent := windowReplaySnapshot{captureReplayFixture(t, f)}
	binding := first.Bindings[0]
	manifest, err := CaptureWindowReplayState(t.Context(), independent, binding.Rule.Meter, binding.Window)
	require.NoError(t, err)
	require.EqualValues(t, 1, manifest.Corrections)
	old := manifest.Window
	old.Consumed = math.MaxInt64
	old.Overflow = true
	putWindowReplay(t, before, meterKey(old.Meter, old.Window), old)
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			view := windowReplaySnapshot{maps.Clone(before.replaySnapshot)}
			target, importer, claim := openWindowReplayTarget(t, backend, view)
			_, err := PrepareAccountingReplay(t.Context(), view, []Record{*after}, []CorrectionReceipt{*receipt})
			require.Error(t, err, "incremental subtraction cannot repair saturation")
			stage, err := PrepareWindowReplay(t.Context(), view, manifest, []Record{*after}, []CorrectionReceipt{*receipt})
			require.NoError(t, err)
			firstReceipt, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), stage)
			require.NoError(t, err)
			view = windowReplayAfter(t, view, stage)
			retained, err := target.repository.Window(t.Context(), old.Meter, old.Window.Start)
			require.NoError(t, err)
			require.True(t, retained.Overflow)
			final, err := FinalizeWindowReplay(t.Context(), view, manifest)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, 2, firstReceipt, strings.Repeat("b", 64), final)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), stage)
			require.NoError(t, err)
			restored, _, err := target.repository.readRecord(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, first.SettledAt, restored.SettledAt)
			require.Equal(t, after.CorrectionID, restored.CorrectionID)
			total, err := target.repository.Window(t.Context(), old.Meter, old.Window.Start)
			require.NoError(t, err)
			require.EqualValues(t, 80, total.Consumed)
			require.False(t, total.Overflow)
			retainedReceipt, err := target.repository.InspectCorrection(t.Context(), attempt.ID, receipt.Correction.ID)
			require.NoError(t, err)
			require.Equal(t, *receipt, *retainedReceipt)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.raw), storage.ErrImportRestricted)
		})
	}
	for _, mode := range []string{"missing-receipt", "reset-first-settlement", "change-pricing", "change-seed", "orphan-receipt", "late-correction"} {
		t.Run(mode, func(t *testing.T) {
			view := windowReplaySnapshot{maps.Clone(before.replaySnapshot)}
			record := *after
			receipts := []CorrectionReceipt{*receipt}
			state := manifest
			switch mode {
			case "missing-receipt":
				receipts = nil
			case "reset-first-settlement":
				record.SettledAt = clock.now
			case "change-pricing":
				record.Attempt.OfferingID = "other"
			case "change-seed":
				state.Window.SeedConsumed++
			case "orphan-receipt":
				record = *first
			case "late-correction":
				receipts[0].RecordedAt = first.SettledAt.Add(91 * 24 * time.Hour)
			}
			changes, err := PrepareWindowReplay(t.Context(), view, state, []Record{record}, receipts)
			if err == nil {
				_, err = FinalizeWindowReplay(t.Context(), windowReplayAfter(t, view, changes), state)
			}
			require.Error(t, err)
		})
	}
}

func TestWindowReplayKeepsNewerHistoryHead(t *testing.T) {
	manifest, independent, records := windowReplayFixture(t, 1)
	newer := manifest.Window
	newer.Window = windowFor(newer.Meter.Interval, newer.Window.End)
	newer.SeedConsumed = 0
	newer.Consumed = 0
	newer.Reserved = 0
	putWindowReplay(t, independent, meterKey(newer.Meter, newer.Window), newer)
	putWindowReplay(t, independent, storageKey("history", newer.Meter), historyState{Version: recordVersion, Meter: newer.Meter, History: manifest.History, Current: newer.Window})
	older, err := CaptureWindowReplayState(t.Context(), independent, manifest.Window.Meter, manifest.Window.Window)
	require.NoError(t, err)
	before := windowReplaySnapshot{replaySnapshot{}}
	stage, err := PrepareWindowReplay(t.Context(), before, older, records, nil)
	require.NoError(t, err)
	staged := windowReplayAfter(t, before, stage)
	_, err = FinalizeWindowReplay(t.Context(), staged, older)
	require.Error(t, err, "head cannot point to a missing newer window")
	current, err := CaptureWindowReplayState(t.Context(), independent, newer.Meter, newer.Window)
	require.NoError(t, err)
	next, err := PrepareWindowReplay(t.Context(), staged, current, nil, nil)
	require.NoError(t, err)
	staged = windowReplayAfter(t, staged, next)
	final, err := FinalizeWindowReplay(t.Context(), staged, current)
	require.NoError(t, err)
	staged = windowReplayAfter(t, staged, final)
	final, err = FinalizeWindowReplay(t.Context(), staged, older)
	require.NoError(t, err)
	staged = windowReplayAfter(t, staged, final)
	head, _, err := readWindowReplayHistory(t.Context(), staged, newer.Meter)
	require.NoError(t, err)
	require.Equal(t, newer.Window, head.Current)
}

func TestWindowReplayPreservesUnresolvedOverflow(t *testing.T) {
	manifest, source, records := windowReplayFixture(t, 1)
	record := records[0]
	record.Attempt.Valuation.Components = slices.Clone(record.Attempt.Valuation.Components)
	record.Attempt.Valuation.Components[0].Price.USD = "0.000000002"
	record.Bindings = slices.Clone(record.Bindings)
	record.Bindings[0].Amount = 1200
	amount := int64(1200)
	record.NanoUSD = &amount
	record.Unresolved = &Evidence{ID: "overflowed-provider-usage", Quantities: Quantities{"output": math.MaxInt64}}
	putWindowReplay(t, source, storageKey("attempt", record.Attempt.ID), record)
	state := manifest.Window
	state.Reserved = 1200
	state.Overflow = true
	putWindowReplay(t, source, meterKey(state.Meter, state.Window), state)
	full, err := CaptureWindowReplayState(t.Context(), source, state.Meter, state.Window)
	require.NoError(t, err)
	empty := windowReplaySnapshot{replaySnapshot{}}
	changes, err := PrepareWindowReplay(t.Context(), empty, full, []Record{record}, nil)
	require.NoError(t, err)
	staged := windowReplayAfter(t, empty, changes)
	final, err := FinalizeWindowReplay(t.Context(), staged, full)
	require.NoError(t, err)
	completed := windowReplayAfter(t, staged, final)
	actual, err := CaptureWindowReplayState(t.Context(), completed, state.Meter, state.Window)
	require.NoError(t, err)
	require.True(t, actual.Window.Overflow)
	require.EqualValues(t, 1200, actual.Window.Reserved)
}

func TestWindowReplayRefusesReservedOverflowAndChangedBase(t *testing.T) {
	manifest, source, records := windowReplayFixture(t, 2)
	for i := range records {
		records[i].Attempt.Bound = Quantities{"output": math.MaxInt64}
		records[i].Bindings = slices.Clone(records[i].Bindings)
		records[i].Bindings[0].Amount = math.MaxInt64
		amount := int64(math.MaxInt64)
		records[i].NanoUSD = &amount
		putWindowReplay(t, source, storageKey("attempt", records[i].Attempt.ID), records[i])
	}
	state := manifest.Window
	state.Reserved = math.MaxInt64
	putWindowReplay(t, source, meterKey(state.Meter, state.Window), state)
	_, err := CaptureWindowReplayState(t.Context(), source, state.Meter, state.Window)
	require.Error(t, err)
	manifest, source, records = windowReplayFixture(t, 1)
	stage, err := PrepareWindowReplay(t.Context(), source, manifest, records, nil)
	require.NoError(t, err)
	staged := windowReplayAfter(t, source, stage)
	changed := manifest.Window
	changed.Consumed++
	putWindowReplay(t, staged, meterKey(changed.Meter, changed.Window), changed)
	_, err = FinalizeWindowReplay(t.Context(), staged, manifest)
	require.ErrorIs(t, err, ErrIdentityConflict)
	_, err = PrepareWindowReplay(t.Context(), staged, manifest, records, nil)
	require.ErrorIs(t, err, ErrIdentityConflict)
}
