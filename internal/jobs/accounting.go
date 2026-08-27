package jobs

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/routing"
)

// AccountingEntry is what one finished job reports to whoever prices it.
//
// The entry carries a state and a chargeable flag rather than a price. This
// package owns when a job ends and whether the end produced work; it owns no
// price, no catalog, and no currency. The half that reads a Starmap offering
// reads them here.
//
// The provider job identifier is absent, as it is from every other value that
// leaves this package. Invariant J1 keeps it inside.
type AccountingEntry struct {
	JobID     string
	Tenant    string
	KeyID     string
	Provider  string
	Model     string
	Operation routing.Operation
	State     JobState
	// Chargeable reports whether this end produced work the tenant pays for.
	// The recipient still decides what it costs, and may find no price at all.
	Chargeable bool
	// SubmittedAt and TerminalAt bound the work. A record of the two is what
	// lets an operator tell a job that took two minutes from one that took two
	// hours, which no single stamp answers.
	SubmittedAt time.Time
	TerminalAt  time.Time
}

// Accountant records what one finished job cost.
//
// This package declares the interface rather than importing the usage seam,
// because internal/jobs is a leaf that owns state and nothing about spend. The
// recipient prices the entry from the catalog and writes one usage record.
//
// A failure here is not the job's failure. The work happened, the caller holds
// the answer, and a job that reported an accounting failure back to a caller
// would tell it the wrong thing. The service therefore absorbs the error.
type Accountant interface {
	RecordJob(ctx context.Context, entry AccountingEntry) error
}

// Meter bounds how many jobs one holder may hold open at a time.
//
// The interface is declared here for the same reason Accountant is: the limit
// vocabulary lives in another package, and a leaf that owns job state may not
// reach across for it. limits.JobMeter satisfies this shape.
type Meter interface {
	Reserve(ctx context.Context, holder string, count, bound int64) error
	Release(ctx context.Context, holder string, count int64) error
}

// settle draws the one usage record a terminal job draws and frees the slot it
// held.
//
// It stamps the record before it reports the entry. That order is what makes
// the count exactly one however often a caller polls: the stamp is a compare
// and swap against the record store, so of two concurrent polls only one gets
// past it. Reporting first and stamping after would draw a second cost for one
// video whenever the stamp lost the race.
//
// The other direction of that trade is that a report lost between the stamp and
// the recipient is lost for good. That is the correct half to give up. The usage
// seam is best-effort by construction and drops records under load already,
// while a duplicated charge is money a tenant did not spend.
func (s *Service) settle(ctx context.Context, job Job) Job {
	if !job.State.Terminal() || job.Accounted() {
		return job
	}
	settled := job
	if err := settled.MarkAccounted(s.now()); err != nil {
		return job
	}
	if err := s.records.Replace(ctx, settled); err != nil {
		return job
	}
	if s.accountant != nil {
		// The caller holds its answer either way. See the note above.
		_ = s.accountant.RecordJob(ctx, entryFor(settled))
	}
	s.releaseSlot(ctx, settled)
	return settled
}

// entryFor projects a settled record into what the accounting seam reads.
func entryFor(job Job) AccountingEntry {
	return AccountingEntry{
		JobID:       job.ID,
		Tenant:      job.Tenant,
		KeyID:       job.KeyID,
		Provider:    job.Provider,
		Model:       job.Model,
		Operation:   job.Operation,
		State:       job.State,
		Chargeable:  job.State.Chargeable(),
		SubmittedAt: job.CreatedAt,
		TerminalAt:  job.TerminalAt,
	}
}

// reserveSlot claims one outstanding job slot for the account.
//
// The claim happens before the provider call. A submission refused for being
// over the limit must not have spent provider work first, or the limit would
// bound what a tenant reads rather than what it pays for.
func (s *Service) reserveSlot(ctx context.Context, tenant string, bound int64) error {
	if s.meter == nil {
		return nil
	}
	return s.meter.Reserve(ctx, tenant, 1, bound)
}

// releaseSlot gives one slot back and reports nothing.
//
// Every caller is either unwinding from a failure it already reports or has
// just settled a job whose answer the caller holds. A leaked slot costs the
// account one slot until the sweep settles the record, and a refused release
// that propagated would turn that into a failed request.
func (s *Service) releaseSlot(ctx context.Context, job Job) {
	if s.meter == nil {
		return
	}
	_ = s.meter.Release(ctx, job.Tenant, 1)
}
