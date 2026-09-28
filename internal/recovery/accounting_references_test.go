package recovery

import (
	"context"
	"encoding/json/v2"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupRejectsAccountingMismatch(t *testing.T) {
	for _, mode := range []string{"reserved", "consumed", "disputes", "missing-attempt", "correction-head", "orphan-correction"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
			require.NoError(t, err)
			meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
			require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 17, reservation.History{ID: "history", Proof: "fixture-history"}))
			attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}}
			_, err = budgets.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
			if mode == "correction-head" || mode == "orphan-correction" {
				prior, err := budgets.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				_, err = budgets.Correct(t.Context(), attempt.ID, reservation.Correction{ID: "repair", ExpectedBinding: reservation.CorrectionBinding(*prior), Actor: "operator", EvidenceReference: "provider/usage", Reason: "provider-confirmed", Evidence: reservation.Evidence{ID: "usage", Quantities: reservation.Quantities{"output": 20}}})
				require.NoError(t, err)
			}
			prefix := "budget:v1:meter:"
			if mode == "missing-attempt" || mode == "correction-head" || mode == "orphan-correction" {
				prefix = "budget:v1:attempt:"
			}
			keys, err := kv.ScanWithPrefix(t.Context(), prefix, 0)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			data, err := kv.Get(t.Context(), keys[0])
			require.NoError(t, err)
			if mode == "missing-attempt" {
				require.NoError(t, kv.Delete(t.Context(), keys[0]))
			} else {
				var fields map[string]any
				require.NoError(t, json.Unmarshal(data, &fields))
				switch mode {
				case "reserved":
					fields["reserved"] = 99
				case "consumed":
					fields["consumed"] = 18
				case "disputes":
					fields["active_disputes"], fields["reconciliation_required"] = 1, true
				case "correction-head":
					fields["reason"] = "changed-after-correction"
				case "orphan-correction":
					delete(fields, "correction_id")
				}
				data, err = json.Marshal(fields)
				require.NoError(t, err)
				require.NoError(t, kv.Set(t.Context(), keys[0], data))
			}
			_, err = inspectReferenceFixture(t, source, request, destination)
			require.Error(t, err, "a valid artifact digest cannot establish accounting consistency")
		})
	}
}

func TestBackupAccountingPreservesAllTransitions(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
	source.KV = transfer
	budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
	require.NoError(t, err)
	attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", TeamID: "team", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}, TokenBound: 40}
	for _, holder := range []struct {
		scope limits.Scope
		id    string
	}{{limits.ScopeAccount, "owner"}, {limits.ScopeKey, "key"}, {limits.ScopeTeam, "team"}} {
		for _, dimension := range []limits.Dimension{limits.DimensionSpend, limits.DimensionTokens} {
			meter := reservation.Meter{Scope: holder.scope, Holder: holder.id, Dimension: dimension, Interval: limits.IntervalDay}
			require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 17, reservation.History{ID: "history", Proof: "fixture-history"}))
			attempt.Rules = append(attempt.Rules, reservation.Rule{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"})
		}
	}
	inspect := func(phase string, held int64) {
		t.Helper()
		before, err := budgets.Inspect(t.Context(), attempt.ID)
		require.NoError(t, err)
		report, err := inspectReferenceFixture(t, source, request, destination+"-"+phase)
		require.NoError(t, err)
		require.EqualValues(t, 6, report.BudgetWindows)
		require.Equal(t, held, report.HeldReservations)
		after, err := budgets.Inspect(t.Context(), attempt.ID)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
	_, err = budgets.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	inspect("reserved", 1)
	canceled := attempt
	canceled.ID = "canceled"
	_, err = budgets.Reserve(t.Context(), canceled)
	require.NoError(t, err)
	require.NoError(t, budgets.CancelBeforeDispatch(t.Context(), canceled.ID))
	require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
	inspect("dispatched", 1)
	require.NoError(t, budgets.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
	inspect("uncertain", 1)
	require.NoError(t, budgets.Reconcile(t.Context(), attempt.ID, reservation.Evidence{ID: "measured", Quantities: reservation.Quantities{"output": 20}, Tokens: 8}))
	inspect("settled", 0)
	require.NoError(t, budgets.FlagDispute(t.Context(), attempt.ID, "provider-revised"))
	inspect("disputed", 0)
	for _, id := range []string{"first", "second"} {
		before, err := budgets.Inspect(t.Context(), attempt.ID)
		require.NoError(t, err)
		_, err = budgets.Correct(t.Context(), attempt.ID, reservation.Correction{ID: id, ExpectedBinding: reservation.CorrectionBinding(*before), Actor: "operator", EvidenceReference: "provider/statement", Reason: "revised-usage", Evidence: reservation.Evidence{ID: id, Quantities: reservation.Quantities{"output": 25}, Tokens: 12}})
		require.NoError(t, err)
		inspect(id, 0)
	}
	// Absolute authority time can move backward inside the permitted correction interval.
	// The immutable state bindings, not timestamp order, establish correction ancestry.
	keys, err := kv.ScanWithPrefix(t.Context(), "budget:v1:correction:", 0)
	require.NoError(t, err)
	for _, key := range keys {
		data, err := kv.Get(t.Context(), key)
		require.NoError(t, err)
		var receipt reservation.CorrectionReceipt
		require.NoError(t, json.Unmarshal(data, &receipt))
		if receipt.Correction.ID != "second" {
			continue
		}
		receipt.RecordedAt = receipt.Before.SettledAt
		data, err = json.Marshal(receipt)
		require.NoError(t, err)
		require.NoError(t, kv.Set(t.Context(), key, data))
	}
	inspect("authority-clock-backward", 0)
}

func TestBackupAccountingPreservesOverflow(t *testing.T) {
	for _, mode := range []string{"aggregate", "valuation"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
			require.NoError(t, err)
			meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
			seed := int64(0)
			price := "0.000000002"
			if mode == "aggregate" {
				seed, price = math.MaxInt64-5, "0.000000001"
			}
			require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), seed, reservation.History{ID: "history", Proof: "fixture-history"}))
			attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Rules: []reservation.Rule{{Meter: meter, Limit: math.MaxInt64, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: price, PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 4}}
			_, err = budgets.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
			units := int64(10)
			if mode == "valuation" {
				units = math.MaxInt64
			}
			err = budgets.Reconcile(t.Context(), attempt.ID, reservation.Evidence{ID: "usage", Quantities: reservation.Quantities{"output": units}})
			if mode == "valuation" {
				require.ErrorIs(t, err, reservation.ErrOverflow)
			} else {
				require.NoError(t, err)
			}
			before, err := budgets.Window(t.Context(), meter, time.Now())
			require.NoError(t, err)
			require.True(t, before.Overflow)
			report, err := inspectReferenceFixture(t, source, request, destination)
			require.NoError(t, err)
			require.EqualValues(t, 1, report.BudgetWindows)
			after, err := budgets.Window(t.Context(), meter, time.Now())
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestBackupCorrectionChainRejectsCycles(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
	source.KV = transfer
	budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
	require.NoError(t, err)
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 0, reservation.History{ID: "history", Proof: "fixture-history"}))
	attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}}
	_, err = budgets.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
	var latest *reservation.CorrectionReceipt
	for _, id := range []string{"first", "second"} {
		before, err := budgets.Inspect(t.Context(), attempt.ID)
		require.NoError(t, err)
		latest, err = budgets.Correct(t.Context(), attempt.ID, reservation.Correction{ID: id, ExpectedBinding: reservation.CorrectionBinding(*before), Actor: "operator", EvidenceReference: "provider/no-charge", Reason: "no-charge", Evidence: reservation.Evidence{ID: "no-charge", NoCharge: true}})
		require.NoError(t, err)
	}
	head, err := budgets.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	keys, err := kv.ScanWithPrefix(t.Context(), "budget:v1:correction:", 0)
	require.NoError(t, err)
	for _, key := range keys {
		data, err := kv.Get(t.Context(), key)
		require.NoError(t, err)
		var receipt reservation.CorrectionReceipt
		require.NoError(t, json.Unmarshal(data, &receipt))
		if receipt.Correction.ID != "first" {
			continue
		}
		receipt.Before = *head
		receipt.Correction.ExpectedBinding = reservation.CorrectionBinding(*head)
		receipt.RecordedAt = latest.RecordedAt
		data, err = json.Marshal(receipt)
		require.NoError(t, err)
		require.NoError(t, kv.Set(t.Context(), key, data))
	}
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err = InspectBundleReferences(ctx, destination, digest, filepath.Dir(destination), source.Encryption)
	require.Error(t, err, "mutually consistent correction edges cannot form a cycle")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "the chain must detect a cycle without waiting for cancellation")
}
