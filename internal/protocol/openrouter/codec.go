// Package openrouter adapts the OpenRouter HTTP protocol to canonical inference values.
package openrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

const functionToolType = "function"

// ChatRequest is the OpenRouter chat-completions wire request.
type ChatRequest struct {
	Model               string               `json:"model,omitempty"`
	Models              []string             `json:"models,omitempty"`
	Messages            []Message            `json:"messages"`
	Temperature         *float32             `json:"temperature,omitempty"`
	TopP                *float32             `json:"top_p,omitempty"`
	TopK                *int                 `json:"top_k,omitempty"`
	N                   *int                 `json:"n,omitempty"`
	Stream              bool                 `json:"stream,omitempty"`
	StreamOptions       *StreamOptions       `json:"stream_options,omitempty"`
	Stop                json.RawMessage      `json:"stop,omitempty"`
	MaxTokens           *int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                 `json:"max_completion_tokens,omitempty"`
	PresencePenalty     *float32             `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float32             `json:"frequency_penalty,omitempty"`
	RepetitionPenalty   *float32             `json:"repetition_penalty,omitempty"`
	MinP                *float32             `json:"min_p,omitempty"`
	TopA                *float32             `json:"top_a,omitempty"`
	LogitBias           map[string]int       `json:"logit_bias,omitempty"`
	LogProbs            *bool                `json:"logprobs,omitempty"`
	TopLogProbs         *int                 `json:"top_logprobs,omitempty"`
	User                string               `json:"user,omitempty"`
	Seed                *int                 `json:"seed,omitempty"`
	Tools               []Tool               `json:"tools,omitempty"`
	ToolChoice          json.RawMessage      `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                `json:"parallel_tool_calls,omitempty"`
	ResponseFormat      *ResponseFormat      `json:"response_format,omitempty"`
	Reasoning           *Reasoning           `json:"reasoning,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Provider            *ProviderPreferences `json:"provider,omitempty"`
	Preset              string               `json:"preset,omitempty"`
	Route               string               `json:"route,omitempty"`
	Transforms          []string             `json:"transforms,omitempty"`
	Plugins             []json.RawMessage    `json:"plugins,omitempty"`
	Usage               json.RawMessage      `json:"usage,omitempty"`
	Debug               json.RawMessage      `json:"debug,omitempty"`
}

// DecodedChat contains canonical inference and OpenRouter routing policy.
type DecodedChat struct {
	Inference inference.ChatRequest
	Route     string
	Provider  *ProviderPreferences
	// Preset names a stored preset the request selects by body field.
	Preset string
}

// ProviderPreferences is the OpenRouter provider-routing policy.
type ProviderPreferences struct {
	Order             []string        `json:"order,omitempty"`
	Only              []string        `json:"only,omitempty"`
	Ignore            []string        `json:"ignore,omitempty"`
	AllowFallbacks    *bool           `json:"allow_fallbacks,omitempty"`
	RequireParameters *bool           `json:"require_parameters,omitempty"`
	DataCollection    string          `json:"data_collection,omitempty"`
	ZDR               *bool           `json:"zdr,omitempty"`
	Sort              string          `json:"sort,omitempty"`
	MaxPrice          json.RawMessage `json:"max_price,omitempty"`
}

// Message is one OpenRouter chat message.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Reasoning  string          `json:"reasoning,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ContentPart is one OpenRouter multipart message input.
type ContentPart struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ImageURL     *ImageURL     `json:"image_url,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageURL is one OpenRouter image input.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// CacheControl marks an ephemeral prompt-cache breakpoint.
type CacheControl struct {
	Type string `json:"type"`
}

// Tool is an OpenRouter function tool.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function defines an OpenRouter function tool.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is an OpenRouter function call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall is one requested function invocation.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions controls OpenRouter stream usage events.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ResponseFormat is the OpenRouter response-format contract.
type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema describes one structured output.
type JSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      bool            `json:"strict,omitempty"`
}

// Reasoning configures OpenRouter reasoning behavior.
type Reasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens *int   `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

// DecodeChat decodes one strict OpenRouter request into canonical inference.
func DecodeChat(reader io.Reader) (DecodedChat, error) {
	var wire ChatRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return DecodedChat{}, err
	}
	if wire.MaxTokens != nil && wire.MaxCompletionTokens != nil {
		return DecodedChat{}, fmt.Errorf("max_tokens and max_completion_tokens cannot both be set")
	}
	messages := make([]inference.Message, len(wire.Messages))
	for index, message := range wire.Messages {
		converted, err := decodeMessage(message)
		if err != nil {
			return DecodedChat{}, fmt.Errorf("messages[%d]: %w", index, err)
		}
		messages[index] = converted
	}
	tools, err := decodeTools(wire.Tools)
	if err != nil {
		return DecodedChat{}, err
	}
	toolChoice, err := decodeToolChoice(wire.ToolChoice)
	if err != nil {
		return DecodedChat{}, err
	}
	stop, err := decodeStop(wire.Stop)
	if err != nil {
		return DecodedChat{}, err
	}
	output, err := decodeResponseFormat(wire.ResponseFormat)
	if err != nil {
		return DecodedChat{}, err
	}
	maxTokens := wire.MaxCompletionTokens
	if maxTokens == nil {
		maxTokens = wire.MaxTokens
	}
	reasoning := inference.Reasoning{Effort: inference.ReasoningEffort(wire.ReasoningEffort)}
	if wire.Reasoning != nil {
		if wire.Reasoning.Effort != "" {
			reasoning.Effort = inference.ReasoningEffort(wire.Reasoning.Effort)
		}
		reasoning.MaxTokens = wire.Reasoning.MaxTokens
		reasoning.Exclude = wire.Reasoning.Exclude
	}
	extensions := make(map[string]json.RawMessage)
	putExtension(extensions, "top_k", wire.TopK)
	putExtension(extensions, "repetition_penalty", wire.RepetitionPenalty)
	putExtension(extensions, "min_p", wire.MinP)
	putExtension(extensions, "top_a", wire.TopA)
	putExtension(extensions, "logprobs", wire.LogProbs)
	putExtension(extensions, "top_logprobs", wire.TopLogProbs)
	putExtension(extensions, "transforms", wire.Transforms)
	putExtension(extensions, "plugins", wire.Plugins)
	if len(extensions) == 0 {
		extensions = nil
	}
	return DecodedChat{
		Inference: inference.ChatRequest{
			Model: wire.Model, FallbackModels: append([]string(nil), wire.Models...), Messages: messages,
			Sampling: inference.Sampling{
				Temperature: wire.Temperature, TopP: wire.TopP, CandidateCount: wire.N,
				MaxTokens: maxTokens, Stop: stop, PresencePenalty: wire.PresencePenalty,
				FrequencyPenalty: wire.FrequencyPenalty, LogitBias: wire.LogitBias, Seed: wire.Seed,
			},
			Tools: tools, ToolChoice: toolChoice, Output: output, Reasoning: reasoning,
			Stream: wire.Stream, User: wire.User, Extensions: extensions,
			StreamOptions: inference.StreamOptions{IncludeUsage: wire.StreamOptions != nil && wire.StreamOptions.IncludeUsage},
		},
		Route: wire.Route, Provider: wire.Provider, Preset: wire.Preset,
	}, nil
}

// EmbeddingRequest is the OpenRouter embeddings wire request.
type EmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	Dimensions     *int            `json:"dimensions,omitempty"`
	User           string          `json:"user,omitempty"`
}

// DecodeEmbedding decodes one strict OpenRouter embeddings request.
func DecodeEmbedding(reader io.Reader) (inference.EmbeddingRequest, error) {
	var wire EmbeddingRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.EmbeddingRequest{}, err
	}
	input, err := decodeEmbeddingInput(wire.Input)
	if err != nil {
		return inference.EmbeddingRequest{}, err
	}
	return inference.EmbeddingRequest{
		Model: wire.Model, Input: input, EncodingFormat: wire.EncodingFormat,
		Dimensions: wire.Dimensions, User: wire.User,
	}, nil
}

// ChatResponse is the OpenRouter chat-completions wire response.
type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Provider          string   `json:"provider,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint *string  `json:"system_fingerprint"`
}

// Choice is one OpenRouter chat-completions result.
type Choice struct {
	Index              int             `json:"index"`
	Message            ResponseMessage `json:"message"`
	FinishReason       string          `json:"finish_reason"`
	NativeFinishReason string          `json:"native_finish_reason,omitempty"`
	LogProbs           *LogProbs       `json:"logprobs,omitempty"`
}

// ResponseMessage is one OpenRouter assistant message.
type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Usage is OpenRouter token accounting.
type Usage struct {
	PromptTokens           int                     `json:"prompt_tokens"`
	CompletionTokens       int                     `json:"completion_tokens"`
	TotalTokens            int                     `json:"total_tokens"`
	CompletionTokenDetails *CompletionTokenDetails `json:"completion_tokens_details,omitempty"`
}

// CompletionTokenDetails contains reasoning-token accounting.
type CompletionTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// LogProbs contains output-token log probabilities.
type LogProbs struct {
	Content []LogProb `json:"content"`
}

// LogProb is one output-token probability.
type LogProb struct {
	Token       string       `json:"token"`
	LogProb     float64      `json:"logprob"`
	Bytes       []int        `json:"bytes,omitempty"`
	TopLogProbs []TopLogProb `json:"top_logprobs"`
}

// TopLogProb is one alternate output token.
type TopLogProb struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

// EncodeChat converts one canonical chat result to OpenRouter wire values.
func EncodeChat(response inference.ChatResponse) ChatResponse {
	choices := make([]Choice, len(response.Choices))
	for index, choice := range response.Choices {
		choices[index] = Choice{
			Index: choice.Index,
			Message: ResponseMessage{
				Role: string(choice.Message.Role), Content: messageText(choice.Message),
				Reasoning: choice.Message.Reasoning, ToolCalls: encodeToolCalls(choice.Message.ToolCalls),
			},
			FinishReason: choice.FinishReason, LogProbs: encodeLogProbs(choice.LogProbs),
		}
	}
	model := responseModel(response.Model, response.ModelUsed)
	return ChatResponse{
		ID: response.ID, Object: "chat.completion", Created: response.CreatedUnix,
		Model: model, Provider: providerFromModel(response.ModelUsed), Choices: choices,
		Usage: encodeUsage(response.Usage), SystemFingerprint: nullableString(response.SystemFingerprint),
	}
}

// EmbeddingResponse is the OpenRouter embeddings wire response.
type EmbeddingResponse struct {
	Object   string      `json:"object"`
	Data     []Embedding `json:"data"`
	Model    string      `json:"model"`
	Provider string      `json:"provider,omitempty"`
	Usage    Usage       `json:"usage"`
}

// Embedding is one OpenRouter embedding vector.
type Embedding struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EncodeEmbedding converts one canonical embedding result to OpenRouter wire values.
func EncodeEmbedding(response inference.EmbeddingResponse) EmbeddingResponse {
	data := make([]Embedding, len(response.Data))
	for index, embedding := range response.Data {
		data[index] = Embedding{Object: "embedding", Index: embedding.Index, Embedding: embedding.Vector}
	}
	return EmbeddingResponse{
		Object: "list", Data: data, Model: response.Model,
		Provider: providerFromModel(response.Model), Usage: encodeUsage(response.Usage),
	}
}

// StreamChunk is one OpenRouter SSE data value.
type StreamChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Provider          string         `json:"provider,omitempty"`
	Choices           []StreamChoice `json:"choices"`
	Usage             *Usage         `json:"usage,omitempty"`
	SystemFingerprint *string        `json:"system_fingerprint"`
}

// StreamChoice is one streamed OpenRouter choice.
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
	LogProbs     *LogProbs    `json:"logprobs,omitempty"`
}

// MessageDelta is one streamed OpenRouter message update.
type MessageDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// EncodeStream converts one canonical stream event to an OpenRouter chunk.
func EncodeStream(event inference.StreamEvent) StreamChunk {
	model := responseModel(event.Model, event.ModelUsed)
	chunk := StreamChunk{
		ID: event.ID, Object: "chat.completion.chunk", Created: event.CreatedUnix,
		Model: model, Provider: providerFromModel(event.ModelUsed), Choices: make([]StreamChoice, 0, len(event.Deltas)),
		SystemFingerprint: nullableString(event.SystemFingerprint),
	}
	if event.Usage != nil {
		usage := encodeUsage(*event.Usage)
		chunk.Usage = &usage
	}
	for _, delta := range event.Deltas {
		choice := StreamChoice{
			Index: delta.Index,
			Delta: MessageDelta{
				Role: string(delta.Role), Content: delta.Text, Reasoning: delta.Reasoning,
				ToolCalls: encodeToolCalls(delta.ToolCalls),
			},
			LogProbs: encodeLogProbs(delta.LogProbs),
		}
		if delta.FinishReason != "" {
			choice.FinishReason = &delta.FinishReason
		}
		chunk.Choices = append(chunk.Choices, choice)
	}
	return chunk
}

// StreamErrorChunk is the OpenRouter mid-stream error contract.
type StreamErrorChunk struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object"`
	Created int64          `json:"created,omitempty"`
	Model   string         `json:"model,omitempty"`
	Error   ErrorDetail    `json:"error"`
	Choices []StreamChoice `json:"choices"`
}

// EncodeStreamError creates an OpenRouter mid-stream error chunk.
func EncodeStreamError(event inference.StreamEvent, code int, message string, metadata map[string]any) StreamErrorChunk {
	finishReason := "error"
	return StreamErrorChunk{
		ID: event.ID, Object: "chat.completion.chunk", Created: event.CreatedUnix,
		Model:   responseModel(event.Model, event.ModelUsed),
		Error:   ErrorDetail{Code: code, Message: message, Metadata: metadata},
		Choices: []StreamChoice{{Index: 0, Delta: MessageDelta{}, FinishReason: &finishReason}},
	}
}

// Model is one OpenRouter model resource.
type Model struct {
	ID                  string        `json:"id"`
	CanonicalSlug       string        `json:"canonical_slug,omitempty"`
	Name                string        `json:"name"`
	Created             int64         `json:"created"`
	Description         string        `json:"description,omitempty"`
	ContextLength       int           `json:"context_length"`
	Architecture        *Architecture `json:"architecture,omitempty"`
	Pricing             *Pricing      `json:"pricing,omitempty"`
	TopProvider         *TopProvider  `json:"top_provider,omitempty"`
	SupportedParameters []string      `json:"supported_parameters"`
}

// Architecture describes one OpenRouter model architecture.
type Architecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     *string  `json:"instruct_type"`
}

// Pricing contains OpenRouter per-token prices.
type Pricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

// TopProvider contains representative provider limits.
type TopProvider struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

// ModelList is the OpenRouter model-list response.
type ModelList struct {
	Data       []Model    `json:"data"`
	TotalCount int        `json:"total_count"`
	Links      ModelLinks `json:"links"`
}

// ModelLinks contains OpenRouter model-list pagination links.
type ModelLinks struct {
	Next *string `json:"next"`
}

// ErrorResponse is the OpenRouter error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is one OpenRouter API error.
type ErrorDetail struct {
	Code     int            `json:"code"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// WriteError writes one OpenRouter error response.
func WriteError(w http.ResponseWriter, status int, message string, metadata map[string]any) {
	_ = WriteJSON(w, status, ErrorResponse{Error: ErrorDetail{Code: status, Message: message, Metadata: metadata}})
}

// WriteJSON writes one OpenRouter JSON value.
func WriteJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func decodeMessage(wire Message) (inference.Message, error) {
	content, err := decodeContent(wire.Content)
	if err != nil {
		return inference.Message{}, err
	}
	toolCalls := make([]inference.ToolCall, len(wire.ToolCalls))
	for index, call := range wire.ToolCalls {
		if call.Type != "" && call.Type != functionToolType {
			return inference.Message{}, fmt.Errorf("tool_calls[%d].type must be function", index)
		}
		toolCalls[index] = inference.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments}
	}
	return inference.Message{
		Role: inference.Role(wire.Role), Content: content, Reasoning: wire.Reasoning,
		Name: wire.Name, ToolCalls: toolCalls, ToolCallID: wire.ToolCallID,
	}, nil
}

func decodeContent(raw json.RawMessage) ([]inference.ContentPart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		return []inference.ContentPart{{Kind: inference.ContentText, Text: text}}, nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return nil, fmt.Errorf("content must be a string or content-part array: %w", err)
	}
	result := make([]inference.ContentPart, len(parts))
	for index, part := range parts {
		switch part.Type {
		case "text", "input_text":
			result[index] = inference.ContentPart{Kind: inference.ContentText, Text: part.Text}
		case "image_url", "input_image":
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return nil, fmt.Errorf("content[%d].image_url.url is required", index)
			}
			result[index] = inference.ContentPart{Kind: inference.ContentImage, Image: &inference.Image{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}}
		default:
			return nil, fmt.Errorf("content[%d].type %q is not supported", index, part.Type)
		}
		if part.CacheControl != nil {
			result[index].CacheControl = part.CacheControl.Type
		}
	}
	return result, nil
}

func decodeTools(wire []Tool) ([]inference.Tool, error) {
	tools := make([]inference.Tool, len(wire))
	for index, tool := range wire {
		if tool.Type != functionToolType || tool.Function.Name == "" {
			return nil, fmt.Errorf("tools[%d] must name a function", index)
		}
		tools[index] = inference.Tool{
			Name: tool.Function.Name, Description: tool.Function.Description,
			Parameters: append(json.RawMessage(nil), tool.Function.Parameters...),
		}
	}
	return tools, nil
}

func decodeToolChoice(raw json.RawMessage) (inference.ToolChoice, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return inference.ToolChoice{}, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		switch inference.ToolChoiceMode(mode) {
		case inference.ToolChoiceAuto, inference.ToolChoiceNone, inference.ToolChoiceRequired:
			return inference.ToolChoice{Mode: inference.ToolChoiceMode(mode)}, nil
		default:
			return inference.ToolChoice{}, fmt.Errorf("tool_choice %q is not supported", mode)
		}
	}
	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err != nil || named.Type != functionToolType || named.Function.Name == "" {
		return inference.ToolChoice{}, fmt.Errorf("tool_choice must name a function")
	}
	return inference.ToolChoice{Mode: inference.ToolChoiceNamed, Name: named.Function.Name}, nil
}

func decodeStop(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, fmt.Errorf("stop must be a string or string array")
	}
	return multiple, nil
}

func decodeResponseFormat(wire *ResponseFormat) (inference.StructuredOutput, error) {
	if wire == nil || wire.Type == "" || wire.Type == "text" {
		return inference.StructuredOutput{Format: inference.OutputText}, nil
	}
	switch wire.Type {
	case "json_object":
		return inference.StructuredOutput{Format: inference.OutputJSONObject}, nil
	case "json_schema":
		if wire.JSONSchema == nil || wire.JSONSchema.Name == "" || len(wire.JSONSchema.Schema) == 0 {
			return inference.StructuredOutput{}, fmt.Errorf("response_format.json_schema requires name and schema")
		}
		return inference.StructuredOutput{
			Format: inference.OutputJSONSchema, Name: wire.JSONSchema.Name,
			Description: wire.JSONSchema.Description, Schema: wire.JSONSchema.Schema, Strict: wire.JSONSchema.Strict,
		}, nil
	default:
		return inference.StructuredOutput{}, fmt.Errorf("response_format.type %q is not supported", wire.Type)
	}
}

func decodeEmbeddingInput(raw json.RawMessage) (inference.EmbeddingInput, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return inference.EmbeddingInput{}, fmt.Errorf("input is required")
	}
	var singleText string
	if err := json.Unmarshal(trimmed, &singleText); err == nil {
		return inference.EmbeddingInput{Texts: []string{singleText}}, nil
	}
	var texts []string
	if err := json.Unmarshal(trimmed, &texts); err == nil {
		return inference.EmbeddingInput{Texts: texts}, nil
	}
	var tokens []int
	if err := json.Unmarshal(trimmed, &tokens); err == nil {
		return inference.EmbeddingInput{TokenIDs: [][]int{tokens}}, nil
	}
	var tokenBatches [][]int
	if err := json.Unmarshal(trimmed, &tokenBatches); err == nil {
		return inference.EmbeddingInput{TokenIDs: tokenBatches}, nil
	}
	return inference.EmbeddingInput{}, fmt.Errorf("input must be text or token IDs")
}

func putExtension(target map[string]json.RawMessage, name string, value any) {
	if value == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err == nil && string(raw) != "null" && string(raw) != "[]" {
		target[name] = raw
	}
}

func encodeToolCalls(calls []inference.ToolCall) []ToolCall {
	result := make([]ToolCall, len(calls))
	for index, call := range calls {
		result[index] = ToolCall{ID: call.ID, Type: functionToolType, Function: FunctionCall{Name: call.Name, Arguments: call.Arguments}}
	}
	return result
}

func encodeUsage(usage inference.Usage) Usage {
	result := Usage{PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens}
	if usage.ReasoningTokens > 0 {
		result.CompletionTokenDetails = &CompletionTokenDetails{ReasoningTokens: usage.ReasoningTokens}
	}
	return result
}

func encodeLogProbs(logProbs []inference.LogProb) *LogProbs {
	if len(logProbs) == 0 {
		return nil
	}
	result := &LogProbs{Content: make([]LogProb, len(logProbs))}
	for index, item := range logProbs {
		result.Content[index] = LogProb{Token: item.Token, LogProb: item.Value, Bytes: item.Bytes, TopLogProbs: make([]TopLogProb, len(item.Top))}
		for topIndex, top := range item.Top {
			result.Content[index].TopLogProbs[topIndex] = TopLogProb{Token: top.Token, LogProb: top.Value, Bytes: top.Bytes}
		}
	}
	return result
}

func messageText(message inference.Message) string {
	var builder strings.Builder
	for _, part := range message.Content {
		if part.Kind == inference.ContentText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func responseModel(model, modelUsed string) string {
	if model != "" {
		return model
	}
	return modelUsed
}

func providerFromModel(model string) string {
	provider, _, found := strings.Cut(model, "/")
	if !found {
		return ""
	}
	return provider
}
