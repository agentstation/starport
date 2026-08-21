package identity

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
	// ErrInvalidRequestLimit reports a non-positive per-key request limit.
	ErrInvalidRequestLimit = errors.New("request limit must be positive")
	// ErrInvalidRequestWindow reports a non-positive per-key request window.
	ErrInvalidRequestWindow = errors.New("request window seconds must be positive")
	// ErrInvalidBudgetLimit reports a non-positive budget limit.
	ErrInvalidBudgetLimit = errors.New("budget limit must be positive")
	// ErrInvalidBudgetInterval reports an unknown budget interval.
	ErrInvalidBudgetInterval = errors.New("budget interval must be day, week, or month")
)

// Limits carries the per-key request-rate override and consumption
// budgets. A nil field leaves that dimension unlimited by the key.
type Limits struct {
	// Requests overrides the global request window for this key.
	Requests *RequestLimit `json:"requests,omitempty"`
	// Spend bounds integer nano-USD spend inside one fixed UTC interval.
	Spend *Budget `json:"spend,omitempty"`
	// Tokens bounds total token consumption inside one fixed UTC interval.
	Tokens *Budget `json:"tokens,omitempty"`
}

// RequestLimit is one per-key request-rate override.
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
	return nil
}

// IsZero reports whether no limit dimension is set.
func (l *Limits) IsZero() bool {
	return l == nil || (l.Requests == nil && l.Spend == nil && l.Tokens == nil)
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
