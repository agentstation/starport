package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/stretchr/testify/require"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/usage"
)

var jobSubmitted = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

func videoEntry(state jobs.JobState) jobs.AccountingEntry {
	return jobs.AccountingEntry{
		JobID:       "job-abc",
		Account:     "account_a",
		KeyID:       "key_a",
		Provider:    "deepinfra",
		Model:       "deepinfra/wan-2.2",
		Operation:   routing.OperationVideosGenerations,
		State:       state,
		Chargeable:  state.Chargeable(),
		SubmittedAt: jobSubmitted,
		TerminalAt:  jobSubmitted.Add(90 * time.Second),
	}
}

// TestAFinishedJobDrawsOneValidRecord states the shape of the record a job
// draws. It is written after the request that started the job returned, so
// nothing on a request path can supply the fields a usage record demands. Every
// one of them has to come off the job entry, and Validate is what proves it.
func TestAFinishedJobDrawsOneValidRecord(t *testing.T) {
	t.Parallel()

	recorder := &recordingUsageRepository{}
	accountant := NewJobAccountant(recorder)

	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateCompleted)))

	records := recorder.all()
	require.Len(t, records, 1)
	record := records[0]
	require.NoError(t, record.Validate())
	require.Equal(t, "job-abc", record.RequestID, "the job identifier is what an operator correlates on")
	require.Equal(t, "key_a", record.KeyID)
	require.Equal(t, "account_a", record.AccountID)
	require.Equal(t, usage.OperationVideos, record.Operation)
	require.Equal(t, usage.StatusOK, record.Status)
	require.Equal(t, int64(90_000), record.LatencyMS, "a job's latency is its whole life, not one request")
	require.NotNil(t, record.Media)
	require.Equal(t, int64(1), record.Media.GeneratedVideos)
}

// Missing usage leaves a failed job unpriced. Failure does not prove a free request.
func TestAFailedJobRecordsNoCostAndNoMediaUnit(t *testing.T) {
	t.Parallel()

	recorder := &recordingUsageRepository{}
	accountant := NewJobAccountant(recorder)

	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateFailed)))

	records := recorder.all()
	require.Len(t, records, 1)
	require.NoError(t, records[0].Validate())
	require.Nil(t, records[0].Cost)
	require.Nil(t, records[0].Media, "a video nobody received is not a media unit")
	require.Equal(t, usage.CostReasonNoUsage, records[0].CostUnavailableReason)
	require.Equal(t, usage.StatusError, records[0].Status)
}

// Cancellation remains distinct from failure and does not establish cost.
func TestACancelledJobIsNotAFailure(t *testing.T) {
	t.Parallel()

	recorder := &recordingUsageRepository{}
	accountant := NewJobAccountant(recorder)

	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateCancelled)))

	records := recorder.all()
	require.Len(t, records, 1)
	require.Equal(t, usage.StatusCancelled, records[0].Status)
	require.Nil(t, records[0].Cost)
}

// TestAVideoPricesPerVideo checks an explicitly declared per-video price.
func TestAVideoPricesPerVideo(t *testing.T) {
	t.Parallel()

	pricing := &starmapcatalogs.ModelPricing{
		Currency:   starmapcatalogs.ModelPricingCurrencyUSD,
		Operations: &starmapcatalogs.ModelOperationPricing{VideoGen: float(0.35)},
	}
	cost, reason := mediaCost(pricing, usage.Tokens{}, usage.Media{GeneratedVideos: 2})
	require.Empty(t, reason)
	require.InDelta(t, 0.70, cost, 1e-12)
}

func TestVideoCountCannotPriceDurationOffering(t *testing.T) {
	t.Parallel()
	client, err := starmap.New()
	require.NoError(t, err)
	offering, err := client.Catalog().Offering(starmapcatalogs.ProviderIDDeepInfra, "Wan-AI/Wan2.2-T2V-A14B")
	require.NoError(t, err)
	require.NotNil(t, offering.Pricing)
	require.NotNil(t, offering.Pricing.Operations)
	require.NotNil(t, offering.Pricing.Operations.OutputSecond)
	require.Nil(t, offering.Pricing.Operations.VideoGen)
	cost, reason := mediaCost(offering.Pricing, usage.Tokens{}, usage.Media{GeneratedVideos: 1})
	require.Zero(t, cost)
	require.Equal(t, usage.CostReasonMediaUnpriced, reason)
}

func TestVideoCountCannotIgnoreAdditionalDurationRates(t *testing.T) {
	for _, operations := range []*starmapcatalogs.ModelOperationPricing{
		{VideoGen: float(0.35), InputSecond: float(0.01)},
		{VideoGen: float(0.35), OutputSecond: float(0.075)},
	} {
		pricing := &starmapcatalogs.ModelPricing{Currency: starmapcatalogs.ModelPricingCurrencyUSD, Operations: operations}
		cost, reason := mediaCost(pricing, usage.Tokens{}, usage.Media{GeneratedVideos: 1})
		require.Zero(t, cost)
		require.Equal(t, usage.CostReasonMediaUnpriced, reason)
	}
}

// TestAnOfferingThatPricesNoVideoWithdrawsTheWholeCost is why the media half
// decides first. A video is the most expensive unit this gateway meters, so
// reporting the token half of such a turn alone would read as the bill and
// understate it by orders of magnitude.
func TestAnOfferingThatPricesNoVideoWithdrawsTheWholeCost(t *testing.T) {
	t.Parallel()

	pricing := &starmapcatalogs.ModelPricing{
		Currency:   starmapcatalogs.ModelPricingCurrencyUSD,
		Tokens:     &starmapcatalogs.ModelTokenPricing{Input: priceOf(2.50), Output: priceOf(10.00)},
		Operations: &starmapcatalogs.ModelOperationPricing{ImageGen: float(0.04)},
	}
	_, reason := mediaCost(pricing, usage.Tokens{Input: 40, Total: 40}, usage.Media{GeneratedVideos: 1})
	require.Equal(t, usage.CostReasonMediaUnpriced, reason)
}

// TestAVideoAloneIsUsage keeps a video out of the "no usage" gap. A video
// carries no token count at all, so a guard that read tokens alone would report
// every finished video as a turn the provider never metered.
func TestAVideoAloneIsUsage(t *testing.T) {
	t.Parallel()

	_, reason := usageCost(nil, usage.Record{Media: &usage.Media{GeneratedVideos: 1}})
	require.Equal(t, usage.CostReasonNoRoute, reason,
		"the missing snapshot is the gap, not the missing tokens")
}

// TestAJobWithNoRecorderStillSettles states what a deployment with usage
// recording switched off gets. The job still ends and still frees its slot; the
// accountant simply has nowhere to write.
func TestAJobWithNoRecorderStillSettles(t *testing.T) {
	t.Parallel()

	accountant := NewJobAccountant(nil)
	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateCompleted)))
}

func TestJobReportingUsesPinnedMeasurementInEveryTerminalState(t *testing.T) {
	for _, state := range []jobs.JobState{jobs.JobStateCompleted, jobs.JobStateFailed, jobs.JobStateCancelled} {
		for _, quantity := range []int64{0, 5} {
			entry := videoEntry(state)
			entry.Valuation = &reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "video_output_seconds", Price: reservation.Price{USD: "0.075", PerUnits: 1}}}}
			entry.Measurement = &reservation.Evidence{ID: entry.JobID + ":usage", Quantities: reservation.Quantities{"video_output_seconds": quantity}}
			recorder := &recordingUsageRepository{}
			accountant := NewJobAccountant(recorder)
			require.NoError(t, accountant.RecordJob(t.Context(), entry))
			record := recorder.all()[0]
			require.NotNil(t, record.Cost)
			require.Equal(t, quantity*75_000_000, record.Cost.NanoUSD)
			require.Empty(t, record.CostUnavailableReason)
			require.True(t, record.Media.VideoOutputSecondsKnown)
			require.Equal(t, quantity, record.Media.VideoOutputSeconds)
		}
	}
}

func TestJobReportingDoesNotInventMissingMeasurement(t *testing.T) {
	for _, state := range []jobs.JobState{jobs.JobStateCompleted, jobs.JobStateFailed, jobs.JobStateCancelled} {
		recorder := &recordingUsageRepository{}
		require.NoError(t, NewJobAccountant(recorder).RecordJob(t.Context(), videoEntry(state)))
		record := recorder.all()[0]
		require.Nil(t, record.Cost)
		require.Equal(t, usage.CostReasonNoUsage, record.CostUnavailableReason)
	}
}

func TestAdministratorBillingDoesNotFabricateProviderUsage(t *testing.T) {
	for _, disposition := range []string{"no_charge", "usage"} {
		t.Run(disposition, func(t *testing.T) {
			recorder := &recordingUsageRepository{}
			accountant := NewJobAccountant(recorder)
			entry := videoEntry(jobs.JobStateFailed)
			entry.BillingDisposition = "administrator_" + disposition
			entry.Valuation = &reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output_seconds", Price: reservation.Price{USD: "0.075", PerUnits: 1}}}}
			entry.BillingEvidence = &reservation.Evidence{ID: "admin-decision", NoCharge: disposition == "no_charge"}
			expected := int64(0)
			if disposition == "usage" {
				entry.BillingEvidence.Quantities = reservation.Quantities{"output_seconds": 5}
				expected = 375000000
			}
			require.NoError(t, accountant.RecordJob(t.Context(), entry))
			records := recorder.all()
			require.Len(t, records, 1)
			require.Equal(t, expected, records[0].Cost.NanoUSD)
			require.Equal(t, entry.BillingDisposition, records[0].BillingDisposition)
			require.Nil(t, records[0].Media, "operator billing does not invent an output or provider duration")
		})
	}
}
