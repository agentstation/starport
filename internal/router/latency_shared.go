package router

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentstation/starport/internal/availability"
)

const (
	defaultLatencyAlpha          = 0.2
	defaultLatencyWindowSize     = 5
	sharedLatencyKeyPrefix       = "provider-latency:instance:"
	sharedLatencyTTL             = time.Minute
	sharedLatencyRefreshInterval = 5 * time.Second
	sharedLatencyScanLimit       = 1024
	sharedLatencyExchangeTimeout = 2 * time.Second
	sharedLatencyDocumentLimit   = 1 << 20
	sharedLatencyProviderLimit   = 4096
)

// sharedLatencyDocument is one replica's published latency snapshot.
type sharedLatencyDocument struct {
	InstanceID string           `json:"instance_id"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Latencies  map[string]int64 `json:"latencies_ms"`
}

// SharedLatencyTracker layers peer latency snapshots from the distributed
// store over a process-local tracker. A local measurement wins. Peers fill in
// the providers this replica has not called yet, so a fresh replica starts
// with the fleet's view.
type SharedLatencyTracker struct {
	local      LatencyTracker
	store      availability.KVStore
	instanceID string

	mu           sync.Mutex
	peers        map[string]peerLatency
	resetVersion uint64
	running      atomic.Bool
}

type peerLatency struct {
	value    time.Duration
	observed time.Time
}

// NewSharedLatencyTracker wraps a local tracker with distributed publication.
func NewSharedLatencyTracker(local LatencyTracker, store availability.KVStore) *SharedLatencyTracker {
	if local == nil {
		local = NewLatencyTracker(defaultLatencyAlpha, defaultLatencyWindowSize)
	}
	return &SharedLatencyTracker{
		local:      local,
		store:      store,
		instanceID: availability.NewInstanceID(),
		peers:      map[string]peerLatency{},
	}
}

// RecordLatency updates local measurements without shared-storage work.
func (t *SharedLatencyTracker) RecordLatency(provider string, latency time.Duration) {
	t.local.RecordLatency(provider, latency)
}

// Run exchanges advisory measurements until cancellation. One worker runs per tracker.
func (t *SharedLatencyTracker) Run(ctx context.Context) {
	if !t.running.CompareAndSwap(false, true) {
		return
	}
	defer t.running.Store(false)
	ticker := time.NewTicker(sharedLatencyRefreshInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		t.exchange(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (t *SharedLatencyTracker) exchange(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, sharedLatencyExchangeTimeout)
	defer cancel()
	t.publishLocal(ctx)
	t.refreshPeers(ctx)
}

func (t *SharedLatencyTracker) publishLocal(ctx context.Context) {
	measurements := t.local.GetAllLatencies()
	if len(measurements) > sharedLatencyProviderLimit {
		return
	}
	doc := sharedLatencyDocument{
		InstanceID: t.instanceID, UpdatedAt: time.Now(), Latencies: make(map[string]int64, len(measurements)),
	}
	for provider, measurement := range measurements {
		doc.Latencies[provider] = measurement.Milliseconds()
	}
	data, err := json.Marshal(doc)
	if err != nil || len(data) > sharedLatencyDocumentLimit {
		return
	}
	_ = t.store.SetWithTTL(ctx, sharedLatencyKeyPrefix+t.instanceID, data, sharedLatencyTTL)
}

// GetLatency returns the local measurement, or the freshest peer measurement
// when this replica holds none.
func (t *SharedLatencyTracker) GetLatency(provider string) time.Duration {
	if measured := t.local.GetLatency(provider); measured > 0 {
		return measured
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	peer := t.peers[provider]
	if !validPeerTime(peer.observed, time.Now()) {
		return 0
	}
	return peer.value
}

// GetAllLatencies merges peer snapshots under the local measurements.
func (t *SharedLatencyTracker) GetAllLatencies() map[string]time.Duration {
	t.mu.Lock()
	merged := make(map[string]time.Duration, len(t.peers))
	now := time.Now()
	for provider, measured := range t.peers {
		if validPeerTime(measured.observed, now) {
			merged[provider] = measured.value
		}
	}
	t.mu.Unlock()
	for provider, measured := range t.local.GetAllLatencies() {
		merged[provider] = measured
	}
	return merged
}

// Reset clears the local measurements and the merged peer view.
func (t *SharedLatencyTracker) Reset() {
	t.local.Reset()
	t.mu.Lock()
	t.peers = map[string]peerLatency{}
	t.resetVersion++
	t.mu.Unlock()
}

// refreshPeers replaces advisory peer measurements from bounded documents.
func (t *SharedLatencyTracker) refreshPeers(ctx context.Context) {
	t.mu.Lock()
	version := t.resetVersion
	t.mu.Unlock()
	keys, err := t.store.ScanWithPrefix(ctx, sharedLatencyKeyPrefix, sharedLatencyScanLimit)
	if err != nil {
		return
	}
	if len(keys) > sharedLatencyScanLimit {
		keys = keys[:sharedLatencyScanLimit]
	}
	now := time.Now()
	peers := make(map[string]peerLatency)
	remainingBytes := 4 * sharedLatencyDocumentLimit
	// Fetch one document at a time to bound retained response bytes.
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		values, err := t.store.BatchGet(ctx, []string{key})
		if err != nil {
			return
		}
		raw := values[key]
		if len(raw) > sharedLatencyDocumentLimit || len(raw) > remainingBytes {
			continue
		}
		var doc sharedLatencyDocument
		if json.Unmarshal(raw, &doc) != nil || doc.InstanceID == t.instanceID ||
			key != sharedLatencyKeyPrefix+doc.InstanceID || !validPeerTime(doc.UpdatedAt, now) ||
			len(doc.Latencies) > sharedLatencyProviderLimit {
			continue
		}
		remainingBytes -= len(raw)
		for provider, milliseconds := range doc.Latencies {
			if milliseconds <= 0 || milliseconds > int64((1<<63-1)/time.Millisecond) {
				continue
			}
			previous, exists := peers[provider]
			if exists && !doc.UpdatedAt.After(previous.observed) {
				continue
			}
			if !exists && len(peers) >= sharedLatencyProviderLimit {
				continue
			}
			peers[provider] = peerLatency{value: time.Duration(milliseconds) * time.Millisecond, observed: doc.UpdatedAt}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if version == t.resetVersion {
		t.peers = peers
	}
}

func validPeerTime(observed, now time.Time) bool {
	return !observed.IsZero() && !observed.After(now) && now.Sub(observed) < sharedLatencyTTL
}
