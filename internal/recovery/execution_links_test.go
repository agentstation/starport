package recovery

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupJobReservationLinks(t *testing.T) {
	for _, mode := range []string{"pending", "retained-evidence", "settled", "missing-job", "wrong-generation", "wrong-reservation", "unbound", "wrong-value", "missing-reservation"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
			require.NoError(t, err)
			meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
			require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 0, reservation.History{ID: "history", Proof: "fixture-history"}))
			attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: string(routing.OperationVideosGenerations), Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}}
			_, err = budgets.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
			job, err := jobs.New("job", "owner", "provider", "provider/model", routing.OperationVideosGenerations, time.Now())
			require.NoError(t, err)
			job.ReservationID, job.CatalogGeneration, job.KeyID = attempt.ID, attempt.CatalogGeneration, attempt.KeyID
			require.NoError(t, job.AdoptProviderJob("private-provider-job"))
			if mode != "unbound" {
				require.NoError(t, budgets.BindJob(t.Context(), attempt.ID, job.ID))
			}
			if mode == "retained-evidence" || mode == "settled" {
				require.NoError(t, budgets.RetainEvidence(t.Context(), attempt.ID, reservation.Evidence{ID: "measured", Quantities: reservation.Quantities{"output": 20}}))
				if mode == "settled" {
					require.NoError(t, budgets.ReconcileRetained(t.Context(), attempt.ID))
				}
			}
			if mode == "wrong-generation" {
				job.CatalogGeneration = "different"
			}
			if mode == "wrong-reservation" {
				job.ReservationID = "different"
			}
			if mode == "wrong-value" {
				value := attempt.Valuation
				value.Components = []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000002", PerUnits: 1}}}
				job.Valuation = &value
			}
			records, err := jobs.OpenRepository(kv)
			require.NoError(t, err)
			if mode != "missing-job" {
				require.NoError(t, records.Create(t.Context(), job))
			}
			if mode == "missing-reservation" {
				keys, err := kv.ScanWithPrefix(t.Context(), reservation.StoragePrefix+"attempt:", 0)
				require.NoError(t, err)
				require.NoError(t, kv.BatchDelete(t.Context(), keys))
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			switch mode {
			case "pending", "retained-evidence", "settled", "missing-job":
				require.NoError(t, err)
				if mode == "missing-job" {
					require.EqualValues(t, 1, report.MissingReservationJobs)
					require.Zero(t, report.JobExecution.ReservedJobs)
				} else {
					require.EqualValues(t, 1, report.JobExecution.ReservedJobs)
					require.Zero(t, report.MissingReservationJobs)
					if mode == "settled" {
						require.Zero(t, report.JobExecution.PendingSettlement)
					} else {
						require.EqualValues(t, 1, report.JobExecution.PendingSettlement)
					}
				}
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestBackupRejectsUnownedJobCorrection(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
	source.KV = transfer
	require.NoError(t, kv.Set(t.Context(), jobs.CorrectionStoragePrefix+"b3duZXI:job:am9i:intent:b25l", []byte(`{"version":1}`)))
	_, err := inspectReferenceFixture(t, source, request, destination)
	require.Error(t, err)
}
