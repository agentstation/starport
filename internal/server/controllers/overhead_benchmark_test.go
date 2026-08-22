package controllers_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/stretchr/testify/require"
)

// The published performance claim: the gateway adds under 50ms p99 to a
// request, measured as x-starport-overhead-ms through the real chat
// pipeline (decode, validate, route, execute, encode) against a mock
// upstream. scripts/benchmark-overhead.sh runs this in CI and fails the
// build when the bound breaks. The methodology lives in
// docs/PERFORMANCE.md.
const (
	overheadBenchRequests   = 200
	overheadBenchBoundMS    = 50
	overheadBenchUpstreamMS = 20
)

type benchConnector struct{}

func (benchConnector) Name() string { return "openai" }

func (benchConnector) Chat(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	time.Sleep(overheadBenchUpstreamMS * time.Millisecond)
	return &connectors.ChatResponse{
		ID:    "chatcmpl-bench",
		Model: req.Model,
		Choices: []connectors.Choice{
			{Message: connectors.Message{Role: "assistant", Content: "ok"}},
		},
	}, nil
}

func (benchConnector) ChatStream(context.Context, *connectors.ChatRequest) (connectors.ChatStream, error) {
	return nil, fmt.Errorf("streaming is not part of this harness")
}

func (benchConnector) Embeddings(context.Context, *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
	return nil, fmt.Errorf("embeddings are not part of this harness")
}

func (benchConnector) Close() error { return nil }

type benchRegistry struct{ connector connectors.Connector }

func (r *benchRegistry) Get(string) connectors.Connector { return r.connector }
func (r *benchRegistry) List() []string                  { return []string{"openai"} }
func (r *benchRegistry) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	return credentials.Material{}, nil
}

func TestGatewayOverheadBenchmark(t *testing.T) {
	if os.Getenv("STARPORT_OVERHEAD_BENCH") == "" {
		t.Skip("set STARPORT_OVERHEAD_BENCH=1 to run the overhead benchmark harness")
	}

	registry := &benchRegistry{connector: benchConnector{}}
	service := proxy.New(nil, router.New(registry))
	controller := controllers.NewChatController(service)
	body := `{"model":"openai/gpt-4","messages":[{"role":"user","content":"benchmark"}]}`

	overheads := make([]int64, 0, overheadBenchRequests)
	for range overheadBenchRequests {
		recorder := httptest.NewRecorder()
		controller.Create(recorder, httptest.NewRequest(
			http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body),
		))
		require.Equal(t, http.StatusOK, recorder.Code)
		value := recorder.Header().Get(proxy.OverheadHeader)
		require.NotEmpty(t, value, "every response must carry the overhead header")
		overhead, err := strconv.ParseInt(value, 10, 64)
		require.NoError(t, err)
		overheads = append(overheads, overhead)
	}

	sort.Slice(overheads, func(i, j int) bool { return overheads[i] < overheads[j] })
	p50 := overheads[(overheadBenchRequests*50+99)/100-1]
	p99 := overheads[(overheadBenchRequests*99+99)/100-1]
	t.Logf("gateway overhead over %d requests (mock upstream %dms): p50=%dms p99=%dms",
		overheadBenchRequests, overheadBenchUpstreamMS, p50, p99)
	require.LessOrEqual(t, p99, int64(overheadBenchBoundMS),
		"gateway overhead p99 exceeds the published %dms bound", overheadBenchBoundMS)
}
