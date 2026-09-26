package proxy

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	responsecache "github.com/agentstation/starport/internal/response/cache"
	"github.com/stretchr/testify/require"
)

func TestStreamCacheLimitPreservesDelivery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		text  string
	}{
		{"bytes", 1, strings.Repeat("x", 1<<20)},
		{"events", 2048, "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newMockCacheManager()
			repository, err := responsecache.Open(manager, nil)
			require.NoError(t, err)
			events := []inference.StreamEvent{{Kind: inference.StreamStart, ID: "bounded", Model: "model"}}
			for range tc.count {
				events = append(events, inference.StreamEvent{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{Text: tc.text}}})
			}
			events = append(events, inference.StreamEvent{Kind: inference.StreamEnd, Deltas: []inference.ChoiceDelta{{FinishReason: "stop"}}})
			wrapper := newCachingStreamWrapper(&errorAfterEventsStream{events: events, err: io.EOF}, repository, "bounded")
			for _, want := range events {
				got, err := wrapper.Read()
				require.NoError(t, err)
				require.Equal(t, want.Clone(), *got)
			}
			_, err = wrapper.Read()
			require.ErrorIs(t, err, io.EOF)
			require.Zero(t, manager.calls["SetResponse"], "oversized streams must bypass caching")
			require.NoError(t, wrapper.Close())
		})
	}
}

func TestStreamCacheReleasesInputOnTermination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal error
	}{
		{"eof", io.EOF}, {"failure", io.ErrUnexpectedEOF}, {"close", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newMockCacheManager()
			repository, err := responsecache.Open(manager, nil)
			require.NoError(t, err)
			source := &errorAfterEventsStream{events: []inference.StreamEvent{{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{Text: "partial"}}}}, err: tc.terminal}
			wrapper := newCachingStreamWrapper(source, repository, "termination")
			_, err = wrapper.Read()
			require.NoError(t, err)
			require.Positive(t, wrapper.buffer.RetainedBytes())
			if tc.terminal == nil {
				require.NoError(t, wrapper.Close())
			} else {
				_, err = wrapper.Read()
				require.ErrorIs(t, err, tc.terminal)
			}
			require.Zero(t, wrapper.buffer.RetainedBytes())
			require.Empty(t, wrapper.buffer.Events())
			require.NoError(t, wrapper.Close())
		})
	}
}

type repeatCacheEventStream struct{}

func (repeatCacheEventStream) Read() (*inference.StreamEvent, error) {
	return &inference.StreamEvent{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{Text: "output"}}}, nil
}
func (repeatCacheEventStream) Close() error { return nil }

func TestStreamCacheConcurrentClose(t *testing.T) {
	repository, err := responsecache.Open(newMockCacheManager(), nil)
	require.NoError(t, err)
	wrapper := newCachingStreamWrapper(repeatCacheEventStream{}, repository, "close")
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			_, _ = wrapper.Read()
		}
	})
	for range 1000 {
		require.NoError(t, wrapper.Close())
	}
	workers.Wait()
	require.Empty(t, wrapper.buffer.Events())
	require.Zero(t, wrapper.buffer.RetainedBytes())
}
