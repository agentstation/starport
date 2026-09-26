package connectors

import (
	"context"
	"io"
	"net"
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

func TestDispatchTransportCancellationDuringDialReleasesCapacity(t *testing.T) {
	started := make(chan struct{})
	base := &http.Transport{MaxConnsPerHost: 1, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	transport := newDispatchTransport(base)
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider.invalid", nil)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := transport.RoundTrip(request); result <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Empty(t, transport.dialing)
	require.Empty(t, transport.connections)
}

func TestDispatchTransportHTTP2StreamFailureDoesNotRetry(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol = %s, want HTTP/2", r.Proto)
		}
		if received.Add(1) == 1 {
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.ForceAttemptHTTP2 = true
	base.MaxConnsPerHost = 1
	client := &http.Client{Transport: newDispatchTransport(base)}
	defer client.CloseIdleConnections()
	_, err := client.Get(server.URL)
	require.Error(t, err)
	require.EqualValues(t, 1, received.Load())
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 2, response.ProtoMajor)
	require.EqualValues(t, 2, received.Load())
}

func TestDispatchTransportPreservesHTTPSProxyTunnel(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer origin.Close()
	target, err := url.Parse(origin.URL)
	require.NoError(t, err)
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target.Host {
			http.Error(w, "unexpected tunnel", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", target.Host, time.Second)
		if err != nil {
			t.Error(err)
			http.Error(w, "dial failed", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		seen <- r.Host
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		<-done
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	base := origin.Client().Transport.(*http.Transport).Clone()
	base.Proxy = http.ProxyURL(proxyURL)
	base.MaxConnsPerHost = 1
	client := &http.Client{Transport: newDispatchTransport(base)}
	defer client.CloseIdleConnections()
	response, err := client.Get(origin.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, target.Host, <-seen)
}
