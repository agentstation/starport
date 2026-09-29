package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type videoReplayRecords map[string]storage.TransferRecord

func (v videoReplayRecords) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	record, ok := v[key]
	if !ok {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(record.Value) > bound {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return record, nil
}
func videoReplayEvidence(t *testing.T, job Job) (videoReplayRecords, RecoveryJob) {
	t.Helper()
	data, err := encodeJob(job)
	require.NoError(t, err)
	source := videoReplayRecords{storageKey(job.Account, job.ID): {Key: storageKey(job.Account, job.ID), Value: data}}
	result, err := CaptureRecoveryJob(t.Context(), source, job.Account, job.ID)
	require.NoError(t, err)
	return source, result
}

func TestVideoJobReplayRetainsPrivateProviderAndAssets(t *testing.T) {
	at := time.Now().UTC()
	job, err := New("job", "owner", "provider", "provider/model", routing.OperationVideosGenerations, at.Add(-time.Minute))
	require.NoError(t, err)
	job.CatalogGeneration = "generation"
	require.NoError(t, job.AdoptProviderJob("private-provider-id"))
	before, _ := videoReplayEvidence(t, job)
	require.NoError(t, job.Transition(JobStateCompleted, at))
	payload := []byte("retained video")
	digest := sha256.Sum256(payload)
	require.NoError(t, job.StoreAsset("asset", "video/mp4", int64(len(payload)), at.Add(time.Hour)))
	job.assetDigest = hex.EncodeToString(digest[:])
	_, next := videoReplayEvidence(t, job)
	assets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	_, err = PrepareJobReplay(t.Context(), before, before, assets, at, next)
	require.Error(t, err)
	_, err = assets.Publish(t.Context(), "asset", bytes.NewReader(payload))
	require.NoError(t, err)
	change, err := PrepareJobReplay(t.Context(), before, before, assets, at, next)
	require.NoError(t, err)
	restored, err := decodeJob(change.NewValue)
	require.NoError(t, err)
	require.Equal(t, "private-provider-id", restored.providerJobID)
	require.Equal(t, job, restored)
	bad := job
	bad.providerJobID = "different-provider-id"
	_, badEvidence := videoReplayEvidence(t, bad)
	_, err = PrepareJobReplay(t.Context(), before, before, assets, at, badEvidence)
	require.Error(t, err)
	badAssets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	_, err = badAssets.Publish(t.Context(), "asset", bytes.NewReader([]byte("corrupt video")))
	require.NoError(t, err)
	_, err = PrepareJobReplay(t.Context(), before, before, badAssets, at, next)
	require.Error(t, err)
}

func TestVideoJobReplayKeepsUncertainNativeSubmission(t *testing.T) {
	at := time.Now().UTC()
	job, err := New("native-job", "owner", "provider", "provider/model", routing.OperationVideosGenerations, at.Add(-time.Minute))
	require.NoError(t, err)
	job.Native, job.SubmissionPending, job.CatalogGeneration = true, true, "generation"
	job.nativeReceiptKey, job.nativeAssetKey, job.nativeRetention, job.nativeAssetBound = "receipt", "asset", time.Hour, 100
	job.Valuation = &reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "1", PerUnits: 1}}}}
	_, evidence := videoReplayEvidence(t, job)
	public, err := json.Marshal(job)
	require.NoError(t, err)
	var unsupported RecoveryJob
	require.Error(t, json.Unmarshal(public, &unsupported), "public job JSON must not substitute for private recovery evidence")
	for _, mode := range []string{"missing", "retained", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			assets, err := blob.NewFilesystem(t.TempDir())
			require.NoError(t, err)
			if mode != "missing" {
				body := []byte("video")
				digest := sha256.Sum256(body)
				receipt := nativeReceipt{Version: 1, JobID: job.ID, Account: job.Account, Provider: job.Provider, Model: job.Model, Generation: job.CatalogGeneration, RequestID: "private-provider-id", State: JobStateCompleted, RecordedAt: at, AssetBytes: int64(len(body)), AssetDigest: hex.EncodeToString(digest[:])}
				header, err := json.Marshal(receipt)
				require.NoError(t, err)
				var prefix [8]byte
				binary.BigEndian.PutUint64(prefix[:], uint64(len(header)))
				data := append(append(prefix[:], header...), body...)
				if mode == "corrupt" {
					data[len(data)-1] ^= 1
				}
				_, err = assets.Publish(t.Context(), "receipt", bytes.NewReader(data))
				require.NoError(t, err)
			}
			mutation, err := PrepareJobReplay(t.Context(), videoReplayRecords{}, videoReplayRecords{}, assets, at, evidence)
			if mode == "corrupt" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			restored, err := decodeJob(mutation.NewValue)
			require.NoError(t, err)
			require.True(t, restored.SubmissionPending)
			require.Equal(t, JobStateQueued, restored.State)
			require.Empty(t, restored.providerJobID, "inspection cannot accept a provider receipt or dispatch again")
			require.Equal(t, job.Valuation, restored.Valuation)
		})
	}
}

func TestVideoJobReplayRejectsUnsupportedPrivateSchema(t *testing.T) {
	job, err := New("job", "owner", "provider", "provider/model", routing.OperationVideosGenerations, time.Now())
	require.NoError(t, err)
	source, _ := videoReplayEvidence(t, job)
	key := storageKey(job.Account, job.ID)
	original := source[key]
	for _, extension := range []string{`,"unknown_private_fact":1`, `,"state":"running"`, `,"measurement":{"id":"job:usage","quantities":{"seconds":1},"unknown_private_fact":1}`} {
		t.Run(fmt.Sprintf("extension-%d", len(extension)), func(t *testing.T) {
			data := append(bytes.Clone(original.Value[:len(original.Value)-1]), []byte(extension+"}")...)
			source[key] = storage.TransferRecord{Key: key, Value: data}
			_, err := CaptureRecoveryJob(t.Context(), source, job.Account, job.ID)
			require.Error(t, err)
		})
	}
}

func TestVideoJobReplayRetainsBudgetAfterTerminalSlotRelease(t *testing.T) {
	store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	owner, err := reservation.Open(store)
	require.NoError(t, err)
	at, err := store.AuthorityTime(t.Context())
	require.NoError(t, err)
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	require.NoError(t, owner.EstablishWindow(t.Context(), meter, at, 0, reservation.History{ID: "history", Proof: "fixture-empty"}))
	attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: string(routing.OperationVideosGenerations), Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"seconds": 100}}
	_, err = owner.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, owner.Begin(t.Context(), attempt.ID))
	require.NoError(t, owner.BindJob(t.Context(), attempt.ID, "job"))
	require.NoError(t, owner.MarkUncertain(t.Context(), attempt.ID, "provider usage unavailable"))
	transfer, err := storage.OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	after := videoReplayRecords{}
	require.NoError(t, transfer.Enumerate(t.Context(), func(record storage.TransferRecord) error { after[record.Key] = record; return nil }))
	job, err := New("job", "owner", "provider", "provider/model", routing.OperationVideosGenerations, at)
	require.NoError(t, err)
	job.KeyID, job.CatalogGeneration, job.ReservationID, job.SlotID = "key", "generation", "attempt", "slot"
	job.Valuation = &attempt.Valuation
	require.NoError(t, job.AdoptProviderJob("private-provider-id"))
	before, _ := videoReplayEvidence(t, job)
	require.NoError(t, job.Fail("provider failed without usage", at.Add(time.Second)))
	job.SlotReleased = true
	_, next := videoReplayEvidence(t, job)
	assets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	change, err := PrepareJobReplay(t.Context(), before, after, assets, at.Add(time.Second), next)
	require.NoError(t, err)
	restored, err := decodeJob(change.NewValue)
	require.NoError(t, err)
	require.True(t, restored.SlotReleased)
	require.False(t, restored.Accounted())
	record, err := reservation.ReadBackupAttempt(t.Context(), after, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, reservation.Uncertain, record.State)
	require.Nil(t, record.Evidence)
	window, err := owner.Window(t.Context(), meter, at)
	require.NoError(t, err)
	require.EqualValues(t, 100, window.Reserved)
}
