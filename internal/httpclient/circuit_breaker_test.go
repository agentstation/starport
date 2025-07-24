package httpclient

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreakerStates(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 2, 100*time.Millisecond)

	// Initial state should be closed
	if cb.GetState() != StateClosed {
		t.Errorf("Initial state should be closed, got %v", cb.GetState())
	}

	// Should allow requests in closed state
	if !cb.Allow() {
		t.Error("Circuit breaker should allow requests in closed state")
	}

	// Record failures to open the circuit
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Circuit should be open
	if cb.GetState() != StateOpen {
		t.Errorf("Circuit should be open after %d failures, got %v", 3, cb.GetState())
	}

	// Should not allow requests in open state
	if cb.Allow() {
		t.Error("Circuit breaker should not allow requests in open state")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open and allow request
	if !cb.Allow() {
		t.Error("Circuit breaker should allow requests after timeout")
	}

	if cb.GetState() != StateHalfOpen {
		t.Errorf("Circuit should be half-open after timeout, got %v", cb.GetState())
	}

	// Record successes to close the circuit
	cb.RecordSuccess()
	cb.RecordSuccess()

	// Circuit should be closed
	if cb.GetState() != StateClosed {
		t.Errorf("Circuit should be closed after %d successes, got %v", 2, cb.GetState())
	}
}

func TestCircuitBreakerHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker("test", 2, 2, 50*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Fatalf("Circuit should be open, got %v", cb.GetState())
	}

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // This should transition to half-open

	// Failure in half-open should reopen immediately
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Errorf("Circuit should be open after failure in half-open state, got %v", cb.GetState())
	}
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	cb := NewCircuitBreaker("test", 10, 5, 100*time.Millisecond)

	var wg sync.WaitGroup
	// Simulate concurrent requests
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				cb.RecordSuccess()
			} else {
				cb.RecordFailure()
			}
			cb.Allow()
		}(i)
	}

	wg.Wait()

	// Circuit breaker should handle concurrent access without panic
	stats := cb.GetStats()
	if stats.Provider != "test" {
		t.Errorf("Expected provider 'test', got %v", stats.Provider)
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := NewCircuitBreaker("test", 2, 2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Fatalf("Circuit should be open, got %v", cb.GetState())
	}

	// Reset the circuit
	cb.Reset()

	if cb.GetState() != StateClosed {
		t.Errorf("Circuit should be closed after reset, got %v", cb.GetState())
	}

	stats := cb.GetStats()
	if stats.Failures != 0 || stats.Successes != 0 {
		t.Errorf("Counters should be reset, got failures=%d, successes=%d", stats.Failures, stats.Successes)
	}
}

func TestCircuitBreakerStats(t *testing.T) {
	cb := NewCircuitBreaker("test-provider", 3, 2, 100*time.Millisecond)

	// Record some activity
	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordFailure()

	stats := cb.GetStats()

	if stats.Provider != "test-provider" {
		t.Errorf("Expected provider 'test-provider', got %v", stats.Provider)
	}

	if stats.State != "closed" {
		t.Errorf("Expected state 'closed', got %v", stats.State)
	}

	if stats.Successes != 2 {
		t.Errorf("Expected 2 successes, got %d", stats.Successes)
	}

	if stats.Failures != 1 {
		t.Errorf("Expected 1 failure, got %d", stats.Failures)
	}

	if stats.LastFailureTime.IsZero() {
		t.Error("LastFailureTime should not be zero after recording failure")
	}

	if stats.TimeSinceLastFailure() == 0 {
		t.Error("TimeSinceLastFailure should not be zero")
	}
}

func BenchmarkCircuitBreaker(b *testing.B) {
	cb := NewCircuitBreaker("bench", 5, 3, 100*time.Millisecond)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if cb.Allow() {
				if i%3 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
			}
			i++
		}
	})
}