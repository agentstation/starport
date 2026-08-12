package cache

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/inference"
)

func TestSemanticKeyAndTenantIsolationContract(t *testing.T) {
	maxTokens := 128
	temperature := float32(0.2)
	topP := float32(0.9)
	candidateCount := 2
	presencePenalty := float32(0.1)
	frequencyPenalty := float32(-0.1)
	seed := 42
	request := inference.ChatRequest{
		Model:          "openai/gpt-4.1",
		FallbackModels: []string{"anthropic/claude-sonnet-4"},
		Messages: []inference.Message{{
			Role:       inference.RoleUser,
			Content:    []inference.ContentPart{{Kind: inference.ContentText, Text: "hello", CacheControl: "ephemeral"}},
			Reasoning:  "prior reasoning",
			Name:       "requester",
			ToolCalls:  []inference.ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"id":"1"}`}},
			ToolCallID: "call-0",
		}},
		Sampling: inference.Sampling{
			Temperature: &temperature, TopP: &topP, CandidateCount: &candidateCount,
			MaxTokens: &maxTokens, Stop: []string{"END"}, PresencePenalty: &presencePenalty,
			FrequencyPenalty: &frequencyPenalty, LogitBias: map[string]int{"42": 1}, Seed: &seed,
		},
		Tools:      []inference.Tool{{Name: "lookup", Description: "look up one item", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: inference.ToolChoice{Mode: inference.ToolChoiceNamed, Name: "lookup"},
		Output:     inference.StructuredOutput{Format: inference.OutputJSONSchema, Name: "answer", Description: "answer schema", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		Reasoning:  inference.Reasoning{Effort: inference.ReasoningHigh, MaxTokens: &maxTokens, Exclude: true},
		User:       "user-1",
	}
	base := ChatIdentity{
		TenantID:          "tenant-1",
		CatalogGeneration: "catalog-1",
		Request:           request,
		Policy: Policy{
			Provider: ProviderPolicy{Order: []string{"openai", "anthropic"}, Only: []string{"openai", "anthropic"}, AllowFallbacks: true, Route: "fallback", ModelOverrides: map[string]string{"openai/gpt-4": "openai/gpt-4.1"}},
			Tenant:   TenantPolicy{AllowedModels: []string{"openai/gpt-4.1"}, AllowedProviders: []string{"openai"}, RateLimitTier: "pro", CredentialStrategy: "operator_first"},
		},
	}
	baseKey, err := ChatKey(base)
	if err != nil {
		t.Fatal(err)
	}

	variants := map[string]func(*ChatIdentity){
		"tenant":                 func(value *ChatIdentity) { value.TenantID = "tenant-2" },
		"catalog generation":     func(value *ChatIdentity) { value.CatalogGeneration = "catalog-2" },
		"model":                  func(value *ChatIdentity) { value.Request.Model = "openai/gpt-4.2" },
		"model chain":            func(value *ChatIdentity) { value.Request.FallbackModels[0] = "google/gemini-2" },
		"message role":           func(value *ChatIdentity) { value.Request.Messages[0].Role = inference.RoleAssistant },
		"message text":           func(value *ChatIdentity) { value.Request.Messages[0].Content[0].Text = "changed" },
		"message cache policy":   func(value *ChatIdentity) { value.Request.Messages[0].Content[0].CacheControl = "" },
		"message reasoning":      func(value *ChatIdentity) { value.Request.Messages[0].Reasoning = "changed" },
		"message name":           func(value *ChatIdentity) { value.Request.Messages[0].Name = "other" },
		"message tool call ID":   func(value *ChatIdentity) { value.Request.Messages[0].ToolCalls[0].ID = "call-2" },
		"message tool call name": func(value *ChatIdentity) { value.Request.Messages[0].ToolCalls[0].Name = "search" },
		"message tool arguments": func(value *ChatIdentity) { value.Request.Messages[0].ToolCalls[0].Arguments = `{}` },
		"message tool result ID": func(value *ChatIdentity) { value.Request.Messages[0].ToolCallID = "call-2" },
		"temperature":            func(value *ChatIdentity) { value.Request.Sampling.Temperature = float32Pointer(0.4) },
		"top p":                  func(value *ChatIdentity) { value.Request.Sampling.TopP = float32Pointer(0.8) },
		"candidate count":        func(value *ChatIdentity) { value.Request.Sampling.CandidateCount = intPointer(3) },
		"max tokens":             func(value *ChatIdentity) { value.Request.Sampling.MaxTokens = intPointer(256) },
		"stop sequence":          func(value *ChatIdentity) { value.Request.Sampling.Stop[0] = "STOP" },
		"presence penalty":       func(value *ChatIdentity) { value.Request.Sampling.PresencePenalty = float32Pointer(0.2) },
		"frequency penalty":      func(value *ChatIdentity) { value.Request.Sampling.FrequencyPenalty = float32Pointer(-0.2) },
		"logit bias":             func(value *ChatIdentity) { value.Request.Sampling.LogitBias["42"] = 2 },
		"seed":                   func(value *ChatIdentity) { value.Request.Sampling.Seed = intPointer(43) },
		"tool name":              func(value *ChatIdentity) { value.Request.Tools[0].Name = "search" },
		"tool description":       func(value *ChatIdentity) { value.Request.Tools[0].Description = "changed" },
		"tool schema":            func(value *ChatIdentity) { value.Request.Tools[0].Parameters = json.RawMessage(`{"type":"string"}`) },
		"tool choice mode":       func(value *ChatIdentity) { value.Request.ToolChoice.Mode = inference.ToolChoiceRequired },
		"tool choice name":       func(value *ChatIdentity) { value.Request.ToolChoice.Name = "search" },
		"output format":          func(value *ChatIdentity) { value.Request.Output.Format = inference.OutputJSONObject },
		"output name":            func(value *ChatIdentity) { value.Request.Output.Name = "other" },
		"output description":     func(value *ChatIdentity) { value.Request.Output.Description = "changed" },
		"output schema":          func(value *ChatIdentity) { value.Request.Output.Schema = json.RawMessage(`{"type":"string"}`) },
		"output strictness":      func(value *ChatIdentity) { value.Request.Output.Strict = false },
		"reasoning effort":       func(value *ChatIdentity) { value.Request.Reasoning.Effort = inference.ReasoningLow },
		"reasoning budget":       func(value *ChatIdentity) { value.Request.Reasoning.MaxTokens = intPointer(256) },
		"reasoning visibility":   func(value *ChatIdentity) { value.Request.Reasoning.Exclude = false },
		"user":                   func(value *ChatIdentity) { value.Request.User = "user-2" },
		"provider order":         func(value *ChatIdentity) { value.Policy.Provider.Order = []string{"anthropic", "openai"} },
		"provider only":          func(value *ChatIdentity) { value.Policy.Provider.Only = []string{"openai"} },
		"provider ignore":        func(value *ChatIdentity) { value.Policy.Provider.Ignore = []string{"google"} },
		"fallback policy":        func(value *ChatIdentity) { value.Policy.Provider.AllowFallbacks = false },
		"route mode":             func(value *ChatIdentity) { value.Policy.Provider.Route = "manual" },
		"model override":         func(value *ChatIdentity) { value.Policy.Provider.ModelOverrides["openai/gpt-4"] = "openai/gpt-4.2" },
		"allowed models":         func(value *ChatIdentity) { value.Policy.Tenant.AllowedModels = []string{"openai/gpt-4.2"} },
		"allowed providers":      func(value *ChatIdentity) { value.Policy.Tenant.AllowedProviders = []string{"anthropic"} },
		"tenant tier":            func(value *ChatIdentity) { value.Policy.Tenant.RateLimitTier = "enterprise" },
		"credential strategy":    func(value *ChatIdentity) { value.Policy.Tenant.CredentialStrategy = "user_only" },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			variant := cloneChatIdentity(base)
			mutate(&variant)
			key, err := ChatKey(variant)
			if err != nil {
				t.Fatal(err)
			}
			if key == baseKey {
				t.Fatalf("%s did not change semantic key", name)
			}
		})
	}

	streamVariant := cloneChatIdentity(base)
	streamVariant.Request.Stream = true
	streamVariant.Request.StreamOptions.IncludeUsage = true
	streamKey, err := ChatKey(streamVariant)
	if err != nil {
		t.Fatal(err)
	}
	if streamKey != baseKey {
		t.Fatal("wire delivery mode changed canonical result identity")
	}

	unsorted := cloneChatIdentity(base)
	unsorted.Policy.Provider.Only = []string{"anthropic", "openai"}
	unsorted.Policy.Tenant.AllowedModels = []string{"z", "a"}
	sorted := cloneChatIdentity(unsorted)
	sorted.Policy.Provider.Only = []string{"openai", "anthropic"}
	sorted.Policy.Tenant.AllowedModels = []string{"a", "z"}
	first, _ := ChatKey(unsorted)
	second, _ := ChatKey(sorted)
	if first != second {
		t.Fatal("set-like policy order changed semantic key")
	}

	t.Run("embedding identity", func(t *testing.T) {
		dimensions := 768
		identity := EmbeddingIdentity{
			TenantID: "tenant-1", CatalogGeneration: "catalog-1",
			Request: inference.EmbeddingRequest{
				Model:          "openai/text-embedding-3-small",
				Input:          inference.EmbeddingInput{Texts: []string{"first", "second"}},
				EncodingFormat: "float", Dimensions: &dimensions, User: "user-1",
			},
			Policy: base.Policy,
		}
		key, err := EmbeddingKey(identity)
		if err != nil {
			t.Fatal(err)
		}
		variants := []EmbeddingIdentity{
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.TenantID = "tenant-2" }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.CatalogGeneration = "catalog-2" }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Request.Model = "openai/text-embedding-3-large" }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Request.Input.Texts = []string{"second", "first"} }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Request.EncodingFormat = "base64" }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Request.Dimensions = intPointer(1536) }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Request.User = "user-2" }),
			cloneEmbeddingIdentity(identity, func(value *EmbeddingIdentity) { value.Policy.Provider.Order = []string{"anthropic", "openai"} }),
		}
		for index, variant := range variants {
			variantKey, err := EmbeddingKey(variant)
			if err != nil {
				t.Fatalf("variant %d: %v", index, err)
			}
			if variantKey == key {
				t.Fatalf("embedding variant %d did not change semantic key", index)
			}
		}
	})

	t.Run("canonical stream reconstruction", func(t *testing.T) {
		response := inference.ChatResponse{
			ID: "chat-1", Model: "openai/gpt-4.1", ModelUsed: "openai/gpt-4.1-2026",
			Choices: []inference.Choice{{
				Message:      inference.Message{Role: inference.RoleAssistant, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "answer"}}},
				FinishReason: "stop",
			}},
			Usage: inference.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		}
		response = response.Clone()
		events, err := StreamEvents(response, inference.StreamOptions{IncludeUsage: true})
		if err != nil {
			t.Fatal(err)
		}
		reconstructed, err := CompleteStream(events)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(response, reconstructed) {
			t.Fatalf("stream reconstruction changed canonical result: %#v", reconstructed)
		}
	})
}

func TestCacheEligibilityRejectsUnsafeShapes(t *testing.T) {
	base := ChatIdentity{
		TenantID:          "tenant",
		CatalogGeneration: "generation",
		Request: inference.ChatRequest{Messages: []inference.Message{{
			Role: inference.RoleUser, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
		}}},
	}
	tests := []struct {
		name   string
		mutate func(*ChatIdentity)
		cause  error
	}{
		{"missing tenant", func(value *ChatIdentity) { value.TenantID = "" }, ErrTenantRequired},
		{"missing generation", func(value *ChatIdentity) { value.CatalogGeneration = "" }, ErrGenerationRequired},
		{"mutable image", func(value *ChatIdentity) {
			value.Request.Messages[0].Content = []inference.ContentPart{{Kind: inference.ContentImage, Image: &inference.Image{URL: "https://example.invalid/image.png"}}}
		}, ErrMutableImage},
		{"provider extension", func(value *ChatIdentity) {
			value.Request.Extensions = map[string]json.RawMessage{"unknown": json.RawMessage(`true`)}
		}, ErrUnknownExtension},
		{"invalid tool schema", func(value *ChatIdentity) {
			value.Request.Tools = []inference.Tool{{Name: "bad", Parameters: json.RawMessage(`{`)}}
		}, ErrInvalidJSONContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneChatIdentity(base)
			test.mutate(&value)
			_, err := ChatKey(value)
			if !errors.Is(err, ErrIneligible) || !errors.Is(err, test.cause) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCanonicalRecordAndStreamReconstruction(t *testing.T) {
	response := inference.ChatResponse{
		ID: "chat-1", CreatedUnix: 42, Model: "openai/gpt-4.1", ModelUsed: "openai/gpt-4.1-2026",
		SystemFingerprint: "fp-1",
		Choices: []inference.Choice{{
			Index: 0,
			Message: inference.Message{
				Role:      inference.RoleAssistant,
				Content:   []inference.ContentPart{{Kind: inference.ContentText, Text: "answer"}},
				Reasoning: "summary",
				ToolCalls: []inference.ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"id":"1"}`}},
			},
			FinishReason: "tool_calls",
			LogProbs:     []inference.LogProb{{Token: "answer", Value: -0.1}},
		}},
		Usage: inference.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6, ReasoningTokens: 1},
	}
	response = response.Clone()
	events, err := StreamEvents(response, inference.StreamOptions{IncludeUsage: true})
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := CompleteStream(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, reconstructed) {
		t.Fatalf("stream round trip changed response\nwant: %#v\ngot:  %#v", response, reconstructed)
	}

	store := newMemoryStore()
	clock := fixedClock{now: time.Unix(100, 0).UTC()}
	repository, err := Open(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "responsecache:v1:chat:test"
	if err := repository.PutChat(ctx, key, response); err != nil {
		t.Fatal(err)
	}
	got, cachedAt, found, err := repository.GetChat(ctx, key)
	if err != nil || !found {
		t.Fatalf("get chat: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(response, got) || !cachedAt.Equal(clock.now) {
		t.Fatal("versioned record changed canonical response")
	}
	var envelope map[string]any
	if err := json.Unmarshal(store.values[key], &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["schema_version"] != float64(RecordSchemaVersion) || envelope["semantic_key"] != key {
		t.Fatalf("record envelope = %#v", envelope)
	}
}

func TestRepositoryRejectsInvalidRecords(t *testing.T) {
	key := "responsecache:v1:chat:test"
	now := time.Unix(100, 0).UTC()
	chat := inference.ChatResponse{ID: "chat-1"}
	embedding := inference.EmbeddingResponse{Model: "embedding-1"}
	tests := []struct {
		name   string
		stored any
		raw    []byte
		cause  error
	}{
		{name: "invalid JSON", raw: []byte(`{`), cause: ErrCorruptRecord},
		{name: "stale schema", stored: record{SchemaVersion: RecordSchemaVersion + 1, Kind: "chat", SemanticKey: key, CachedAt: now, Chat: &chat}, cause: ErrCorruptRecord},
		{name: "wrong key", stored: record{SchemaVersion: RecordSchemaVersion, Kind: "chat", SemanticKey: key + "-other", CachedAt: now, Chat: &chat}, cause: ErrCorruptRecord},
		{name: "missing timestamp", stored: record{SchemaVersion: RecordSchemaVersion, Kind: "chat", SemanticKey: key, Chat: &chat}, cause: ErrCorruptRecord},
		{name: "wrong kind", stored: record{SchemaVersion: RecordSchemaVersion, Kind: "embedding", SemanticKey: key, CachedAt: now, Embedding: &embedding}, cause: ErrKindMismatch},
		{name: "ambiguous payload", stored: record{SchemaVersion: RecordSchemaVersion, Kind: "chat", SemanticKey: key, CachedAt: now, Chat: &chat, Embedding: &embedding}, cause: ErrKindMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			data := test.raw
			if test.stored != nil {
				var err error
				data, err = json.Marshal(test.stored)
				if err != nil {
					t.Fatal(err)
				}
			}
			store.values[key] = data
			repository, err := Open(store, fixedClock{now: now})
			if err != nil {
				t.Fatal(err)
			}
			_, _, found, err := repository.GetChat(context.Background(), key)
			if found || !errors.Is(err, test.cause) {
				t.Fatalf("found = %v, error = %v", found, err)
			}
		})
	}
}

func FuzzSemanticKey(f *testing.F) {
	f.Add("tenant", "generation", "hello")
	f.Fuzz(func(t *testing.T, tenant, generation, text string) {
		if tenant == "" || generation == "" {
			return
		}
		identity := ChatIdentity{
			TenantID: tenant, CatalogGeneration: generation,
			Request: inference.ChatRequest{Messages: []inference.Message{{Role: inference.RoleUser, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: text}}}}},
		}
		first, err := ChatKey(identity)
		if err != nil {
			return
		}
		second, err := ChatKey(cloneChatIdentity(identity))
		if err != nil || first != second {
			t.Fatalf("semantic key is not deterministic: %q %q %v", first, second, err)
		}
	})
}

type memoryStore struct{ values map[string][]byte }

func newMemoryStore() *memoryStore { return &memoryStore{values: make(map[string][]byte)} }

func (s *memoryStore) GetResponse(_ context.Context, key string) ([]byte, bool, error) {
	value, found := s.values[key]
	return append([]byte(nil), value...), found, nil
}

func (s *memoryStore) SetResponse(_ context.Context, key string, value []byte) error {
	s.values[key] = append([]byte(nil), value...)
	return nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func cloneChatIdentity(value ChatIdentity) ChatIdentity {
	value.Request = value.Request.Clone()
	value.Policy = normalizePolicy(value.Policy)
	return value
}

func cloneEmbeddingIdentity(value EmbeddingIdentity, mutate func(*EmbeddingIdentity)) EmbeddingIdentity {
	value.Request = value.Request.Clone()
	value.Policy = normalizePolicy(value.Policy)
	mutate(&value)
	return value
}

func float32Pointer(value float32) *float32 { return &value }
func intPointer(value int) *int             { return &value }
