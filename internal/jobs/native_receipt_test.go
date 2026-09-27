package jobs_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type nativeRunner struct {
	*recordingRunner
	valuation   *reservation.Valuation
	measurement *reservation.Evidence
	assetURL    string
}

func nativeFixture() *nativeRunner {
	r := acceptedRunner()
	r.asset = jobs.Asset{ContentType: "video/mp4", Bytes: []byte("measured-native-video")}
	return &nativeRunner{recordingRunner: r,
		valuation:   &reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output_seconds", Price: reservation.Price{USD: "0.075", PerUnits: 1}}}},
		measurement: &reservation.Evidence{Quantities: reservation.Quantities{"output_seconds": 5}},
	}
}

func (r *nativeRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	if err := recorder.BeforeDispatch(ctx, jobs.Dispatch{Native: true, Provider: r.acceptance.Provider, Model: r.acceptance.Model, CatalogGeneration: "generation", Valuation: r.valuation}); err != nil {
		return jobs.Acceptance{}, err
	}
	r.submits++
	if r.submitErr != nil {
		return jobs.Acceptance{}, r.submitErr
	}
	answer := r.acceptance
	answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "private-native-request", Measurement: r.measurement, Asset: r.asset, AssetURL: r.assetURL}
	return answer, recorder.Accepted(ctx, answer)
}

type interruptedNativeAssets struct {
	firstKey string
	blob.Store
	puts   int
	failAt int
	after  bool
}

func (s *interruptedNativeAssets) Put(ctx context.Context, key string, r io.Reader) (blob.Info, error) {
	s.puts++
	if s.puts == 1 {
		s.firstKey = key
	}
	if s.puts == s.failAt {
		if s.after {
			if _, err := s.Store.Put(ctx, key, r); err != nil {
				return blob.Info{}, err
			}
		}
		return blob.Info{}, errors.New("blob acknowledgement unavailable")
	}
	return s.Store.Put(ctx, key, r)
}

func TestNativeReceiptRecoversWithoutProviderReplay(t *testing.T) {
	for _, failure := range []string{"none", "record before commit", "record after commit", "receipt after commit", "asset before commit", "asset after commit"} {
		t.Run(failure, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				records, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				assets, err := blob.NewFilesystem(t.TempDir())
				require.NoError(t, err)
				var writes jobs.Repository = records
				switch failure {
				case "record before commit":
					writes = interruptedAcceptance{records, false}
				case "record after commit":
					writes = interruptedAcceptance{records, true}
				}
				blobs := &interruptedNativeAssets{Store: assets}
				switch failure {
				case "receipt after commit":
					blobs.failAt, blobs.after = 1, true
				case "asset before commit":
					blobs.failAt = 2
				case "asset after commit":
					blobs.failAt, blobs.after = 2, true
				}
				service, err := jobs.NewService(writes, jobs.WithAssetStore(blobs), jobs.WithAssetBound(4096), jobs.WithRetention(time.Hour), jobs.WithClock(func() time.Time { return submitted }))
				require.NoError(t, err)
				runner := nativeFixture()
				job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
				if failure == "none" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.NotEmpty(t, job.ID)
				// A new process can apply different settings without changing this job's promise.
				reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAssetBound(1), jobs.WithRetention(time.Minute), jobs.WithClock(func() time.Time { return submitted.Add(time.Second) }))
				require.NoError(t, err)
				recovered, err := reopened.Refresh(t.Context(), runner, accountA, job.ID)
				require.NoError(t, err)
				require.Equal(t, jobs.JobStateCompleted, recovered.State)
				require.False(t, recovered.SubmissionPending)
				require.False(t, recovered.HasProviderJob(), "a native request ID grants no polling capability")
				require.Equal(t, submitted.Add(time.Hour), recovered.AssetExpiresAt)
				require.Equal(t, runner.valuation, recovered.Valuation)
				require.Equal(t, job.ID+":usage", recovered.Measurement.ID)
				require.Equal(t, runner.measurement.Quantities, recovered.Measurement.Quantities)
				_, reader, err := reopened.Open(t.Context(), accountA, job.ID)
				require.NoError(t, err)
				data, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				require.Equal(t, runner.asset.Bytes, data)
				_, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
				require.NoError(t, err)
				_, err = reopened.Cancel(t.Context(), runner, accountA, job.ID)
				require.ErrorIs(t, err, jobs.ErrNativeCancellationUnsupported)
				_, err = reopened.Get(t.Context(), accountB, job.ID)
				require.ErrorIs(t, err, jobs.ErrJobNotFound)
				require.Equal(t, 1, runner.submits)
				require.Zero(t, runner.polls)
				require.Zero(t, runner.fetches)
				require.Zero(t, runner.cancels)
			})
		})
	}
}

func TestNativeLostResponseRetainsUncertainJob(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.submitErr = errors.New("response lost")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
		recovered, err := service.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.True(t, recovered.SubmissionPending)
		require.Nil(t, recovered.Measurement)
		require.False(t, recovered.Accounted())
		result, err := service.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, result.AwaitingReconciliation)
		require.Equal(t, 1, runner.submits)
		require.Zero(t, runner.polls)
	})
}

func TestNativeMeasurementSurvivesAbsentPrice(t *testing.T) {
	service, _, _, _ := newAssetService(t)
	runner := nativeFixture()
	runner.valuation = nil
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.Nil(t, job.Valuation)
	require.Equal(t, int64(5), job.Measurement.Quantities["output_seconds"])
}

func TestNativeReceiptRejectsCorruptionBeforeCompletion(t *testing.T) {
	for _, damage := range []string{"account", "asset", "header size", "measurement identity"} {
		t.Run(damage, func(t *testing.T) {
			_, records, assets, _ := newAssetService(t)
			blobs := &interruptedNativeAssets{Store: assets}
			service, err := jobs.NewService(interruptedAcceptance{records, false}, jobs.WithAssetStore(blobs))
			require.NoError(t, err)
			runner := nativeFixture()
			job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
			require.Error(t, err)
			reader, err := assets.Get(t.Context(), blobs.firstKey)
			require.NoError(t, err)
			raw, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			switch damage {
			case "asset":
				raw[len(raw)-1] ^= 1
			case "header size":
				binary.BigEndian.PutUint64(raw[:8], 1<<30)
			default:
				size := binary.BigEndian.Uint64(raw[:8])
				var header map[string]any
				require.NoError(t, json.Unmarshal(raw[8:8+size], &header))
				if damage == "account" {
					header["account"] = accountB
				} else {
					header["measurement"].(map[string]any)["id"] = "another:usage"
				}
				encoded, err := json.Marshal(header)
				require.NoError(t, err)
				combined := make([]byte, 8)
				binary.BigEndian.PutUint64(combined, uint64(len(encoded)))
				combined = append(combined, encoded...)
				raw = append(combined, raw[8+size:]...)
			}
			_, err = assets.Put(t.Context(), blobs.firstKey, bytes.NewReader(raw))
			require.NoError(t, err)
			reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets))
			require.NoError(t, err)
			_, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
			require.Error(t, err)
			stored, err := records.Get(t.Context(), accountA, job.ID)
			require.NoError(t, err)
			require.True(t, stored.SubmissionPending)
			require.Nil(t, stored.Measurement)
			require.False(t, stored.HasAsset())
			require.Equal(t, 1, runner.submits)
			require.Zero(t, runner.polls)
		})
	}
}

func TestNativeExternalAssetExpiresWithoutDownload(t *testing.T) {
	service, _, _, clock := newAssetService(t)
	runner := nativeFixture()
	runner.asset = jobs.Asset{}
	runner.assetURL = "https://assets.example/video.mp4?signature=private"
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, job.State)
	clock.now = submitted.Add(retentionWindow)
	_, _, err = service.Open(t.Context(), accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrAssetExpired)
	require.Equal(t, 1, runner.submits)
}
