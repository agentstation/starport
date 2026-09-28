package jobs_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
)

type waitingNativeRunner struct {
	*nativeRunner
	dispatched chan struct{}
	release    chan struct{}
	completed  chan error
}

func waitingNativeFixture() *waitingNativeRunner {
	return &waitingNativeRunner{nativeRunner: nativeFixture(), dispatched: make(chan struct{}), release: make(chan struct{}), completed: make(chan error, 1)}
}
func (r *waitingNativeRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	if err := recorder.BeforeDispatch(ctx, jobs.Dispatch{Native: true, Provider: r.acceptance.Provider, Model: r.acceptance.Model, CatalogGeneration: "generation", Valuation: r.valuation}); err != nil {
		return jobs.Acceptance{}, err
	}
	r.submits++
	close(r.dispatched)
	select {
	case <-r.release:
		answer := r.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "private-native-request", Measurement: r.measurement, Asset: r.asset}
		err := recorder.Accepted(ctx, answer)
		r.completed <- err
		return answer, err
	case <-ctx.Done():
		r.completed <- ctx.Err()
		return jobs.Acceptance{}, ctx.Err()
	}
}

func TestNativeBackgroundSurvivesCallerDisconnect(t *testing.T) {
	service, records, _, _ := newAssetService(t)
	runner := waitingNativeFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	job, err := service.SubmitBackground(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.True(t, job.SubmissionPending)
	job.Valuation.Components[0].Price.USD = "99"
	stored, err := records.Get(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	require.True(t, stored.Native)
	require.Equal(t, "0.075", stored.Valuation.Components[0].Price.USD)
	<-runner.dispatched
	cancel()
	close(runner.release)
	require.NoError(t, <-runner.completed, "disconnect after durable dispatch must not cancel generation")
	require.NoError(t, service.Close(t.Context()))
	stored, err = records.Get(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, stored.State)
	require.NotNil(t, stored.Measurement)
	require.True(t, stored.HasAsset())
	require.Equal(t, 1, runner.submits)
}

func TestNativeWorkersBoundAndShutdownRetainUncertainty(t *testing.T) {
	service, records, assets, _ := newAssetService(t)
	service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithWorkers(1, time.Minute))
	require.NoError(t, err)
	runner := waitingNativeFixture()
	job, err := service.SubmitBackground(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	opened := false
	_, err = service.SubmitBackground(t.Context(), func(context.Context) (jobs.Runner, error) { opened = true; return runner, nil }, submissionFor(accountB))
	require.ErrorIs(t, err, jobs.ErrWorkersBusy)
	require.False(t, opened)
	require.NoError(t, service.Close(t.Context()))
	require.ErrorIs(t, <-runner.completed, context.Canceled)
	stored, err := records.Get(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	require.True(t, stored.SubmissionPending)
	require.Nil(t, stored.Measurement)
	require.False(t, stored.Accounted())
	_, err = service.SubmitBackground(t.Context(), open(runner), submissionFor(accountA))
	require.ErrorIs(t, err, jobs.ErrServiceClosed)
	require.Equal(t, 1, runner.submits)
}

func TestBackgroundOpenFailureReleasesWorker(t *testing.T) {
	service, _, _, _ := newAssetService(t)
	broken := errors.New("cannot acquire gateway")
	_, err := service.SubmitBackground(t.Context(), func(context.Context) (jobs.Runner, error) { return nil, broken }, submissionFor(accountA))
	require.ErrorIs(t, err, broken)
	job, err := service.SubmitBackground(t.Context(), open(nativeFixture()), submissionFor(accountA))
	require.NoError(t, err)
	require.NotEmpty(t, job.ID)
	require.NoError(t, service.Close(t.Context()))
}

func TestNativeWorkerDeadlineRetainsUncertainty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, records, assets, _ := newAssetService(t)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithWorkers(1, time.Minute))
		require.NoError(t, err)
		runner := waitingNativeFixture()
		job, err := service.SubmitBackground(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		require.ErrorIs(t, <-runner.completed, context.DeadlineExceeded)
		require.NoError(t, service.Close(t.Context()))
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.True(t, stored.SubmissionPending)
		require.Nil(t, stored.Measurement)
		require.False(t, stored.Accounted())
	})
}
