package app

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func budgetJobFixture(t *testing.T, application *App) (jobs.Job, reservation.Meter) {
	t.Helper()
	ledger := application.budget.ledger
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "job-account", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	now := time.Now()
	require.NoError(t, ledger.EstablishWindow(t.Context(), meter, now, 0, reservation.History{ID: "history", Proof: "empty-fixture-history"}))
	attempt := reservation.Attempt{
		ID: "job-attempt", RequestID: "job-request", AccountID: meter.Holder, KeyID: "job-key",
		OfferingID: "fixture/model", CatalogGeneration: "original-generation", Operation: string(routing.OperationVideosGenerations),
		Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}},
		Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{
			{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}},
		}},
		Bound: reservation.Quantities{"output": 600},
	}
	_, err := ledger.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, ledger.Begin(t.Context(), attempt.ID))
	job, err := jobs.New("job", meter.Holder, "fixture", attempt.OfferingID, routing.OperationVideosGenerations, now)
	require.NoError(t, err)
	job.KeyID, job.CatalogGeneration, job.ReservationID = attempt.KeyID, attempt.CatalogGeneration, attempt.ID
	require.NoError(t, job.AdoptProviderJob("private-provider-job"))
	return job, meter
}

func TestProductionJobsRequireSettledReservation(t *testing.T) {
	application, err := New(validProductionConfig(t), withRuntimeFactories(explicitTestFactories(t)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	job, meter := budgetJobFixture(t, application)
	require.NoError(t, application.budget.BindJob(t.Context(), job))
	require.NoError(t, job.Fail("provider_failed", time.Now()))
	records, err := jobs.OpenRepository(application.store)
	require.NoError(t, err)
	require.NoError(t, records.Create(t.Context(), job))
	result, err := application.jobs.Sweep(t.Context())
	require.ErrorIs(t, err, jobs.ErrSettlementPending)
	require.Equal(t, 1, result.Failed)
	stored, err := records.Get(t.Context(), job.Account, job.ID)
	require.NoError(t, err)
	require.False(t, stored.Accounted(), "failed work does not prove zero charge")
	windows, err := application.budget.ledger.Window(t.Context(), meter, job.CreatedAt)
	require.NoError(t, err)
	require.EqualValues(t, 600, windows.Reserved)
	require.NoError(t, application.budget.ledger.RetainEvidence(t.Context(), job.ReservationID, reservation.Evidence{ID: "provider-measured", Quantities: reservation.Quantities{"output": 200}}))
	result, err = application.jobs.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, result.Accounted)
	stored, err = records.Get(t.Context(), job.Account, job.ID)
	require.NoError(t, err)
	require.True(t, stored.Accounted())
	windows, err = application.budget.ledger.Window(t.Context(), meter, job.CreatedAt)
	require.NoError(t, err)
	require.Zero(t, windows.Reserved)
	require.EqualValues(t, 200, windows.Consumed)
	result, err = application.jobs.Sweep(t.Context())
	require.NoError(t, err)
	require.Zero(t, result.Accounted)
}

func TestJobReservationChecksOriginalIdentity(t *testing.T) {
	application, err := New(validProductionConfig(t), withRuntimeFactories(explicitTestFactories(t)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	job, _ := budgetJobFixture(t, application)
	for name, change := range map[string]func(*jobs.Job){
		"account":    func(j *jobs.Job) { j.Account = "other" },
		"key":        func(j *jobs.Job) { j.KeyID = "other" },
		"model":      func(j *jobs.Job) { j.Model = "fixture/other" },
		"provider":   func(j *jobs.Job) { j.Provider = "other" },
		"generation": func(j *jobs.Job) { j.CatalogGeneration = "other" },
		"operation":  func(j *jobs.Job) { j.Operation = routing.OperationChatCompletions },
	} {
		t.Run(name, func(t *testing.T) {
			other := job
			change(&other)
			require.ErrorIs(t, application.budget.BindJob(t.Context(), other), reservation.ErrIdentityConflict)
			require.ErrorIs(t, application.budget.ConfirmJob(t.Context(), other), reservation.ErrIdentityConflict)
		})
	}
	require.NoError(t, application.budget.BindJob(t.Context(), job))
	require.NoError(t, application.budget.ledger.Reconcile(t.Context(), job.ReservationID, reservation.Evidence{ID: "provider-measured", Quantities: reservation.Quantities{"output": 200}}))
	require.NoError(t, application.budget.ConfirmJob(t.Context(), job))
	other := job
	other.ID = "another-job"
	require.ErrorIs(t, application.budget.ConfirmJob(t.Context(), other), reservation.ErrIdentityConflict)
}

func TestSettledJobRequiresOriginalSharedApproval(t *testing.T) {
	address := os.Getenv("TEST_VALKEY_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	deployment := "job-settlement-" + rand.Text()
	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: deployment, AllowInsecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := recovery.New(db)
	require.NoError(t, err)
	closed, err := witness.Initialize(t.Context(), deployment)
	require.NoError(t, err)
	backend := store.(storage.IncarnationProvider)
	identity, err := backend.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	approved, err := witness.ApproveAuthority(t.Context(), backend, closed, identity, "test-reconciled", "first")
	require.NoError(t, err)
	authority, err := witness.OpenAuthority(t.Context(), backend, deployment)
	require.NoError(t, err)
	ledger, err := reservation.Open(authority)
	require.NoError(t, err)
	owner := &budgetOwner{ledger: ledger, shared: authority}
	job, _ := budgetJobFixture(t, &App{budget: owner})
	require.NoError(t, owner.BindJob(t.Context(), job))
	require.NoError(t, ledger.Reconcile(t.Context(), job.ReservationID, reservation.Evidence{ID: "provider-measured", Quantities: reservation.Quantities{"output": 200}}))
	require.NoError(t, owner.ConfirmJob(t.Context(), job))
	closed, err = witness.Close(t.Context(), approved)
	require.NoError(t, err)
	require.Error(t, owner.ConfirmJob(t.Context(), job), "a settled record is not independent approval")
	_, err = witness.ApproveAuthority(t.Context(), backend, closed, identity, "test-reconciled-again", "second")
	require.NoError(t, err)
	require.Error(t, owner.ConfirmJob(t.Context(), job), "the old owner cannot adopt a new recovery epoch")
	authority, err = witness.OpenAuthority(t.Context(), backend, deployment)
	require.NoError(t, err)
	ledger, err = reservation.Open(authority)
	require.NoError(t, err)
	reopened := &budgetOwner{ledger: ledger, shared: authority}
	require.NoError(t, reopened.ConfirmJob(t.Context(), job))
}
