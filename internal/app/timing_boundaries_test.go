package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server"
)

// boundaryDelay is the one delay that each subtest injects at its boundary.
// The boundary milestone must fall in [delay/2, 3*delay/2) and every other
// milestone below delay/2. The ranges do not overlap, so a milestone in the
// first range carries the injected delay and a milestone in the second does not.
const boundaryDelay = 600 * time.Millisecond

const (
	boundaryProbeHeader = "X-Starport-Timing-Probe"
	boundaryHost        = "provider.starport-timing.test"
	gatewayProcessing   = "gateway_processing"
)

// boundaryProbe records the net/http trace milestones of one gateway provider
// request and the time the gateway spent blocked in client writes. The
// production dispatch transport and net/http emit each trace event on the real
// request path. The probe only reads them.
type boundaryProbe struct {
	mu                                    sync.Mutex
	getConn, dnsStart, dnsDone            time.Time
	connectStart, setupDone, tlsDone      time.Time
	wroteHeaders, wroteRequest, firstByte time.Time
	handler, clientWrites                 time.Duration
	queued, done                          chan struct{}
	queuedOnce                            sync.Once
}

func newBoundaryProbe() *boundaryProbe {
	return &boundaryProbe{queued: make(chan struct{}), done: make(chan struct{})}
}

func (p *boundaryProbe) first(at *time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if at.IsZero() {
		*at = time.Now()
	}
}

func (p *boundaryProbe) last(at *time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	*at = time.Now()
}

func (p *boundaryProbe) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(string) {
			p.first(&p.getConn)
			p.queuedOnce.Do(func() { close(p.queued) })
		},
		DNSStart:     func(httptrace.DNSStartInfo) { p.first(&p.dnsStart) },
		DNSDone:      func(httptrace.DNSDoneInfo) { p.last(&p.dnsDone) },
		ConnectStart: func(string, string) { p.first(&p.connectStart) },
		ConnectDone:  func(string, string, error) { p.last(&p.setupDone) },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			p.last(&p.setupDone)
			p.last(&p.tlsDone)
		},
		WroteHeaders:         func() { p.first(&p.wroteHeaders) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { p.first(&p.wroteRequest) },
		GotFirstResponseByte: func() { p.first(&p.firstByte) },
	}
}

// boundarySample holds the separated milestones of one probed request.
type boundarySample struct {
	milestones map[string]time.Duration
	dialed     bool
	resolved   bool
	tls        bool
}

// sample separates the boundaries from the trace events. Connection queueing
// ends at the first connection work or at the header write on a pooled
// connection. Provider wait runs from the written request to the first
// response byte. Gateway processing is the handler time that no measured
// boundary covers. It includes the connector codecs and the body transfer.
func (p *boundaryProbe) sample(t *testing.T) boundarySample {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the probed gateway handler did not complete")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	require.False(t, p.getConn.IsZero(), "the dispatch transport did not report the connection request")
	require.False(t, p.wroteRequest.IsZero(), "the provider request was not written")
	require.False(t, p.firstByte.IsZero(), "the provider response byte was not observed")
	queueEnd := p.wroteHeaders
	for _, work := range []time.Time{p.dnsStart, p.connectStart} {
		if !work.IsZero() && work.Before(queueEnd) {
			queueEnd = work
		}
	}
	interval := func(start, end time.Time) time.Duration {
		if start.IsZero() || end.IsZero() {
			return 0
		}
		return end.Sub(start)
	}
	milestones := map[string]time.Duration{
		"connection_queueing": queueEnd.Sub(p.getConn),
		"dns":                 interval(p.dnsStart, p.dnsDone),
		"tcp_tls_setup":       interval(p.connectStart, p.setupDone),
		"provider_wait":       p.firstByte.Sub(p.wroteRequest),
		"client_backpressure": p.clientWrites,
	}
	processing := p.handler
	for _, measured := range milestones {
		processing -= measured
	}
	milestones[gatewayProcessing] = processing
	return boundarySample{
		milestones: milestones,
		dialed:     !p.connectStart.IsZero(),
		resolved:   !p.dnsStart.IsZero(),
		tls:        !p.tlsDone.IsZero(),
	}
}

// requireBoundary proves that the named milestone carries the injected delay
// and that no other milestone absorbs it.
func requireBoundary(t *testing.T, sample boundarySample, boundary string) {
	t.Helper()
	requireBoundaryBelow(t, sample, boundary, boundaryDelay+boundaryDelay/2)
}

// requireBoundaryBelow is requireBoundary with a caller-selected ceiling for
// the named milestone.
func requireBoundaryBelow(t *testing.T, sample boundarySample, boundary string, ceiling time.Duration) {
	t.Helper()
	require.Contains(t, sample.milestones, boundary)
	t.Logf("%s milestones: %v", boundary, sample.milestones)
	for name, value := range sample.milestones {
		if name == boundary {
			require.GreaterOrEqual(t, value, boundaryDelay/2, "%s did not carry the injected delay: %v", name, sample.milestones)
			require.Less(t, value, ceiling, "%s carried more than its ceiling %s: %v", name, ceiling, sample.milestones)
			continue
		}
		require.Less(t, value, boundaryDelay/2, "%s absorbed the %s delay: %v", name, boundary, sample.milestones)
	}
}

// timedResponseWriter adds the time that the gateway spends in client writes.
// A slow reader fills the socket buffers, and then each write blocks.
type timedResponseWriter struct {
	http.ResponseWriter
	blocked time.Duration
}

func (w *timedResponseWriter) Write(data []byte) (int, error) {
	start := time.Now()
	defer func() { w.blocked += time.Since(start) }()
	return w.ResponseWriter.Write(data)
}

func (w *timedResponseWriter) Flush() {
	start := time.Now()
	defer func() { w.blocked += time.Since(start) }()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *timedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// boundaryGateway serves the real router of the fixture. It attaches the probe
// trace to a request that carries the probe header. The trace travels on the
// request context to the provider request that the connector sends.
func boundaryGateway(t *testing.T, f *performanceFixture, probe *boundaryProbe, listen func(net.Listener) net.Listener) *httptest.Server {
	t.Helper()
	httpServer, ok := f.application.httpServer.(*server.Server)
	require.True(t, ok)
	router := httpServer.Router()
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(boundaryProbeHeader) == "" {
			router.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		writer := &timedResponseWriter{ResponseWriter: w}
		router.ServeHTTP(writer, r.WithContext(httptrace.WithClientTrace(r.Context(), probe.clientTrace())))
		probe.mu.Lock()
		probe.handler, probe.clientWrites = time.Since(start), writer.blocked
		probe.mu.Unlock()
		close(probe.done)
	}))
	if listen != nil {
		gateway.Listener = listen(gateway.Listener)
	}
	gateway.Start()
	t.Cleanup(gateway.Close)
	return gateway
}

func boundaryRequest(t *testing.T, gateway string, content string, stream, probed bool) *http.Request {
	t.Helper()
	body := fmt.Sprintf(`{"model":"openai/gpt-4o-mini","stream":%t,"max_tokens":32,"messages":[{"role":"user","content":%q}]}`, stream, content)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, gateway+"/v1/chat/completions", strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	request.Header.Set("Content-Type", "application/json")
	if probed {
		request.Header.Set(boundaryProbeHeader, "measured")
	}
	return request
}

func sendBoundaryRequest(t *testing.T, client *http.Client, request *http.Request) string {
	t.Helper()
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, "%s", data)
	return string(data)
}

// boundaryUpstream decodes the chat request, lets hold delay it by its content,
// and answers with one fixed completion.
func boundaryUpstream(hold func(content string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 1 {
			http.Error(w, "invalid fixture body", http.StatusBadRequest)
			return
		}
		if hold != nil {
			hold(body.Messages[0].Content)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"timing","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`)
	})
}

func newBoundaryFixture(t *testing.T, start performanceOrigin, upstream http.Handler, configure ...func(*config.Config)) *performanceFixture {
	t.Helper()
	return newPerformanceFixtureAt(t, start, 0, nil, true,
		&limits.Limits{Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}, upstream,
		[]performanceProvider{{catalogs.ProviderIDOpenAI, "OPENAI_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/chat/completions"}}},
		nil, nil, configure...)
}

// TestGatewayProviderTimingBoundaries injects one fixed delay at one boundary
// of the real gateway request path in each subtest. Each subtest proves that
// the milestone of that boundary carries the delay and that gateway
// processing and the other boundaries do not absorb it. No milestone subtracts
// a whole connector call.
//
// The dns and tcp_tls_setup subtests replace process-wide network defaults
// that the provider dialer reads, so this test does not run in parallel.
func TestGatewayProviderTimingBoundaries(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	t.Run("connection_queueing", func(t *testing.T) {
		probe := newBoundaryProbe()
		arrived := make(chan struct{})
		f := newBoundaryFixture(t, startPerformanceUpstream, boundaryUpstream(func(content string) {
			if content != "blocker" {
				return
			}
			close(arrived)
			<-probe.queued
			time.Sleep(boundaryDelay)
		}), func(cfg *config.Config) {
			provider := cfg.Providers[catalogs.ProviderIDOpenAI]
			provider.MaxConnections = 1
			cfg.Providers[catalogs.ProviderIDOpenAI] = provider
		})
		gateway := boundaryGateway(t, f, probe, nil)
		blockerRequest := boundaryRequest(t, gateway.URL, "blocker", false, false)
		blocker := make(chan *http.Response, 1)
		blockerErr := make(chan error, 1)
		go func() {
			response, err := client.Do(blockerRequest)
			blocker <- response
			blockerErr <- err
		}()
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("the blocker request did not reach the upstream")
		}
		sendBoundaryRequest(t, client, boundaryRequest(t, gateway.URL, "measured", false, true))
		blockerResponse := <-blocker
		require.NoError(t, <-blockerErr)
		defer blockerResponse.Body.Close()
		require.Equal(t, http.StatusOK, blockerResponse.StatusCode)
		sample := probe.sample(t)
		require.False(t, sample.dialed, "the measured request must wait for the one pooled connection")
		requireBoundary(t, sample, "connection_queueing")
	})

	t.Run("dns", func(t *testing.T) {
		previous := net.DefaultResolver
		net.DefaultResolver = delayedResolver(t, boundaryDelay)
		t.Cleanup(func() { net.DefaultResolver = previous })
		probe := newBoundaryProbe()
		f := newBoundaryFixture(t, func(t testing.TB, handler http.Handler) (*httptest.Server, string) {
			upstream, _ := startPerformanceUpstream(t, handler)
			_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
			require.NoError(t, err)
			return upstream, "http://" + net.JoinHostPort(boundaryHost, port)
		}, boundaryUpstream(nil))
		gateway := boundaryGateway(t, f, probe, nil)
		sendBoundaryRequest(t, client, boundaryRequest(t, gateway.URL, "measured", false, true))
		sample := probe.sample(t)
		require.True(t, sample.resolved, "the provider dialer did not resolve the provider host")
		requireBoundary(t, sample, "dns")
	})

	t.Run("tcp_tls_setup", func(t *testing.T) {
		probe := newBoundaryProbe()
		f := newBoundaryFixture(t, func(t testing.TB, handler http.Handler) (*httptest.Server, string) {
			upstream := httptest.NewUnstartedServer(handler)
			upstream.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				time.Sleep(boundaryDelay)
				return nil, nil
			}}
			upstream.StartTLS()
			trustUpstreamCertificate(t, upstream)
			return upstream, upstream.URL
		}, boundaryUpstream(nil))
		gateway := boundaryGateway(t, f, probe, nil)
		sendBoundaryRequest(t, client, boundaryRequest(t, gateway.URL, "measured", false, true))
		sample := probe.sample(t)
		require.True(t, sample.dialed && sample.tls, "the provider dialer did not set up a TLS connection")
		requireBoundary(t, sample, "tcp_tls_setup")
	})

	t.Run("provider_wait", func(t *testing.T) {
		probe := newBoundaryProbe()
		f := newBoundaryFixture(t, startPerformanceUpstream, boundaryUpstream(func(string) { time.Sleep(boundaryDelay) }))
		gateway := boundaryGateway(t, f, probe, nil)
		sendBoundaryRequest(t, client, boundaryRequest(t, gateway.URL, "measured", false, true))
		requireBoundary(t, probe.sample(t), "provider_wait")
	})

	t.Run("client_backpressure", func(t *testing.T) {
		probe := newBoundaryProbe()
		f := newBoundaryFixture(t, startPerformanceUpstream, streamingBoundaryUpstream())
		gateway := boundaryGateway(t, f, probe, func(listener net.Listener) net.Listener {
			return smallBufferListener{listener}
		})
		slowClient := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err == nil {
					err = conn.(*net.TCPConn).SetReadBuffer(smallSocketBuffer)
				}
				return conn, err
			},
		}}
		started := time.Now()
		response, err := slowClient.Do(boundaryRequest(t, gateway.URL, "measured", true, true))
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		events, done := 0, false
		for scanner.Scan() {
			line := scanner.Bytes()
			if bytes.Equal(line, []byte("data: [DONE]")) {
				done = true
				continue
			}
			if bytes.HasPrefix(line, []byte("data: ")) {
				events++
				if events == 1 {
					time.Sleep(boundaryDelay)
				}
			}
		}
		require.NoError(t, scanner.Err())
		require.True(t, done, "the stream did not complete")
		require.Greater(t, events, backpressureEvents)
		// The small socket buffers also slow the transfer after the pause, and
		// the kernel sets that rate. That time is client backpressure too, so
		// the ceiling is the time that the client took to receive the stream.
		requireBoundaryBelow(t, probe.sample(t), "client_backpressure", time.Since(started))
	})
}

const (
	// smallSocketBuffer bounds the client receive buffer and the gateway send
	// buffer, so the stream below fills both while the client pauses.
	smallSocketBuffer  = 4 << 10
	backpressureEvents = 64
	backpressureText   = 4 << 10
)

type smallBufferListener struct{ net.Listener }

func (l smallBufferListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		err = conn.(*net.TCPConn).SetWriteBuffer(smallSocketBuffer)
	}
	return conn, err
}

// streamingBoundaryUpstream writes about 256 KiB of stream events at once, so
// a client that pauses reading blocks the gateway in its client writes.
func streamingBoundaryUpstream() http.Handler {
	text := strings.Repeat("x", backpressureText)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range backpressureEvents + 1 {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"timing\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
			_ = http.NewResponseController(w).Flush()
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"timing\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":65,\"total_tokens\":73}}\n\ndata: [DONE]\n\n")
	})
}

// trustUpstreamCertificate lets the provider transport, which clones the
// default transport, verify the certificate of the TLS upstream.
func trustUpstreamCertificate(t testing.TB, upstream *httptest.Server) {
	t.Helper()
	transport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	previous := transport.TLSClientConfig
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	t.Cleanup(func() { transport.TLSClientConfig = previous })
}

// delayedResolver answers each address query for any name after the delay. An
// A query receives the IPv4 loopback address. Another query type receives no
// record. The resolver serves DNS over a stream connection in memory.
func delayedResolver(t testing.TB, delay time.Duration) *net.Resolver {
	t.Helper()
	return &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		client, resolver := net.Pipe()
		go func() {
			defer resolver.Close()
			var size [2]byte
			if _, err := io.ReadFull(resolver, size[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(resolver, query); err != nil {
				return
			}
			time.Sleep(delay)
			answer := dnsLoopbackAnswer(query)
			_, _ = resolver.Write(binary.BigEndian.AppendUint16(nil, uint16(len(answer))))
			_, _ = resolver.Write(answer)
		}()
		return client, nil
	}}
}

// dnsLoopbackAnswer builds the answer to one DNS query with one question.
func dnsLoopbackAnswer(query []byte) []byte {
	const headerSize, typeA = 12, 1
	end := headerSize
	for end < len(query) && query[end] != 0 {
		end += int(query[end]) + 1
	}
	end += 5
	if end > len(query) {
		return nil
	}
	questionType := binary.BigEndian.Uint16(query[end-4 : end-2])
	answers := uint16(0)
	if questionType == typeA {
		answers = 1
	}
	answer := append([]byte{}, query[:2]...)
	// The flags state a response, an authoritative answer, and recursion.
	answer = append(answer, 0x85, 0x80, 0, 1)
	answer = binary.BigEndian.AppendUint16(answer, answers)
	answer = append(answer, 0, 0, 0, 0)
	answer = append(answer, query[headerSize:end]...)
	if answers == 1 {
		// The name is a pointer to the question name at offset 12.
		answer = append(answer, 0xc0, headerSize, 0, typeA, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 1)
	}
	return answer
}
