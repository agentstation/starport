package jobslots

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type slotReplaySnapshot map[string]storage.TransferRecord

func (s slotReplaySnapshot) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	record, ok := s[key]
	if !ok {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(record.Value) > maximum {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return record, nil
}
func (s slotReplaySnapshot) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(s[key]); err != nil {
			return err
		}
	}
	return nil
}
func slotClaim(id string) Claim {
	return Claim{Version: recordVersion, CreatedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Account: "account", ID: id, JobID: "job-" + id, Kind: "video", Bound: 1000}
}
func slotSnapshot(t *testing.T, claims ...Claim) slotReplaySnapshot {
	t.Helper()
	view := slotReplaySnapshot{}
	totals := map[string]int64{}
	for _, claim := range claims {
		data, err := replayClaimBytes(claim)
		require.NoError(t, err)
		view[claimKey(claim.Account, claim.ID)] = storage.TransferRecord{Key: claimKey(claim.Account, claim.ID), Value: data}
		if !claim.Released {
			totals[claim.Account]++
		} else if _, ok := totals[claim.Account]; !ok {
			totals[claim.Account] = 0
		}
	}
	for account, total := range totals {
		data, err := json.Marshal(counter{Version: recordVersion, Total: total})
		require.NoError(t, err)
		view[countKey(account)] = storage.TransferRecord{Key: countKey(account), Value: data}
		view[historyKey(account)] = storage.TransferRecord{Key: historyKey(account), Value: []byte("3")}
	}
	return view
}
func applySlotReplay(t *testing.T, view slotReplaySnapshot, changes []storage.CompareAndSwapMutation) slotReplaySnapshot {
	t.Helper()
	next := maps.Clone(view)
	for _, change := range changes {
		require.Equal(t, view[change.Key].Value, change.ExpectedValue)
		if change.NewValue == nil {
			delete(next, change.Key)
		} else {
			next[change.Key] = storage.TransferRecord{Key: change.Key, Value: change.NewValue}
		}
	}
	return next
}
func nativeSlotReplay(t *testing.T, backend string, before slotReplaySnapshot) (storage.KVStore, storage.ImportReconciler, []byte) {
	t.Helper()
	var store storage.KVStore
	var err error
	if backend == "badger" {
		store, err = storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20})
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err = storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: "slot-replay-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		if backend == "valkey" {
			keys, err := store.ScanWithPrefix(context.Background(), "", 0)
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, store.BatchDelete(context.Background(), keys))
			}
		}
		require.NoError(t, store.Close())
	})
	identity := ""
	if provider, ok := store.(storage.IncarnationProvider); ok {
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	claim := []byte("slot-owner-recovery")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	for _, record := range before {
		require.NoError(t, transfer.Import(t.Context(), claim, record))
	}
	return store, transfer.(storage.ImportReconciler), claim
}

func TestSlotReplayNativeOrderedRetries(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			first, other := slotClaim("first"), slotClaim("other")
			first.Attached = true
			before := slotSnapshot(t, first, other)
			store, importer, claim := nativeSlotReplay(t, backend, before)
			later := first
			later.Released = true
			added := slotClaim("later")
			added.Attached = true
			changes, err := PrepareClaimReplay(t.Context(), before, []Claim{later, added})
			require.NoError(t, err)
			repeated, err := PrepareClaimReplay(t.Context(), before, []Claim{added, later})
			require.NoError(t, err)
			require.Equal(t, changes, repeated, "input order cannot change native receipt identity")
			firstReceipt, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
			require.NoError(t, err)
			view := applySlotReplay(t, before, changes)
			added.Released = true
			next, err := PrepareClaimReplay(t.Context(), view, []Claim{added})
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, 2, firstReceipt, strings.Repeat("b", 64), next)
			require.NoError(t, err)
			retry, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), repeated)
			require.NoError(t, err)
			require.Equal(t, firstReceipt, retry)
			meter, err := Open(store)
			require.NoError(t, err)
			count, err := meter.Total(t.Context(), "account")
			require.NoError(t, err)
			require.EqualValues(t, 1, count, "retry cannot restore old capacity or release the other claim")
			retained, err := meter.Get(t.Context(), other.Account, other.ID)
			require.NoError(t, err)
			require.Equal(t, other, retained)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
		})
	}
}

func TestSlotReplayRefusesUnsafeState(t *testing.T) {
	for _, mode := range []string{"account", "job", "kind", "bound", "created", "detach", "unrelease", "attach-released", "missing-counter", "missing-history", "expiring", "wrong-key", "underflow", "mixed-delta-underflow", "unknown-claim-field", "unknown-counter-field", "duplicate", "stage-active", "canceled", "too-many"} {
		t.Run(mode, func(t *testing.T) {
			before := slotClaim("one")
			before.Attached = true
			if mode == "unrelease" || mode == "attach-released" {
				before.Released = true
			}
			if mode == "attach-released" {
				before.Attached = false
			}
			view := slotSnapshot(t, before)
			after := before
			switch mode {
			case "account":
				after.Account = "other"
			case "job":
				after.JobID = "other"
			case "kind":
				after.Kind = "batch"
			case "bound":
				after.Bound++
			case "created":
				after.CreatedAt = after.CreatedAt.Add(time.Second)
			case "detach":
				after.Attached = false
			case "unrelease":
				after.Released = false
			case "attach-released":
				after.Attached = true
			case "missing-counter":
				delete(view, countKey(before.Account))
			case "missing-history":
				delete(view, historyKey(before.Account))
			case "expiring":
				r := view[claimKey(before.Account, before.ID)]
				r.ExpiresAtMillis = 1
				view[r.Key] = r
			case "wrong-key":
				r := view[claimKey(before.Account, before.ID)]
				r.Key = "other"
				view[claimKey(before.Account, before.ID)] = r
			case "underflow", "mixed-delta-underflow":
				r := view[countKey(before.Account)]
				r.Value = []byte(`{"version":3,"total":0}`)
				view[r.Key] = r
				after.Released = true
			case "unknown-claim-field", "unknown-counter-field":
				key := claimKey(before.Account, before.ID)
				if mode == "unknown-counter-field" {
					key = countKey(before.Account)
				}
				r := view[key]
				r.Value = append(append([]byte(nil), r.Value[:len(r.Value)-1]...), []byte(`,"unrecognized_history":true}`)...)
				view[key] = r
			case "stage-active":
				key := replayAccountKey(before.Account)
				view[key] = storage.TransferRecord{Key: key, Value: []byte("stage")}
			}
			later := []Claim{after}
			if mode == "duplicate" {
				later = append(later, after)
			}
			if mode == "mixed-delta-underflow" {
				later = append(later, slotClaim("new"))
			}
			if mode == "too-many" {
				later = make([]Claim, 65)
			}
			ctx := t.Context()
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := PrepareClaimReplay(ctx, view, later)
			require.Error(t, err)
		})
	}
}

func TestSlotReplayNormalizesClaimTime(t *testing.T) {
	before := slotClaim("one")
	view := slotSnapshot(t, before)
	after := before
	after.CreatedAt = after.CreatedAt.In(time.FixedZone("offset", 3600))
	after.Attached = true
	first, err := PrepareClaimReplay(t.Context(), view, []Claim{after})
	require.NoError(t, err)
	after.CreatedAt = before.CreatedAt
	second, err := PrepareClaimReplay(t.Context(), view, []Claim{after})
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestSlotAccountReplaySpansNativeSteps(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			claims := make([]Claim, 147)
			for i := range claims {
				claims[i] = slotClaim(fmt.Sprintf("%04d", i))
				claims[i].Attached = true
				claims[i].Released = i%3 == 0
			}
			independent := slotSnapshot(t, claims...)
			state, err := CaptureAccountReplayState(t.Context(), independent, "account")
			require.NoError(t, err)
			require.EqualValues(t, 147, state.Claims)
			require.EqualValues(t, 98, state.Held)
			before := slotReplaySnapshot{}
			store, importer, claim := nativeSlotReplay(t, backend, before)
			previous := ""
			sequence := int64(1)
			var first []storage.CompareAndSwapMutation
			firstReceipt := ""
			for offset := 0; offset < len(claims); offset += 64 {
				changes, err := PrepareAccountReplay(t.Context(), before, state, claims[offset:min(offset+64, len(claims))])
				require.NoError(t, err)
				repeated, err := PrepareAccountReplay(t.Context(), before, state, claims[offset:min(offset+64, len(claims))])
				require.NoError(t, err)
				require.Equal(t, changes, repeated)
				receipt, err := importer.ReconcileImport(t.Context(), claim, sequence, previous, strings.Repeat("a", 64), changes)
				require.NoError(t, err)
				if first == nil {
					first, firstReceipt = changes, receipt
				}
				before = applySlotReplay(t, before, changes)
				previous = receipt
				sequence++
				_, err = store.Get(t.Context(), countKey("account"))
				require.ErrorIs(t, err, storage.ErrNotFound)
				meter, err := Open(store)
				require.NoError(t, err)
				_, err = meter.Total(t.Context(), "account")
				require.ErrorIs(t, err, ErrHistoryUnknown)
				if offset == 0 {
					_, err = FinalizeAccountReplay(t.Context(), before, state)
					require.ErrorIs(t, err, ErrHistoryUnknown)
				}
			}
			final, err := FinalizeAccountReplay(t.Context(), before, state)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, sequence, previous, strings.Repeat("b", 64), final)
			require.NoError(t, err)
			retry, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), first)
			require.NoError(t, err)
			require.Equal(t, firstReceipt, retry)
			meter, err := Open(store)
			require.NoError(t, err)
			count, err := meter.Total(t.Context(), "account")
			require.NoError(t, err)
			require.EqualValues(t, 98, count)
			_, err = store.Get(t.Context(), replayAccountKey("account"))
			require.ErrorIs(t, err, storage.ErrNotFound)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
			view := applySlotReplay(t, before, final)
			actual, err := CaptureAccountReplayState(t.Context(), view, "account")
			require.NoError(t, err)
			require.Equal(t, state, actual)
			_, err = PrepareAccountReplay(t.Context(), view, state, claims[:1])
			require.Error(t, err, "completed state cannot become a new staging authority")
		})
	}
}
