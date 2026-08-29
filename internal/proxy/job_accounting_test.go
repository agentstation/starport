package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/jobs"
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
	accountant := NewJobAccountant(nil, recorder)

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

// TestAFailedJobRecordsNoCostAndNoMediaUnit is the rule an account reads its bill
// through. The provider produced nothing, so nothing is priced, and the record
// carries a named reason rather than a zero that would read as a free video.
func TestAFailedJobRecordsNoCostAndNoMediaUnit(t *testing.T) {
	t.Parallel()

	recorder := &recordingUsageRepository{}
	accountant := NewJobAccountant(nil, recorder)

	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateFailed)))

	records := recorder.all()
	require.Len(t, records, 1)
	require.NoError(t, records[0].Validate())
	require.Nil(t, records[0].Cost)
	require.Nil(t, records[0].Media, "a video nobody received is not a media unit")
	require.Equal(t, usage.CostReasonNoUsage, records[0].CostUnavailableReason)
	require.Equal(t, usage.StatusError, records[0].Status)
}

// TestACancelledJobIsNotAFailure separates the two free ends. Both cost
// nothing, and an account reading its own history needs to tell a provider that
// broke from a caller that changed its mind.
func TestACancelledJobIsNotAFailure(t *testing.T) {
	t.Parallel()

	recorder := &recordingUsageRepository{}
	accountant := NewJobAccountant(nil, recorder)

	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateCancelled)))

	records := recorder.all()
	require.Len(t, records, 1)
	require.Equal(t, usage.StatusCancelled, records[0].Status)
	require.Nil(t, records[0].Cost)
}

// TestAVideoPricesPerVideo holds the pricing half. Starmap prices a video per
// video, not per second and not per token, so the video count is the whole
// meter for the operation.
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

	accountant := NewJobAccountant(nil, nil)
	require.NoError(t, accountant.RecordJob(context.Background(), videoEntry(jobs.JobStateCompleted)))
}
