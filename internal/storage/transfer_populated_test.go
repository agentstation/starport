package storage

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// populatedTestCapture is an immutable capture in strict key order.
type populatedTestCapture []TransferRecord

func (c populatedTestCapture) Enumerate(ctx context.Context, visit func(TransferRecord) error) error {
	for _, record := range c {
		if err := ctx.Err(); err != nil {
			return err
		}
		record.Value = bytes.Clone(record.Value)
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}

func (c populatedTestCapture) ReadCaptured(_ context.Context, key string, limit int) (TransferRecord, error) {
	index, found := slices.BinarySearchFunc(c, key, func(record TransferRecord, key string) int { return strings.Compare(record.Key, key) })
	if !found {
		return TransferRecord{}, ErrNotFound
	}
	if len(c[index].Value) > limit {
		return TransferRecord{}, ErrValueTooLarge
	}
	record := c[index]
	record.Value = bytes.Clone(record.Value)
	return record, nil
}

func (c populatedTestCapture) with(record TransferRecord) populatedTestCapture {
	records := append(slices.Clone(c), record)
	slices.SortFunc(records, func(a, b TransferRecord) int { return strings.Compare(a.Key, b.Key) })
	return records
}

func capturePopulatedTest(t *testing.T, transfer RecordTransfer) populatedTestCapture {
	t.Helper()
	records := map[string]TransferRecord{}
	require.NoError(t, transfer.Enumerate(t.Context(), func(record TransferRecord) error { records[record.Key] = record; return nil }))
	return slices.SortedFunc(maps.Values(records), func(a, b TransferRecord) int { return strings.Compare(a.Key, b.Key) })
}

// populatedTestState reads every physical record, including native controls.
func populatedTestState(t *testing.T, store KVStore) map[string]TransferRecord {
	t.Helper()
	s := store.(*ValkeyStore)
	keys, err := s.ScanWithPrefix(t.Context(), "", 100000)
	require.NoError(t, err)
	state := make(map[string]TransferRecord, len(keys))
	for _, key := range keys {
		value, err := s.do(t.Context(), s.client.B().Get().Key(s.prefix+key).Build()).AsBytes()
		require.NoError(t, err)
		state[key] = TransferRecord{Key: key, Value: value, ExpiresAtMillis: transferTestExpiry(t, store, key)}
	}
	return state
}

// populatedTestTarget builds a running deployment and captures it after writers stop.
// With history, an earlier recovery epoch leaves receipts and both current roots.
func populatedTestTarget(t *testing.T, history bool) (KVStore, RecordTransfer, populatedTestCapture, ImportControls) {
	t.Helper()
	store, transfer := transferTestStore(t, StorageTypeValkey)
	if history {
		prior := []byte("prior-epoch")
		require.NoError(t, transfer.Claim(t.Context(), prior))
		require.NoError(t, transfer.Import(t.Context(), prior, TransferRecord{Key: "account", Value: []byte("active")}))
		receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), prior, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "account", ExpectedValue: []byte("active"), NewValue: []byte("revoked")}})
		require.NoError(t, err)
		require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), prior, ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, strings.Repeat("d", 64)))
	}
	require.NoError(t, store.Set(t.Context(), "persistent", []byte{0, 255}))
	require.NoError(t, store.SetWithTTL(t.Context(), "expiring", []byte("value"), time.Hour))
	capture := capturePopulatedTest(t, transfer)
	controls, err := transfer.(PopulatedImportClaimer).ObserveImportControls(t.Context())
	require.NoError(t, err)
	require.Equal(t, history, controls.ActivationPresent)
	require.Equal(t, history, controls.ReconciliationPresent)
	return store, transfer, capture, controls
}

func TestPopulatedClaimAdoptsExactTargetAndSharesImportFlow(t *testing.T) {
	for _, history := range []bool{true, false} {
		t.Run(map[bool]string{true: "earlier-epoch", false: "fresh-deployment"}[history], func(t *testing.T) {
			store, transfer, capture, controls := populatedTestTarget(t, history)
			if !history {
				require.ErrorIs(t, transfer.Claim(t.Context(), []byte("ordinary")), ErrDatabaseNotEmpty)
			}
			claimer := transfer.(PopulatedImportClaimer)
			claim := []byte("populated-adoption")
			before := populatedTestState(t, store)
			require.NoError(t, claimer.ClaimPopulated(t.Context(), claim, controls, capture))

			// The claim changes only the barrier, both roots, and the new closure receipt.
			after := populatedTestState(t, store)
			closureKey := transferPopulatedPrefix + reconciliationDigest(claim)
			closure := after[closureKey]
			require.NoError(t, closure.Validate())
			require.Equal(t, TransferRecord{Key: TransferBarrierKey, Value: claim}, after[TransferBarrierKey])
			expected := maps.Clone(before)
			delete(expected, transferActivationCurrent)
			delete(expected, transferReconciliationCurrent)
			expected[TransferBarrierKey] = after[TransferBarrierKey]
			expected[closureKey] = closure
			require.Equal(t, expected, after)

			// Exact retries after a lost reply write nothing, also through a fresh handle.
			require.NoError(t, claimer.ClaimPopulated(t.Context(), claim, controls, capture))
			identity, err := store.(IncarnationProvider).ObserveIncarnation(t.Context())
			require.NoError(t, err)
			fresh, err := OpenRecordTransfer(t.Context(), store, identity)
			require.NoError(t, err)
			require.NoError(t, fresh.(PopulatedImportClaimer).ClaimPopulated(t.Context(), claim, controls, capture))
			require.Equal(t, after, populatedTestState(t, store))

			// The shared flow treats the populated claim as an opaque ordinary claim.
			require.ErrorIs(t, transfer.Enumerate(t.Context(), func(TransferRecord) error { return nil }), ErrImportRestricted)
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			_, err = claimer.ObserveImportControls(t.Context())
			require.ErrorIs(t, err, ErrImportRestricted)
			require.NoError(t, transfer.(ImportUnreleasedInspector).CheckUnreleasedImport(t.Context(), claim))
			require.NoError(t, transfer.(ImportPositionInspector).CheckImportPosition(t.Context(), claim, ImportReplayPosition{}))
			inspected := map[string]TransferRecord{}
			require.NoError(t, transfer.(ImportInspector).InspectImport(t.Context(), claim, ImportReplayPosition{}, func(record TransferRecord) error {
				inspected[record.Key] = record
				return nil
			}))
			wantInspected := map[string]TransferRecord{closureKey: closure}
			for _, record := range capture {
				wantInspected[record.Key] = record
			}
			require.Equal(t, wantInspected, inspected)
			read, err := transfer.(ImportRecordReader).ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "persistent", 16)
			require.NoError(t, err)
			require.Equal(t, []byte{0, 255}, read.Value)

			receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "added", NewValue: []byte("final")}})
			require.NoError(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			decision := strings.Repeat("e", 64)
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, decision))
			require.NoError(t, transfer.(ImportActivationInspector).CheckActivatedImportAt(t.Context(), claim, position, decision))
			startup, err := InspectRecoveryStartup(t.Context(), store)
			require.NoError(t, err)
			require.Equal(t, reconciliationDigest(claim), startup.ClaimSHA256)

			// Acknowledged records and historical receipts keep their exact bytes and expiry.
			final := populatedTestState(t, store)
			for _, record := range capture {
				require.Equal(t, record, final[record.Key])
			}
			require.Equal(t, closure, final[closureKey])
			require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), claim, controls, capture), ErrConflict)
			require.Equal(t, final, populatedTestState(t, store))
		})
	}
}

func TestPopulatedClaimRefusesWithoutChange(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	tests := []struct {
		name    string
		history bool
		change  func(*testing.T, KVStore, populatedTestCapture, ImportControls) ([]byte, populatedTestCapture, ImportControls)
		want    error
	}{
		{"changed-value", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Set(t.Context(), "persistent", []byte("changed")))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"changed-expiry", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.ExpireAt(t.Context(), "expiring", time.Now().Add(2*time.Hour)))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"expiry-added", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.ExpireAt(t.Context(), "persistent", time.Now().Add(2*time.Hour)))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"extra-live-record", true, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Set(t.Context(), "extra", []byte("unacknowledged")))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"missing-persistent-record", true, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Delete(t.Context(), "persistent"))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"missing-unexpired-record", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			return []byte("claim"), capture.with(TransferRecord{Key: "lost", Value: []byte("acknowledged"), ExpiresAtMillis: future}), controls
		}, ErrConflict},
		{"missing-historical-receipt", true, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Delete(t.Context(), transferActivationPrefix+reconciliationDigest([]byte("prior-epoch"))))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"changed-control-root", true, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Set(t.Context(), transferActivationCurrent, []byte("changed")))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"created-control-root", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Set(t.Context(), transferReconciliationCurrent, []byte("created")))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"stale-observation", true, func(t *testing.T, _ KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			controls.Reconciliation = []byte("stale")
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"existing-barrier", false, func(t *testing.T, store KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			require.NoError(t, store.Set(t.Context(), TransferBarrierKey, []byte("other-operation")))
			return []byte("claim"), capture, controls
		}, ErrConflict},
		{"unordered-capture", false, func(t *testing.T, _ KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			return []byte("claim"), slices.Concat(capture, capture), controls
		}, ErrInvalidMutation},
		{"invalid-observation", false, func(t *testing.T, _ KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			controls.Activation = []byte("absent-with-bytes")
			return []byte("claim"), capture, controls
		}, ErrInvalidMutation},
		{"empty-claim", false, func(t *testing.T, _ KVStore, capture populatedTestCapture, controls ImportControls) ([]byte, populatedTestCapture, ImportControls) {
			return nil, capture, controls
		}, ErrInvalidMutation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, transfer, capture, controls := populatedTestTarget(t, test.history)
			claim, capture, controls := test.change(t, store, capture, controls)
			before := populatedTestState(t, store)
			require.ErrorIs(t, transfer.(PopulatedImportClaimer).ClaimPopulated(t.Context(), claim, controls, capture), test.want)
			require.Equal(t, before, populatedTestState(t, store))
		})
	}
}

func TestPopulatedClaimBindsExpiredCapturedRecords(t *testing.T) {
	store, transfer, capture, controls := populatedTestTarget(t, true)
	claimer := transfer.(PopulatedImportClaimer)
	expired := capture.with(TransferRecord{Key: "expired", Value: []byte("gone"), ExpiresAtMillis: time.Now().Add(-time.Minute).UnixMilli()})
	claim := []byte("expired-capture")
	require.NoError(t, claimer.ClaimPopulated(t.Context(), claim, controls, expired))
	after := populatedTestState(t, store)
	require.NotContains(t, after, "expired")

	// The receipt binds the census, so the same claim refuses a different capture or observation.
	require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), claim, controls, capture), ErrConflict)
	changed := controls
	changed.ActivationPresent, changed.Activation = false, nil
	require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), claim, changed, expired), ErrConflict)
	require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), []byte("different-operation"), controls, expired), ErrConflict)
	require.NoError(t, claimer.ClaimPopulated(t.Context(), claim, controls, expired))
	require.Equal(t, after, populatedTestState(t, store))
}

func TestPopulatedClaimRequiresApprovedIncarnation(t *testing.T) {
	store, transfer, capture, controls := populatedTestTarget(t, true)
	v := transfer.(*valkeyTransfer)
	approved := v.bound.identity
	v.bound.identity = "0000000000000000000000000000000000000000:0000000000000000000000000000000000000000"
	before := populatedTestState(t, store)
	_, err := v.ObserveImportControls(t.Context())
	require.ErrorIs(t, err, ErrIncarnationChanged)
	require.ErrorIs(t, v.ClaimPopulated(t.Context(), []byte("claim"), controls, capture), ErrIncarnationChanged)
	require.Equal(t, before, populatedTestState(t, store))
	v.bound.identity = approved
	require.NoError(t, v.ClaimPopulated(t.Context(), []byte("claim"), controls, capture))
}

func TestPopulatedClaimRefusesBadger(t *testing.T) {
	store, transfer := transferTestStore(t, StorageTypeBadger)
	require.NoError(t, store.Set(t.Context(), "persistent", []byte("retained")))
	claimer, ok := transfer.(PopulatedImportClaimer)
	require.True(t, ok)
	_, err := claimer.ObserveImportControls(t.Context())
	require.ErrorIs(t, err, ErrPopulatedImportUnsupported)
	require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), []byte("claim"), ImportControls{}, populatedTestCapture{}), ErrPopulatedImportUnsupported)
	require.NoError(t, CheckImportBarrier(t.Context(), store))
	records := capturePopulatedTest(t, transfer)
	require.Equal(t, populatedTestCapture{{Key: "persistent", Value: []byte("retained")}}, records)
}

func TestPopulatedClaimReceiptHistory(t *testing.T) {
	claim := []byte("receipt")
	controls := ImportControls{ActivationPresent: true, Activation: []byte("activation"), ReconciliationPresent: true, Reconciliation: []byte{}}
	census := newPopulatedCensus()
	require.NoError(t, census.add(TransferRecord{Key: "a", Value: []byte("one")}))
	require.NoError(t, census.add(TransferRecord{Key: "b", Value: []byte("two"), ExpiresAtMillis: 5}))
	require.ErrorIs(t, census.add(TransferRecord{Key: "b", Value: []byte("two"), ExpiresAtMillis: 5}), ErrInvalidMutation)
	key, receipt, err := populatedClaimReceiptAt(claim, controls, census.sum())
	require.NoError(t, err)
	require.Equal(t, transferPopulatedPrefix+reconciliationDigest(claim), key)
	require.NoError(t, TransferRecord{Key: key, Value: receipt}.Validate())
	again, err := func() ([]byte, error) {
		_, encoded, err := populatedClaimReceiptAt(claim, controls, census.sum())
		return encoded, err
	}()
	require.NoError(t, err)
	require.Equal(t, receipt, again)
	absent := controls
	absent.ReconciliationPresent, absent.Reconciliation = false, nil
	_, other, err := populatedClaimReceiptAt(claim, absent, census.sum())
	require.NoError(t, err)
	require.NotEqual(t, receipt, other)

	for _, record := range []TransferRecord{
		{Key: key, Value: receipt, ExpiresAtMillis: time.Now().Add(time.Hour).UnixMilli()},
		{Key: transferPopulatedPrefix + strings.Repeat("0", 64), Value: receipt},
		{Key: key, Value: bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":2`), 1)},
		{Key: key, Value: append(bytes.Clone(receipt), ' ')},
		{Key: key, Value: []byte("{}")},
	} {
		require.ErrorIs(t, record.Validate(), ErrInvalidMutation)
	}
	_, _, err = populatedClaimReceiptAt(claim, controls, "short")
	require.ErrorIs(t, err, ErrInvalidMutation)
}
