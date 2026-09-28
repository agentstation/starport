package app

import (
	"context"
	"testing"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRunRecoversRetainedBudgetEvidence(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Jobs.SweepInterval = 24 * time.Hour
	factories := explicitTestFactories(t)
	factories.openCatalog = func(ctx context.Context, store storage.KVStore, _ *sqlstore.DB, _ runtimecatalog.Settings, _ runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		return newLifecycleCatalogRuntime(ctx, store)
	}
	fakeHTTP := newBlockingHTTPRuntime()
	factories.newServer = func(*server.Config, server.Dependencies) (httpRuntime, error) { return fakeHTTP, nil }
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	ledger := application.budget.ledger
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "recovery-account", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	now := time.Now()
	require.NoError(t, ledger.EstablishWindow(t.Context(), meter, now, 0, reservation.History{ID: "history", Proof: "empty-fixture-history"}))
	attempt := reservation.Attempt{
		ID: "recovery-attempt", RequestID: "recovery-request", AccountID: meter.Holder, KeyID: "recovery-key",
		OfferingID: "fixture/model", CatalogGeneration: "original-generation", Operation: "chat",
		Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}},
		Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{
			{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}},
		}},
		Bound: reservation.Quantities{"output": 600},
	}
	_, err = ledger.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, ledger.Begin(t.Context(), attempt.ID))
	evidence := reservation.Evidence{ID: "provider-measured", Quantities: reservation.Quantities{"output": 200}}
	require.NoError(t, ledger.RetainEvidence(t.Context(), attempt.ID, evidence))
	record, err := ledger.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, reservation.Uncertain, record.State, "construction starts no recovery worker")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("application recovery worker did not stop")
		}
	})
	select {
	case <-fakeHTTP.started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP runtime did not start")
	}
	require.Eventually(t, func() bool {
		record, err := ledger.Inspect(t.Context(), attempt.ID)
		return err == nil && record.State == reservation.Settled
	}, 3*time.Second, 10*time.Millisecond, "startup must resume durable measured usage without a new request")
	record, err = ledger.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Evidence)
	require.EqualValues(t, 200, *record.NanoUSD)
	window, err := ledger.Window(t.Context(), meter, now)
	require.NoError(t, err)
	require.Zero(t, window.Reserved)
	require.EqualValues(t, 200, window.Consumed)
}
