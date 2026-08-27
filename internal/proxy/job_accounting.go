package proxy

import (
	"context"
	"fmt"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/usage"
)

// JobAccountant prices one finished job and writes its single usage record.
//
// It lives here rather than in internal/jobs because pricing reads a Starmap
// offering, and internal/jobs owns job state and nothing else. It lives here
// rather than in the composition root because this package already holds the
// catalog-to-cost rules that every other operation uses, and a video priced by
// a second copy of them would drift from the rest of the bill.
//
// It is deliberately not a method on Proxy. Nothing on the request path calls
// it: a job settles when a poll, a cancel, or the sweep reaches its terminal
// state, which may be long after the submitting request returned.
type JobAccountant struct {
	// snapshots reads the catalog at the moment the job ends, not the one that
	// routed its submission. A record cannot hold a snapshot, and a poll may
	// land minutes later. The alternative is no price at all, and a price from
	// a catalog that moved between the two is closer to the truth than that.
	snapshots func() *runtimecatalog.RoutableSnapshot
	recorder  UsageRecorder
}

// NewJobAccountant returns an accountant over one catalog reader and one record
// store. Either may be absent, which is what a deployment with usage recording
// switched off gets: the job still settles and still frees its slot.
func NewJobAccountant(
	snapshots func() *runtimecatalog.RoutableSnapshot,
	recorder UsageRecorder,
) *JobAccountant {
	return &JobAccountant{snapshots: snapshots, recorder: recorder}
}

// RecordJob writes the one usage record a terminal job draws.
//
// A failed job and a cancelled job draw a record with no cost rather than no
// record. The work is a real event in the account's history, and a spend report
// that showed only the jobs that succeeded would answer "what did this account
// do" with a shorter list than the truth. Their cost is zero, which is what
// CostReasonNoUsage already means everywhere else.
func (a *JobAccountant) RecordJob(ctx context.Context, entry jobs.AccountingEntry) error {
	if a == nil || a.recorder == nil {
		return nil
	}
	record := usage.Record{
		RequestID:      entry.JobID,
		KeyID:          orAnonymous(entry.KeyID, usageAnonymousKeyID),
		TenantID:       orAnonymous(entry.Tenant, usageAnonymousTenantID),
		Timestamp:      entry.TerminalAt,
		Operation:      usage.OperationVideos,
		ModelRequested: entry.Model,
		ModelUsed:      entry.Model,
		Provider:       entry.Provider,
		Status:         jobStatus(entry.State),
		LatencyMS:      entry.TerminalAt.Sub(entry.SubmittedAt).Milliseconds(),
	}
	// ErrorClass stays empty. internal/jobs holds a caller-facing reason string
	// and no failure kind, and the failure vocabulary is not reachable from a
	// leaf that owns job state. A class invented here would be a guess an
	// operator could not act on, and Status already says the job ended badly.
	if !entry.Chargeable {
		// Nothing was produced, so nothing is priced. The reason names the gap
		// rather than reporting a zero that reads like a free video.
		record.CostUnavailableReason = usage.CostReasonNoUsage
		return a.put(ctx, record)
	}
	media := usage.Media{GeneratedVideos: 1}
	record.Media = &media
	cost, reason := usageCost(a.snapshot(), entry.Model, usage.Tokens{}, &media, "")
	record.Cost = cost
	record.CostUnavailableReason = reason
	return a.put(ctx, record)
}

func (a *JobAccountant) put(ctx context.Context, record usage.Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("proxy: account for job %s: %w", record.RequestID, err)
	}
	return a.recorder.Put(ctx, record)
}

func (a *JobAccountant) snapshot() *runtimecatalog.RoutableSnapshot {
	if a.snapshots == nil {
		return nil
	}
	return a.snapshots()
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
