package limits

import "errors"

// OutstandingJobsPrefix is the retained account-count namespace.
// The jobslots repository owns its versioned format and durable claim records.
const OutstandingJobsPrefix = "limits:v1:outstanding_jobs:"

// ErrTooManyOutstandingJobs refuses work beyond the account's current bound.
var ErrTooManyOutstandingJobs = errors.New("outstanding job limit exceeded")

// ErrOutstandingJobsRecoveryRequired refuses admission without known ownership.
var ErrOutstandingJobsRecoveryRequired = errors.New("outstanding job ownership requires recovery")
