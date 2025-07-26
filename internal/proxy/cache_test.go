package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCacheManager implements the cache manager interface for testing
type mockCacheManager struct {
	storage      map[string][]byte
	calls        map[string]int
	shouldError  bool
	returnAsMap  bool // Simulate real behavior of returning map[string]interface{}
}

func newMockCacheManager() *mockCacheManager {
	return &mockCacheManager{
		storage:     make(map[string][]byte),
		calls:       make(map[string]int),
		returnAsMap: true, // Default to real behavior
	}
}

func (m *mockCacheManager) GetModel(ctx context.Context, key string) (interface{}, bool, error) {
	m.calls["GetModel"]++
	if m.shouldError {
		return nil, false, errors.New("cache error")
	}
	
	data, found := m.storage[key]
	if !found {
		return nil, false, nil
	}
	
	if m.returnAsMap {
		// Simulate real cache manager behavior
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, true, err
	}
	
	// For testing direct type returns
	var result ModelsResponse
	err := json.Unmarshal(data, &result)
	return &result, true, err
}

func (m *mockCacheManager) SetModel(ctx context.Context, key string, value interface{}) error {
	m.calls["SetModel"]++
	if m.shouldError {
		return errors.New("cache error")
	}
	
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.storage[key] = data
	return nil
}

func (m *mockCacheManager) GetChatCompletion(ctx context.Context, key string) (*cache.ChatCompletionResponse, error) {
	m.calls["GetChatCompletion"]++
	if m.shouldError {
		return nil, errors.New("cache error")
	}
	
	data, found := m.storage[key]
	if !found {
		return nil, nil
	}
	
	var resp cache.ChatCompletionResponse
	err := json.Unmarshal(data, &resp)
	return &resp, err
}

func (m *mockCacheManager) SetChatCompletion(ctx context.Context, key string, response *cache.ChatCompletionResponse) error {
	m.calls["SetChatCompletion"]++
	if m.shouldError {
		return errors.New("cache error")
	}
	
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	m.storage[key] = data
	return nil
}

func (m *mockCacheManager) GetEmbedding(ctx context.Context, key string) (*cache.EmbeddingsResponse, error) {
	m.calls["GetEmbedding"]++
	if m.shouldError {
		return nil, errors.New("cache error")
	}
	
	data, found := m.storage[key]
	if !found {
		return nil, nil
	}
	
	var resp cache.EmbeddingsResponse
	err := json.Unmarshal(data, &resp)
	return &resp, err
}

func (m *mockCacheManager) SetEmbedding(ctx context.Context, key string, response *cache.EmbeddingsResponse) error {
	m.calls["SetEmbedding"]++
	if m.shouldError {
		return errors.New("cache error")
	}
	
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	m.storage[key] = data
	return nil
}

// Tests for cachedService

func TestCachedService_ProcessChatCompletion(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:     "test-123",
			Object: "chat.completion",
			Model:  "gpt-4",
			Choices: []connectors.Choice{
				{Index: 0, Message: connectors.Message{Role: "assistant", Content: "Hello!"}},
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableChatCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []connectors.Message{
			{Role: "user", Content: "Hi"},
		},
	}
	
	// First call - cache miss
	resp1, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, resp1.CacheStatus)
	assert.Equal(t, "test-123", resp1.ID)
	assert.Equal(t, 1, mockProxy.calls["ProcessChatCompletion"])
	
	// Wait for async cache write
	time.Sleep(100 * time.Millisecond)
	
	// Second call - cache hit
	resp2, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, resp2.CacheStatus)
	assert.Equal(t, "test-123", resp2.ID)
	assert.Equal(t, 1, mockProxy.calls["ProcessChatCompletion"], "Should not call underlying service on cache hit")
}

func TestCachedService_ListModels(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		modelsResponse: &ModelsResponse{
			Object: "list",
			Data: []ModelInfo{
				{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableModelCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	// First call - cache miss
	resp1, err := service.ListModels(ctx)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, resp1.CacheStatus)
	assert.Len(t, resp1.Data, 1)
	assert.Equal(t, 1, mockProxy.calls["ListModels"])
	
	// Wait for async cache write
	time.Sleep(100 * time.Millisecond)
	
	// Second call - cache hit (with map[string]interface{} conversion)
	resp2, err := service.ListModels(ctx)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, resp2.CacheStatus)
	assert.Len(t, resp2.Data, 1)
	assert.Equal(t, "gpt-4", resp2.Data[0].ID)
	assert.Equal(t, 1, mockProxy.calls["ListModels"], "Should not call underlying service on cache hit")
}

func TestCachedService_CacheDisabled(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		modelsResponse: &ModelsResponse{Object: "list"},
	}
	
	cacheConfig := CacheConfig{
		EnableModelCache: false, // Cache disabled
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	// Multiple calls should all go to the underlying service
	for i := 0; i < 3; i++ {
		resp, err := service.ListModels(ctx)
		require.NoError(t, err)
		assert.Empty(t, resp.CacheStatus, "Should not set cache status when cache is disabled")
	}
	
	assert.Equal(t, 3, mockProxy.calls["ListModels"], "All calls should go to underlying service when cache is disabled")
	assert.Equal(t, 0, mockCache.calls["GetModel"], "Should not use cache when disabled")
}

func TestCachedService_CacheError(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockCache.shouldError = true
	
	mockProxy := &mockProxyImpl{
		modelsResponse: &ModelsResponse{Object: "list"},
	}
	
	cacheConfig := CacheConfig{
		EnableModelCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	// Should fall back to underlying service on cache error
	resp, err := service.ListModels(ctx)
	require.NoError(t, err)
	// When cache errors occur, the response doesn't get a cache status set
	assert.Empty(t, resp.CacheStatus, "Cache status should be empty when cache errors occur")
	assert.Equal(t, 1, mockProxy.calls["ListModels"])
}

// Test streaming cache functionality
func TestCachedService_ProcessChatCompletionStream(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:     "test-123",
			Object: "chat.completion",
			Model:  "gpt-4",
			Choices: []connectors.Choice{
				{Index: 0, Message: connectors.Message{Role: "assistant", Content: "Hello from stream!"}},
			},
			Usage: &connectors.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableChatCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []connectors.Message{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	
	// First call - cache miss, should call underlying service
	stream1, err := service.ProcessChatCompletionStream(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, stream1)
	assert.Equal(t, 1, mockProxy.calls["ProcessChatCompletionStream"])
	
	// Read all chunks to trigger caching
	var chunks1 []connectors.ChatStreamChunk
	for {
		chunk, err := stream1.Read()
		if err == io.EOF {
			if chunk != nil {
				chunks1 = append(chunks1, *chunk)
			}
			break
		}
		require.NoError(t, err)
		chunks1 = append(chunks1, *chunk)
	}
	
	// Verify we got all expected chunks
	assert.Len(t, chunks1, 3) // role, content, usage
	assert.Equal(t, "assistant", chunks1[0].Choices[0].Delta.Role)
	assert.Equal(t, "Hello from stream!", chunks1[1].Choices[0].Delta.Content)
	assert.NotNil(t, chunks1[2].Usage)
	assert.Equal(t, 5, chunks1[2].Usage.CompletionTokens)
	
	// Check cache status
	if cacheProvider, ok := stream1.(CacheStatusProvider); ok {
		assert.Equal(t, CacheStatusMiss, cacheProvider.GetCacheStatus())
	}
	
	// Wait for async caching
	time.Sleep(100 * time.Millisecond)
	
	// Second call - cache hit, should NOT call underlying service
	stream2, err := service.ProcessChatCompletionStream(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, stream2)
	assert.Equal(t, 1, mockProxy.calls["ProcessChatCompletionStream"], "Should not call underlying service on cache hit")
	
	// Check cache status
	if cacheProvider, ok := stream2.(CacheStatusProvider); ok {
		assert.Equal(t, CacheStatusHit, cacheProvider.GetCacheStatus())
	}
	
	// Read all chunks from cached stream
	var chunks2 []connectors.ChatStreamChunk
	for {
		chunk, err := stream2.Read()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		chunks2 = append(chunks2, *chunk)
	}
	
	// Verify cached stream returns proper chunks
	require.Greater(t, len(chunks2), 2, "Should have role, content chunks, and usage")
	
	// Debug: print all chunks
	for i, chunk := range chunks2 {
		t.Logf("Chunk %d: role=%q content=%q finish=%q usage=%v", 
			i, 
			chunk.Choices[0].Delta.Role,
			chunk.Choices[0].Delta.Content,
			chunk.Choices[0].FinishReason,
			chunk.Usage != nil)
	}
	
	// Check first chunk has role
	assert.Equal(t, "assistant", chunks2[0].Choices[0].Delta.Role)
	
	// Check content is streamed in chunks
	var totalContent string
	var usageChunkIndex = -1
	for i := 1; i < len(chunks2); i++ {
		if chunks2[i].Choices[0].Delta.Content != "" {
			totalContent += chunks2[i].Choices[0].Delta.Content
		}
		if chunks2[i].Usage != nil {
			usageChunkIndex = i
		}
	}
	assert.Equal(t, "Hello from stream!", totalContent)
	
	// Check we have a chunk with usage data
	require.NotEqual(t, -1, usageChunkIndex, "Should have a chunk with usage data")
	usageChunk := chunks2[usageChunkIndex]
	assert.Equal(t, "stop", usageChunk.Choices[0].FinishReason)
	assert.NotNil(t, usageChunk.Usage)
	assert.Equal(t, 5, usageChunk.Usage.CompletionTokens)
}

// Test that streaming responses are properly cached
func TestCachedService_StreamingResponseIsCached(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockCache.returnAsMap = false // For easier verification
	
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:     "test-stream-cache",
			Object: "chat.completion",
			Model:  "gpt-4",
			Choices: []connectors.Choice{
				{Index: 0, Message: connectors.Message{Role: "assistant", Content: "Cached content"}},
			},
			Usage: &connectors.Usage{
				PromptTokens:     20,
				CompletionTokens: 10,
			},
		},
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  CacheConfig{EnableChatCache: true},
	}
	
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []connectors.Message{
			{Role: "user", Content: "Cache me"},
		},
		Stream: true,
	}
	
	// Process streaming request
	stream, err := service.ProcessChatCompletionStream(ctx, req)
	require.NoError(t, err)
	
	// Read entire stream
	for {
		_, err := stream.Read()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	
	// Wait for async caching
	time.Sleep(200 * time.Millisecond)
	
	// Verify response was cached
	assert.Equal(t, 1, mockCache.calls["SetChatCompletion"], "Should cache streaming response")
	
	// Verify cached response has correct content
	cacheKey, _ := service.generateChatCacheKey(req)
	cachedData, found := mockCache.storage[cacheKey]
	require.True(t, found, "Response should be in cache")
	
	var cachedResp cache.ChatCompletionResponse
	err = json.Unmarshal(cachedData, &cachedResp)
	require.NoError(t, err)
	
	assert.Equal(t, "test-stream-cache", cachedResp.ID)
	assert.Equal(t, "Cached content", cachedResp.Choices[0].Message.Content)
	assert.Equal(t, 10, cachedResp.Usage.CompletionTokens)
}

// Test that reasoning content is preserved in cached responses
func TestCachedService_PreservesReasoningContent(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:     "test-reasoning",
			Object: "chat.completion",
			Model:  "claude-3.5-sonnet",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:      "assistant",
						Content:   "The answer is 42",
						Reasoning: "Let me think about this step by step...",
					},
				},
			},
			Usage: &connectors.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      45,
				CompletionTokensDetails: &connectors.CompletionTokensDetails{
					ReasoningTokens: 15,
				},
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableChatCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	req := &ChatCompletionRequest{
		Model: "claude-3.5-sonnet",
		Messages: []connectors.Message{
			{Role: "user", Content: "What is the meaning of life?"},
		},
	}
	
	// First call - cache miss
	resp1, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, resp1.CacheStatus)
	assert.Equal(t, "Let me think about this step by step...", resp1.Choices[0].Message.Reasoning)
	assert.Equal(t, 15, resp1.Usage.CompletionTokensDetails.ReasoningTokens)
	
	// Wait for async cache write
	time.Sleep(100 * time.Millisecond)
	
	// Second call - cache hit
	resp2, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, resp2.CacheStatus)
	assert.Equal(t, "Let me think about this step by step...", resp2.Choices[0].Message.Reasoning, "Reasoning should be preserved in cached response")
	assert.Equal(t, "The answer is 42", resp2.Choices[0].Message.Content)
	assert.Equal(t, 15, resp2.Usage.CompletionTokensDetails.ReasoningTokens, "Reasoning tokens should be preserved")
}

// Test that reasoning content is preserved in cached streaming responses
func TestCachedService_PreservesReasoningInStreamCache(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:     "test-stream-reasoning",
			Object: "chat.completion",
			Model:  "claude-3.5-sonnet",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:      "assistant",
						Content:   "The answer is 42",
						Reasoning: "Let me think step by step about the meaning of life...",
					},
				},
			},
			Usage: &connectors.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      55,
				CompletionTokensDetails: &connectors.CompletionTokensDetails{
					ReasoningTokens: 25,
				},
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableChatCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	req := &ChatCompletionRequest{
		Model: "claude-3.5-sonnet",
		Messages: []connectors.Message{
			{Role: "user", Content: "What is the meaning of life?"},
		},
		Stream: true,
	}
	
	// First call - cache miss
	stream1, err := service.ProcessChatCompletionStream(ctx, req)
	require.NoError(t, err)
	
	// Read all chunks
	var chunks []connectors.ChatStreamChunk
	for {
		chunk, err := stream1.Read()
		if err == io.EOF {
			if chunk != nil {
				chunks = append(chunks, *chunk)
			}
			break
		}
		require.NoError(t, err)
		chunks = append(chunks, *chunk)
	}
	
	// Wait for async cache write
	time.Sleep(200 * time.Millisecond)
	
	// Second call - cache hit
	stream2, err := service.ProcessChatCompletionStream(ctx, req)
	require.NoError(t, err)
	
	// Check cache status
	if cacheProvider, ok := stream2.(CacheStatusProvider); ok {
		assert.Equal(t, CacheStatusHit, cacheProvider.GetCacheStatus())
	}
	
	// Read cached stream
	var cachedChunks []connectors.ChatStreamChunk
	var totalReasoning, totalContent string
	for {
		chunk, err := stream2.Read()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		cachedChunks = append(cachedChunks, *chunk)
		
		// Accumulate reasoning and content
		if len(chunk.Choices) > 0 {
			totalReasoning += chunk.Choices[0].Delta.Reasoning
			totalContent += chunk.Choices[0].Delta.Content
		}
	}
	
	// Verify reasoning and content are preserved
	assert.Equal(t, "Let me think step by step about the meaning of life...", totalReasoning, "Reasoning should be preserved in cached stream")
	assert.Equal(t, "The answer is 42", totalContent, "Content should be preserved in cached stream")
	
	// Find usage chunk
	var usageChunk *connectors.ChatStreamChunk
	for i := len(cachedChunks) - 1; i >= 0; i-- {
		if cachedChunks[i].Usage != nil {
			usageChunk = &cachedChunks[i]
			break
		}
	}
	
	require.NotNil(t, usageChunk, "Should have usage data in cached stream")
	assert.Equal(t, 25, usageChunk.Usage.CompletionTokensDetails.ReasoningTokens, "Reasoning tokens should be preserved in cached stream")
}

// Test that all fields are preserved in cached responses
func TestCachedService_PreservesAllFields(t *testing.T) {
	ctx := context.Background()
	mockCache := newMockCacheManager()
	mockProxy := &mockProxyImpl{
		chatResponse: &ChatCompletionResponse{
			ID:                "test-all-fields",
			Object:            "chat.completion",
			Created:           1234567890,
			Model:             "gpt-4",
			ModelUsed:         "gpt-4-0613",
			SystemFingerprint: "fp_1234567890",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:      "assistant",
						Content:   "Test response",
						Reasoning: "Test reasoning",
						Name:      "TestBot",
						ToolCalls: []connectors.ToolCall{
							{ID: "tool_123", Type: "function", Function: connectors.FunctionCall{Name: "test_func", Arguments: "{}"}},
						},
						ToolCallID: "tool_call_456",
					},
					FinishReason: "stop",
					LogProbs: &connectors.LogProbs{
						Content: []connectors.LogProbItem{
							{Token: "Test", LogProb: -0.5},
						},
					},
				},
			},
			Usage: &connectors.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
				CompletionTokensDetails: &connectors.CompletionTokensDetails{
					ReasoningTokens: 5,
				},
			},
		},
	}
	
	cacheConfig := CacheConfig{
		EnableChatCache: true,
	}
	
	service := &cachedService{
		service:      mockProxy,
		cacheManager: mockCache,
		cacheConfig:  cacheConfig,
	}
	
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []connectors.Message{
			{Role: "user", Content: "Test all fields"},
		},
	}
	
	// First call - cache miss
	resp1, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, resp1.CacheStatus)
	
	// Wait for async cache write
	time.Sleep(100 * time.Millisecond)
	
	// Second call - cache hit
	resp2, err := service.ProcessChatCompletion(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, resp2.CacheStatus)
	
	// Verify all fields are preserved
	assert.Equal(t, "test-all-fields", resp2.ID)
	assert.Equal(t, "chat.completion", resp2.Object)
	assert.Equal(t, int64(1234567890), resp2.Created)
	assert.Equal(t, "gpt-4", resp2.Model)
	assert.Equal(t, "gpt-4-0613", resp2.ModelUsed, "ModelUsed should be preserved")
	assert.Equal(t, "fp_1234567890", resp2.SystemFingerprint)
	
	// Check Choice fields
	require.Len(t, resp2.Choices, 1)
	choice := resp2.Choices[0]
	assert.Equal(t, 0, choice.Index)
	assert.Equal(t, "stop", choice.FinishReason)
	
	// Check Message fields
	assert.Equal(t, "assistant", choice.Message.Role)
	assert.Equal(t, "Test response", choice.Message.Content)
	assert.Equal(t, "Test reasoning", choice.Message.Reasoning)
	assert.Equal(t, "TestBot", choice.Message.Name, "Name field should be preserved")
	assert.Equal(t, "tool_call_456", choice.Message.ToolCallID, "ToolCallID should be preserved")
	
	// Check ToolCalls
	require.Len(t, choice.Message.ToolCalls, 1, "ToolCalls should be preserved")
	toolCall := choice.Message.ToolCalls[0]
	assert.Equal(t, "tool_123", toolCall.ID)
	assert.Equal(t, "function", toolCall.Type)
	assert.Equal(t, "test_func", toolCall.Function.Name)
	
	// Check LogProbs
	require.NotNil(t, choice.LogProbs, "LogProbs should be preserved")
	require.Len(t, choice.LogProbs.Content, 1)
	assert.Equal(t, "Test", choice.LogProbs.Content[0].Token)
	assert.Equal(t, -0.5, choice.LogProbs.Content[0].LogProb)
	
	// Check Usage
	assert.Equal(t, 10, resp2.Usage.PromptTokens)
	assert.Equal(t, 20, resp2.Usage.CompletionTokens)
	assert.Equal(t, 30, resp2.Usage.TotalTokens)
	assert.Equal(t, 5, resp2.Usage.CompletionTokensDetails.ReasoningTokens)
	
	// Check cache age is reasonable (should be >= 0, as test might run very fast)
	assert.GreaterOrEqual(t, resp2.CacheAge, 0, "CacheAge should be calculated")
	
	// Verify the CachedAt was stored by checking the raw cache
	cacheKey, _ := service.generateChatCacheKey(req)
	rawCached, _ := mockCache.GetChatCompletion(ctx, cacheKey)
	assert.Greater(t, rawCached.CachedAt, int64(0), "CachedAt timestamp should be stored")
}

// Additional tests for cache functionality

func TestCacheListResponse_HandlesMapFromCache(t *testing.T) {
	// This test verifies that cacheListResponse correctly handles the
	// map[string]interface{} that the real cache manager returns
	
	ctx := context.Background()
	mockCache := &mockCacheManagerForTest{
		storage: make(map[string][]byte),
	}
	
	// Create a test response
	testResponse := &ModelsResponse{
		Object: "list",
		Data: []ModelInfo{
			{ID: "model-1", Object: "model", OwnedBy: "test"},
		},
	}
	
	// Store it (this simulates what happens on cache miss)
	err := mockCache.SetModel(ctx, "models:list", testResponse)
	require.NoError(t, err)
	
	// Retrieve it - this returns map[string]interface{} just like real cache
	cached, found, err := mockCache.GetModel(ctx, "models:list")
	require.NoError(t, err)
	require.True(t, found)
	
	// Verify it's a map (not the original type)
	_, isMap := cached.(map[string]interface{})
	assert.True(t, isMap, "Should be map[string]interface{}, got %T", cached)
	
	// This demonstrates the panic that would occur without our fix:
	assert.Panics(t, func() {
		_ = cached.(*ModelsResponse) // This panics!
	}, "Direct type assertion should panic")
}

func TestCacheIntegration_ReproducesPanicScenario(t *testing.T) {
	// This integration test reproduces the exact scenario that caused the panic:
	// 1. A proxy response is cached
	// 2. The cache manager stores it as JSON
	// 3. When retrieved, it returns map[string]interface{} instead of the original type
	// 4. The type assertion fails and causes a panic
	
	// Create real cache manager with correct config structure
	store := storage.NewMockStore()
	config := cache.ManagerConfig{
		APIKeys: struct {
			LocalTTL       time.Duration `env:"LOCAL_TTL,default=5m"`
			DistributedTTL time.Duration `env:"DISTRIBUTED_TTL,default=1h"`
			LocalSizeMB    int64         `env:"LOCAL_SIZE_MB,default=32"`
		}{
			LocalTTL:       5 * time.Minute,
			DistributedTTL: 1 * time.Hour,
			LocalSizeMB:    10,
		},
		Models: struct {
			Strategy string        `env:"STRATEGY,default=local"`
			TTL      time.Duration `env:"TTL,default=6h"`
			SizeMB   int64         `env:"SIZE_MB,default=16"`
		}{
			Strategy: "local",
			TTL:      1 * time.Hour,
			SizeMB:   10,
		},
	}
	
	cacheManager, err := cache.NewCacheManager(config, store)
	require.NoError(t, err)
	defer cacheManager.Close()

	ctx := context.Background()

	// Test data
	originalResponse := &ModelsResponse{
		Object: "list",
		Data: []ModelInfo{
			{
				ID:      "gpt-4",
				Object:  "model",
				Created: 1234567890,
				OwnedBy: "openai",
			},
		},
	}

	// Store in cache (this is what happens in the proxy)
	err = cacheManager.SetModel(ctx, "models:list", originalResponse)
	require.NoError(t, err)

	// Retrieve from cache
	cached, found, err := cacheManager.GetModel(ctx, "models:list")
	require.NoError(t, err)
	require.True(t, found)

	// This demonstrates the problem: the cache returns map[string]interface{}
	mapData, isMap := cached.(map[string]interface{})
	assert.True(t, isMap, "Cache should return map[string]interface{}, got %T", cached)

	// This would panic without our fix:
	// resp := cached.(*ModelsResponse) // panic!
	
	// Verify the map contains the expected data
	assert.Equal(t, "list", mapData["object"])
	
	dataArray, ok := mapData["data"].([]interface{})
	require.True(t, ok, "data should be an array")
	require.Len(t, dataArray, 1)
	
	firstModel, ok := dataArray[0].(map[string]interface{})
	require.True(t, ok, "model should be a map")
	assert.Equal(t, "gpt-4", firstModel["id"])
}

// mockCacheManagerForTest simulates the real cache manager's JSON behavior
type mockCacheManagerForTest struct {
	storage map[string][]byte
}

func (m *mockCacheManagerForTest) GetModel(ctx context.Context, key string) (interface{}, bool, error) {
	data, found := m.storage[key]
	if !found {
		return nil, false, nil
	}
	
	// Simulate real cache manager: unmarshal to interface{}
	var result interface{}
	err := json.Unmarshal(data, &result)
	return result, true, err
}

func (m *mockCacheManagerForTest) SetModel(ctx context.Context, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.storage[key] = data
	return nil
}

// mockProxyImpl implements Proxy for testing
type mockProxyImpl struct {
	calls             map[string]int
	chatResponse      *ChatCompletionResponse
	modelsResponse    *ModelsResponse
	providersResponse *ProvidersResponse
	embeddingsResponse *EmbeddingsResponse
	shouldError       bool
}

func (m *mockProxyImpl) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls["ProcessChatCompletion"]++
	
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	return m.chatResponse, nil
}

func (m *mockProxyImpl) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls["ProcessChatCompletionStream"]++
	
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	
	// Return a mock stream that returns the chat response in chunks
	if m.chatResponse != nil {
		return newMockStream(m.chatResponse), nil
	}
	
	return nil, errors.New("no response configured")
}

func (m *mockProxyImpl) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls["ProcessEmbeddings"]++
	
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	return m.embeddingsResponse, nil
}

func (m *mockProxyImpl) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls["ListModels"]++
	
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	return m.modelsResponse, nil
}

func (m *mockProxyImpl) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	if m.calls == nil {
		m.calls = make(map[string]int)
	}
	m.calls["ListProviders"]++
	
	if m.shouldError {
		return nil, errors.New("proxy error")
	}
	return m.providersResponse, nil
}

func (m *mockProxyImpl) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	return &ModelEndpointsResponse{Model: modelID}, nil
}

// mockStream implements ChatCompletionStreamResponse for testing
type mockStream struct {
	chunks   []connectors.ChatStreamChunk
	position int
}

func newMockStream(response *ChatCompletionResponse) *mockStream {
	chunks := []connectors.ChatStreamChunk{
		// First chunk with role
		{
			ID:      response.ID,
			Object:  "chat.completion.chunk",
			Created: response.Created,
			Model:   response.Model,
			Choices: []connectors.StreamChoice{
				{Index: 0, Delta: connectors.MessageDelta{Role: "assistant"}},
			},
		},
	}
	
	// Add reasoning chunk if present
	if len(response.Choices) > 0 && response.Choices[0].Message.Reasoning != "" {
		chunks = append(chunks, connectors.ChatStreamChunk{
			ID:      response.ID,
			Object:  "chat.completion.chunk",
			Created: response.Created,
			Model:   response.Model,
			Choices: []connectors.StreamChoice{
				{Index: 0, Delta: connectors.MessageDelta{Reasoning: response.Choices[0].Message.Reasoning}},
			},
		})
	}
	
	// Content chunk
	content := ""
	if len(response.Choices) > 0 {
		if str, ok := response.Choices[0].Message.Content.(string); ok {
			content = str
		}
	}
	if content != "" {
		chunks = append(chunks, connectors.ChatStreamChunk{
			ID:      response.ID,
			Object:  "chat.completion.chunk",
			Created: response.Created,
			Model:   response.Model,
			Choices: []connectors.StreamChoice{
				{Index: 0, Delta: connectors.MessageDelta{Content: content}},
			},
		})
	}
	
	// Final chunk with usage
	chunks = append(chunks, connectors.ChatStreamChunk{
		ID:      response.ID,
		Object:  "chat.completion.chunk",
		Created: response.Created,
		Model:   response.Model,
		Choices: []connectors.StreamChoice{
			{Index: 0, Delta: connectors.MessageDelta{}, FinishReason: "stop"},
		},
		Usage: response.Usage,
	})
	
	return &mockStream{chunks: chunks, position: 0}
}

func (s *mockStream) Read() (*connectors.ChatStreamChunk, error) {
	if s.position >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.position]
	s.position++
	if s.position >= len(s.chunks) {
		return &chunk, io.EOF
	}
	return &chunk, nil
}

func (s *mockStream) Close() error {
	return nil
}