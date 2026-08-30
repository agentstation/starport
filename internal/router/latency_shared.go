package router

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/availability"
)

const (
	sharedLatencyKeyPrefix       = "provider-latency:instance:"
	sharedLatencyTTL             = time.Minute
	sharedLatencyRefreshInterval = 5 * time.Second
	sharedLatencyScanLimit       = 1024
)

// sharedLatencyDocument is one replica's published latency snapshot.
type sharedLatencyDocument struct {
	InstanceID string           `json:"instance_id"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Latencies  map[string]int64 `json:"latencies_ms"`
}

// SharedLatencyTracker layers peer latency snapshots from the distributed
// store over a process-local tracker. A local measurement wins; peers fill in
// the providers this replica has not called yet, so a fresh replica starts
// with the fleet's view.
type SharedLatencyTracker struct {
	local      LatencyTracker
	store      availability.KVStore
	instanceID string

	mu          sync.Mutex
	peers       map[string]time.Duration
	lastPublish time.Time
	lastFetch   time.Time
}

// NewSharedLatencyTracker wraps a local tracker with distributed publication.
func NewSharedLatencyTracker(local LatencyTracker, store availability.KVStore) *SharedLatencyTracker {
	return &SharedLatencyTracker{
		local:      local,
		store:      store,
		instanceID: availability.NewInstanceID(),
		peers:      map[string]time.Duration{},
	}
}

// RecordLatency records the measurement locally and publishes the replica
// snapshot at most once per refresh interval.
func (t *SharedLatencyTracker) RecordLatency(provider string, latency time.Duration) {
	t.local.RecordLatency(provider, latency)

	now := time.Now()
	t.mu.Lock()
	if !t.lastPublish.IsZero() && now.Sub(t.lastPublish) < sharedLatencyRefreshInterval {
		t.mu.Unlock()
		return
	}
	t.lastPublish = now
	t.mu.Unlock()

	doc := sharedLatencyDocument{
		InstanceID: t.instanceID,
		UpdatedAt:  now,
		Latencies:  map[string]int64{},
	}
	for name, measured := range t.local.GetAllLatencies() {
		doc.Latencies[name] = measured.Milliseconds()
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return
	}
	// The snapshot is an advisory routing hint, so a write failure only
	// keeps peers on their current view.
	_ = t.store.SetWithTTL(context.Background(), sharedLatencyKeyPrefix+t.instanceID, data, sharedLatencyTTL)
}

// GetLatency returns the local measurement, or the freshest peer measurement
// when this replica holds none.
func (t *SharedLatencyTracker) GetLatency(provider string) time.Duration {
	if measured := t.local.GetLatency(provider); measured > 0 {
		return measured
	}
	t.refreshPeers()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peers[provider]
}

// GetAllLatencies merges peer snapshots under the local measurements.
func (t *SharedLatencyTracker) GetAllLatencies() map[string]time.Duration {
	t.refreshPeers()
	t.mu.Lock()
	merged := make(map[string]time.Duration, len(t.peers))
	for provider, measured := range t.peers {
		merged[provider] = measured
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
	t.peers = map[string]time.Duration{}
	t.lastPublish = time.Time{}
	t.lastFetch = time.Time{}
	t.mu.Unlock()
}

// refreshPeers reads every replica snapshot at most once per refresh
// interval. The freshest snapshot wins per provider.
func (t *SharedLatencyTracker) refreshPeers() {
	now := time.Now()
	t.mu.Lock()
	if !t.lastFetch.IsZero() && now.Sub(t.lastFetch) < sharedLatencyRefreshInterval {
		t.mu.Unlock()
		return
	}
	t.lastFetch = now
	t.mu.Unlock()

	ctx := context.Background()
	keys, err := t.store.ScanWithPrefix(ctx, sharedLatencyKeyPrefix, sharedLatencyScanLimit)
	if err != nil || len(keys) == 0 {
		return
	}
	values, err := t.store.BatchGet(ctx, keys)
	if err != nil {
		return
	}
	documents := make([]sharedLatencyDocument, 0, len(values))
	for _, raw := range values {
		var doc sharedLatencyDocument
		if json.Unmarshal(raw, &doc) != nil || doc.InstanceID == t.instanceID {
			continue
		}
		documents = append(documents, doc)
	}
	if len(documents) == 0 {
		return
	}

	freshest := make(map[string]time.Time, len(documents))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = make(map[string]time.Duration, len(documents))
	for _, doc := range documents {
		for provider, milliseconds := range doc.Latencies {
			if milliseconds <= 0 {
				continue
			}
			if seen, ok := freshest[provider]; ok && !doc.UpdatedAt.After(seen) {
				continue
			}
			freshest[provider] = doc.UpdatedAt
			t.peers[provider] = time.Duration(milliseconds) * time.Millisecond
		}
	}
}
