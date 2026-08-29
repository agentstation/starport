// Package limits owns the request-rate and consumption vocabulary that
// bounds what a holder may spend. Both a gateway API key and an account hold
// limits, so the vocabulary lives here rather than inside either owner.
package limits

import "errors"

// Budget interval names. The values match the fixed UTC aggregation
// intervals that the usage seam maintains, so a stored budget interval
// reads one exact aggregate window.
const (
	// IntervalDay selects the fixed UTC day window.
	IntervalDay = "day"
	// IntervalWeek selects the fixed UTC ISO week window.
	IntervalWeek = "week"
	// IntervalMonth selects the fixed UTC month window.
	IntervalMonth = "month"
)

var (
	// ErrInvalidRequestLimit reports a non-positive request limit.
	ErrInvalidRequestLimit = errors.New("request limit must be positive")
	// ErrInvalidRequestWindow reports a non-positive request window.
	ErrInvalidRequestWindow = errors.New("request window seconds must be positive")
	// ErrInvalidBudgetLimit reports a non-positive budget limit.
	ErrInvalidBudgetLimit = errors.New("budget limit must be positive")
	// ErrInvalidBudgetInterval reports an unknown budget interval.
	ErrInvalidBudgetInterval = errors.New("budget interval must be day, week, or month")
	// ErrInvalidStoredBytes reports a non-positive stored byte bound.
	ErrInvalidStoredBytes = errors.New("stored bytes limit must be positive")
	// ErrInvalidOutstandingJobs reports a non-positive outstanding job bound.
	ErrInvalidOutstandingJobs = errors.New("outstanding jobs limit must be positive")
)

// Limits carries the request-rate override and the consumption budgets of
// one holder. A nil field leaves that dimension unlimited by this holder.
type Limits struct {
	// Requests overrides the global request window for this holder.
	Requests *RequestLimit `json:"requests,omitempty"`
	// Spend bounds integer nano-USD spend inside one fixed UTC interval.
	Spend *Budget `json:"spend,omitempty"`
	// Tokens bounds total token consumption inside one fixed UTC interval.
	Tokens *Budget `json:"tokens,omitempty"`
	// StoredBytes bounds how many bytes this holder keeps in file storage at
	// one time. It is a level rather than a rate, so no interval resets it: a
	// write raises the total and a delete lowers it.
	StoredBytes *int64 `json:"stored_bytes,omitempty"`
	// OutstandingJobs bounds how many jobs this holder may have running at
	// one time. Like StoredBytes it is a level rather than a rate, and unlike
	// every other limit here it meters work that has not finished: a submitted
	// job is a spend commitment this gateway cannot read yet.
	OutstandingJobs *int64 `json:"outstanding_jobs,omitempty"`
}

// RequestLimit is one request-rate override.
type RequestLimit struct {
	Limit         int64 `json:"limit"`
	WindowSeconds int64 `json:"window_seconds"`
}

// Budget bounds one consumption dimension inside one fixed UTC interval.
type Budget struct {
	Limit    int64  `json:"limit"`
	Interval string `json:"interval"`
}

// Validate checks the limits invariants.
func (l *Limits) Validate() error {
	if l == nil {
		return nil
	}
	if l.Requests != nil {
		if l.Requests.Limit <= 0 {
			return ErrInvalidRequestLimit
		}
		if l.Requests.WindowSeconds <= 0 {
			return ErrInvalidRequestWindow
		}
	}
	for _, budget := range []*Budget{l.Spend, l.Tokens} {
		if budget == nil {
			continue
		}
		if budget.Limit <= 0 {
			return ErrInvalidBudgetLimit
		}
		if !ValidInterval(budget.Interval) {
			return ErrInvalidBudgetInterval
		}
	}
	if l.StoredBytes != nil && *l.StoredBytes <= 0 {
		return ErrInvalidStoredBytes
	}
	if l.OutstandingJobs != nil && *l.OutstandingJobs <= 0 {
		return ErrInvalidOutstandingJobs
	}
	return nil
}

// IsZero reports whether no limit dimension is set.
func (l *Limits) IsZero() bool {
	return l == nil || (l.Requests == nil && l.Spend == nil &&
		l.Tokens == nil && l.StoredBytes == nil && l.OutstandingJobs == nil)
}

// Clone returns a deep copy of the limits.
func (l *Limits) Clone() *Limits {
	if l == nil {
		return nil
	}
	clone := &Limits{}
	if l.Requests != nil {
		requests := *l.Requests
		clone.Requests = &requests
	}
	if l.Spend != nil {
		spend := *l.Spend
		clone.Spend = &spend
	}
	if l.Tokens != nil {
		tokens := *l.Tokens
		clone.Tokens = &tokens
	}
	if l.StoredBytes != nil {
		storedBytes := *l.StoredBytes
		clone.StoredBytes = &storedBytes
	}
	if l.OutstandingJobs != nil {
		outstandingJobs := *l.OutstandingJobs
		clone.OutstandingJobs = &outstandingJobs
	}
	return clone
}

// ValidInterval reports whether interval names a supported budget window.
func ValidInterval(interval string) bool {
	switch interval {
	case IntervalDay, IntervalWeek, IntervalMonth:
		return true
	}
	return false
}
