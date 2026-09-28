package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type interruptedSlotRelease struct {
	*jobslots.Store
	after  bool
	failed atomic.Bool
}

func (m *interruptedSlotRelease) Release(ctx context.Context, account, id string) error {
	if m.failed.CompareAndSwap(false, true) {
		if m.after {
			if err := m.Store.Release(ctx, account, id); err != nil {
				return err
			}
		}
		return errors.New("release acknowledgement unavailable")
	}
	return m.Store.Release(ctx, account, id)
}

func TestVideoSlotReleaseRecoversWhileReportingRemainsPending(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before commit"
		if after {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				records, err := jobs.OpenRepository(backing)
				require.NoError(t, err)
				meter, err := jobslots.Open(backing)
				require.NoError(t, err)
				ctx := t.Context()
				require.NoError(t, meter.Reserve(ctx, accountA, "other", "other-job", "batch", 2))
				interrupted := &interruptedSlotRelease{Store: meter, after: after}
				service, err := jobs.NewService(records, jobs.WithJobMeter(interrupted), jobs.WithAccountant(&recordingAccountant{err: errors.New("optional reporting unavailable")}))
				require.NoError(t, err)
				runner := acceptedRunner()
				runner.poll = jobs.Report{State: jobs.JobStateCompleted}
				job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
				require.NoError(t, err)
				ended, err := service.Refresh(ctx, runner, accountA, job.ID)
				require.NoError(t, err)
				require.False(t, ended.Accounted())
				require.False(t, ended.SlotReleased)
				reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter))
				require.NoError(t, err)
				_, err = reopened.Sweep(ctx)
				require.NoError(t, err)
				stored, err := records.Get(ctx, accountA, job.ID)
				require.NoError(t, err)
				require.True(t, stored.SlotReleased)
				_, err = reopened.Sweep(ctx)
				require.NoError(t, err)
				total, err := meter.Total(ctx, accountA)
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
			})
		})
	}
}

type refusedSlotStamp struct{ jobs.Repository }

func (r refusedSlotStamp) Replace(ctx context.Context, expected, next jobs.Job) error {
	if next.SlotReleased && !expected.SlotReleased {
		return errors.New("slot stamp unavailable")
	}
	return r.Repository.Replace(ctx, expected, next)
}

func TestVideoReleaseSurvivesLostJobStamp(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		require.NoError(t, meter.Reserve(ctx, accountA, "other", "other-job", "batch", 2))
		service, err := jobs.NewService(refusedSlotStamp{records}, jobs.WithJobMeter(meter))
		require.NoError(t, err)
		runner := acceptedRunner()
		runner.poll = jobs.Report{State: jobs.JobStateCompleted}
		job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
		require.NoError(t, err)
		_, err = service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		stored, err := records.Get(ctx, accountA, job.ID)
		require.NoError(t, err)
		require.False(t, stored.SlotReleased)
		reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter))
		require.NoError(t, err)
		_, err = reopened.Sweep(ctx)
		require.NoError(t, err)
		stored, err = records.Get(ctx, accountA, job.ID)
		require.NoError(t, err)
		require.True(t, stored.SlotReleased)
		total, err := meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
	})
}

func TestCancelledBatchRetainsItsSlotUntilLinesDrain(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenBatchRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		service, err := jobs.NewBatchService(records, jobs.WithBatchJobMeter(meter))
		require.NoError(t, err)
		runner := newBlockingRunner()
		batch, err := service.Submit(ctx, jobs.BatchSubmission{Account: accountA, Endpoint: "/v1/chat/completions", InputFileID: "input", IO: newMemoryBatchIO("{}\n"), Runner: runner, OutstandingBound: 1})
		require.NoError(t, err)
		released := false
		defer func() {
			if !released {
				close(runner.release)
			}
		}()
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("no dispatched line")
		}
		_, err = service.Cancel(ctx, accountA, batch.ID)
		require.NoError(t, err)
		cancelled, err := service.Get(ctx, accountA, batch.ID)
		require.NoError(t, err)
		require.False(t, cancelled.RunFinished)
		require.False(t, cancelled.SlotReleased)
		total, err := meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		close(runner.release)
		released = true
		require.Eventually(t, func() bool {
			job, err := service.Get(ctx, accountA, batch.ID)
			return err == nil && job.RunFinished && job.SlotReleased
		}, 5*time.Second, time.Millisecond)
		total, err = meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Zero(t, total)
	})
}

func TestFinishedBatchSlotReleaseRecoversAfterRestart(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenBatchRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		batch, err := jobs.NewBatch("batch", accountA, "/v1/chat/completions", "input", time.Now())
		require.NoError(t, err)
		batch.SlotID = "claim"
		require.NoError(t, meter.Reserve(ctx, accountA, batch.SlotID, batch.ID, "batch", 2))
		require.NoError(t, meter.Reserve(ctx, accountA, "other", "other-job", "video", 2))
		attachment, err := meter.Attachment(ctx, batch.Account, batch.SlotID, batch.ID, "batch")
		require.NoError(t, err)
		require.NoError(t, records.CreateClaimed(ctx, batch, attachment))
		done := batch
		require.NoError(t, done.Transition(jobs.JobStateCompleted, time.Now()))
		done.RunFinished = true
		require.NoError(t, records.Replace(ctx, batch, done))
		interrupted := &interruptedSlotRelease{Store: meter, after: true}
		service, err := jobs.NewBatchService(records, jobs.WithBatchJobMeter(interrupted))
		require.NoError(t, err)
		first, err := service.Get(ctx, accountA, batch.ID)
		require.NoError(t, err)
		require.False(t, first.SlotReleased)
		reopened, err := jobs.NewBatchService(records, jobs.WithBatchJobMeter(meter))
		require.NoError(t, err)
		final, err := reopened.Get(ctx, accountA, batch.ID)
		require.NoError(t, err)
		require.True(t, final.SlotReleased)
		require.ErrorIs(t, records.Replace(ctx, batch, batch), storage.ErrConflict)
		total, err := meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
	})
}

func TestDuplicateVideoIdentityCannotReleaseExistingWork(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithIdentifiers(func() string { return "same-job" }))
		require.NoError(t, err)
		first := acceptedRunner()
		job, err := service.Submit(t.Context(), open(first), submissionFor(accountA))
		require.NoError(t, err)
		second := acceptedRunner()
		_, err = service.Submit(t.Context(), open(second), submissionFor(accountA))
		require.ErrorIs(t, err, jobs.ErrJobExists)
		require.Zero(t, second.submits)
		total, err := meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		held, err := meter.Get(t.Context(), accountA, job.SlotID)
		require.NoError(t, err)
		require.Equal(t, job.ID, held.JobID)
		require.Equal(t, "video", held.Kind)
		require.False(t, held.Released)
	})
}

func TestLegacyJobSchemasRequireMigration(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		service, err := jobs.NewService(records)
		require.NoError(t, err)
		job, err := service.Submit(t.Context(), open(acceptedRunner()), submissionFor(accountA))
		require.NoError(t, err)
		batchRecords, err := jobs.OpenBatchRepository(backing)
		require.NoError(t, err)
		batch, err := jobs.NewBatch("batch", accountA, "/v1/chat/completions", "input", time.Now())
		require.NoError(t, err)
		require.NoError(t, batchRecords.Create(t.Context(), batch))
		for _, prefix := range []string{jobs.StoragePrefix, jobs.BatchStoragePrefix} {
			keys, err := backing.ScanWithPrefix(t.Context(), prefix, 0)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			data, err := backing.Get(t.Context(), keys[0])
			require.NoError(t, err)
			current := []byte(`"schema_version":5`)
			if prefix == jobs.BatchStoragePrefix {
				require.Contains(t, string(data), string(current))
				legacyAuthorization := bytes.Replace(data, current, []byte(`"schema_version":4`), 1)
				require.NoError(t, backing.Set(t.Context(), keys[0], legacyAuthorization))
				_, authorizationReadErr := batchRecords.Get(t.Context(), accountA, batch.ID)
				require.ErrorIs(t, authorizationReadErr, jobs.ErrCorruptBatchRecord)
				legacyAggregate := bytes.Replace(data, current, []byte(`"schema_version":3`), 1)
				require.NoError(t, backing.Set(t.Context(), keys[0], legacyAggregate))
				_, readErr := batchRecords.Get(t.Context(), accountA, batch.ID)
				require.ErrorIs(t, readErr, jobs.ErrCorruptBatchRecord)
			}
			if prefix == jobs.StoragePrefix {
				current = []byte(`"schema_version":6`)
				legacyCorrection := bytes.Replace(data, current, []byte(`"schema_version":5`), 1)
				require.NoError(t, backing.Set(t.Context(), keys[0], legacyCorrection))
				_, correctionReadErr := records.Get(t.Context(), accountA, job.ID)
				require.ErrorIs(t, correctionReadErr, jobs.ErrCorruptRecord)
				legacyPublication := bytes.Replace(data, current, []byte(`"schema_version":4`), 1)
				require.NoError(t, backing.Set(t.Context(), keys[0], legacyPublication))
				_, readErr := records.Get(t.Context(), accountA, job.ID)
				require.ErrorIs(t, readErr, jobs.ErrCorruptRecord)
			}
			require.Contains(t, string(data), string(current))
			legacy := bytes.Replace(data, current, []byte(`"schema_version":1`), 1)
			require.NoError(t, backing.Set(t.Context(), keys[0], legacy))
		}
		_, err = records.Get(t.Context(), accountA, job.ID)
		require.ErrorIs(t, err, jobs.ErrCorruptRecord)
		_, err = batchRecords.Get(t.Context(), accountA, batch.ID)
		require.ErrorIs(t, err, jobs.ErrCorruptBatchRecord)
	})
}
