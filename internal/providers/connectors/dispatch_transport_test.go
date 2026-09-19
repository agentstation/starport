package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
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
	transport.mu.Lock()
	conn := transport.connections[0].conn
	transport.mu.Unlock()
	client.CloseIdleConnections()
	close(release)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "startfinish", string(body))
	require.Eventually(t, func() bool { return conn.Err() != nil }, time.Second, time.Millisecond)
}

func TestDispatchCapacityNotificationWakesEveryWaiter(t *testing.T) {
	var wake dispatchWake
	revision := wake.revision.Load()
	first := wake.subscribe(revision)
	second := wake.subscribe(revision)
	wake.signal()
	select {
	case <-first:
	default:
		t.Fatal("first waiter missed capacity change")
	}
	select {
	case <-second:
	default:
		t.Fatal("second waiter missed capacity change")
	}
	require.Nil(t, wake.subscribe(revision), "a change before subscription must not be lost")
}

func TestDispatchTransportBoundsIdleConnectionsAcrossOrigins(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	first := httptest.NewServer(handler)
	defer first.Close()
	second := httptest.NewServer(handler)
	defer second.Close()
	transport := newDispatchTransport(&http.Transport{MaxConnsPerHost: 1, MaxIdleConns: 1, IdleConnTimeout: time.Minute})
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	for _, endpoint := range []string{first.URL, second.URL} {
		response, err := client.Get(endpoint)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	require.Eventually(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		idle := 0
		for _, entry := range transport.connections {
			if entry.conn.Err() == nil && entry.conn.InFlight() == 0 {
				idle++
			}
		}
		return idle <= 1
	}, time.Second, time.Millisecond)
}

func TestDispatchTransportPreservesForwardProxy(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	endpoint, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	transport := newDispatchTransport(&http.Transport{Proxy: http.ProxyURL(endpoint), MaxConnsPerHost: 1})
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://provider.invalid/inference")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "http://provider.invalid/inference", <-seen)
}

func TestDispatchTransportDoesNotRetryAndRecoversClosedConnection(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if received.Add(1) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	transport := newDispatchTransport(&http.Transport{MaxConnsPerHost: 1})
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	_, err := client.Get(server.URL)
	require.Error(t, err)
	require.EqualValues(t, 1, received.Load())
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 2, received.Load())
}

func BenchmarkProviderDispatchTransport(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	for _, reserved := range []bool{false, true} {
		name := "standard"
		if reserved {
			name = "reserved"
		}
		b.Run(name, func(b *testing.B) {
			base := &http.Transport{MaxConnsPerHost: 16, MaxIdleConns: 16, MaxIdleConnsPerHost: 16}
			client := &http.Client{Transport: base}
			if reserved {
				client.Transport = newDispatchTransport(base)
			}
			defer client.CloseIdleConnections()
			response, err := client.Get(server.URL)
			if err != nil {
				b.Fatal(err)
			}
			_ = response.Body.Close()
			b.ReportAllocs()
			for b.Loop() {
				request, err := http.NewRequestWithContext(b.Context(), http.MethodGet, server.URL, nil)
				if err != nil {
					b.Fatal(err)
				}
				response, err := client.Do(request)
				if err != nil {
					b.Fatal(err)
				}
				_ = response.Body.Close()
			}
		})
	}
}
