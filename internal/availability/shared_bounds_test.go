package availability

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
	*fakeKVStore
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
	return s.fakeKVStore.BatchGet(ctx, keys)
}

func (s *boundedAdvisoryStore) put(t *testing.T, id string, doc any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	key := sharedHealthKeyPrefix + id
	s.keys = append(s.keys, key)
	require.NoError(t, s.SetWithTTL(t.Context(), key, raw, time.Minute))
}

func TestHealthExchangeBounds(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeKVStore: newFakeKVStore()}
		for i := range sharedScanLimit + 1 {
			store.keys = append(store.keys, fmt.Sprint(i))
		}
		tracker := sharedTracker(t, nil, store, "local")
		tracker.mergePeerState(t.Context())
		require.Equal(t, sharedScanLimit, store.scanLimit)
		require.Equal(t, sharedScanLimit, store.reads)
	})
	t.Run("document_bytes", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeKVStore: newFakeKVStore()}
		now := time.Now()
		store.put(t, "peer", sharedDocument{InstanceID: "peer", UpdatedAt: now, Records: []sharedRecord{{ProviderID: "provider", ProviderModelID: strings.Repeat("x", sharedDocumentLimit), State: StateUnavailable, UpdatedAt: now}}})
		tracker := sharedTracker(t, nil, store, "local")
		tracker.mergePeerState(t.Context())
		require.Empty(t, tracker.Snapshot().Records)
	})
	t.Run("refresh_bytes", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeKVStore: newFakeKVStore()}
		now := time.Now()
		for i := range 6 {
			id := fmt.Sprint(i)
			store.put(t, id, sharedDocument{InstanceID: id, UpdatedAt: now, Records: []sharedRecord{{ProviderID: id, ProviderModelID: strings.Repeat("x", 3*sharedDocumentLimit/4), State: StateUnavailable, UpdatedAt: now}}})
		}
		tracker := sharedTracker(t, nil, store, "local")
		tracker.mergePeerState(t.Context())
		require.Len(t, tracker.Snapshot().Records, 5)
	})
	t.Run("retained_records_across_refreshes", func(t *testing.T) {
		store := &boundedAdvisoryStore{fakeKVStore: newFakeKVStore()}
		clock := &fakeClock{now: time.Now()}
		tracker := sharedTracker(t, clock, store, "local")
		for pass := range 2 {
			store.keys = nil
			for doc := range 8 {
				id := fmt.Sprint(doc)
				records := make([]sharedRecord, 512)
				for i := range records {
					records[i] = sharedRecord{ProviderID: "provider", ProviderModelID: fmt.Sprint(pass*4096 + doc*512 + i), State: StateUnavailable, UpdatedAt: clock.Now()}
				}
				store.put(t, id, sharedDocument{InstanceID: id, UpdatedAt: clock.Now(), Records: records})
			}
			tracker.mergePeerState(t.Context())
			require.Len(t, tracker.Snapshot().Records, sharedRecordLimit)
			clock.Advance(DefaultSharedRefreshInterval)
		}
	})
}
