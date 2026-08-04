package router

import (
	"sync"
	"time"
)

// LatencyTracker tracks provider latencies using exponential moving average (EMA)
type LatencyTracker interface {
	// RecordLatency records a latency measurement for a provider
	RecordLatency(provider string, latency time.Duration)

	// GetLatency returns the current EMA latency for a provider
	GetLatency(provider string) time.Duration

	// GetAllLatencies returns latencies for all tracked providers
	GetAllLatencies() map[string]time.Duration

	// Reset clears all latency data
	Reset()
}

// EMALatencyTracker implements LatencyTracker using exponential moving average
type EMALatencyTracker struct {
	mu         sync.RWMutex
	latencies  map[string]*emaState
	alpha      float64 // EMA smoothing factor (0-1, higher = more weight on recent)
	windowSize int     // Number of samples before EMA kicks in
}

type emaState struct {
	ema         float64
	sampleCount int
	samples     []float64
}

// NewLatencyTracker creates a new EMA-based latency tracker
func NewLatencyTracker(alpha float64, windowSize int) LatencyTracker {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.2 // Default to 20% weight on new samples
	}
	if windowSize <= 0 {
		windowSize = 5
	}

	return &EMALatencyTracker{
		latencies:  make(map[string]*emaState),
		alpha:      alpha,
		windowSize: windowSize,
	}
}

// RecordLatency records a latency measurement for a provider
func (t *EMALatencyTracker) RecordLatency(provider string, latency time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	latencyMs := float64(latency.Milliseconds())

	state, exists := t.latencies[provider]
	if !exists {
		state = &emaState{
			samples: make([]float64, 0, t.windowSize),
		}
		t.latencies[provider] = state
	}

	// During warmup, collect samples
	if state.sampleCount < t.windowSize {
		state.samples = append(state.samples, latencyMs)
		state.sampleCount++

		// Calculate simple average during warmup
		sum := 0.0
		for _, s := range state.samples {
			sum += s
		}
		state.ema = sum / float64(len(state.samples))
	} else {
		// Apply EMA formula: EMA = α * current + (1 - α) * previous_EMA
		state.ema = t.alpha*latencyMs + (1-t.alpha)*state.ema
		state.sampleCount++
	}
}

// GetLatency returns the current EMA latency for a provider
func (t *EMALatencyTracker) GetLatency(provider string) time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.latencies[provider]
	if !exists || state.sampleCount == 0 {
		return 0
	}

	return time.Duration(state.ema) * time.Millisecond
}

// GetAllLatencies returns latencies for all tracked providers
func (t *EMALatencyTracker) GetAllLatencies() map[string]time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]time.Duration)
	for provider, state := range t.latencies {
		if state.sampleCount > 0 {
			result[provider] = time.Duration(state.ema) * time.Millisecond
		}
	}

	return result
}

// Reset clears all latency data
func (t *EMALatencyTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.latencies = make(map[string]*emaState)
}
