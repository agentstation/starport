package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestTerminalNotificationDoesNotWaitForSettlement(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		job := storedJob(t, "notification-pending-budget", accountA, time.Now())
		job.CatalogGeneration, job.ReservationID = "generation", "reservation"
		require.NoError(t, job.AdoptProviderJob("private-provider-job"))
		require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
		require.NoError(t, records.Create(t.Context(), job))
		notifier := &recordingNotifier{}
		for range 3 {
			service, err := jobs.NewService(records, jobs.WithNotifier(notifier))
			require.NoError(t, err)
			_, err = service.Sweep(t.Context())
			require.ErrorIs(t, err, jobs.ErrSettlementPending)
		}
		require.Len(t, notifier.all(), 1, "terminal notification is independent of required billing")
		kept, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, kept.Accounted())
	})
}

func TestOptionalJobAccountingRetriesAfterReopen(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		job := storedJob(t, "report-retry", accountA, time.Now())
		require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
		require.NoError(t, records.Create(t.Context(), job))
		unavailable := errors.New("usage storage unavailable")
		accountant := &recordingAccountant{err: unavailable}
		notifier := &recordingNotifier{}
		service, err := jobs.NewService(records, jobs.WithAccountant(accountant), jobs.WithNotifier(notifier))
		require.NoError(t, err)
		result, err := service.Sweep(t.Context())
		require.ErrorIs(t, err, unavailable)
		require.Equal(t, 1, result.Failed)
		kept, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, kept.Accounted(), "failed delivery must remain retryable")
		require.Len(t, notifier.all(), 1)
		accountant.err = nil
		reopened, err := jobs.NewService(records, jobs.WithAccountant(accountant), jobs.WithNotifier(notifier))
		require.NoError(t, err)
		result, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, result.Accounted)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Len(t, accountant.all(), 2, "one failed delivery and one accepted retry")
		require.Equal(t, accountant.all()[0], accountant.all()[1])
		require.Len(t, notifier.all(), 1)
	})
}

type accountingMarkFailure struct {
	jobs.Repository
	fail bool
}

func (r *accountingMarkFailure) Replace(ctx context.Context, old, next jobs.Job) error {
	if r.fail && !old.Accounted() && next.Accounted() {
		return errors.New("accounting marker unavailable")
	}
	return r.Repository.Replace(ctx, old, next)
}

// durableJobAccountant uses the real atomic usage repository for retry evidence.
type durableJobAccountant struct{ usage.Repository }

func (a durableJobAccountant) RecordJob(ctx context.Context, entry jobs.AccountingEntry) error {
	return a.Put(ctx, usage.Record{
		RequestID: entry.JobID, AccountID: entry.Account, KeyID: "key",
		Timestamp: entry.TerminalAt, Operation: usage.OperationVideos,
		ModelRequested: entry.Model, ModelUsed: entry.Model, Provider: entry.Provider,
		Status: usage.StatusOK, Cost: &usage.Cost{NanoUSD: 75000000, Currency: "USD"},
	})
}

func TestJobReportingRecoversMarkerFailureWithoutDuplicateTotals(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		reportStore, err := usage.Open(store, usage.Options{})
		require.NoError(t, err)
		accountant := durableJobAccountant{reportStore}
		job := storedJob(t, "report-marker-failure", accountA, time.Now())
		require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
		require.NoError(t, records.Create(t.Context(), job))
		failing := &accountingMarkFailure{Repository: records, fail: true}
		first, err := jobs.NewService(failing, jobs.WithAccountant(accountant))
		require.NoError(t, err)
		result, err := first.Sweep(t.Context())
		require.ErrorContains(t, err, "accounting marker unavailable")
		require.Zero(t, result.Accounted)
		// Independent service instances race after a restart. All writes use the
		// same record identity and must contribute to the real totals only once.
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				service, openErr := jobs.NewService(records, jobs.WithAccountant(accountant))
				if openErr != nil {
					t.Error(openErr)
					return
				}
				_, sweepErr := service.Sweep(t.Context())
				if sweepErr != nil && !errors.Is(sweepErr, storage.ErrConflict) {
					t.Error(sweepErr)
				}
			})
		}
		workers.Wait()
		reopened, err := jobs.NewService(records, jobs.WithAccountant(accountant))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		kept, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.True(t, kept.Accounted())
		for _, scope := range []usage.Scope{usage.KeyScope("key"), usage.AccountScope(accountA), usage.GatewayScope()} {
			totals, err := reportStore.Totals(t.Context(), scope, usage.IntervalDay, job.TerminalAt)
			require.NoError(t, err)
			require.Equal(t, usage.Totals{Requests: 1, SpendNanoUSD: 75000000}, totals)
		}
	})
}

func TestConcurrentTerminalNotificationClaimsOneAttempt(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		job := storedJob(t, "concurrent-notification", accountA, time.Now())
		job.CatalogGeneration, job.ReservationID = "generation", "reservation"
		require.NoError(t, job.AdoptProviderJob("private-provider-job"))
		require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
		require.NoError(t, records.Create(t.Context(), job))
		notifier := &recordingNotifier{}
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				service, openErr := jobs.NewService(records, jobs.WithNotifier(notifier))
				if openErr != nil {
					t.Error(openErr)
					return
				}
				_, sweepErr := service.Sweep(t.Context())
				if !errors.Is(sweepErr, jobs.ErrSettlementPending) {
					t.Errorf("missing settlement refusal: %v", sweepErr)
				}
			})
		}
		workers.Wait()
		require.Len(t, notifier.all(), 1)
		kept, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, kept.Accounted())
	})
}
