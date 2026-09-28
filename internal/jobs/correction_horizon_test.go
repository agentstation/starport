package jobs_test

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestJobCorrectionHorizon(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		inspector, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		original, err := inspector.InspectReconciliation(t.Context(), job.Account, job.ID)
		require.NoError(t, err)
		now := original.Decision.DecidedAt.Add(90*24*time.Hour - time.Second)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		first := correctionIntent(t, job, "within-horizon").Decision.ReconciliationRequest
		_, err = service.CorrectAdministrator(t.Context(), job.Account, job.ID, "key:admin", first)
		require.NoError(t, err)
		current, err := records.Get(t.Context(), job.Account, job.ID)
		require.NoError(t, err)
		next := jobs.ReconciliationRequest{DecisionID: "past-horizon", Binding: current.CorrectionBinding(""), EvidenceReference: "invoice:late", Reason: "Late correction", Disposition: "no_charge"}
		now = now.Add(time.Second)
		_, err = service.CorrectAdministrator(t.Context(), job.Account, job.ID, "key:admin", next)
		require.Error(t, err, "new corrections stop at the original 90-day deadline")
		_, err = service.CorrectAdministrator(t.Context(), job.Account, job.ID, "key:admin", first)
		require.NoError(t, err, "exact accepted retry survives expiry")
		view, err := service.InspectReconciliation(t.Context(), job.Account, job.ID)
		require.NoError(t, err)
		require.Equal(t, "correction_horizon_expired", view.CorrectionUnavailableReason)
		require.Empty(t, view.CorrectionBinding)
		require.Equal(t, original.Decision, view.Decision)
	})
}
