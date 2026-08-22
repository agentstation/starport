package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	responsecache "github.com/agentstation/starport/internal/response/cache"
	"github.com/stretchr/testify/require"
)

type mockCacheManager struct {
	storage     map[string][]byte
	calls       map[string]int
	shouldError bool
}

func newMockCacheManager() *mockCacheManager {
	return &mockCacheManager{storage: make(map[string][]byte), calls: make(map[string]int)}
}

func (m *mockCacheManager) GetModel(_ context.Context, key string) (any, bool, error) {
	m.calls["GetModel"]++
	if m.shouldError {
		return nil, false, errors.New("cache error")
	}
	data, found := m.storage[key]
	if !found {
		return nil, false, nil
	}
	var result any
	err := json.Unmarshal(data, &result)
	return result, true, err
}

func (m *mockCacheManager) SetModel(_ context.Context, key string, value any) error {
	m.calls["SetModel"]++
	if m.shouldError {
		return errors.New("cache error")
	}
	data, err := json.Marshal(value)
	if err == nil {
		m.storage[key] = data
	}
	return err
}

func (m *mockCacheManager) GetResponse(_ context.Context, key string) ([]byte, bool, error) {
	m.calls["GetResponse"]++
	if m.shouldError {
		return nil, false, errors.New("cache error")
	}
	data, found := m.storage[key]
	return append([]byte(nil), data...), found, nil
}

func (m *mockCacheManager) SetResponse(_ context.Context, key string, response []byte) error {
	m.calls["SetResponse"]++
	if m.shouldError {
		return errors.New("cache error")
	}
	m.storage[key] = append([]byte(nil), response...)
	return nil
}

func TestCachedServiceProcessChatCompletion(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: cacheManager,
		cacheConfig: CacheConfig{EnableChatCache: true},
	}
	request := testChatRequest("tenant-1")

	first, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, first.CacheStatus)
	require.Equal(t, "chatcmpl-test", first.Response.ID)

	second, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CacheStatusHit, second.CacheStatus)
	require.Equal(t, "chatcmpl-test", second.Response.ID)
	require.Equal(t, 1, upstream.calls["ProcessChatCompletion"])
}

func TestCachedService_TenantAndGenerationIsolation(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: cacheManager,
		cacheConfig: CacheConfig{EnableChatCache: true},
	}
	request := testChatRequest("tenant-1")

	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	_, err = service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)

	otherTenant := testChatRequest("tenant-2")
	response, err := service.ProcessChatCompletion(context.Background(), otherTenant)
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)

	service.generation = "catalog-2"
	response, err = service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)
	require.Equal(t, 3, upstream.calls["ProcessChatCompletion"])
}

func TestCachedServicePreservesCanonicalResponse(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: cacheManager,
		cacheConfig: CacheConfig{EnableChatCache: true},
	}
	request := testChatRequest("tenant-1")

	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	response, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)

	require.Equal(t, canonicalChatResponse().Response, response.Response)
	require.GreaterOrEqual(t, response.CacheAge, 0)
}

func TestCachedServiceCachesCompleteStream(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: cacheManager,
		cacheConfig: CacheConfig{EnableChatCache: true},
	}
	request := testChatRequest("tenant-1")
	request.Request.Stream = true
	request.Request.StreamOptions.IncludeUsage = true

	stream, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	readAllEvents(t, stream)
	require.Equal(t, 1, cacheManager.calls["SetResponse"])

	cached, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	events := readAllEvents(t, cached)
	require.Equal(t, 1, upstream.calls["ProcessChatCompletionStream"])
	require.Equal(t, CacheStatusHit, cached.(CacheStatusProvider).GetCacheStatus())
	require.Equal(t, inference.StreamStart, events[0].Kind)
	require.Equal(t, "answer", events[1].Deltas[0].Text)
	require.Equal(t, inference.StreamUsage, events[len(events)-1].Kind)
}

func TestCachingStreamWrapper_DoesNotCachePartialStream(t *testing.T) {
	cacheManager := newMockCacheManager()
	repository, err := responsecache.Open(cacheManager, nil)
	require.NoError(t, err)
	upstreamError := errors.New("upstream stream failed")
	stream := &errorAfterEventsStream{
		events: []inference.StreamEvent{{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{Text: "partial"}}}},
		err:    upstreamError,
	}
	wrapper := newCachingStreamWrapper(stream, repository, "responsecache:v1:chat:partial")

	_, err = wrapper.Read()
	require.NoError(t, err)
	_, err = wrapper.Read()
	require.ErrorIs(t, err, upstreamError)
	require.Zero(t, cacheManager.calls["SetResponse"])
}

func TestCachedServiceListModelsConvertsCachedJSONMap(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{modelsResponse: &ModelsResponse{
		Object: "list",
		Data:   []ModelInfo{{ID: "openai/gpt-4.1", Object: "model", OwnedBy: "openai"}},
	}}
	service := &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: cacheManager,
		cacheConfig: CacheConfig{EnableModelCache: true},
	}

	_, err := service.ListModels(context.Background())
	require.NoError(t, err)
	response, err := service.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, CacheStatusHit, response.CacheStatus)
	require.Equal(t, "openai/gpt-4.1", response.Data[0].ID)
	require.Equal(t, 1, upstream.calls["ListModels"])
}

func TestCachedServiceRetainsOneRuntimeGeneration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	generationID := plane.Current().GenerationID()

	t.Run("chat", func(t *testing.T) {
		source := &cacheRuntimeSource{snapshot: plane.Current()}
		upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
		upstream.onChat = assertBorrowedCacheRuntime(t, source)
		service := &cachedService{
			service: upstream, runtime: source, cacheManager: newMockCacheManager(),
			cacheConfig: CacheConfig{EnableChatCache: true},
		}

		_, err := service.ProcessChatCompletion(t.Context(), testChatRequest("tenant-1"))
		require.NoError(t, err)
		require.True(t, source.lastLease(t).released.Load())
	})

	t.Run("stream", func(t *testing.T) {
		source := &cacheRuntimeSource{snapshot: plane.Current()}
		upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
		upstream.onStream = assertBorrowedCacheRuntime(t, source)
		service := &cachedService{
			service: upstream, runtime: source, cacheManager: newMockCacheManager(),
			cacheConfig: CacheConfig{EnableChatCache: true},
		}
		request := testChatRequest("tenant-1")
		request.Request.Stream = true

		stream, err := service.ProcessChatCompletionStream(t.Context(), request)
		require.NoError(t, err)
		lease := source.lastLease(t)
		require.False(t, lease.released.Load())
		readAllEvents(t, stream)
		require.True(t, lease.released.Load())
	})

	t.Run("discovery", func(t *testing.T) {
		source := &cacheRuntimeSource{snapshot: plane.Current()}
		manager := newMockCacheManager()
		upstream := &mockProxyImpl{modelsResponse: &ModelsResponse{Object: "list"}}
		upstream.onListModels = assertBorrowedCacheRuntime(t, source)
		service := &cachedService{
			service: upstream, runtime: source, cacheManager: manager,
			cacheConfig: CacheConfig{EnableModelCache: true},
		}

		_, err := service.ListModels(t.Context())
		require.NoError(t, err)
		require.True(t, source.lastLease(t).released.Load())
		_, found := manager.storage["models:list:"+generationID]
		require.True(t, found)
	})
}

func assertBorrowedCacheRuntime(
	t *testing.T,
	source *cacheRuntimeSource,
) func(context.Context) {
	t.Helper()
	return func(ctx context.Context) {
		runtime := connectors.RuntimeLeaseFromContext(ctx)
		require.Same(t, source.lastLease(t), runtime)
		require.False(t, source.lastLease(t).released.Load())
	}
}

func testChatRequest(tenantID string) *ChatCompletionRequest {
	return &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "question"}},
			}},
		},
		TenantID: tenantID,
	}
}

func canonicalChatResponse() *ChatCompletionResponse {
	return &ChatCompletionResponse{Response: inference.ChatResponse{
		ID:                "chatcmpl-test",
		CreatedUnix:       1234567890,
		Model:             "openai/gpt-4.1",
		ModelUsed:         "openai/gpt-4.1-2025-04-14",
		SystemFingerprint: "fp_test",
		Choices: []inference.Choice{{
			Index: 0,
			Message: inference.Message{
				Role:       inference.RoleAssistant,
				Content:    []inference.ContentPart{{Kind: inference.ContentText, Text: "answer"}},
				Reasoning:  "reasoning",
				Name:       "assistant",
				ToolCalls:  []inference.ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{}`}},
				ToolCallID: "call_0",
			},
			FinishReason: "stop",
			LogProbs:     []inference.LogProb{{Token: "answer", Value: -0.1, Top: []inference.TopLogProb{}}},
		}},
		Usage: inference.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ReasoningTokens: 5},
	}}
}

func readAllEvents(t *testing.T, stream ChatCompletionStreamResponse) []inference.StreamEvent {
	t.Helper()
	defer stream.Close()
	var events []inference.StreamEvent
	for {
		event, err := stream.Read()
		if errors.Is(err, io.EOF) {
			return events
		}
		require.NoError(t, err)
		require.NotNil(t, event)
		events = append(events, *event)
	}
}

type mockProxyImpl struct {
	calls              map[string]int
	chatResponse       *ChatCompletionResponse
	modelsResponse     *ModelsResponse
	providersResponse  *ProvidersResponse
	embeddingsResponse *EmbeddingsResponse
	shouldError        bool
	onChat             func(context.Context)
	onStream           func(context.Context)
	onListModels       func(context.Context)
}

func (m *mockProxyImpl) count(name string) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls[name]++
}

func (m *mockProxyImpl) ProcessChatCompletion(ctx context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.count("ProcessChatCompletion")
	if m.onChat != nil {
		m.onChat(ctx)
	}
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	return m.chatResponse, nil
}

func (m *mockProxyImpl) ProcessChatCompletionStream(ctx context.Context, _ *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	m.count("ProcessChatCompletionStream")
	if m.onStream != nil {
		m.onStream(ctx)
	}
	if m.shouldError || m.chatResponse == nil {
		return nil, errors.New("proxy error")
	}
	return newMockStream(m.chatResponse), nil
}

func (m *mockProxyImpl) ProcessEmbeddings(context.Context, *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	m.count("ProcessEmbeddings")
	return m.embeddingsResponse, nil
}

func (m *mockProxyImpl) ListModels(ctx context.Context) (*ModelsResponse, error) {
	m.count("ListModels")
	if m.onListModels != nil {
		m.onListModels(ctx)
	}
	return m.modelsResponse, nil
}

func (m *mockProxyImpl) ListProviders(context.Context) (*ProvidersResponse, error) {
	m.count("ListProviders")
	return m.providersResponse, nil
}

func (m *mockProxyImpl) ListAuthors(context.Context) (*AuthorsResponse, error) {
	m.count("ListAuthors")
	return &AuthorsResponse{}, nil
}

func (m *mockProxyImpl) GetAuthor(_ context.Context, authorID string) (*AuthorInfo, error) {
	return &AuthorInfo{ID: authorID}, nil
}

func (m *mockProxyImpl) GetModelEndpoints(_ context.Context, modelID string) (*ModelEndpointsResponse, error) {
	return &ModelEndpointsResponse{Model: modelID}, nil
}

type mockStream struct {
	events   []inference.StreamEvent
	position int
}

func newMockStream(response *ChatCompletionResponse) *mockStream {
	canonical := response.Response
	events := []inference.StreamEvent{
		{Kind: inference.StreamStart, ID: canonical.ID, CreatedUnix: canonical.CreatedUnix, Model: canonical.Model, ModelUsed: canonical.ModelUsed},
	}
	for _, choice := range canonical.Choices {
		events = append(events, inference.StreamEvent{Kind: inference.StreamDelta, ID: canonical.ID, Model: canonical.Model, ModelUsed: canonical.ModelUsed, Deltas: []inference.ChoiceDelta{{
			Index: choice.Index, Role: choice.Message.Role, Text: choice.Message.Content[0].Text, Reasoning: choice.Message.Reasoning,
		}}})
	}
	usage := canonical.Usage
	events = append(events,
		inference.StreamEvent{Kind: inference.StreamUsage, ID: canonical.ID, Model: canonical.Model, ModelUsed: canonical.ModelUsed, Usage: &usage},
		inference.StreamEvent{Kind: inference.StreamEnd, ID: canonical.ID, Model: canonical.Model, ModelUsed: canonical.ModelUsed, Deltas: []inference.ChoiceDelta{{Index: 0, FinishReason: "stop"}}},
	)
	return &mockStream{events: events}
}

func (s *mockStream) Read() (*inference.StreamEvent, error) {
	if s.position >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.position].Clone()
	s.position++
	return &event, nil
}

func (s *mockStream) Close() error { return nil }

type errorAfterEventsStream struct {
	events   []inference.StreamEvent
	position int
	err      error
}

func (s *errorAfterEventsStream) Read() (*inference.StreamEvent, error) {
	if s.position >= len(s.events) {
		return nil, s.err
	}
	event := s.events[s.position].Clone()
	s.position++
	return &event, nil
}

func (s *errorAfterEventsStream) Close() error { return nil }

type cacheRuntimeSource struct {
	snapshot *runtimecatalog.RoutableSnapshot

	mu     sync.Mutex
	leases []*cacheRuntimeLease
}

func (s *cacheRuntimeSource) AcquireRuntime() (connectors.RuntimeLease, error) {
	lease := &cacheRuntimeLease{snapshot: s.snapshot}
	s.mu.Lock()
	s.leases = append(s.leases, lease)
	s.mu.Unlock()
	return lease, nil
}

func (s *cacheRuntimeSource) lastLease(t *testing.T) *cacheRuntimeLease {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.leases)
	return s.leases[len(s.leases)-1]
}

type cacheRuntimeLease struct {
	snapshot *runtimecatalog.RoutableSnapshot
	released atomic.Bool
}

func (l *cacheRuntimeLease) Snapshot() *runtimecatalog.RoutableSnapshot { return l.snapshot }
func (l *cacheRuntimeLease) Get(string) connectors.Connector            { return nil }
func (l *cacheRuntimeLease) RequiresAuthentication(string) bool         { return false }
func (l *cacheRuntimeLease) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	return credentials.Material{}, nil
}
func (l *cacheRuntimeLease) Release() { l.released.Store(true) }
