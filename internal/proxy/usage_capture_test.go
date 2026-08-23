package proxy

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/usage"
)

// recordingUsageRepository captures usage records in memory for assertions.
type recordingUsageRepository struct {
	mu      sync.Mutex
	records []usage.Record
	err     error
}

func (r *recordingUsageRepository) Put(_ context.Context, record usage.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.records = append(r.records, record)
	return nil
}

func (r *recordingUsageRepository) all() []usage.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]usage.Record(nil), r.records...)
}

// usageEvidenceRouter returns a fixed routed response with route evidence.
type usageEvidenceRouter struct {
	response *routepkg.Response
	stream   execution.ManagedStream
	err      error
}

func (r *usageEvidenceRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, nil
}

func (r *usageEvidenceRouter) RouteWithFallback(context.Context, *routepkg.Request) (*routepkg.Response, error) {
	return r.response, r.err
}

func (r *usageEvidenceRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return r.stream, r.err
}

func (r *usageEvidenceRouter) RouteEmbeddings(context.Context, *routepkg.EmbeddingRequest) (*routepkg.EmbeddingResponse, error) {
	return nil, errors.New("not used")
}

// pricedTestSnapshot returns a real routable snapshot plus one route ID whose
// offering has cache-aware token pricing.
func pricedTestSnapshot(t *testing.T) (*runtimecatalog.RoutableSnapshot, string, *starmapcatalogs.ModelTokenPricing) {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	providerID, offering := firstCachePricedOffering(t, client.Catalog())
	types := make([]starmapcatalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID:    providerID,
		Registered:    true,
		Operations:    append([]starmapcatalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
	}))
	routeID := string(providerID) + "/" + string(offering.ProviderModelID)
	_, found := plane.Current().ResolveRoute(routeID)
	require.True(t, found)
	return plane.Current(), routeID, offering.Pricing.Tokens
}

func chatEvidenceResponse(modelID string, snapshot *runtimecatalog.RoutableSnapshot, promptTokens, completionTokens int) *routepkg.Response {
	return &routepkg.Response{
		ChatResponse: &connectors.ChatResponse{
			ID:      "chatcmpl-usage",
			Object:  "chat.completion",
			Created: 1,
			Model:   modelID,
			Choices: []connectors.Choice{{
				Index:   0,
				Message: connectors.Message{Role: "assistant", Content: "ok"},
			}},
			Usage: connectors.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		},
		ModelUsed:       modelID,
		ProviderUsed:    "test-provider",
		Attempts:        1,
		Metadata:        &routepkg.Metadata{RoutingDuration: 25 * time.Millisecond},
		CatalogSnapshot: snapshot,
	}
}

func usageChatRequest() *ChatCompletionRequest {
	return &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
		// The tenant and the key are deliberately different values. Usage is
		// attributed to the key, never to the account it belongs to.
		TenantID:  "acme",
		KeyID:     "key-1",
		RequestID: "req-1",
		Protocol:  "openai",
	}
}

func TestChatCompletionWritesUsageRecord(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 100, 40)}
	service := capture.Wrap(&proxy{router: router})

	request := usageChatRequest()
	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]
	require.Equal(t, "req-1", record.RequestID)
	// A usage record is keyed by the gateway API key, not by the tenant. A
	// tenant's consumption is the sum of its keys, so re-keying the record onto
	// the account would erase which key spent what.
	require.Equal(t, "key-1", record.KeyID)
	require.NotEqual(t, request.TenantID, record.KeyID)
	require.Equal(t, "openai", record.Protocol)
	require.Equal(t, usage.OperationChat, record.Operation)
	require.Equal(t, "openai/gpt-4o", record.ModelRequested)
	require.Equal(t, routeID, record.ModelUsed)
	require.Equal(t, "test-provider", record.Provider)
	require.False(t, record.Streaming)
	require.Equal(t, usage.StatusOK, record.Status)
	require.Equal(t, 200, record.StatusCode)
	require.EqualValues(t, 100, record.Tokens.Input)
	require.EqualValues(t, 40, record.Tokens.Output)
	require.EqualValues(t, 140, record.Tokens.Total)
	require.Equal(t, 1, record.Attempts)
	require.EqualValues(t, 25, record.RoutingMS)
	require.NotNil(t, record.Cost)
	require.Equal(t, "USD", record.Cost.Currency)
	require.Positive(t, record.Cost.NanoUSD)
	require.Empty(t, record.CostUnavailableReason)
	require.NoError(t, record.Validate())
}

func TestUsageRecordCostFromSnapshotPricing(t *testing.T) {
	snapshot, routeID, prices := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)

	response := chatEvidenceResponse(routeID, snapshot, 1000, 200)
	response.ChatResponse.Usage.PromptTokensDetails = &connectors.PromptTokensDetails{CachedTokens: 300}
	response.ChatResponse.Usage.CacheWriteTokens = 100
	router := &usageEvidenceRouter{response: response}
	service := capture.Wrap(&proxy{router: router})

	_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]
	require.NotNil(t, record.Cost)

	inputRate := modelTokenPrice(prices.Input)
	outputRate := modelTokenPrice(prices.Output)
	readRate := inputRate
	if prices.CacheRead != nil {
		readRate = modelTokenPrice(prices.CacheRead)
	}
	writeRate := inputRate
	if prices.CacheWrite != nil {
		writeRate = modelTokenPrice(prices.CacheWrite)
	}
	expected := int64(math.Round((600*inputRate + 300*readRate + 100*writeRate + 200*outputRate) * 1e9))
	require.Equal(t, expected, record.Cost.NanoUSD)
	require.EqualValues(t, 300, record.Tokens.CacheRead)
	require.EqualValues(t, 100, record.Tokens.CacheWrite)
}

func TestUsageRecordCostUnavailableReason(t *testing.T) {
	t.Run("no route without snapshot", func(t *testing.T) {
		repository := &recordingUsageRepository{}
		capture := NewUsageCapture(repository)
		router := &usageEvidenceRouter{response: chatEvidenceResponse("openai/gpt-4o", nil, 10, 5)}
		service := capture.Wrap(&proxy{router: router})

		_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
		require.NoError(t, err)
		capture.Flush()

		records := repository.all()
		require.Len(t, records, 1)
		require.Nil(t, records[0].Cost)
		require.Equal(t, usage.CostReasonNoRoute, records[0].CostUnavailableReason)
		require.NoError(t, records[0].Validate())
	})

	t.Run("no usage from provider", func(t *testing.T) {
		snapshot, routeID, _ := pricedTestSnapshot(t)
		repository := &recordingUsageRepository{}
		capture := NewUsageCapture(repository)
		router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 0, 0)}
		service := capture.Wrap(&proxy{router: router})

		_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
		require.NoError(t, err)
		capture.Flush()

		records := repository.all()
		require.Len(t, records, 1)
		require.Nil(t, records[0].Cost)
		require.Equal(t, usage.CostReasonNoUsage, records[0].CostUnavailableReason)
	})

	t.Run("failed request records error", func(t *testing.T) {
		repository := &recordingUsageRepository{}
		capture := NewUsageCapture(repository)
		router := &usageEvidenceRouter{err: errors.New("provider exploded")}
		service := capture.Wrap(&proxy{router: router})

		_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
		require.Error(t, err)
		capture.Flush()

		records := repository.all()
		require.Len(t, records, 1)
		record := records[0]
		require.Equal(t, usage.StatusError, record.Status)
		require.NotEmpty(t, record.ErrorClass)
		require.GreaterOrEqual(t, record.StatusCode, 400)
		require.Nil(t, record.Cost)
		require.Equal(t, usage.CostReasonNoRoute, record.CostUnavailableReason)
		require.NoError(t, record.Validate())
	})
}

func TestUsageCaptureFailureDoesNotFailRequest(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{err: errors.New("storage down")}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 10, 5)}
	service := capture.Wrap(&proxy{router: router})

	response, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
	require.NoError(t, err)
	require.NotNil(t, response)
	capture.Flush()
	require.Empty(t, repository.all())
}

// evidenceStream is a managed stream that carries route evidence like the
// router's production stream wrapper.
type evidenceStream struct {
	events   []inference.StreamEvent
	position int
	snapshot *runtimecatalog.RoutableSnapshot
	provider string
	model    string
	closed   bool
}

func (s *evidenceStream) Read() (*inference.StreamEvent, error) {
	if s.position >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.position].Clone()
	s.position++
	return &event, nil
}

func (s *evidenceStream) Close() error                          { s.closed = true; return nil }
func (s *evidenceStream) Attempts() []execution.AttemptEvidence { return nil }
func (s *evidenceStream) Committed() bool                       { return true }
func (s *evidenceStream) ModelUsed() string                     { return s.model }
func (s *evidenceStream) ProviderUsed() string                  { return s.provider }
func (s *evidenceStream) AttemptCount() int                     { return 1 }
func (s *evidenceStream) RoutingDuration() time.Duration        { return 10 * time.Millisecond }
func (s *evidenceStream) CatalogSnapshot() *runtimecatalog.RoutableSnapshot {
	return s.snapshot
}

var _ routepkg.StreamEvidence = (*evidenceStream)(nil)

func TestStreamingChatWritesUsageRecordWithProvider(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	stream := &evidenceStream{
		snapshot: snapshot,
		provider: "test-provider",
		model:    routeID,
		events: []inference.StreamEvent{
			{Kind: inference.StreamDelta, Model: routeID, ModelUsed: routeID,
				Deltas: []inference.ChoiceDelta{{Index: 0, Text: "ok"}}},
			{Kind: inference.StreamUsage, Model: routeID, ModelUsed: routeID,
				Usage: &inference.Usage{InputTokens: 50, OutputTokens: 20, TotalTokens: 70}},
		},
	}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{stream: stream}
	service := capture.Wrap(&proxy{router: router})

	request := usageChatRequest()
	request.Request.Stream = true
	streamResponse, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	for {
		_, readErr := streamResponse.Read()
		if readErr != nil {
			require.ErrorIs(t, readErr, io.EOF)
			break
		}
	}
	require.NoError(t, streamResponse.Close())
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]
	require.Equal(t, usage.OperationChat, record.Operation)
	require.True(t, record.Streaming)
	require.Equal(t, usage.StatusOK, record.Status)
	require.Equal(t, routeID, record.ModelUsed)
	require.Equal(t, "test-provider", record.Provider)
	require.EqualValues(t, 50, record.Tokens.Input)
	require.EqualValues(t, 20, record.Tokens.Output)
	require.NotNil(t, record.Cost)
	require.Positive(t, record.Cost.NanoUSD)
	require.NoError(t, record.Validate())
}

func TestStreamingChatRecordsTimeToFirstToken(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	stream := &evidenceStream{
		snapshot: snapshot,
		provider: "test-provider",
		model:    routeID,
		events: []inference.StreamEvent{
			{Kind: inference.StreamDelta, Model: routeID, ModelUsed: routeID,
				Deltas: []inference.ChoiceDelta{{Index: 0, Text: "ok"}}},
			{Kind: inference.StreamUsage, Model: routeID, ModelUsed: routeID,
				Usage: &inference.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}},
		},
	}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{router: &usageEvidenceRouter{stream: stream}})

	request := usageChatRequest()
	request.Request.Stream = true
	streamResponse, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	// The first event arrives 30 ms after the stream opened.
	time.Sleep(30 * time.Millisecond)
	for {
		_, readErr := streamResponse.Read()
		if readErr != nil {
			require.ErrorIs(t, readErr, io.EOF)
			break
		}
	}
	require.NoError(t, streamResponse.Close())
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]
	require.True(t, record.Streaming)
	require.GreaterOrEqual(t, record.TTFTMS, int64(30))
	require.LessOrEqual(t, record.TTFTMS, record.LatencyMS)
}

func TestNonStreamingChatRecordsNoTimeToFirstToken(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 10, 5)}
	service := capture.Wrap(&proxy{router: router})

	_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	require.Zero(t, records[0].TTFTMS)
}

func TestStreamingCancellationRecordsCancelledStatus(t *testing.T) {
	stream := &evidenceStream{
		provider: "test-provider",
		model:    "openai/gpt-4o",
		events: []inference.StreamEvent{
			{Kind: inference.StreamDelta, Model: "openai/gpt-4o",
				Deltas: []inference.ChoiceDelta{{Index: 0, Text: "partial"}}},
		},
	}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{stream: stream}
	service := capture.Wrap(&proxy{router: router})

	request := usageChatRequest()
	request.Request.Stream = true
	streamResponse, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	_, readErr := streamResponse.Read()
	require.NoError(t, readErr)
	// Close before the stream ends: the client disconnected.
	require.NoError(t, streamResponse.Close())
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	require.Equal(t, usage.StatusCancelled, records[0].Status)
	require.True(t, records[0].Streaming)
	require.True(t, stream.closed)
}
