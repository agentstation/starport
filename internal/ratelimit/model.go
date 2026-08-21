// Package ratelimit owns fixed-window rate-limit state and persistence.
package ratelimit

import "time"

// Decision is one atomic fixed-window consumption result.
type Decision struct {
	Allowed   bool
	Limit     int64
	Count     int64
	Remaining int64
	ResetAt   time.Time
}
