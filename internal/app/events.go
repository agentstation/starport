package app

import (
	"context"
	"strconv"

	"github.com/agentstation/starport/internal/events"
	"github.com/agentstation/starport/internal/jobs"
)

// eventEmitter is the one-method emit contract the composition's own
// adapters push through, declared here for the same reason the jobs
// package declares Notifier: the consumer names its seam. The webhook
// dispatcher satisfies it.
type eventEmitter interface {
	Emit(eventType string, data map[string]string)
}

// jobEventNotifier adapts the webhook dispatcher onto the job service's
// notifier seam, the way usageSinkObserver adapts the export sink onto the
// capture seam. The payload carries identifiers and states only: what the
// job produced stays behind the asset routes, and a prompt never entered
// the entry at all.
type jobEventNotifier struct {
	events *events.Dispatcher
}

func (n jobEventNotifier) JobEnded(_ context.Context, entry jobs.AccountingEntry) {
	n.events.Emit(events.TypeForJobState(string(entry.State)), map[string]string{
		"job_id":     entry.JobID,
		"account":    entry.Account,
		"provider":   entry.Provider,
		"model":      entry.Model,
		"operation":  string(entry.Operation),
		"state":      string(entry.State),
		"chargeable": strconv.FormatBool(entry.Chargeable),
	})
}
