package inference

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCanonicalInferenceContract(t *testing.T) {
	maxTokens := 512
	request := ChatRequest{
		Model:          "openai/gpt-4.1",
		FallbackModels: []string{"anthropic/claude-sonnet-4"},
		Messages: []Message{
			{
				Role: RoleUser,
				Content: []ContentPart{
					{Kind: ContentText, Text: "describe this image"},
					{Kind: ContentImage, Image: &Image{URL: "https://example.invalid/image.png", Detail: "high"}},
				},
			},
		},
		Sampling: Sampling{MaxTokens: &maxTokens, LogitBias: map[string]int{"42": -10}},
		Tools: []Tool{{
			Name:        "lookup",
			Description: "Look up a record.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
		}},
		ToolChoice: ToolChoice{Mode: ToolChoiceNamed, Name: "lookup"},
		Output: StructuredOutput{
			Format: OutputJSONSchema,
			Name:   "record",
			Schema: json.RawMessage(`{"type":"object","required":["id"]}`),
			Strict: true,
		},
		Reasoning: Reasoning{Effort: ReasoningHigh, MaxTokens: &maxTokens},
		Stream:    true,
	}

	cloned := request.Clone()
	request.FallbackModels[0] = "changed"
	request.Messages[0].Content[0].Text = "changed"
	request.Messages[0].Content[1].Image.URL = "changed"
	request.Sampling.LogitBias["42"] = 0
	request.Tools[0].Parameters[0] = '['
	request.Output.Schema[0] = '['

	if got := cloned.FallbackModels[0]; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("fallback model = %q", got)
	}
	if got := cloned.Messages[0].Content[0].Text; got != "describe this image" {
		t.Fatalf("text content = %q", got)
	}
	if got := cloned.Messages[0].Content[1].Image.URL; got != "https://example.invalid/image.png" {
		t.Fatalf("image URL = %q", got)
	}
	if got := cloned.Sampling.LogitBias["42"]; got != -10 {
		t.Fatalf("logit bias = %d", got)
	}
	if !json.Valid(cloned.Tools[0].Parameters) || !json.Valid(cloned.Output.Schema) {
		t.Fatal("structured schema was not copied")
	}

	response := ChatResponse{
		ID:    "chatcmpl-test",
		Model: "openai/gpt-4.1",
		Choices: []Choice{{
			Index: 0,
			Message: Message{
				Role:      RoleAssistant,
				Reasoning: "private reasoning summary",
				ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"id":"1"}`}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, ReasoningTokens: 3},
	}
	responseCopy := response.Clone()
	response.Choices[0].Message.ToolCalls[0].Arguments = "changed"
	if got := responseCopy.Choices[0].Message.ToolCalls[0].Arguments; got != `{"id":"1"}` {
		t.Fatalf("tool arguments = %q", got)
	}

	events := []StreamEvent{
		{
			Kind:   StreamDelta,
			Model:  "openai/gpt-4.1",
			Deltas: []ChoiceDelta{{Index: 0, Text: "hello", Reasoning: "summary", ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup"}}}},
		},
		{Kind: StreamUsage, Usage: &response.Usage},
		{Kind: StreamEnd, Deltas: []ChoiceDelta{{Index: 0, FinishReason: "tool_calls"}}},
	}
	eventCopy := events[0].Clone()
	events[0].Deltas[0].ToolCalls[0].Name = "changed"
	if got := eventCopy.Deltas[0].ToolCalls[0].Name; got != "lookup" {
		t.Fatalf("stream tool name = %q", got)
	}

	embeddings := EmbeddingRequest{
		Model: "openai/text-embedding-3-small",
		Input: EmbeddingInput{Texts: []string{"alpha"}, TokenIDs: [][]int{{1, 2, 3}}},
	}
	embeddingsCopy := embeddings.Clone()
	embeddings.Input.Texts[0] = "changed"
	embeddings.Input.TokenIDs[0][0] = 9
	if embeddingsCopy.Input.Texts[0] != "alpha" || embeddingsCopy.Input.TokenIDs[0][0] != 1 {
		t.Fatal("embedding input was not copied")
	}
	embeddingResponse := EmbeddingResponse{Data: []Embedding{{Index: 0, Vector: []float32{1, 2}}}}
	embeddingResponseCopy := embeddingResponse.Clone()
	embeddingResponse.Data[0].Vector[0] = 9
	if embeddingResponseCopy.Data[0].Vector[0] != 1 {
		t.Fatal("embedding response was not copied")
	}

	assertNoTransportTags(t, ChatRequest{})
	assertNoTransportTags(t, ChatResponse{})
	assertNoTransportTags(t, StreamEvent{})
	assertNoTransportTags(t, EmbeddingRequest{})
}

func assertNoTransportTags(t *testing.T, value any) {
	t.Helper()
	seen := make(map[reflect.Type]bool)
	var inspect func(reflect.Type)
	inspect = func(typeOfValue reflect.Type) {
		for typeOfValue.Kind() == reflect.Pointer || typeOfValue.Kind() == reflect.Slice || typeOfValue.Kind() == reflect.Map {
			typeOfValue = typeOfValue.Elem()
		}
		if typeOfValue.Kind() != reflect.Struct || typeOfValue.PkgPath() != "github.com/agentstation/starport/internal/inference" || seen[typeOfValue] {
			return
		}
		seen[typeOfValue] = true
		for i := 0; i < typeOfValue.NumField(); i++ {
			field := typeOfValue.Field(i)
			if field.Tag != "" {
				t.Fatalf("%s.%s has transport tag %q", typeOfValue.Name(), field.Name, field.Tag)
			}
			inspect(field.Type)
		}
	}
	inspect(reflect.TypeOf(value))
}

func FuzzCanonicalInference(f *testing.F) {
	f.Add("hello", `{"type":"object"}`)
	f.Fuzz(func(t *testing.T, text, schema string) {
		rawSchema := json.RawMessage(schema)
		if !json.Valid(rawSchema) {
			rawSchema = json.RawMessage(`{"type":"object"}`)
		}

		request := ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: []ContentPart{{Kind: ContentText, Text: text}}}},
			Output:   StructuredOutput{Format: OutputJSONSchema, Schema: rawSchema},
		}
		clone := request.Clone()
		if len(request.Output.Schema) > 0 {
			request.Output.Schema[0] ^= 1
		}
		if clone.Messages[0].Content[0].Text != text || !json.Valid(clone.Output.Schema) {
			t.Fatal("clone changed canonical inference data")
		}
	})
}
