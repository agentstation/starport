package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type importActivation interface {
	ActivateImport(context.Context, []byte, string) error
}

func transferActivator(t *testing.T, transfer RecordTransfer) importActivation {
	t.Helper()
	require.Implements(t, (*importActivation)(nil), transfer)
	return transfer.(importActivation)
}

func TestImportActivationExactRetry(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			activate := transferActivator(t, transfer)
			claim := []byte("activation-operation")
			decision := strings.Repeat("a", 64)
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "data", Value: []byte("original")}))
			require.NoError(t, activate.ActivateImport(t.Context(), claim, decision))
			require.NoError(t, CheckImportBarrier(t.Context(), store))
			require.NoError(t, store.Set(t.Context(), "data", []byte("later")))
			require.NoError(t, activate.ActivateImport(t.Context(), claim, decision))
			value, err := store.Get(t.Context(), "data")
			require.NoError(t, err)
			require.Equal(t, []byte("later"), value)
			require.ErrorIs(t, activate.ActivateImport(t.Context(), claim, strings.Repeat("b", 64)), ErrConflict)
			require.ErrorIs(t, activate.ActivateImport(t.Context(), []byte("other-operation"), decision), ErrConflict)
			require.Error(t, transfer.Claim(t.Context(), claim))
			require.Error(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "late-import"}))
		})
	}
}

func TestImportActivationRefusesInvalidOwnership(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, invalid := range []string{"missing", "different", "expiring", "canceled", "empty-claim", "large-claim", "short-decision", "uppercase-decision", "nonhex-decision"} {
				t.Run(invalid, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					activate := transferActivator(t, transfer)
					claim, decision := []byte("owner"), strings.Repeat("a", 64)
					ctx := t.Context()
					if invalid != "missing" {
						require.NoError(t, transfer.Claim(ctx, claim))
					}
					switch invalid {
					case "different":
						claim = []byte("other")
					case "expiring":
						require.NoError(t, store.SetWithTTL(ctx, TransferBarrierKey, claim, time.Hour))
					case "canceled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					case "empty-claim":
						claim = nil
					case "large-claim":
						claim = make([]byte, 4097)
					case "short-decision":
						decision = "digest"
					case "uppercase-decision":
						decision = strings.Repeat("A", 64)
					case "nonhex-decision":
						decision = strings.Repeat("g", 64)
					}
					err := activate.ActivateImport(ctx, claim, decision)
					require.Error(t, err)
					if invalid == "canceled" {
						require.ErrorIs(t, err, context.Canceled)
					}
					if invalid != "missing" {
						require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
					}
				})
			}
		})
	}
}

func TestImportActivationConcurrentDecisions(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, transfer := transferTestStore(t, kind)
			activate := transferActivator(t, transfer)
			claim := []byte("concurrent")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			type result struct {
				decision string
				err      error
			}
			results := make(chan result, 2)
			var wg sync.WaitGroup
			for _, decision := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
				wg.Go(func() { results <- result{decision, activate.ActivateImport(t.Context(), claim, decision)} })
			}
			wg.Wait()
			close(results)
			winners := 0
			for result := range results {
				if result.err == nil {
					winners++
					require.NoError(t, activate.ActivateImport(t.Context(), claim, result.decision))
				} else {
					require.True(t, errors.Is(result.err, ErrConflict), "%v", result.err)
				}
			}
			require.Equal(t, 1, winners)
		})
	}
}

func TestImportActivationChecksNativeIncarnation(t *testing.T) {
	_, transfer := transferTestStore(t, StorageTypeValkey)
	activate := transferActivator(t, transfer)
	claim := []byte("native")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	transfer.(*valkeyTransfer).bound.identity = strings.Repeat("0", 40) + ":" + strings.Repeat("0", 40)
	require.ErrorIs(t, activate.ActivateImport(t.Context(), claim, strings.Repeat("a", 64)), ErrIncarnationChanged)
}

func TestImportActivationHistoricalReceiptsAreNotAuthority(t *testing.T) {
	for _, sourceKind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(sourceKind, func(t *testing.T) {
			_, source := transferTestStore(t, sourceKind)
			oldClaim, decision := []byte("old-operation"), strings.Repeat("a", 64)
			require.NoError(t, source.Claim(t.Context(), oldClaim))
			require.NoError(t, source.Import(t.Context(), oldClaim, TransferRecord{Key: "data", Value: []byte("source")}))
			require.NoError(t, transferActivator(t, source).ActivateImport(t.Context(), oldClaim, decision))
			var records []TransferRecord
			require.NoError(t, source.Enumerate(t.Context(), func(record TransferRecord) error {
				require.NotEqual(t, transferActivationCurrent, record.Key)
				records = append(records, record)
				return nil
			}))
			require.Len(t, records, 2, "payload and portable historical receipt")
			for _, targetKind := range []string{StorageTypeBadger, StorageTypeValkey} {
				t.Run(targetKind, func(t *testing.T) {
					targetStore, target := transferTestStore(t, targetKind)
					newClaim := []byte("new-operation")
					require.NoError(t, target.Claim(t.Context(), newClaim))
					for _, record := range records {
						require.NoError(t, target.Import(t.Context(), newClaim, record))
					}
					activate := transferActivator(t, target)
					require.ErrorIs(t, activate.ActivateImport(t.Context(), oldClaim, decision), ErrConflict)
					require.ErrorIs(t, CheckImportBarrier(t.Context(), targetStore), ErrImportRestricted)
					require.NoError(t, activate.ActivateImport(t.Context(), newClaim, strings.Repeat("b", 64)))
					require.ErrorIs(t, activate.ActivateImport(t.Context(), oldClaim, decision), ErrConflict)
					exported := 0
					require.NoError(t, target.Enumerate(t.Context(), func(record TransferRecord) error { exported++; return nil }))
					require.Equal(t, 3, exported, "payload and both historical receipts")
					require.ErrorIs(t, target.Import(t.Context(), newClaim, TransferRecord{Key: transferActivationCurrent, Value: []byte("forged")}), ErrInvalidKey)
				})
			}
			for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
				t.Run("old-claim-"+kind, func(t *testing.T) {
					store, target := transferTestStore(t, kind)
					require.NoError(t, target.Claim(t.Context(), oldClaim))
					for _, record := range records {
						require.NoError(t, target.Import(t.Context(), oldClaim, record))
					}
					require.ErrorIs(t, transferActivator(t, target).ActivateImport(t.Context(), oldClaim, decision), ErrConflict)
					require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
				})
			}
		})
	}
}

func TestImportActivationRefusesChangedCompletion(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, changed := range []string{"current-missing", "receipt-missing", "current-corrupt", "receipt-corrupt", "current-expiring", "receipt-expiring", "barrier-revived"} {
				t.Run(changed, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					activate := transferActivator(t, transfer)
					claim, decision := []byte("completed"), strings.Repeat("a", 64)
					require.NoError(t, transfer.Claim(t.Context(), claim))
					require.NoError(t, activate.ActivateImport(t.Context(), claim, decision))
					historyKey, receipt, err := activationReceipt(claim, decision)
					require.NoError(t, err)
					switch changed {
					case "current-missing":
						require.NoError(t, store.Delete(t.Context(), transferActivationCurrent))
					case "receipt-missing":
						require.NoError(t, store.Delete(t.Context(), historyKey))
					case "current-corrupt":
						require.NoError(t, store.Set(t.Context(), transferActivationCurrent, []byte("corrupt")))
					case "receipt-corrupt":
						require.NoError(t, store.Set(t.Context(), historyKey, []byte("corrupt")))
					case "current-expiring":
						require.NoError(t, store.SetWithTTL(t.Context(), transferActivationCurrent, receipt, time.Hour))
					case "receipt-expiring":
						require.NoError(t, store.SetWithTTL(t.Context(), historyKey, receipt, time.Hour))
					case "barrier-revived":
						require.NoError(t, store.Set(t.Context(), TransferBarrierKey, claim))
					}
					require.ErrorIs(t, activate.ActivateImport(t.Context(), claim, decision), ErrConflict)
					if changed == "barrier-revived" {
						require.ErrorIs(t, transfer.Claim(t.Context(), claim), ErrConflict)
						require.ErrorIs(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "unowned"}), ErrConflict)
						require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
					}
				})
			}
		})
	}
}

func TestImportActivationValidatesPortableHistory(t *testing.T) {
	key, value, err := activationReceipt([]byte("history"), strings.Repeat("a", 64))
	require.NoError(t, err)
	valid := TransferRecord{Key: key, Value: value}
	require.NoError(t, valid.Validate())
	for _, invalid := range []TransferRecord{
		{Key: transferActivationCurrent, Value: value},
		{Key: key, Value: value, ExpiresAtMillis: 1},
		{Key: key, Value: []byte("corrupt")},
		{Key: key, Value: append([]byte(" "), value...)},
		{Key: key + "bad", Value: value},
		{Key: key, Value: []byte(strings.Replace(string(value), `"version":1`, `"version":2`, 1))},
	} {
		require.Error(t, invalid.Validate())
	}
}

func TestImportActivationDoesNotReviveClaimFromCurrentMarker(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("used-operation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transferActivator(t, transfer).ActivateImport(t.Context(), claim, strings.Repeat("a", 64)))
			key, _, err := activationReceipt(claim, strings.Repeat("a", 64))
			require.NoError(t, err)
			require.NoError(t, store.Delete(t.Context(), key))
			require.Error(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, CheckImportBarrier(t.Context(), store))
		})
	}
}

func TestImportActivationBadgerReopen(t *testing.T) {
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20}}
	store, err := OpenBadger(cfg.Badger)
	require.NoError(t, err)
	transfer, err := OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	claim, decision := []byte("durable-activation"), strings.Repeat("a", 64)
	require.NoError(t, transfer.Claim(t.Context(), claim))
	require.NoError(t, transferActivator(t, transfer).ActivateImport(t.Context(), claim, decision))
	require.NoError(t, store.Close())
	reopened, err := Open(cfg)
	require.NoError(t, err)
	defer reopened.Close()
	again, err := OpenRecordTransfer(t.Context(), reopened, "")
	require.NoError(t, err)
	require.NoError(t, transferActivator(t, again).ActivateImport(t.Context(), claim, decision))
	require.ErrorIs(t, again.Claim(t.Context(), claim), ErrConflict)
}

func TestImportActivationValkeyReconnect(t *testing.T) {
	store, transfer := transferTestStore(t, StorageTypeValkey)
	claim, decision := []byte("connection-activation"), strings.Repeat("a", 64)
	require.NoError(t, transfer.Claim(t.Context(), claim))
	require.NoError(t, transferActivator(t, transfer).ActivateImport(t.Context(), claim, decision))
	reopened, err := OpenValkey(store.(*ValkeyStore).config)
	require.NoError(t, err)
	defer reopened.Close()
	identity, err := reopened.(IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	again, err := OpenRecordTransfer(t.Context(), reopened, identity)
	require.NoError(t, err)
	require.NoError(t, transferActivator(t, again).ActivateImport(t.Context(), claim, decision))
	require.ErrorIs(t, again.Claim(t.Context(), claim), ErrConflict)
}

func TestImportActivationRequiresDurableBadger(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsynced", true: "memory"}[memory], func(t *testing.T) {
			cfg := BadgerConfig{InMemory: memory, SyncWrites: memory, MemTableSize: 8 << 20}
			if !memory {
				cfg.Path = t.TempDir()
			}
			store, err := OpenBadger(cfg)
			require.NoError(t, err)
			defer store.Close()
			transfer, err := OpenRecordTransfer(t.Context(), store, "")
			require.NoError(t, err)
			claim := []byte("unsafe")
			require.NoError(t, store.Set(t.Context(), TransferBarrierKey, claim))
			require.ErrorContains(t, transferActivator(t, transfer).ActivateImport(t.Context(), claim, strings.Repeat("a", 64)), "persistent Badger with sync_writes")
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
		})
	}
}
