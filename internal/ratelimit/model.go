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

// TokenBucket is one in-memory token-bucket state value.
type TokenBucket struct {
	Tokens     float64   `json:"tokens"`
	Capacity   float64   `json:"capacity"`
	RefillRate float64   `json:"refill_rate"`
	LastRefill time.Time `json:"last_refill"`
}

// Validate checks token-bucket invariants.
func (b TokenBucket) Validate() error {
	if b.Capacity <= 0 {
		return ErrInvalidCapacity
	}
	if b.RefillRate <= 0 {
		return ErrInvalidRefillRate
	}
	if b.Tokens < 0 || b.Tokens > b.Capacity {
		return ErrInvalidTokens
	}
	return nil
}

// RefillAt applies elapsed refill at a deterministic time.
func (b *TokenBucket) RefillAt(now time.Time) {
	if b == nil || now.Before(b.LastRefill) {
		return
	}
	b.Tokens += now.Sub(b.LastRefill).Seconds() * b.RefillRate
	if b.Tokens > b.Capacity {
		b.Tokens = b.Capacity
	}
	b.LastRefill = now
}

// TryConsumeAt refills and consumes tokens at a deterministic time.
func (b *TokenBucket) TryConsumeAt(tokens float64, now time.Time) bool {
	if b == nil || tokens < 0 {
		return false
	}
	b.RefillAt(now)
	if b.Tokens < tokens {
		return false
	}
	b.Tokens -= tokens
	return true
}
