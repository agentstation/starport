package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func capturedCatalogView(t *testing.T, kv storage.KVStore) *recovery.KVSnapshotView {
	t.Helper()
	identity := ""
	if provider, ok := kv.(storage.IncarnationProvider); ok {
		var err error
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	source, err := storage.OpenRecordTransfer(t.Context(), kv, identity)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "private")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	destination := filepath.Join(parent, "snapshot")
	receipt, err := recovery.SnapshotKV(t.Context(), source, destination)
	require.NoError(t, err)
	view, err := recovery.OpenKVSnapshot(t.Context(), recovery.KVSnapshotPath(destination), parent, receipt)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	return view
}

func TestCapturedCatalogChecksNativeGenerationReferences(t *testing.T) {
	modes := []string{"valid", "candidate-ahead", "index-behind", "orphan-chunk", "missing-current", "missing-chunk", "corrupt-chunk", "expired-record", "negative-size", "oversized-size", "wrong-chunk-size", "wrong-id", "history-checksum", "history-time", "duplicate-history", "unknown-schema"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			kv := openInMemoryBadger(t)
			owner, err := NewGenerationStore(kv)
			require.NoError(t, err)
			generation := runtimeTestGeneration(t, "accepted", testEmptyCatalog(t, "test"), time.Now().UTC())
			require.NoError(t, owner.Commit(t.Context(), generation, ""))
			data, err := kv.Get(t.Context(), catalogGenerationKey("accepted"))
			require.NoError(t, err)
			record, err := decodeGenerationRecord(data, "accepted")
			require.NoError(t, err)
			switch mode {
			case "candidate-ahead":
				candidate, err := newCandidateGenerationStore(kv)
				require.NoError(t, err)
				generation.Manifest.GenerationID = "candidate"
				require.NoError(t, candidate.Commit(t.Context(), generation, ""))
			case "index-behind":
				require.NoError(t, kv.Delete(t.Context(), catalogGenerationIndexKey))
			case "orphan-chunk":
				require.NoError(t, kv.Set(t.Context(), catalogGenerationChunkKey(payloadDigest([]byte("unselected"))), []byte("unselected")))
			case "missing-current":
				require.NoError(t, kv.Set(t.Context(), catalogCurrentGenerationKey, []byte("absent")))
			case "missing-chunk":
				require.NoError(t, kv.Delete(t.Context(), catalogGenerationChunkKey(record.Chunks[0])))
			case "corrupt-chunk":
				require.NoError(t, kv.Set(t.Context(), catalogGenerationChunkKey(record.Chunks[0]), []byte("corrupt")))
			case "expired-record":
				require.NoError(t, kv.SetWithTTL(t.Context(), catalogCurrentGenerationKey, []byte("accepted"), time.Hour))
			case "negative-size", "oversized-size", "wrong-chunk-size":
				switch mode {
				case "negative-size":
					record.Size = -1
				case "oversized-size":
					record.Size = 1 << 62
				case "wrong-chunk-size":
					record.ChunkSize = 1
				}
				data, err = json.Marshal(record)
				require.NoError(t, err)
				require.NoError(t, kv.Set(t.Context(), catalogGenerationKey("accepted"), data))
			case "wrong-id":
				require.NoError(t, kv.Set(t.Context(), catalogGenerationKey("foreign"), data))
			case "history-checksum", "history-time", "duplicate-history":
				history, err := owner.History(t.Context())
				require.NoError(t, err)
				switch mode {
				case "history-checksum":
					history[0].PayloadChecksum = strings.Repeat("0", 64)
				case "history-time":
					history[0].GeneratedAt = history[0].GeneratedAt.Add(time.Second)
				case "duplicate-history":
					history = append(history, history[0])
				}
				data, err = json.Marshal(history)
				require.NoError(t, err)
				require.NoError(t, kv.Set(t.Context(), catalogGenerationIndexKey, data))
			case "unknown-schema":
				require.NoError(t, kv.Set(t.Context(), "catalog_generation:v2:current", []byte("accepted")))
			}
			view := capturedCatalogView(t, kv)
			before := capturedCatalogBytes(t, view)
			err = InspectCapturedCatalog(t.Context(), view, recovery.Record{DeploymentID: "standalone", Epoch: 1})
			if mode == "valid" || mode == "candidate-ahead" || mode == "index-behind" || mode == "orphan-chunk" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, capturedCatalogBytes(t, view), "inspection must preserve every captured byte and expiration")
		})
	}
}

func capturedCatalogBytes(t *testing.T, view *recovery.KVSnapshotView) []storage.TransferRecord {
	t.Helper()
	var records []storage.TransferRecord
	require.NoError(t, view.Enumerate(t.Context(), func(record storage.TransferRecord) error {
		record.Value = bytes.Clone(record.Value)
		records = append(records, record)
		return nil
	}))
	return records
}

func TestCapturedCatalogChecksContextAndBoundary(t *testing.T) {
	view := capturedCatalogView(t, openInMemoryBadger(t))
	require.Error(t, InspectCapturedCatalog(t.Context(), nil, recovery.Record{}))
	require.Error(t, InspectCapturedCatalog(t.Context(), view, recovery.Record{DeploymentID: "test", Epoch: 1, Open: true}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, InspectCapturedCatalog(ctx, view, recovery.Record{DeploymentID: "test", Epoch: 1}), context.Canceled)
	require.NoError(t, InspectCapturedCatalog(t.Context(), view, recovery.Record{DeploymentID: "test", Epoch: 1}))
}

func TestGenerationDescriptorRefusesUnsafeAllocation(t *testing.T) {
	for _, size := range []int{-1, 1 << 62} {
		record, _ := encodeGenerationPayload([]byte("content"))
		record.Size = size
		_, err := readGenerationPayload(t.Context(), storage.NewMockStore(), record, "invalid")
		require.Error(t, err)
		require.False(t, errors.Is(err, storage.ErrNotFound), "descriptor validation must precede chunk access")
	}
}
