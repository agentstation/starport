package reservation

import (
	"context"
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type replaySnapshot map[string]storage.TransferRecord

func (s replaySnapshot) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	r, ok := s[key]
	if !ok {
		return r, storage.ErrNotFound
	}
	if len(r.Value) > bound {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return r, nil
}

func captureReplayFixture(t *testing.T, f fixture) replaySnapshot {
	t.Helper()
	identity := ""
	if p, ok := f.raw.(storage.IncarnationProvider); ok {
		var err error
		identity, err = p.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), f.raw, identity)
	require.NoError(t, err)
	result := replaySnapshot{}
	require.NoError(t, transfer.Enumerate(t.Context(), func(r storage.TransferRecord) error { result[r.Key] = r; return nil }))
	return result
}

func TestAccountingReplayPreservesOriginalWindows(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			before := captureReplayFixture(t, f)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "provider-receipt", Quantities: Quantities{"output": 40}, Tokens: 17}))
			after, _, err := f.repository.readRecord(t.Context(), attempt.ID)
			require.NoError(t, err)
			mutations, err := PrepareAccountingReplay(t.Context(), before, []Record{*after}, nil)
			require.NoError(t, err)
			require.Len(t, mutations, 7)
			target := openFixture(t, backend)
			identity := ""
			if p, ok := target.raw.(storage.IncarnationProvider); ok {
				identity, err = p.ObserveIncarnation(t.Context())
				require.NoError(t, err)
			}
			transfer, err := storage.OpenRecordTransfer(t.Context(), target.raw, identity)
			require.NoError(t, err)
			claim := []byte("accounting-replay")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			for _, r := range before {
				require.NoError(t, transfer.Import(t.Context(), claim, r))
			}
			receipt, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), mutations)
			require.NoError(t, err)
			repeated, err := PrepareAccountingReplay(t.Context(), before, []Record{*after}, nil)
			require.NoError(t, err)
			require.Equal(t, mutations, repeated)
			again, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), repeated)
			require.NoError(t, err)
			require.Equal(t, receipt, again)
			actual, _, err := target.repository.readRecord(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, *after, *actual)
			for _, binding := range after.Bindings {
				state, _, err := target.repository.readWindow(t.Context(), binding.Rule.Meter, binding.Window)
				require.NoError(t, err)
				want := int64(40)
				if binding.Rule.Meter.Dimension == limits.DimensionTokens {
					want = 17
				}
				require.Equal(t, want, state.Consumed)
				require.Zero(t, state.Reserved)
				require.Zero(t, state.SeedConsumed)
			}
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.raw), storage.ErrImportRestricted)
		})
	}
}

func replayAfter(t *testing.T, source replaySnapshot, mutations []storage.CompareAndSwapMutation) replaySnapshot {
	t.Helper()
	after := maps.Clone(source)
	for _, change := range mutations {
		require.Equal(t, source[change.Key].Value, change.ExpectedValue)
		after[change.Key] = storage.TransferRecord{Key: change.Key, Value: change.NewValue}
	}
	return after
}

func TestAccountingReplayRetainsSeedAndOtherAttempts(t *testing.T) {
	f := openFixture(t, "badger")
	first := attemptFixture()
	f.provision(t, &first)
	first.Bound, first.TokenBound = Quantities{"output": 200}, 200
	_, err := f.repository.Reserve(t.Context(), first)
	require.NoError(t, err)
	second := first
	second.ID += "-other"
	_, err = f.repository.Reserve(t.Context(), second)
	require.NoError(t, err)
	before := captureReplayFixture(t, f)
	// Imported seed consumption has no local attempt record to subtract.
	for key, raw := range before {
		if !strings.HasPrefix(key, StoragePrefix+"meter:") {
			continue
		}
		var window WindowState
		require.NoError(t, json.Unmarshal(raw.Value, &window))
		window.SeedConsumed, window.Consumed = 11, 11
		raw.Value, err = replayBytes(window)
		require.NoError(t, err)
		before[key] = raw
	}
	require.NoError(t, f.repository.Begin(t.Context(), first.ID))
	require.NoError(t, f.repository.Reconcile(t.Context(), first.ID, Evidence{ID: "settled", Quantities: Quantities{"output": 40}, Tokens: 17}))
	later, _, err := f.repository.readRecord(t.Context(), first.ID)
	require.NoError(t, err)
	changes, err := PrepareAccountingReplay(t.Context(), before, []Record{*later}, nil)
	require.NoError(t, err)
	view := replayAfter(t, before, changes)
	for _, binding := range later.Bindings {
		raw := view[meterKey(binding.Rule.Meter, binding.Window)]
		state, err := decodeWindowRecord(binding.Rule.Meter, binding.Window, raw.Value)
		require.NoError(t, err)
		want := int64(51)
		if binding.Rule.Meter.Dimension == limits.DimensionTokens {
			want = 28
		}
		require.Equal(t, want, state.Consumed)
		require.EqualValues(t, 200, state.Reserved)
		require.EqualValues(t, 11, state.SeedConsumed)
	}
	// A later uncertain attempt absent from the backup adds capacity, never permission.
	third := first
	third.ID += "-new"
	_, err = f.repository.Reserve(t.Context(), third)
	require.NoError(t, err)
	require.NoError(t, f.repository.Begin(t.Context(), third.ID))
	require.NoError(t, f.repository.MarkUncertain(t.Context(), third.ID, "lost-provider-response"))
	uncertain, _, err := f.repository.readRecord(t.Context(), third.ID)
	require.NoError(t, err)
	changes, err = PrepareAccountingReplay(t.Context(), before, []Record{*later, *uncertain}, nil)
	require.NoError(t, err)
	view = replayAfter(t, before, changes)
	for _, binding := range later.Bindings {
		state, err := decodeWindowRecord(binding.Rule.Meter, binding.Window, view[meterKey(binding.Rule.Meter, binding.Window)].Value)
		require.NoError(t, err)
		require.EqualValues(t, 400, state.Reserved)
	}
}

func TestAccountingReplayPreservesCorrectionHistory(t *testing.T) {
	f := openFixture(t, "badger")
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, time.December, 31, 23, 59, 0, 0, time.UTC)}
	var err error
	f.repository, err = Open(clock)
	require.NoError(t, err)
	f.store, f.now = clock, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", Quantities: Quantities{"output": 40}, Tokens: 17})
	require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late-charge"))
	before := captureReplayFixture(t, f)
	original, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	clock.now = clock.now.Add(2 * time.Hour)
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Quantities: Quantities{"output": 80}, Tokens: 90})
	receipt, err := f.repository.Correct(t.Context(), attempt.ID, correction)
	require.NoError(t, err)
	later, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	changes, err := PrepareAccountingReplay(t.Context(), before, []Record{*later}, []CorrectionReceipt{*receipt})
	require.NoError(t, err)
	view := replayAfter(t, before, changes)
	checked, err := VerifyBackupRecord(t.Context(), view, view[storageKey("attempt", attempt.ID)])
	require.NoError(t, err)
	require.EqualValues(t, 1, checked.Corrections)
	require.True(t, checked.Attempt.SettledAt.Equal(original.SettledAt))
	for _, binding := range later.Bindings {
		state, err := decodeWindowRecord(binding.Rule.Meter, binding.Window, view[meterKey(binding.Rule.Meter, binding.Window)].Value)
		require.NoError(t, err)
		require.Zero(t, state.ActiveDisputes)
		want := int64(80)
		if binding.Rule.Meter.Dimension == limits.DimensionTokens {
			want = 90
		}
		require.Equal(t, want, state.Consumed)
		require.Equal(t, binding.Window, state.Window)
	}
	_, err = PrepareAccountingReplay(t.Context(), before, []Record{*later}, nil)
	require.Error(t, err, "a correction head requires its immutable receipt")
	reopened := *later
	reopened.SettledAt = clock.now
	_, err = PrepareAccountingReplay(t.Context(), before, []Record{reopened}, []CorrectionReceipt{*receipt})
	require.Error(t, err, "recovery cannot restart the correction horizon")
}

func TestAccountingReplayRefusesUnsafeTransitions(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, f.repository.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
	before := captureReplayFixture(t, f)
	original, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	for _, state := range []State{Reserved, Dispatched, Canceled} {
		t.Run(string(state), func(t *testing.T) {
			after := *original
			after.State = state
			_, err := PrepareAccountingReplay(t.Context(), before, []Record{after}, nil)
			require.Error(t, err)
		})
	}
	t.Run("pinned-valuation", func(t *testing.T) {
		after := *original
		after.Attempt.CatalogGeneration = "new-generation"
		_, err := PrepareAccountingReplay(t.Context(), before, []Record{after}, nil)
		require.ErrorIs(t, err, ErrIdentityConflict)
	})
	t.Run("missing-window", func(t *testing.T) {
		view := maps.Clone(before)
		delete(view, meterKey(original.Bindings[0].Rule.Meter, original.Bindings[0].Window))
		_, err := PrepareAccountingReplay(t.Context(), view, []Record{*original}, nil)
		require.Error(t, err)
	})
	t.Run("duplicate", func(t *testing.T) {
		_, err := PrepareAccountingReplay(t.Context(), before, []Record{*original, *original}, nil)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("pending-evidence", func(t *testing.T) {
		prior := *original
		prior.Pending = &Evidence{ID: "known", Quantities: Quantities{"output": 40}, Tokens: 17}
		after := prior
		after.Pending = nil
		require.ErrorIs(t, verifyReplayProgress(prior, after), ErrTransition)
		after.State = Settled
		after.Evidence = &Evidence{ID: "different", NoCharge: true}
		require.ErrorIs(t, verifyReplayProgress(prior, after), ErrTransition)
	})
	t.Run("overflow", func(t *testing.T) {
		window := replayWindow{state: &WindowState{Consumed: 100, Overflow: true}}
		require.ErrorIs(t, applyReplayTotals(&window), ErrUnavailable)
	})
}

func TestAccountingReplayRefusesIncompleteCapturedState(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	before := captureReplayFixture(t, f)
	later, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	for _, mode := range []string{"expired-window", "wrong-window", "under-counted-reservation", "changed-history"} {
		t.Run(mode, func(t *testing.T) {
			view := maps.Clone(before)
			binding := later.Bindings[0]
			key := meterKey(binding.Rule.Meter, binding.Window)
			raw := view[key]
			var state WindowState
			require.NoError(t, json.Unmarshal(raw.Value, &state))
			switch mode {
			case "expired-window":
				raw.ExpiresAtMillis = 1
			case "wrong-window":
				raw.Key += "-other"
			case "under-counted-reservation":
				state.Reserved = 0
			case "changed-history":
				state.HistoryID = "other-history"
			}
			raw.Value, err = replayBytes(state)
			require.NoError(t, err)
			view[key] = raw
			changes, err := PrepareAccountingReplay(t.Context(), view, []Record{*later}, nil)
			require.Error(t, err)
			require.Nil(t, changes)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PrepareAccountingReplay(ctx, before, []Record{*later}, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = PrepareAccountingReplay(t.Context(), before, make([]Record, 17), nil)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = PrepareAccountingReplay(t.Context(), before, []Record{*later}, make([]CorrectionReceipt, 65))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestAccountingReplayNormalizesTokenOnlyCollections(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	var rules []Rule
	for _, rule := range attempt.Rules {
		if rule.Meter.Dimension == limits.DimensionTokens {
			rules = append(rules, rule)
		}
	}
	attempt.Rules, attempt.Valuation, attempt.Bound, attempt.TokenOnly = rules, Valuation{}, nil, true
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	before := captureReplayFixture(t, f)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "measured", Tokens: 17}))
	later, _, err := f.repository.readRecord(t.Context(), attempt.ID)
	require.NoError(t, err)
	canonical, err := PrepareAccountingReplay(t.Context(), before, []Record{*later}, nil)
	require.NoError(t, err)
	later.Attempt.Valuation.Components = nil
	later.Attempt.Bound = nil
	later.Evidence.Quantities = nil
	withoutEmpty, err := PrepareAccountingReplay(t.Context(), before, []Record{*later}, nil)
	require.NoError(t, err)
	require.Equal(t, canonical, withoutEmpty)
}
