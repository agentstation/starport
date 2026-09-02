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
	unroutedOperations
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
		// The account and the key are deliberately different values. Usage is
		// attributed to the key, never to the account it belongs to.
		AccountID: "acme",
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
	// A usage record is keyed by the gateway API key, not by the account. A
	// account's consumption is the sum of its keys, so re-keying the record onto
	// the account would erase which key spent what.
	require.Equal(t, "key-1", record.KeyID)
	require.NotEqual(t, request.AccountID, record.KeyID)
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

// The team rides the record beside the key and the account: the team counter
// sums keys across accounts, so neither of the other two identifiers can
// stand in for it. A teamless request carries no team at all.
func TestChatUsageRecordCarriesTeamAttribution(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 10, 4)}
	service := capture.Wrap(&proxy{router: router})

	attributed := usageChatRequest()
	attributed.TeamID = "team-platform"
	_, err := service.ProcessChatCompletion(context.Background(), attributed)
	require.NoError(t, err)

	teamless := usageChatRequest()
	teamless.RequestID = "req-2"
	_, err = service.ProcessChatCompletion(context.Background(), teamless)
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 2)
	// Capture flushes asynchronously, so the two records arrive in no
	// promised order: the request ID names each one.
	teamByRequest := make(map[string]string, len(records))
	for _, record := range records {
		teamByRequest[record.RequestID] = record.TeamID
	}
	require.Equal(t, "team-platform", teamByRequest["req-1"])
	require.Contains(t, teamByRequest, "req-2")
	require.Empty(t, teamByRequest["req-2"])
}

// TestBatchLineUsageRecordCarriesTheBatchID holds the batch attribution
// contract. A batch line writes one usage record like any online request,
// and the batch identifier on the record is the only join between the spend
// and the batch that caused it. An online request leaves the field empty.
func TestBatchLineUsageRecordCarriesTheBatchID(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 10, 4)}
	service := capture.Wrap(&proxy{router: router})

	online := usageChatRequest()
	_, err := service.ProcessChatCompletion(context.Background(), online)
	require.NoError(t, err)

	batched := usageChatRequest()
	batched.RequestID = "req-2"
	batched.BatchID = "batch-7"
	_, err = service.ProcessChatCompletion(context.Background(), batched)
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 2)
	byRequest := map[string]usage.Record{}
	for _, record := range records {
		byRequest[record.RequestID] = record
	}
	require.Empty(t, byRequest["req-1"].BatchID)
	require.Equal(t, "batch-7", byRequest["req-2"].BatchID)
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
	events     []inference.StreamEvent
	position   int
	snapshot   *runtimecatalog.RoutableSnapshot
	provider   string
	credential string
	model      string
	closed     bool
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
func (s *evidenceStream) CredentialSourceUsed() string          { return s.credential }
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

// TestUsageRecordCarriesTheServedCredentialSource proves the plane the router
// chose survives the trip to the usage record. Everything an operator later
// reads about who paid for a request is read off this field, and a record that
// drops it is indistinguishable from one written before the gateway recorded
// planes at all. The two paths are checked separately because a buffered
// response and a stream carry the fact through different seams.
func TestUsageRecordCarriesTheServedCredentialSource(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)

	t.Run("buffered", func(t *testing.T) {
		repository := &recordingUsageRepository{}
		capture := NewUsageCapture(repository)
		response := chatEvidenceResponse(routeID, snapshot, 10, 5)
		response.CredentialSource = "byok"
		service := capture.Wrap(&proxy{router: &usageEvidenceRouter{response: response}})

		_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
		require.NoError(t, err)
		capture.Flush()

		records := repository.all()
		require.Len(t, records, 1)
		require.Equal(t, "byok", records[0].CredentialSource)
	})

	t.Run("streaming", func(t *testing.T) {
		repository := &recordingUsageRepository{}
		capture := NewUsageCapture(repository)
		stream := &evidenceStream{
			snapshot:   snapshot,
			provider:   "test-provider",
			credential: "gateway",
			model:      routeID,
			events: []inference.StreamEvent{
				{Kind: inference.StreamUsage, Model: routeID, ModelUsed: routeID,
					Usage: &inference.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
			},
		}
		service := capture.Wrap(&proxy{router: &usageEvidenceRouter{stream: stream}})

		request := usageChatRequest()
		request.Request.Stream = true
		streamResponse, err := service.ProcessChatCompletionStream(context.Background(), request)
		require.NoError(t, err)
		for {
			if _, readErr := streamResponse.Read(); readErr != nil {
				require.ErrorIs(t, readErr, io.EOF)
				break
			}
		}
		require.NoError(t, streamResponse.Close())
		capture.Flush()

		records := repository.all()
		require.Len(t, records, 1)
		require.Equal(t, "gateway", records[0].CredentialSource)
	})
}

// recordingUsageObserver captures the records submit hands to observers.
type recordingUsageObserver struct {
	mu      sync.Mutex
	records []usage.Record
}

func (o *recordingUsageObserver) ObserveUsage(record usage.Record) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, record)
}

func (o *recordingUsageObserver) all() []usage.Record {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]usage.Record(nil), o.records...)
}

// An observer sees the same record the repository stores, before the async
// write: a dropped store write must still reach the metric surface.
func TestUsageCaptureNotifiesObservers(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	observer := &recordingUsageObserver{}
	// A nil observer is filtered at construction, so composition roots can pass
	// an optional metric surface without a guard at every call site.
	capture := NewUsageCapture(repository, observer, nil)
	router := &usageEvidenceRouter{response: chatEvidenceResponse(routeID, snapshot, 100, 40)}
	service := capture.Wrap(&proxy{router: router})

	_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
	require.NoError(t, err)
	capture.Flush()

	observed := observer.all()
	require.Len(t, observed, 1)
	stored := repository.all()
	require.Len(t, stored, 1)
	require.Equal(t, stored[0], observed[0])
	require.Equal(t, usage.OperationChat, observed[0].Operation)
	require.Equal(t, "test-provider", observed[0].Provider)
	require.EqualValues(t, 100, observed[0].Tokens.Input)
	require.EqualValues(t, 40, observed[0].Tokens.Output)
}

// semanticCacheProxy stands in for the cache layer beneath the capture: it
// answers a stream that reports a semantic similarity.
type semanticCacheProxy struct {
	*mockProxyImpl
	stream ChatCompletionStreamResponse
}

func (p *semanticCacheProxy) ProcessChatCompletionStream(context.Context, *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	return p.stream, nil
}

// similarityStream is a cache-served stream that names its similarity the
// way cachedEventStream does.
type similarityStream struct {
	ChatCompletionStreamResponse
	similarity float64
}

func (s *similarityStream) GetCacheStatus() string      { return CacheStatusHit }
func (s *similarityStream) GetCacheAge() int            { return 1 }
func (s *similarityStream) GetCacheSimilarity() float64 { return s.similarity }

func TestUsageRecordCarriesSemanticCacheSimilarity(t *testing.T) {
	tests := []struct {
		name       string
		similarity float64
		semantic   bool
	}{
		{name: "semantic hit", similarity: 0.93, semantic: true},
		{name: "exact hit", similarity: 0, semantic: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := canonicalChatResponse()
			response.CacheStatus = CacheStatusHit
			response.CacheSimilarity = tt.similarity
			repository := &recordingUsageRepository{}
			capture := NewUsageCapture(repository)
			service := capture.Wrap(&mockProxyImpl{chatResponse: response})

			_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
			require.NoError(t, err)
			capture.Flush()

			records := repository.all()
			require.Len(t, records, 1)
			require.Equal(t, CacheStatusHit, records[0].CacheStatus)
			require.InDelta(t, tt.similarity, records[0].CacheSimilarity, 1e-9)
			require.Equal(t, tt.semantic, records[0].CacheSemantic)
		})
	}
}

func TestStreamingUsageRecordCarriesSemanticCacheSimilarity(t *testing.T) {
	response := canonicalChatResponse()
	stream := &similarityStream{ChatCompletionStreamResponse: newMockStream(response), similarity: 0.88}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&semanticCacheProxy{mockProxyImpl: &mockProxyImpl{chatResponse: response}, stream: stream})

	request := usageChatRequest()
	request.Request.Stream = true
	streamResponse, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	for {
		if _, readErr := streamResponse.Read(); readErr != nil {
			require.ErrorIs(t, readErr, io.EOF)
			break
		}
	}
	require.NoError(t, streamResponse.Close())
	capture.Flush()

	// The wrapper forwards the similarity for the header beside the status.
	provider, ok := streamResponse.(CacheSimilarityProvider)
	require.True(t, ok)
	require.InDelta(t, 0.88, provider.GetCacheSimilarity(), 1e-9)

	records := repository.all()
	require.Len(t, records, 1)
	require.Equal(t, CacheStatusHit, records[0].CacheStatus)
	require.InDelta(t, 0.88, records[0].CacheSimilarity, 1e-9)
	require.True(t, records[0].CacheSemantic)
}
