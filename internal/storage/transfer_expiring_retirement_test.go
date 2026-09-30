package storage

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type expiringImportRetirement interface {
	ReconcileExpiringImport(context.Context, []byte, int64, string, string, []TransferRecord) (string, error)
}

func TestExpiringImportRetirementKeepsOriginalExpiryAndDurableRetry(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("captured-expiring-controls")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			record := TransferRecord{Key: "catalog:fleet:{captured}:v1:lease", Value: []byte("original lease"), ExpiresAtMillis: time.Now().Add(time.Hour).UnixMilli()}
			require.NoError(t, transfer.Import(t.Context(), claim, record))
			_, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: record.Key, ExpectedValue: record.Value}})
			require.ErrorIs(t, err, ErrConflict, "ordinary import CAS must continue to require persistent records")
			retirer, ok := transfer.(expiringImportRetirement)
			require.True(t, ok, "native owner must retire exact captured expiring controls without renewing them")
			receipt, err := retirer.ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
			require.NoError(t, err)
			_, err = store.Get(t.Context(), record.Key)
			require.ErrorIs(t, err, ErrNotFound)
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			// This independent write occurs after the native retirement commit whose reply was lost.
			next, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: record.Key, NewValue: []byte("later persistent state")}})
			require.NoError(t, err)
			repeated, err := retirer.ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
			require.NoError(t, err)
			require.Equal(t, receipt, repeated)
			value, err := store.Get(t.Context(), record.Key)
			require.NoError(t, err)
			require.Equal(t, []byte("later persistent state"), value)
			record.ExpiresAtMillis++
			_, err = retirer.ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
			require.Error(t, err, "original absolute expiry is part of the immutable operation fingerprint")
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, ImportReplayPosition{Sequence: 2, ReceiptSHA256: next}, strings.Repeat("d", 64)))
		})
	}
}

func TestExpiringImportRetirementProvesNativePreimageOrExpiredAbsence(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"original", "expired", "future-absence", "different-bytes", "different-expiry", "persistent", "wrong-claim"} {
				t.Run(mode, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					claim := []byte("retire-only-original-control")
					require.NoError(t, transfer.Claim(t.Context(), claim))
					record := TransferRecord{Key: "catalog:fleet:{captured}:v1:maintenance", Value: []byte("original maintenance"), ExpiresAtMillis: time.Now().Add(time.Hour).UnixMilli()}
					original := record
					if mode == "expired" {
						record.ExpiresAtMillis, original.ExpiresAtMillis = 1, 1
					}
					if mode != "future-absence" {
						require.NoError(t, transfer.Import(t.Context(), claim, original))
					}
					switch mode {
					case "different-bytes":
						record.Value = []byte("other maintenance")
					case "different-expiry":
						record.ExpiresAtMillis += 1000
					case "persistent":
						require.NoError(t, store.Set(t.Context(), record.Key, original.Value))
					case "wrong-claim":
						claim = []byte("another-claim")
					}
					retirer, ok := transfer.(expiringImportRetirement)
					require.True(t, ok)
					_, err := retirer.ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
					if mode == "original" || mode == "expired" {
						require.NoError(t, err)
						_, err = store.Get(t.Context(), record.Key)
						require.ErrorIs(t, err, ErrNotFound)
					} else {
						require.Error(t, err)
						_, err = store.Get(t.Context(), transferReconciliationCurrent)
						require.ErrorIs(t, err, ErrNotFound, "refusal must not advance the durable replay cursor")
					}
				})
			}
		})
	}
}

func TestExpiringImportRetirementValidationKeepsDataAndCursor(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("bounded-expiring-retirement")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			record := TransferRecord{Key: "catalog:fleet:{captured}:v1:lease", Value: []byte("original lease"), ExpiresAtMillis: time.Now().Add(time.Hour).UnixMilli()}
			require.NoError(t, transfer.Import(t.Context(), claim, record))
			retirer := transfer.(expiringImportRetirement)
			for _, mode := range []string{"empty", "duplicate", "persistent", "control", "oversized", "partial", "cancelled", "nil-context"} {
				t.Run(mode, func(t *testing.T) {
					records := []TransferRecord{record}
					ctx := t.Context()
					switch mode {
					case "empty":
						records = nil
					case "duplicate":
						records = append(records, record)
					case "persistent":
						records[0].ExpiresAtMillis = 0
					case "control":
						records[0].Key = TransferBarrierKey
					case "oversized":
						records[0].Value = make([]byte, ImportReplayMaxBytes)
					case "partial":
						records = append(records, TransferRecord{Key: "catalog:fleet:{captured}:v1:maintenance", Value: []byte("missing future control"), ExpiresAtMillis: record.ExpiresAtMillis})
					case "cancelled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					case "nil-context":
						ctx = nil
					}
					_, err := retirer.ReconcileExpiringImport(ctx, claim, 1, "", strings.Repeat("a", 64), records)
					require.Error(t, err)
					value, err := store.Get(t.Context(), record.Key)
					require.NoError(t, err)
					require.Equal(t, record.Value, value)
					_, err = store.Get(t.Context(), transferReconciliationCurrent)
					require.ErrorIs(t, err, ErrNotFound)
				})
			}
		})
	}
}

func TestExpiringImportRetirementReopensOriginalReceipt(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			var open func() (KVStore, error)
			if kind == StorageTypeBadger {
				cfg := BadgerConfig{Path: t.TempDir(), SyncWrites: true, MemTableSize: 8 << 20}
				open = func() (KVStore, error) { return OpenBadger(cfg) }
			} else {
				address := os.Getenv("TEST_VALKEY_URL")
				if address == "" {
					t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
				}
				cfg := ValkeyConfig{URL: address, DeploymentID: "retirement-reopen-" + rand.Text(), AllowInsecure: true}
				open = func() (KVStore, error) { return OpenValkey(cfg) }
			}
			store, err := open()
			require.NoError(t, err)
			t.Cleanup(func() {
				if kind == StorageTypeValkey {
					keys, scanErr := store.ScanWithPrefix(context.Background(), "", 1000)
					require.NoError(t, scanErr)
					if len(keys) > 0 {
						require.NoError(t, store.BatchDelete(context.Background(), keys))
					}
				}
				require.NoError(t, store.Close())
			})
			identity := ""
			if shared, ok := store.(IncarnationProvider); ok {
				identity, err = shared.ObserveIncarnation(t.Context())
				require.NoError(t, err)
			}
			transfer, err := OpenRecordTransfer(t.Context(), store, identity)
			require.NoError(t, err)
			claim := []byte("durable-original-expiry")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			record := TransferRecord{Key: "catalog:fleet:{captured}:v1:lease", Value: []byte("original lease"), ExpiresAtMillis: time.Now().Add(time.Hour).UnixMilli()}
			require.NoError(t, transfer.Import(t.Context(), claim, record))
			receipt, err := transfer.(ImportExpiringRetirer).ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
			require.NoError(t, err)
			require.NoError(t, store.Close())
			store, err = open()
			require.NoError(t, err)
			transfer, err = OpenRecordTransfer(t.Context(), store, identity)
			require.NoError(t, err)
			repeated, err := transfer.(ImportExpiringRetirer).ReconcileExpiringImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []TransferRecord{record})
			require.NoError(t, err)
			require.Equal(t, receipt, repeated)
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			_, err = store.Get(t.Context(), record.Key)
			require.ErrorIs(t, err, ErrNotFound)
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, strings.Repeat("d", 64)))
		})
	}
}

func TestExpiringImportReceiptPreservesEmptyValuePresence(t *testing.T) {
	record := TransferRecord{Key: "catalog:fleet:{captured}:v1:lease", ExpiresAtMillis: 1}
	absent, err := ImportExpiringReconciliationSHA256([]byte("claim"), 1, "", strings.Repeat("a", 64), []TransferRecord{record})
	require.NoError(t, err)
	record.Value = []byte{}
	present, err := ImportExpiringReconciliationSHA256([]byte("claim"), 1, "", strings.Repeat("a", 64), []TransferRecord{record})
	require.NoError(t, err)
	require.NotEqual(t, absent, present, "the receipt must retain exact original slice presence")
}
