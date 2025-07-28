package httpclient

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open
var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the circuit breaker state
type State int32

const (
	// StateClosed allows all requests
	StateClosed State = iota
	// StateOpen blocks all requests
	StateOpen
	// StateHalfOpen allows limited requests to test recovery
	StateHalfOpen
)

// String returns the string representation of the state
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for a provider
type CircuitBreaker struct {
	provider         string
	failureThreshold int
	successThreshold int
	timeout          time.Duration

	mu              sync.RWMutex
	state           State
	failures        int64
	successes       int64
	lastFailureTime time.Time
	lastStateChange time.Time
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(provider string, failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		provider:         provider,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
		state:            StateClosed,
		lastStateChange:  time.Now(),
	}
}

// Allow returns true if the request should be allowed
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.RLock()
	state := cb.state
	lastStateChange := cb.lastStateChange
	cb.mu.RUnlock()

	switch state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if we should transition to half-open
		if time.Since(lastStateChange) > cb.timeout {
			cb.transitionTo(StateHalfOpen)
			return true
		}
		return false

	case StateHalfOpen:
		// In half-open state, we allow limited requests
		return true

	default:
		return false
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.successes++

	switch cb.state {
	case StateHalfOpen:
		// Check if we have enough successes to close the circuit
		if cb.successes >= int64(cb.successThreshold) {
			cb.transitionToLocked(StateClosed)
		}

	case StateClosed:
		// Reset failure count on success in closed state
		cb.failures = 0
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		// Check if we've exceeded the failure threshold
		if cb.failures >= int64(cb.failureThreshold) {
			cb.transitionToLocked(StateOpen)
		}

	case StateHalfOpen:
		// Any failure in half-open state reopens the circuit
		cb.transitionToLocked(StateOpen)
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats returns statistics about the circuit breaker
func (cb *CircuitBreaker) GetStats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return CircuitBreakerStats{
		Provider:        cb.provider,
		State:           cb.state.String(),
		Failures:        cb.failures,
		Successes:       cb.successes,
		LastFailureTime: cb.lastFailureTime,
		LastStateChange: cb.lastStateChange,
	}
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.lastStateChange = time.Now()
}

// transitionTo transitions to a new state (must be called with lock held)
func (cb *CircuitBreaker) transitionTo(newState State) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.transitionToLocked(newState)
}

// transitionToLocked transitions to a new state (lock must already be held)
func (cb *CircuitBreaker) transitionToLocked(newState State) {
	if cb.state == newState {
		return
	}

	oldState := cb.state
	cb.state = newState
	cb.lastStateChange = time.Now()

	// Reset counters based on transition
	switch newState {
	case StateClosed:
		cb.failures = 0
		cb.successes = 0

	case StateOpen:
		cb.successes = 0

	case StateHalfOpen:
		cb.failures = 0
		cb.successes = 0
	}

	// Log state transition (in production, you'd use proper logging)
	_ = oldState // Avoid unused variable warning
}

// CircuitBreakerStats contains statistics about the circuit breaker
type CircuitBreakerStats struct {
	Provider        string
	State           string
	Failures        int64
	Successes       int64
	LastFailureTime time.Time
	LastStateChange time.Time
}

// IsOpen returns true if the circuit breaker is open
func (s CircuitBreakerStats) IsOpen() bool {
	return s.State == "open"
}

// TimeSinceLastFailure returns the time since the last failure
func (s CircuitBreakerStats) TimeSinceLastFailure() time.Duration {
	if s.LastFailureTime.IsZero() {
		return 0
	}
	return time.Since(s.LastFailureTime)
}
