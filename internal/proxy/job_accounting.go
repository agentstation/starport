package proxy

import (
	"context"
	"fmt"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/usage"
)

// JobAccountant reports measured usage at the rates pinned before dispatch.
// A terminal state alone establishes neither a charge nor a free request.
type JobAccountant struct {
	recorder UsageRecorder
}

// NewJobAccountant returns an optional usage reporter.
func NewJobAccountant(recorder UsageRecorder) *JobAccountant {
	return &JobAccountant{recorder: recorder}
}

// RecordJob records a terminal result without consulting the current catalog.
func (a *JobAccountant) RecordJob(ctx context.Context, entry jobs.AccountingEntry) error {
	if a == nil || a.recorder == nil {
		return nil
	}
	record := usage.Record{
		RequestID:      entry.JobID,
		KeyID:          orAnonymous(entry.KeyID, usageAnonymousKeyID),
		AccountID:      orAnonymous(entry.Account, usageAnonymousAccountID),
		Timestamp:      entry.TerminalAt,
		Operation:      usage.OperationVideos,
		ModelRequested: entry.Model,
		ModelUsed:      entry.Model,
		Provider:       entry.Provider,
		Status:         jobStatus(entry.State),
		LatencyMS:      entry.TerminalAt.Sub(entry.SubmittedAt).Milliseconds(),
	}
	if entry.State == jobs.JobStateCompleted {
		record.Media = &usage.Media{GeneratedVideos: 1}
	}
	if entry.Measurement != nil {
		if seconds, known := entry.Measurement.Quantities[runtimecatalog.VideoOutputSecondUnit]; known && seconds >= 0 {
			if record.Media == nil {
				record.Media = &usage.Media{}
			}
			record.Media.VideoOutputSeconds, record.Media.VideoOutputSecondsKnown = seconds, true
		}
	}
	switch {
	case entry.Measurement == nil:
		record.CostUnavailableReason = usage.CostReasonNoUsage
	case entry.Valuation == nil:
		record.CostUnavailableReason = usage.CostReasonNoPricing
	default:
		amount, err := entry.Valuation.NanoUSD(entry.Measurement.Quantities)
		if err != nil {
			record.CostUnavailableReason = usage.CostReasonInvalidUsage
		} else {
			record.Cost = &usage.Cost{NanoUSD: amount, Currency: "USD"}
		}
	}
	return a.put(ctx, record)
}

func (a *JobAccountant) put(ctx context.Context, record usage.Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("proxy: account for job %s: %w", record.RequestID, err)
	}
	return a.recorder.Put(ctx, record)
}

// jobStatus maps the terminal job vocabulary onto the usage vocabulary. The two
// are separate on purpose: a job state answers a caller polling its work, and a
// usage status answers an operator reading a spend report.
func jobStatus(state jobs.JobState) string {
	switch state {
	case jobs.JobStateCancelled:
		return usage.StatusCancelled
	case jobs.JobStateCompleted:
		return usage.StatusOK
	default:
		return usage.StatusError
	}
}

func orAnonymous(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
