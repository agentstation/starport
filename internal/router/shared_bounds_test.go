package router

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type boundedAdvisoryStore struct {
	*fakeHealthStore
	keys      []string
	reads     int
	scanLimit int
}

func (s *boundedAdvisoryStore) ScanWithPrefix(_ context.Context, _ string, limit int) ([]string, error) {
	s.scanLimit = limit
	return s.keys, nil
}
func (s *boundedAdvisoryStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.reads += len(keys)
	return s.fakeHealthStore.BatchGet(ctx, keys)
}

func (s *boundedAdvisoryStore) put(t *testing.T, id string, doc any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	key := sharedLatencyKeyPrefix + id
	s.keys = append(s.keys, key)
	require.NoError(t, s.SetWithTTL(t.Context(), key, raw, time.Minute))
}

func TestLatencyExchangeBounds(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeHealthStore: newFakeHealthStore()}
		for i := range sharedLatencyScanLimit + 1 {
			store.keys = append(store.keys, fmt.Sprint(i))
		}
		tracker := NewSharedLatencyTracker(nil, store)
		tracker.refreshPeers(t.Context())
		require.Equal(t, sharedLatencyScanLimit, store.scanLimit)
		require.Equal(t, sharedLatencyScanLimit, store.reads)
	})
	t.Run("document_bytes", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeHealthStore: newFakeHealthStore()}
		store.put(t, "peer", sharedLatencyDocument{InstanceID: "peer", UpdatedAt: time.Now(), Latencies: map[string]int64{strings.Repeat("x", sharedLatencyDocumentLimit): 1}})
		tracker := NewSharedLatencyTracker(nil, store)
		tracker.refreshPeers(t.Context())
		require.Empty(t, tracker.GetAllLatencies())
	})
	t.Run("refresh_bytes", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeHealthStore: newFakeHealthStore()}
		for i := range 6 {
			id := fmt.Sprint(i)
			store.put(t, id, sharedLatencyDocument{InstanceID: id, UpdatedAt: time.Now(), Latencies: map[string]int64{id + strings.Repeat("x", 3*sharedLatencyDocumentLimit/4): 1}})
		}
		tracker := NewSharedLatencyTracker(nil, store)
		tracker.refreshPeers(t.Context())
		require.Len(t, tracker.GetAllLatencies(), 5)
	})
	t.Run("retained_records", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeHealthStore: newFakeHealthStore()}
		for doc := range 9 {
			id := fmt.Sprint(doc)
			records := make(map[string]int64)
			for i := range 512 {
				records[fmt.Sprint(doc*512+i)] = 1
			}
			store.put(t, id, sharedLatencyDocument{InstanceID: id, UpdatedAt: time.Now(), Latencies: records})
		}
		tracker := NewSharedLatencyTracker(nil, store)
		tracker.refreshPeers(t.Context())
		require.Len(t, tracker.GetAllLatencies(), sharedLatencyProviderLimit)
	})
}
