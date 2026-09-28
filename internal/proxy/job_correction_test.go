package proxy

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestJobCorrectionsPreserveOriginalUsage(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := usage.Open(store, usage.Options{})
		require.NoError(t, err)
		accountant := NewJobAccountant(records)
		entry := videoEntry(jobs.JobStateFailed)
		entry.SubmittedAt = time.Now().UTC().Add(-time.Minute)
		entry.TerminalAt = entry.SubmittedAt.Add(30 * time.Second)
		entry.Valuation = &reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "0.075", PerUnits: 1}}}}
		entry.BillingDisposition = "administrator_no_charge"
		entry.BillingEvidence = &reservation.Evidence{ID: "original", NoCharge: true}
		correction := jobs.AccountingCorrection{Original: entry, ID: "correction-1", RecordedAt: entry.TerminalAt.Add(time.Second), Evidence: reservation.Evidence{ID: "corrected", Quantities: reservation.Quantities{"seconds": 5}, Tokens: 7}}
		require.NoError(t, accountant.RecordJobCorrection(t.Context(), correction))
		require.NoError(t, accountant.RecordJobCorrection(t.Context(), correction))
		require.NoError(t, accountant.RecordJob(t.Context(), entry))
		page, err := records.List(t.Context(), usage.Query{AccountID: entry.Account})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		record := page.Records[0]
		require.EqualValues(t, 375000000, record.Cost.NanoUSD)
		require.NotEqual(t, correction.ID, record.BillingAdjustment.ID)
		require.Len(t, record.BillingAdjustment.ID, 64)
		require.EqualValues(t, 0, record.BillingAdjustment.OriginalCost.NanoUSD)
		require.EqualValues(t, 7, record.Tokens.Total)
		require.Equal(t, "administrator_usage", record.BillingDisposition)
		require.Nil(t, record.Media, "an administrator correction does not fabricate a provider output")
		totals, err := records.Totals(t.Context(), usage.AccountScope(entry.Account), usage.IntervalDay, entry.TerminalAt)
		require.NoError(t, err)
		require.Equal(t, usage.Totals{Requests: 1, Tokens: 7, SpendNanoUSD: 375000000}, totals)
		correction.ID, correction.PreviousID = "correction-2", "correction-1"
		correction.Evidence = reservation.Evidence{ID: "refund", NoCharge: true}
		require.NoError(t, accountant.RecordJobCorrection(t.Context(), correction))
		totals, err = records.Totals(t.Context(), usage.AccountScope(entry.Account), usage.IntervalDay, entry.TerminalAt)
		require.NoError(t, err)
		require.Equal(t, usage.Totals{Requests: 1}, totals)
	})
}

func TestJobCorrectionRefusesUnsupportedOrInvalidReporting(t *testing.T) {
	require.NoError(t, NewJobAccountant(nil).RecordJobCorrection(t.Context(), jobs.AccountingCorrection{}))
	unsupported := &recordingUsageRepository{}
	require.ErrorIs(t, NewJobAccountant(unsupported).RecordJobCorrection(t.Context(), jobs.AccountingCorrection{}), ErrCorrectionReportingUnavailable)
	require.Empty(t, unsupported.all())
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := usage.Open(store, usage.Options{})
		require.NoError(t, err)
		entry := videoEntry(jobs.JobStateFailed)
		entry.SubmittedAt = time.Now().UTC().Add(-time.Minute)
		entry.TerminalAt = entry.SubmittedAt.Add(30 * time.Second)
		correction := jobs.AccountingCorrection{Original: entry, ID: "bad", RecordedAt: time.Now().UTC(), Evidence: reservation.Evidence{ID: "no-price", Quantities: reservation.Quantities{"seconds": 5}}}
		require.ErrorIs(t, NewJobAccountant(records).RecordJobCorrection(t.Context(), correction), usage.ErrInvalidRecord)
		page, err := records.List(t.Context(), usage.Query{})
		require.NoError(t, err)
		require.Empty(t, page.Records)
	})
}
