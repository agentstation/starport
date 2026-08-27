//nolint:dupl // JobMeter repeats StorageMeter on purpose. See JobMeter.
package limits

import (
	"context"
	"errors"
)

const (
	// OutstandingJobsSchemaVersion identifies the only outstanding job schema.
	OutstandingJobsSchemaVersion = 1
	// OutstandingJobsPrefix is the outstanding job v1 namespace.
	OutstandingJobsPrefix = "limits:v1:outstanding_jobs:"
)

// ErrTooManyOutstandingJobs reports a submission that would put a holder past
// its outstanding job bound.
var ErrTooManyOutstandingJobs = errors.New("outstanding job limit exceeded")

// JobMeter bounds how many jobs one holder may have running at a time.
//
// An outstanding job is a spend commitment this gateway has already made to a
// provider and cannot read yet. Every other limit meters something that is
// already over: a request that returned, or bytes that are already stored. This
// one meters work in flight, which is the only bound that can refuse a caller
// before the provider bills for it.
//
// It repeats the shape of StorageMeter deliberately. Both wrap the same level
// meter, and merging them into one type would make a byte budget and a job
// budget interchangeable at every call site that takes either. They already
// satisfy the same structural interface elsewhere, so a single type would let a
// deployment count videos against its stored byte bound and compile.
//
//nolint:dupl // Two dimensions, two types. See above.
type JobMeter struct {
	level levelMeter
}

// NewJobMeter builds a meter over an atomic counter.
func NewJobMeter(counter Counter) (*JobMeter, error) {
	if counter == nil {
		return nil, ErrCounterRequired
	}
	return &JobMeter{level: levelMeter{
		counter:   counter,
		prefix:    OutstandingJobsPrefix,
		full:      ErrTooManyOutstandingJobs,
		what:      "outstanding jobs",
		unit:      "outstanding jobs",
		boundUnit: "outstanding job",
	}}, nil
}

// Reserve claims count job slots for the holder and reports whether the claim
// fits inside bound. A bound of zero or less leaves the holder unbounded.
func (m *JobMeter) Reserve(ctx context.Context, holder string, count, bound int64) error {
	return m.level.reserve(ctx, holder, count, bound)
}

// Release gives count job slots back to the holder. One job frees its slot when
// it reaches a terminal state, and a submission that never reached a provider
// frees the slot it claimed.
func (m *JobMeter) Release(ctx context.Context, holder string, count int64) error {
	return m.level.release(ctx, holder, count)
}

// Total reports how many jobs this holder currently has outstanding.
func (m *JobMeter) Total(ctx context.Context, holder string) (int64, error) {
	return m.level.total(ctx, holder)
}
