package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server"
)

const performanceGatewayKey = "performance-fixture-gateway-key"

// All timestamps share the process monotonic clock. Durations use nanoseconds.
// The controlled upstream replaces the provider service, never its connector.
type performanceUpstreamSample struct {
	arrived time.Time
	waited  time.Duration
	events  []time.Time
}

type performanceSample struct {
	ElapsedNS        int64   `json:"elapsed_ns"`
	HandlerNS        int64   `json:"gateway_handler_ns"`
	BeforeUpstreamNS int64   `json:"before_upstream_ns"`
	ControlledWaitNS int64   `json:"controlled_wait_ns"`
	AdjustedNS       int64   `json:"wait_adjusted_elapsed_ns"`
	FirstByteNS      int64   `json:"first_byte_ns"`
	HeadersNS        int64   `json:"headers_received_ns"`
	ConnectionNS     int64   `json:"client_connection_acquisition_ns"`
	ConnectionReused bool    `json:"client_connection_reused"`
	FirstTokenNS     int64   `json:"first_token_ns"`
	ForwardingNS     []int64 `json:"event_forwarding_ns"`
	ResponseBytes    int     `json:"response_bytes"`
	RequestBytes     int     `json:"request_bytes"`
}

type performancePair struct {
	Stream          bool              `json:"stream"`
	Order           string            `json:"order"`
	Direct          performanceSample `json:"direct"`
	Proxied         performanceSample `json:"proxied"`
	AdjustedDeltaNS int64             `json:"paired_adjusted_delta_ns"`
}

type performanceFixture struct {
	upstream   *httptest.Server
	gateway    *httptest.Server
	client     *http.Client
	samples    chan performanceUpstreamSample
	handlers   chan time.Duration
	calls      atomic.Int64
	wait       time.Duration
	generation string
	checksum   string
	routes     int
}

func newPerformanceFixture(tb testing.TB, wait time.Duration) *performanceFixture {
	tb.Helper()
	f := &performanceFixture{samples: make(chan performanceUpstreamSample, 1), handlers: make(chan time.Duration, 1), wait: wait}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
			tb.Errorf("unexpected fixture request: path=%s credential_match=%t", r.URL.Path, r.Header.Get("Authorization") == "Bearer sk-test-key")
		}
		f.serveUpstream(w, r)
	}))
	tb.Cleanup(f.upstream.Close)
	cfg := validProductionConfig(tb)
	cfg.Catalog.StateDirectory = tb.TempDir()
	cfg.Storage.SQL.SQLite.Path = filepath.Join(tb.TempDir(), "starport.db")
	cfg.Telemetry.Metrics = config.TelemetryMetricsOn
	cfg.RateLimiting.DefaultRequestsPerMinute = 1_000_000
	provider := cfg.Providers[catalogs.ProviderIDOpenAI]
	provider.BaseURL = f.upstream.URL
	provider.CredentialReferences = nil
	cfg.Providers[catalogs.ProviderIDOpenAI] = provider
	loaded, err := config.NewLoader().WithEnvironment(map[string]string{"OPENAI_API_KEY": "sk-test-key"}).WithEnvFiles().
		WithPaths(config.PathsForConfigDir(tb.TempDir())).Load(tb.Context(), func(target *config.Config) { *target = *cfg })
	require.NoError(tb, err)
	cfg = loaded

	// Seed and reopen the real on-disk adapter as application startup does.
	store, err := openStorage(cfg.Storage)
	require.NoError(tb, err)
	keys, err := apikey.Open(store)
	require.NoError(tb, err)
	hash := sha256.Sum256([]byte(performanceGatewayKey))
	key := testAPIKey()
	key.Hash = hex.EncodeToString(hash[:])
	key.Limits = &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}
	_, err = keys.Create(tb.Context(), key)
	require.NoError(tb, err)
	require.NoError(tb, store.Close())
	application, err := New(cfg)
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, application.Close(context.Background())) })
	httpServer, ok := application.httpServer.(*server.Server)
	require.True(tb, ok)
	f.generation = application.catalog.Current().GenerationID()
	f.checksum = application.catalog.Current().PayloadChecksum()
	f.routes = len(application.catalog.Current().Routes())
	// App.New composes the real middleware, stores, catalog and connectors.
	// App.Run's scheduled maintenance stays outside this quiescent baseline.
	f.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		httpServer.Router().ServeHTTP(w, r)
		f.handlers <- time.Since(start)
	}))
	tb.Cleanup(f.gateway.Close)
	transport := &http.Transport{MaxIdleConns: 4, MaxIdleConnsPerHost: 2}
	f.client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	tb.Cleanup(transport.CloseIdleConnections)
	return f
}

func (f *performanceFixture) serveUpstream(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	sample := performanceUpstreamSample{arrived: time.Now()}
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
		http.Error(w, "unexpected fixture destination or credential", http.StatusBadRequest)
		return
	}
	var body struct {
		Stream    bool   `json:"stream"`
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid fixture body", http.StatusBadRequest)
		return
	}
	if body.Model != "gpt-4o-mini" || body.MaxTokens != 32 || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "Hello" {
		http.Error(w, "fixture request changed model, token limit or message", http.StatusBadRequest)
		return
	}
	wait := func() {
		start := time.Now()
		time.Sleep(f.wait)
		sample.waited += time.Since(start)
	}
	wait()
	if body.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		for index, token := range []string{"one ", "two ", "three"} {
			if index > 0 {
				wait()
			}
			sample.events = append(sample.events, time.Now())
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"perf\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", token)
			_ = http.NewResponseController(w).Flush()
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"perf\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"perf","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"one two three"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`)
	}
	f.samples <- sample
}

// measure includes the loopback client, HTTP stacks and gateway work. Only
// observed deliberate upstream sleeps are removed from AdjustedNS.
func (f *performanceFixture) measure(tb testing.TB, proxied, stream bool) performanceSample {
	tb.Helper()
	base, model, key := f.upstream.URL, "gpt-4o-mini", "sk-test-key"
	if proxied {
		base, model, key = f.gateway.URL, "openai/gpt-4o-mini", performanceGatewayKey
	}
	body := fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`, model, stream)
	request, err := http.NewRequestWithContext(tb.Context(), http.MethodPost, base+"/v1/chat/completions", strings.NewReader(body))
	require.NoError(tb, err)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	start := time.Now()
	var firstByte, connection atomic.Int64
	var reused atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotFirstResponseByte: func() { firstByte.Store(time.Since(start).Nanoseconds()) },
		GotConn: func(info httptrace.GotConnInfo) {
			connection.Store(time.Since(start).Nanoseconds())
			reused.Store(info.Reused)
		},
	}))
	response, err := f.client.Do(request)
	require.NoError(tb, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		tb.Fatalf("full-path response = %d: %s", response.StatusCode, failure)
	}
	sample := performanceSample{
		FirstByteNS: firstByte.Load(), HeadersNS: time.Since(start).Nanoseconds(),
		ConnectionNS: connection.Load(), ConnectionReused: reused.Load(),
		RequestBytes: len(body),
	}
	var received []time.Time
	if stream {
		scanner := bufio.NewScanner(response.Body)
		var answer strings.Builder
		done := false
		for scanner.Scan() {
			line := scanner.Bytes()
			sample.ResponseBytes += len(line) + 1
			if bytes.Equal(line, []byte("data: [DONE]")) {
				done = true
				continue
			}
			data, found := bytes.CutPrefix(line, []byte("data: "))
			if !found {
				continue
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			require.NoError(tb, json.Unmarshal(data, &chunk))
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					received = append(received, time.Now())
					answer.WriteString(choice.Delta.Content)
				}
			}
		}
		require.NoError(tb, scanner.Err())
		require.True(tb, done)
		require.Equal(tb, "one two three", answer.String())
	} else {
		data, err := io.ReadAll(response.Body)
		require.NoError(tb, err)
		sample.ResponseBytes = len(data)
		require.Contains(tb, string(data), "one two three")
	}
	sample.ElapsedNS = time.Since(start).Nanoseconds()
	if proxied {
		select {
		case elapsed := <-f.handlers:
			sample.HandlerNS = elapsed.Nanoseconds()
		case <-time.After(time.Second):
			tb.Fatal("gateway handler completion sample is missing")
		}
	}
	var upstream performanceUpstreamSample
	select {
	case upstream = <-f.samples:
	case <-time.After(time.Second):
		tb.Fatal("controlled upstream sample is missing")
	}
	sample.BeforeUpstreamNS = upstream.arrived.Sub(start).Nanoseconds()
	sample.ControlledWaitNS = upstream.waited.Nanoseconds()
	sample.AdjustedNS = sample.ElapsedNS - sample.ControlledWaitNS
	require.GreaterOrEqual(tb, sample.AdjustedNS, int64(0))
	require.Len(tb, received, len(upstream.events))
	for index, timestamp := range received {
		if index == 0 {
			sample.FirstTokenNS = timestamp.Sub(start).Nanoseconds()
		}
		delay := timestamp.Sub(upstream.events[index]).Nanoseconds()
		require.GreaterOrEqual(tb, delay, int64(0))
		sample.ForwardingNS = append(sample.ForwardingNS, delay)
	}
	return sample
}

func TestFullPathMeasurementIncludesAuthentication(t *testing.T) {
	f := newPerformanceFixture(t, 0)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.gateway.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	response, err := f.client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	select {
	case <-f.handlers:
	case <-time.After(time.Second):
		t.Fatal("authentication refusal did not complete")
	}
	require.Zero(t, f.calls.Load(), "authentication must run before the provider")
	f.measure(t, true, false)
	require.Equal(t, int64(1), f.calls.Load())
}

func TestFullPathMeasurementBaseline(t *testing.T) {
	f := newPerformanceFixture(t, time.Millisecond)
	count := 8
	if value := os.Getenv("STARPORT_FULL_PATH_SAMPLES"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, count, 1)
		require.LessOrEqual(t, count, 100_000)
	}
	var cold []performancePair
	var pairs []performancePair
	for _, stream := range []bool{false, true} {
		initial := performancePair{Stream: stream, Order: "direct-first", Direct: f.measure(t, false, stream), Proxied: f.measure(t, true, stream)}
		initial.AdjustedDeltaNS = initial.Proxied.AdjustedNS - initial.Direct.AdjustedNS
		cold = append(cold, initial)
		for index := range count {
			pair := performancePair{Stream: stream, Order: "direct-first"}
			if index%2 == 0 {
				pair.Direct, pair.Proxied = f.measure(t, false, stream), f.measure(t, true, stream)
			} else {
				pair.Order = "proxy-first"
				pair.Proxied, pair.Direct = f.measure(t, true, stream), f.measure(t, false, stream)
			}
			pair.AdjustedDeltaNS = pair.Proxied.AdjustedNS - pair.Direct.AdjustedNS
			pairs = append(pairs, pair)
		}
	}
	if path := os.Getenv("STARPORT_FULL_PATH_REPORT"); path != "" {
		report := map[string]any{
			"schema_version": 1, "profile": "local-quiescent-baseline-v1", "qualification": "UNVERIFIED",
			"go_version": runtime.Version(), "os": runtime.GOOS, "architecture": runtime.GOARCH,
			"catalog_generation": f.generation, "catalog_checksum": f.checksum, "catalog_routes": f.routes,
			"gomaxprocs": runtime.GOMAXPROCS(0), "concurrency": 1, "load": "closed-loop, alternating direct/proxy order",
			"storage": "persistent Badger and SQLite in isolated temporary directories", "response_cache": "disabled",
			"credentials": "fixture environment credential", "metrics": "on", "usage_capture": "on",
			"maintenance": "App.Run loops are not started", "network": "loopback HTTP/1.1, pooled connections, no TLS or DNS",
			"controlled_wait_per_event_ns": f.wait.Nanoseconds(), "warm_pairs": pairs, "initial_pairs": cold,
			"limitations": "Paired deltas include loopback and client work. They do not isolate gateway CPU or qualify production latency.",
		}
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	}
}

// Allocations include the client, upstream fixture and complete gateway.
// Profiles must attribute gateway frames before any gateway allocation claim.
func BenchmarkFullPathHTTP(b *testing.B) {
	for _, stream := range []bool{false, true} {
		b.Run(fmt.Sprintf("stream=%t", stream), func(b *testing.B) {
			f := newPerformanceFixture(b, 0)
			f.measure(b, true, stream)
			b.ReportAllocs()
			for b.Loop() {
				f.measure(b, true, stream)
			}
		})
	}
}
