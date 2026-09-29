package storage

import (
	"context"
	"crypto/rand"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type importReconciliation interface {
	ReconcileImport(context.Context, []byte, int64, string, string, []CompareAndSwapMutation) (string, error)
}

func TestImportReconciliationOrderedRetry(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			reconcile, ok := transfer.(importReconciliation)
			require.True(t, ok, "native import must reconcile later history without opening admission")
			claim := []byte("ordered-history")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			first := []CompareAndSwapMutation{{Key: "account", NewValue: []byte("active")}, {Key: "spend", NewValue: []byte("10")}}
			receipt, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), first)
			require.NoError(t, err)
			require.Len(t, receipt, 64)
			require.ErrorIs(t, transfer.Claim(t.Context(), claim), ErrConflict)
			require.ErrorIs(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "late-import"}), ErrConflict)
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			second := []CompareAndSwapMutation{{Key: "account", ExpectedValue: []byte("active"), NewValue: []byte("revoked")}, {Key: "spend", ExpectedValue: []byte("10"), NewValue: []byte("20")}}
			next, err := reconcile.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), second)
			require.NoError(t, err)
			require.NotEqual(t, receipt, next)
			repeated, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), first)
			require.NoError(t, err)
			require.Equal(t, receipt, repeated)
			value, err := store.Get(t.Context(), "account")
			require.NoError(t, err)
			require.Equal(t, []byte("revoked"), value)
			value, err = store.Get(t.Context(), "spend")
			require.NoError(t, err)
			require.Equal(t, []byte("20"), value)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("c", 64), second)
			require.Error(t, err)
		})
	}
}

func TestImportReconciliationRefusesPartialAndOutOfOrderWrites(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			reconcile := transfer.(ImportReconciler)
			claim := []byte("history")
			evidence := strings.Repeat("a", 64)
			changes := []CompareAndSwapMutation{{Key: "first", NewValue: []byte("first")}, {Key: "second", ExpectedValue: []byte("missing"), NewValue: []byte("second")}}
			require.NoError(t, transfer.Claim(t.Context(), claim))
			_, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
			require.ErrorIs(t, err, ErrConflict)
			_, err = store.Get(t.Context(), "first")
			require.ErrorIs(t, err, ErrNotFound)
			_, err = store.Get(t.Context(), transferReconciliationCurrent)
			require.ErrorIs(t, err, ErrNotFound)
			changes = changes[:1]
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, evidence, evidence, changes)
			require.ErrorIs(t, err, ErrConflict)
			first, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
			require.NoError(t, err)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 3, first, evidence, changes)
			require.ErrorIs(t, err, ErrConflict)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, strings.Repeat("b", 64), evidence, changes)
			require.ErrorIs(t, err, ErrConflict)
			changed := []CompareAndSwapMutation{{Key: "first", NewValue: []byte("changed")}}
			_, err = reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, changed)
			require.ErrorIs(t, err, ErrConflict, "receipt must bind mutations as well as evidence")
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
		})
	}
}

func TestImportReconciliationInvalidInputs(t *testing.T) {
	valid := []CompareAndSwapMutation{{Key: "data", NewValue: []byte("value")}}
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			reconcile := transfer.(ImportReconciler)
			claim := []byte("input")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			for _, invalid := range []string{"zero-sequence", "negative-sequence", "first-previous", "second-no-previous", "no-evidence", "large-claim", "empty", "duplicate", "reserved", "large", "ttl", "absent-key"} {
				t.Run(invalid, func(t *testing.T) {
					current, sequence, previous, evidence, mutations := claim, int64(1), "", strings.Repeat("a", 64), valid
					switch invalid {
					case "zero-sequence":
						sequence = 0
					case "negative-sequence":
						sequence = -1
					case "first-previous":
						previous = evidence
					case "second-no-previous":
						sequence = 2
					case "no-evidence":
						evidence = ""
					case "large-claim":
						current = make([]byte, 4097)
					case "empty":
						mutations = nil
					case "duplicate":
						mutations = append(append([]CompareAndSwapMutation{}, valid...), valid...)
					case "reserved":
						mutations = []CompareAndSwapMutation{{Key: TransferBarrierKey}}
					case "large":
						mutations = []CompareAndSwapMutation{{Key: "data", NewValue: make([]byte, reconciliationMaxBytes+1)}}
					case "ttl":
						mutations = []CompareAndSwapMutation{{Key: "data", TTL: time.Minute}}
					case "absent-key":
						mutations = []CompareAndSwapMutation{{NewValue: []byte("data")}}
					}
					_, err := reconcile.ReconcileImport(t.Context(), current, sequence, previous, evidence, mutations)
					require.Error(t, err)
					_, err = store.Get(t.Context(), transferReconciliationCurrent)
					require.ErrorIs(t, err, ErrNotFound)
				})
			}
		})
	}
}

func TestImportReconciliationChecksPersistentOwnership(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, invalid := range []string{"missing", "other-claim", "expiring-barrier", "expiring-data", "active", "canceled", "corrupt-cursor", "corrupt-history"} {
				t.Run(invalid, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					reconcile := transfer.(ImportReconciler)
					claim := []byte("owner")
					evidence := strings.Repeat("a", 64)
					ctx := t.Context()
					changes := []CompareAndSwapMutation{{Key: "data", NewValue: []byte("value")}}
					if invalid != "missing" {
						require.NoError(t, transfer.Claim(ctx, claim))
					}
					switch invalid {
					case "other-claim":
						claim = []byte("other")
					case "expiring-barrier":
						require.NoError(t, store.SetWithTTL(ctx, TransferBarrierKey, claim, time.Hour))
					case "expiring-data":
						require.NoError(t, store.SetWithTTL(ctx, "data", []byte("before"), time.Hour))
						changes[0].ExpectedValue = []byte("before")
					case "active":
						require.NoError(t, transfer.(ImportActivator).ActivateImport(ctx, claim, evidence))
					case "canceled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					case "corrupt-cursor":
						require.NoError(t, store.Set(ctx, transferReconciliationCurrent, []byte("{}")))
					case "corrupt-history":
						_, err := reconcile.ReconcileImport(ctx, claim, 1, "", evidence, changes)
						require.NoError(t, err)
						require.NoError(t, store.Set(ctx, reconciliationKey(reconciliationDigest(claim), 1), []byte("{}")))
					}
					_, err := reconcile.ReconcileImport(ctx, claim, 1, "", evidence, changes)
					require.Error(t, err)
					if invalid != "missing" && invalid != "active" {
						require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
					}
				})
			}
		})
	}
}

func TestImportReconciliationDistinguishesEmptyAndAbsent(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			reconcile := transfer.(ImportReconciler)
			claim := []byte("empty")
			evidence := strings.Repeat("a", 64)
			require.NoError(t, transfer.Claim(t.Context(), claim))
			empty := []CompareAndSwapMutation{{Key: "empty", NewValue: []byte{}}}
			first, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, empty)
			require.NoError(t, err)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, []CompareAndSwapMutation{{Key: "empty"}})
			require.ErrorIs(t, err, ErrConflict, "empty and deleted must have different evidence")
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, first, evidence, []CompareAndSwapMutation{{Key: "empty", NewValue: []byte("overwrite")}})
			require.ErrorIs(t, err, ErrConflict, "empty existing value must not satisfy absence")
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, first, evidence, []CompareAndSwapMutation{{Key: "empty", ExpectedValue: []byte{}}})
			require.NoError(t, err)
			_, err = store.Get(t.Context(), "empty")
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestImportReconciliationConcurrentConflict(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, transfer := transferTestStore(t, kind)
			reconcile := transfer.(ImportReconciler)
			claim := []byte("concurrent")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, value := range []string{"a", "b"} {
				wg.Go(func() {
					_, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat(value, 64), []CompareAndSwapMutation{{Key: "data", NewValue: []byte(value)}})
					results <- err
				})
			}
			wg.Wait()
			close(results)
			winners := 0
			for err := range results {
				if err == nil {
					winners++
				} else {
					require.ErrorIs(t, err, ErrConflict)
				}
			}
			require.Equal(t, 1, winners)
		})
	}
}

func TestImportReconciliationHistoryCannotAuthorizeAnotherRestore(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, source := transferTestStore(t, kind)
			claim := []byte("source")
			evidence := strings.Repeat("a", 64)
			changes := []CompareAndSwapMutation{{Key: "data", NewValue: []byte("value")}}
			require.NoError(t, source.Claim(t.Context(), claim))
			_, err := source.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
			require.NoError(t, err)
			require.NoError(t, source.(ImportActivator).ActivateImport(t.Context(), claim, evidence))
			var records []TransferRecord
			require.NoError(t, source.Enumerate(t.Context(), func(r TransferRecord) error {
				require.NotEqual(t, transferReconciliationCurrent, r.Key)
				records = append(records, r)
				return nil
			}))
			require.Len(t, records, 3, "payload, reconciliation receipt, activation receipt")
			store, target := transferTestStore(t, kind)
			require.NoError(t, target.Claim(t.Context(), claim))
			for _, record := range records {
				require.NoError(t, target.Import(t.Context(), claim, record))
			}
			_, err = target.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
			require.ErrorIs(t, err, ErrConflict, "historical receipt has no native cursor")
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			require.Error(t, TransferRecord{Key: transferReconciliationCurrent}.Validate())
		})
	}
}

func TestImportReconciliationChecksNativeIncarnation(t *testing.T) {
	_, transfer := transferTestStore(t, StorageTypeValkey)
	claim := []byte("native")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	transfer.(*valkeyTransfer).bound.identity = strings.Repeat("0", 40) + ":" + strings.Repeat("0", 40)
	_, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "data", NewValue: []byte("value")}})
	require.ErrorIs(t, err, ErrIncarnationChanged)
}

func TestImportReconciliationSurvivesBadgerReopen(t *testing.T) {
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, MemTableSize: 8 << 20, NumVersions: 1, NumLevelZero: 5}}
	store, transfer, err := OpenImportTarget(t.Context(), cfg)
	require.NoError(t, err)
	claim := []byte("restart")
	evidence := strings.Repeat("a", 64)
	changes := []CompareAndSwapMutation{{Key: "data", NewValue: []byte("retained")}}
	require.NoError(t, transfer.Claim(t.Context(), claim))
	first, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, transfer, err = OpenImportTarget(t.Context(), cfg)
	require.NoError(t, err)
	defer store.Close()
	require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
	retry, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
	require.NoError(t, err)
	require.Equal(t, first, retry, "lost response followed by reopen must preserve its receipt")
	_, err = transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 2, first, evidence, []CompareAndSwapMutation{{Key: "data", ExpectedValue: []byte("retained"), NewValue: []byte("next")}})
	require.NoError(t, err)
}

func TestImportReconciliationPreflightsWritePermissions(t *testing.T) {
	admin, transfer := transferTestStore(t, StorageTypeValkey)
	native := admin.(*ValkeyStore)
	claim := []byte("restricted-user")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "delete", Value: []byte("retained")}))
	user, password := "replay-"+rand.Text(), rand.Text()
	require.NoError(t, native.client.Do(t.Context(), native.client.B().Arbitrary("ACL", "SETUSER", user, "reset", "on", ">"+password, "~*", "&*", "+@all", "-del").Build()).Error())
	defer func() {
		require.NoError(t, native.client.Do(t.Context(), native.client.B().Arbitrary("ACL", "DELUSER", user).Build()).Error())
	}()
	cfg := native.config
	selected, err := url.Parse(cfg.URL)
	require.NoError(t, err)
	selected.User = url.UserPassword(user, password)
	cfg.URL = selected.String()
	cfg.Username, cfg.Password = "", ""
	restricted, err := OpenValkey(cfg)
	require.NoError(t, err)
	defer restricted.Close()
	identity, err := restricted.(IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	target, err := OpenRecordTransfer(t.Context(), restricted, identity)
	require.NoError(t, err)
	changes := []CompareAndSwapMutation{{Key: "first", NewValue: []byte("must-not-write")}, {Key: "delete", ExpectedValue: []byte("retained")}}
	_, err = target.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
	require.Error(t, err)
	_, err = admin.Get(t.Context(), "first")
	require.ErrorIs(t, err, ErrNotFound, "a denied later DEL must not leave the earlier SET committed")
	current, err := admin.Get(t.Context(), "delete")
	require.NoError(t, err)
	require.Equal(t, []byte("retained"), current)
	_, err = admin.Get(t.Context(), transferReconciliationCurrent)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, CheckImportBarrier(t.Context(), admin), ErrImportRestricted)
	require.NoError(t, native.client.Do(t.Context(), native.client.B().Arbitrary("ACL", "SETUSER", user, "+del").Build()).Error())
	_, err = target.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
	require.NoError(t, err, "exact retry can complete after the operator restores permissions")
}
