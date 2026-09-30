package catalog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// This fixture uses real persistent native import receipts. It does not qualify
// the separate SQL recovery guard or grant permission to serve requests.
type topologyImportTestTarget struct {
	kv        storage.KVStore
	transfer  storage.RecordTransfer
	claim     []byte
	position  storage.ImportReplayPosition
	completed map[string]storage.ImportReplayPosition
	failAfter bool
}

func topologyPersistentImportTarget(t *testing.T, source *recovery.KVSnapshotView) *topologyImportTestTarget {
	t.Helper()
	kv, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, MemTableSize: 8 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	transfer, err := storage.OpenRecordTransfer(t.Context(), kv, "")
	require.NoError(t, err)
	target := &topologyImportTestTarget{kv: kv, transfer: transfer, claim: []byte("sealed-topology-native-import"), completed: map[string]storage.ImportReplayPosition{}}
	require.NoError(t, transfer.Claim(t.Context(), target.claim))
	require.NoError(t, source.Enumerate(t.Context(), func(record storage.TransferRecord) error { return transfer.Import(t.Context(), target.claim, record) }))
	return target
}

func (t *topologyImportTestTarget) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	record, err := t.transfer.(storage.ImportRecordReader).ReadImportAt(ctx, t.claim, t.position, key, limit)
	return record.Value, err
}
func (t *topologyImportTestTarget) CompletedCatalogTopology(ctx context.Context, digest string, index int) (bool, error) {
	if err := t.transfer.(storage.ImportPositionInspector).CheckImportPosition(ctx, t.claim, t.position); err != nil {
		return false, err
	}
	_, ok := t.completed[fmt.Sprint(digest, "/", index)]
	return ok, nil
}
func (t *topologyImportTestTarget) ApplyCatalogTopology(ctx context.Context, digest string, index int, mutations []storage.CompareAndSwapMutation) error {
	return t.apply(ctx, digest, index, func(sequence int64, previous, evidence string) (string, error) {
		return t.transfer.(storage.ImportReconciler).ReconcileImport(ctx, t.claim, sequence, previous, evidence, mutations)
	})
}
func (t *topologyImportTestTarget) ApplyExpiringCatalogTopology(ctx context.Context, digest string, index int, records []storage.TransferRecord) error {
	return t.apply(ctx, digest, index, func(sequence int64, previous, evidence string) (string, error) {
		return t.transfer.(storage.ImportExpiringRetirer).ReconcileExpiringImport(ctx, t.claim, sequence, previous, evidence, records)
	})
}
func (t *topologyImportTestTarget) apply(ctx context.Context, digest string, index int, commit func(int64, string, string) (string, error)) error {
	id := fmt.Sprint(digest, "/", index)
	sequence, previous := t.position.Sequence+1, t.position.ReceiptSHA256
	prior, done := t.completed[id]
	if done {
		sequence = prior.Sequence
		previous = ""
		if sequence > 1 {
			earlier, ok := t.completed[fmt.Sprint(digest, "/", index-1)]
			if !ok {
				return storage.ErrConflict
			}
			previous = earlier.ReceiptSHA256
		}
	} else if int64(index) != t.position.Sequence {
		return storage.ErrConflict
	}
	receipt, err := commit(sequence, previous, payloadDigest([]byte(id)))
	if err != nil {
		return err
	}
	if done {
		if receipt != prior.ReceiptSHA256 {
			return storage.ErrConflict
		}
		return nil
	}
	t.position = storage.ImportReplayPosition{Sequence: sequence, ReceiptSHA256: receipt}
	t.completed[id] = t.position
	if t.failAfter {
		t.failAfter = false
		return errors.New("lost committed native import reply")
	}
	return nil
}

func (t *topologyImportTestTarget) Enumerate(ctx context.Context, yield func(storage.TransferRecord) error) error {
	return t.transfer.(storage.ImportInspector).InspectImport(ctx, t.claim, t.position, yield)
}
func (t *topologyImportTestTarget) ExpiryResolution() time.Duration {
	return t.transfer.ExpiryResolution()
}
func (target *topologyImportTestTarget) captured(t *testing.T) *recovery.KVSnapshotView {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	destination := filepath.Join(parent, "snapshot")
	receipt, err := recovery.SnapshotKV(t.Context(), target, destination)
	require.NoError(t, err)
	view, err := recovery.OpenKVSnapshot(t.Context(), recovery.KVSnapshotPath(destination), parent, receipt)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	return view
}

func TestTopologyExpiringNativeRetirementKeepsSealedInputsAndLaterState(t *testing.T) {
	for _, mode := range []string{"original", "expired", "lost-reply", "future-absence", "changed-bytes", "stale-cursor", "wrong-claim"} {
		t.Run(mode, func(t *testing.T) {
			ttl := time.Hour
			if mode == "expired" {
				ttl = 2 * time.Second
			}
			compiled, source, prefix := topologyExpiringFixture(t, ttl)
			target := topologyPersistentImportTarget(t, source)
			expiringIndex := -1
			for index, stage := range compiled.stages {
				if len(stage.expiring) != 0 {
					expiringIndex = index
					break
				}
			}
			require.GreaterOrEqual(t, expiringIndex, 0)
			// The native owner refuses a future stage before its exact original cursor.
			require.Error(t, compiled.ApplyStage(t.Context(), expiringIndex, target))
			for index := 0; index < expiringIndex; index++ {
				require.NoError(t, compiled.ApplyStage(t.Context(), index, target))
			}
			original := compiled.stages[expiringIndex].expiring[0]
			switch mode {
			case "expired":
				delay := time.Until(time.UnixMilli(original.ExpiresAtMillis).Add(time.Second))
				if delay > 0 {
					timer := time.NewTimer(delay)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					}
				}
			case "lost-reply":
				target.failAfter = true
			case "future-absence":
				require.NoError(t, target.kv.Delete(t.Context(), original.Key))
			case "changed-bytes":
				require.NoError(t, target.kv.SetWithTTL(t.Context(), original.Key, []byte("different captured control"), time.Hour))
			case "stale-cursor":
				_, err := target.transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), target.claim, target.position.Sequence+1, target.position.ReceiptSHA256, strings.Repeat("f", 64), []storage.CompareAndSwapMutation{{Key: "catalog:independent:withdrawal", NewValue: []byte("later")}})
				require.NoError(t, err)
			case "wrong-claim":
				target.claim = []byte("different-native-import")
			}
			before := target.position
			err := compiled.ApplyStage(t.Context(), expiringIndex, target)
			if mode == "future-absence" || mode == "changed-bytes" || mode == "stale-cursor" || mode == "wrong-claim" {
				require.Error(t, err)
				require.Equal(t, before, target.position, "refusal cannot publish a new replay cursor")
				return
			}
			if mode == "lost-reply" {
				require.ErrorContains(t, err, "lost committed native import reply")
			} else {
				require.NoError(t, err)
			}
			exact := target.position
			require.NoError(t, compiled.ApplyStage(t.Context(), expiringIndex, target))
			require.Equal(t, exact, target.position)
			for index := expiringIndex + 1; index < compiled.StageCount(); index++ {
				require.NoError(t, compiled.ApplyStage(t.Context(), index, target))
			}
			proof := &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("e", 64)}
			require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
			require.Equal(t, int64(compiled.StageCount()+1), target.position.Sequence)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.kv), storage.ErrImportRestricted)
			require.NoError(t, InspectCapturedCatalog(t.Context(), target.captured(t), compiled.request.DestinationBoundary))
			require.ErrorIs(t, func() error { _, err := target.kv.Get(t.Context(), prefix+"maintenance"); return err }(), storage.ErrNotFound)
			// An old exact receipt must not delete a later independent control or withdrawal.
			later, err := target.transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), target.claim, target.position.Sequence+1, target.position.ReceiptSHA256, strings.Repeat("f", 64), []storage.CompareAndSwapMutation{{Key: original.Key, NewValue: []byte("later independent control")}})
			require.NoError(t, err)
			target.position = storage.ImportReplayPosition{Sequence: target.position.Sequence + 1, ReceiptSHA256: later}
			require.NoError(t, compiled.ApplyStage(t.Context(), expiringIndex, target))
			require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
			value, err := target.kv.Get(t.Context(), original.Key)
			require.NoError(t, err)
			require.Equal(t, []byte("later independent control"), value)
			require.Error(t, compiled.CheckSelection(t.Context(), proof, target))
		})
	}
}
