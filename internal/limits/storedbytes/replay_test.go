package storedbytes

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type byteSnapshot map[string]storage.TransferRecord

func (s byteSnapshot) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	r, ok := s[key]
	if !ok {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(r.Value) > maximum {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return r, nil
}
func (s byteSnapshot) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	for _, k := range slices.Sorted(maps.Keys(s)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(s[k]); err != nil {
			return err
		}
	}
	return nil
}
func byteEvidence(t *testing.T, claims ...RecoveryClaim) byteSnapshot {
	t.Helper()
	s := byteSnapshot{}
	var total int64
	for _, c := range claims {
		data, err := json.Marshal(c)
		require.NoError(t, err)
		key := byteClaimKey(c.Holder, c.ID)
		s[key] = storage.TransferRecord{Key: key, Value: data}
		if !c.Released {
			total += c.Bytes
		}
	}
	data, err := json.Marshal(byteTotal{Version: 2, Bytes: total})
	require.NoError(t, err)
	key := byteTotalKey("account")
	s[key] = storage.TransferRecord{Key: key, Value: data}
	return s
}
func byteClaimEvidence(id string) RecoveryClaim {
	return RecoveryClaim{Version: 2, Holder: "account", ID: id, Initial: 10, Bytes: 10, Bound: 10000, CreatedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
}
func applyByteMutations(t *testing.T, s byteSnapshot, changes []storage.CompareAndSwapMutation) byteSnapshot {
	t.Helper()
	next := maps.Clone(s)
	for _, m := range changes {
		require.Equal(t, s[m.Key].Value, m.ExpectedValue)
		if m.NewValue == nil {
			delete(next, m.Key)
		} else {
			next[m.Key] = storage.TransferRecord{Key: m.Key, Value: m.NewValue}
		}
	}
	return next
}
func nativeByteImport(t *testing.T, kind string, before byteSnapshot) (storage.KVStore, storage.ImportReconciler, []byte) {
	t.Helper()
	var store storage.KVStore
	var err error
	if kind == "badger" {
		store, err = storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20})
	} else {
		url := os.Getenv("TEST_VALKEY_URL")
		if url == "" {
			t.Skip("UNVERIFIED: native byte replay requires Valkey")
		}
		store, err = storage.OpenValkey(storage.ValkeyConfig{URL: url, DeploymentID: "byte-replay-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		if kind == "valkey" {
			keys, err := store.ScanWithPrefix(context.Background(), "", 0)
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, store.BatchDelete(context.Background(), keys))
			}
		}
		require.NoError(t, store.Close())
	})
	incarnation := ""
	if p, ok := store.(storage.IncarnationProvider); ok {
		incarnation, err = p.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	target, err := storage.OpenRecordTransfer(t.Context(), store, incarnation)
	require.NoError(t, err)
	claim := []byte("stored-bytes-recovery")
	require.NoError(t, target.Claim(t.Context(), claim))
	for _, r := range before {
		require.NoError(t, target.Import(t.Context(), claim, r))
	}
	return store, target.(storage.ImportReconciler), claim
}

func TestStoredByteReplayNativeCensusAndExactRetry(t *testing.T) {
	for _, kind := range []string{"badger", "valkey"} {
		t.Run(kind, func(t *testing.T) {
			old := byteClaimEvidence("old")
			old.Attached = true
			before := byteEvidence(t, old)
			later := old
			later.Measured = true
			later.Bytes = 4
			later.Released = true
			claims := []RecoveryClaim{later}
			for i := range 70 {
				claims = append(claims, byteClaimEvidence(fmt.Sprint(i)))
			}
			independent := byteEvidence(t, claims...)
			state, err := CaptureAccountReplayState(t.Context(), independent, "account")
			require.NoError(t, err)
			store, target, claim := nativeByteImport(t, kind, before)
			step1, err := PrepareAccountReplay(t.Context(), before, state, claims[:64])
			require.NoError(t, err)
			receipt, err := target.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), step1)
			require.NoError(t, err)
			intermediate := applyByteMutations(t, before, step1)
			_, err = FinalizeAccountReplay(t.Context(), intermediate, state)
			require.Error(t, err, "incomplete census must retain original total")
			step2, err := PrepareAccountReplay(t.Context(), intermediate, state, claims[64:])
			require.NoError(t, err)
			receipt2, err := target.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), step2)
			require.NoError(t, err)
			complete := applyByteMutations(t, intermediate, step2)
			final, err := FinalizeAccountReplay(t.Context(), complete, state)
			require.NoError(t, err)
			_, err = target.ReconcileImport(t.Context(), claim, 3, receipt2, strings.Repeat("c", 64), final)
			require.NoError(t, err)
			again, err := target.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), step1)
			require.NoError(t, err)
			require.Equal(t, receipt, again)
			total, err := store.Get(t.Context(), byteTotalKey("account"))
			require.NoError(t, err)
			item, err := VerifyRecoveryRecord(storage.TransferRecord{Key: byteTotalKey("account"), Value: total})
			require.NoError(t, err)
			require.Equal(t, state.Bytes, *item.Total)
			_, err = store.Get(t.Context(), replayKey("account"))
			require.ErrorIs(t, err, storage.ErrNotFound)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
		})
	}
}

func TestStoredByteReplayRefusesMissingMismatchedAndRegressedHistory(t *testing.T) {
	original := byteClaimEvidence("one")
	original.Attached = true
	original.Measured = true
	original.Bytes = 8
	source := byteEvidence(t, original)
	state, err := CaptureAccountReplayState(t.Context(), source, "account")
	require.NoError(t, err)
	missing := maps.Clone(source)
	delete(missing, byteTotalKey("account"))
	_, err = CaptureAccountReplayState(t.Context(), missing, "account")
	require.Error(t, err)
	bad := maps.Clone(source)
	raw := bad[byteTotalKey("account")]
	raw.Value = []byte(`{"version":2,"bytes":0}`)
	bad[raw.Key] = raw
	_, err = CaptureAccountReplayState(t.Context(), bad, "account")
	require.Error(t, err)
	for name, edit := range map[string]func(*RecoveryClaim){"resize-measured": func(c *RecoveryClaim) { c.Bytes++ }, "unattach": func(c *RecoveryClaim) { c.Attached = false }, "new-expiry-origin": func(c *RecoveryClaim) { c.CreatedAt = c.CreatedAt.Add(time.Second) }, "bound": func(c *RecoveryClaim) { c.Bound++ }} {
		t.Run(name, func(t *testing.T) {
			next := original
			edit(&next)
			_, err := PrepareAccountReplay(t.Context(), source, state, []RecoveryClaim{next})
			require.Error(t, err)
		})
	}
	_, err = PrepareAccountReplay(t.Context(), source, state, []RecoveryClaim{original, original})
	require.Error(t, err)
	extra := byteClaimEvidence("omitted")
	withExtra := byteEvidence(t, original, extra)
	staged, err := PrepareAccountReplay(t.Context(), withExtra, state, []RecoveryClaim{original})
	require.NoError(t, err)
	_, err = FinalizeAccountReplay(t.Context(), applyByteMutations(t, withExtra, staged), state)
	require.Error(t, err)
	first, second := byteClaimEvidence("max"), byteClaimEvidence("overflow")
	first.Initial = math.MaxInt64
	first.Bytes = math.MaxInt64
	overflow := byteEvidence(t, first)
	data, err := json.Marshal(second)
	require.NoError(t, err)
	key := byteClaimKey(second.Holder, second.ID)
	overflow[key] = storage.TransferRecord{Key: key, Value: data}
	_, err = CaptureAccountReplayState(t.Context(), overflow, "account")
	require.Error(t, err)
}

func TestStoredByteRecoveryAttachmentsAndStrictSchemas(t *testing.T) {
	c := byteClaimEvidence("file")
	c.Attached = true
	c.Measured = true
	f := RecoveryAttachment{Holder: c.Holder, FileID: c.ID, Metered: true, Ready: true, Bytes: c.Bytes}
	require.NoError(t, VerifyRecoveryAttachment(&c, &f))
	require.Error(t, VerifyRecoveryAttachment(nil, &f))
	require.Error(t, VerifyRecoveryAttachment(&c, nil))
	c.Released = true
	require.Error(t, VerifyRecoveryAttachment(&c, &f))
	f.Ready = false
	f.Deleting = true
	f.Retired = true
	require.NoError(t, VerifyRecoveryAttachment(&c, &f))
	require.NoError(t, VerifyRecoveryAttachment(&c, nil))
	f.FileID = "other"
	require.Error(t, VerifyRecoveryAttachment(&c, &f))
	f.FileID = c.ID
	f.Metered = false
	require.Error(t, VerifyRecoveryAttachment(&c, &f))
	data, err := json.Marshal(c)
	require.NoError(t, err)
	key := byteClaimKey(c.Holder, c.ID)
	for _, raw := range []storage.TransferRecord{{Key: key, Value: append(data[:len(data)-1], []byte(`,"future":true}`)...)}, {Key: key, Value: data, ExpiresAtMillis: 1}, {Key: byteClaimKey("other", c.ID), Value: data}} {
		_, err := VerifyRecoveryRecord(raw)
		require.Error(t, err)
	}
}

func TestStoredByteReplayNativePreservesAbsentTotalPreimage(t *testing.T) {
	for _, kind := range []string{"badger", "valkey"} {
		t.Run(kind, func(t *testing.T) {
			independent := byteEvidence(t, byteClaimEvidence("one"))
			state, err := CaptureAccountReplayState(t.Context(), independent, "account")
			require.NoError(t, err)
			before := byteSnapshot{}
			store, target, claim := nativeByteImport(t, kind, before)
			staged, err := PrepareAccountReplay(t.Context(), before, state, []RecoveryClaim{byteClaimEvidence("one")})
			require.NoError(t, err)
			receipt, err := target.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), staged)
			require.NoError(t, err)
			intermediate := applyByteMutations(t, before, staged)
			final, err := FinalizeAccountReplay(t.Context(), intermediate, state)
			require.NoError(t, err)
			for _, change := range final {
				if change.Key == byteTotalKey("account") {
					require.Nil(t, change.ExpectedValue)
				}
			}
			_, err = target.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), final)
			require.NoError(t, err)
			meter, err := NewStorageMeter(store)
			require.NoError(t, err)
			total, err := meter.Total(t.Context(), "account")
			require.NoError(t, err)
			require.Equal(t, state.Bytes, total)
		})
	}
}
