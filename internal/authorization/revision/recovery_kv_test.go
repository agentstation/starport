package revision

import (
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRevisionRecoveryKVNativeAtomicReplacement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		for _, present := range []bool{false, true} {
			name := backend + "/absent"
			if present {
				name = backend + "/present"
			}
			t.Run(name, func(t *testing.T) {
				source := permissionStore(t, backend)
				if present {
					raw, err := json.Marshal(Stamp{Epoch: "old-epoch", Sequence: math.MaxUint64})
					require.NoError(t, err)
					require.NoError(t, source.Set(t.Context(), StorageKey, raw))
				}
				require.NoError(t, source.Set(t.Context(), "policy", []byte("allowed")))
				before := capturePermissions(t, source)
				stamp, digest, err := CaptureKVRecovery(t.Context(), before)
				require.NoError(t, err)
				require.Equal(t, present, stamp != nil)
				transition, err := NewKVRecoveryTransition(digest, RecoveryAuthority{RecoveryID: "accepted-operation", Epoch: "accepted-fresh-epoch"})
				require.NoError(t, err)
				mutation, err := PrepareKVRecovery(t.Context(), before, transition)
				require.NoError(t, err)
				retry, err := PrepareKVRecovery(t.Context(), before, transition)
				require.NoError(t, err)
				require.Equal(t, mutation, retry)
				target, reconcile, claim := importedPermissions(t, backend, before)
				failed := []storage.CompareAndSwapMutation{mutation, {Key: "policy", ExpectedValue: []byte("wrong"), NewValue: []byte("withdrawn")}}
				evidence, err := transition.Digest()
				require.NoError(t, err)
				_, err = reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, failed)
				require.ErrorIs(t, err, storage.ErrConflict)
				got, err := NewKV(target, nil).Read(t.Context())
				if present {
					require.NoError(t, err)
					require.Equal(t, *stamp, got)
				} else {
					require.ErrorIs(t, err, storage.ErrNotFound)
				}
				changes := []storage.CompareAndSwapMutation{mutation, {Key: "policy", ExpectedValue: []byte("allowed"), NewValue: []byte("withdrawn")}}
				receipt, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
				require.NoError(t, err)
				again, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", evidence, changes)
				require.NoError(t, err)
				require.Equal(t, receipt, again)
				got, err = NewKV(target, nil).Read(t.Context())
				require.NoError(t, err)
				require.Equal(t, Stamp{Epoch: "accepted-fresh-epoch", Sequence: 1}, got)
				value, err := target.Get(t.Context(), "policy")
				require.NoError(t, err)
				require.Equal(t, []byte("withdrawn"), value)
				require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
			})
		}
	}
}

type failedRevisionSource struct{ err error }

func (s failedRevisionSource) ReadCaptured(context.Context, string, int) (storage.TransferRecord, error) {
	return storage.TransferRecord{}, s.err
}

func TestRevisionRecoveryKVRefusesUncertainOrUnsupportedPreimages(t *testing.T) {
	for _, cause := range []error{storage.ErrValueTooLarge, context.DeadlineExceeded, errors.New("unavailable")} {
		stamp, digest, err := CaptureKVRecovery(t.Context(), failedRevisionSource{cause})
		require.ErrorIs(t, err, cause)
		require.Nil(t, stamp)
		require.Empty(t, digest)
		transition, err := NewKVRecoveryTransition("", RecoveryAuthority{RecoveryID: "recovery", Epoch: "fresh"})
		require.NoError(t, err)
		_, err = PrepareKVRecovery(t.Context(), failedRevisionSource{cause}, transition)
		require.ErrorIs(t, err, cause)
	}
	for _, raw := range []string{`{"epoch":"old","sequence":1,"future":true}`, `{"epoch":"old","epoch":"other","sequence":1}`, `{"epoch":"old","sequence":0}`, strings.Repeat("x", 1025)} {
		_, _, err := CaptureKVRecovery(t.Context(), permissionSnapshot{StorageKey: {Key: StorageKey, Value: []byte(raw)}})
		require.Error(t, err)
	}
	source := permissionSnapshot{StorageKey: {Key: StorageKey, Value: []byte("{\n\"epoch\":\"old\",\"sequence\":1\n}")}}
	_, digest, err := CaptureKVRecovery(t.Context(), source)
	require.NoError(t, err)
	for _, value := range []struct{ expected, epoch string }{{digest, "old"}, {"", "fresh"}, {strings.Repeat("0", 64), "fresh"}} {
		transition, err := NewKVRecoveryTransition(value.expected, RecoveryAuthority{RecoveryID: "recovery", Epoch: value.epoch})
		require.NoError(t, err)
		_, err = PrepareKVRecovery(t.Context(), source, transition)
		require.ErrorIs(t, err, ErrRecoveryConflict)
	}
	transition, err := NewKVRecoveryTransition(digest, RecoveryAuthority{RecoveryID: "recovery", Epoch: "fresh"})
	require.NoError(t, err)
	mutation, err := PrepareKVRecovery(t.Context(), source, transition)
	require.NoError(t, err)
	require.Equal(t, source[StorageKey].Value, mutation.ExpectedValue)
	record := source[StorageKey]
	record.ExpiresAtMillis = 1
	source[StorageKey] = record
	_, _, err = CaptureKVRecovery(t.Context(), source)
	require.ErrorIs(t, err, ErrRecoveryConflict)
}
