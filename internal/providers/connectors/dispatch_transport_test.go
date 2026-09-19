package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchTransportIdleConnectionExpires(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	transport := newDispatchTransport(&http.Transport{MaxConnsPerHost: 1, IdleConnTimeout: 20 * time.Millisecond})
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	transport.mu.Lock()
	conn := transport.connections[0].conn
	transport.mu.Unlock()
	require.Eventually(t, func() bool { return conn.Err() != nil }, time.Second, time.Millisecond)
}

func TestDispatchTransportCanceledRequestPreservesActiveStream(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "start")
		w.(http.Flusher).Flush()
		close(started)
		<-release
		_, _ = io.WriteString(w, "finish")
	}))
	defer server.Close()
	transport := newDispatchTransport(&http.Transport{MaxConnsPerHost: 1})
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(request)
	require.ErrorIs(t, err, context.Canceled)
	client.CloseIdleConnections()
	close(release)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "startfinish", string(body))
	transport.mu.Lock()
	conn := transport.connections[0].conn
	transport.mu.Unlock()
	require.Eventually(t, func() bool { return conn.Err() != nil }, time.Second, time.Millisecond)
}
