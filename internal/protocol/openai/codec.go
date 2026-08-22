// Package openai adapts the OpenAI HTTP protocol to canonical inference values.
package openai

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

// ChatRequest is the OpenAI chat-completions wire request.
type ChatRequest struct {
	Model               string            `json:"model"`
	Messages            []Message         `json:"messages"`
	Temperature         *float32          `json:"temperature,omitempty"`
	TopP                *float32          `json:"top_p,omitempty"`
	N                   *int              `json:"n,omitempty"`
	Stream              bool              `json:"stream,omitempty"`
	StreamOptions       *StreamOptions    `json:"stream_options,omitempty"`
	Stop                json.RawMessage   `json:"stop,omitempty"`
	MaxTokens           *int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	PresencePenalty     *float32          `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float32          `json:"frequency_penalty,omitempty"`
	LogitBias           map[string]int    `json:"logit_bias,omitempty"`
	LogProbs            *bool             `json:"logprobs,omitempty"`
	TopLogProbs         *int              `json:"top_logprobs,omitempty"`
	User                string            `json:"user,omitempty"`
	Seed                *int              `json:"seed,omitempty"`
	Tools               []Tool            `json:"tools,omitempty"`
	ToolChoice          json.RawMessage   `json:"tool_choice,omitempty"`
	ResponseFormat      *ResponseFormat   `json:"response_format,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls,omitempty"`
	Store               *bool             `json:"store,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	ServiceTier         string            `json:"service_tier,omitempty"`
}

// Message is one OpenAI chat message.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Refusal    string          `json:"refusal,omitempty"`
}

// ContentPart is one OpenAI multipart message input.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL is an OpenAI image input.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Tool is an OpenAI function tool.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function defines an OpenAI function tool.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is an OpenAI function call.
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

// StreamOptions controls OpenAI stream usage events.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ResponseFormat is the OpenAI response-format contract.
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

// DecodeChat decodes one strict OpenAI request into canonical inference.
func DecodeChat(reader io.Reader) (inference.ChatRequest, error) {
	var wire ChatRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.ChatRequest{}, err
	}
	if wire.MaxTokens != nil && wire.MaxCompletionTokens != nil {
		return inference.ChatRequest{}, fmt.Errorf("max_tokens and max_completion_tokens cannot both be set")
	}
	messages := make([]inference.Message, len(wire.Messages))
	for index, message := range wire.Messages {
		converted, err := decodeMessage(message, false)
		if err != nil {
			return inference.ChatRequest{}, fmt.Errorf("messages[%d]: %w", index, err)
		}
		messages[index] = converted
	}
	tools, err := decodeTools(wire.Tools)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	toolChoice, err := decodeToolChoice(wire.ToolChoice)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	stop, err := decodeStop(wire.Stop)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	output, err := decodeResponseFormat(wire.ResponseFormat)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	maxTokens := wire.MaxCompletionTokens
	if maxTokens == nil {
		maxTokens = wire.MaxTokens
	}
	extensions := make(map[string]json.RawMessage)
	putExtension(extensions, "logprobs", wire.LogProbs)
	putExtension(extensions, "top_logprobs", wire.TopLogProbs)
	if len(extensions) == 0 {
		extensions = nil
	}
	return inference.ChatRequest{
		Model: wire.Model, Messages: messages,
		Sampling: inference.Sampling{
			Temperature: wire.Temperature, TopP: wire.TopP, CandidateCount: wire.N,
			MaxTokens: maxTokens, Stop: stop, PresencePenalty: wire.PresencePenalty,
			FrequencyPenalty: wire.FrequencyPenalty, LogitBias: wire.LogitBias, Seed: wire.Seed,
		},
		Tools: tools, ToolChoice: toolChoice, ParallelToolCalls: wire.ParallelToolCalls, Output: output,
		Reasoning: inference.Reasoning{Effort: inference.ReasoningEffort(wire.ReasoningEffort)},
		Stream:    wire.Stream, User: wire.User, Extensions: extensions,
		StreamOptions: inference.StreamOptions{IncludeUsage: wire.StreamOptions != nil && wire.StreamOptions.IncludeUsage},
	}, nil
}

// EmbeddingRequest is the OpenAI embeddings wire request.
type EmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	Dimensions     *int            `json:"dimensions,omitempty"`
	User           string          `json:"user,omitempty"`
}

// DecodeEmbedding decodes one strict OpenAI embeddings request.
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

// ChatResponse is the OpenAI chat-completions wire response.
type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	ServiceTier       string   `json:"service_tier,omitempty"`
}

// Choice is one OpenAI chat-completions result.
type Choice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
	LogProbs     *LogProbs       `json:"logprobs,omitempty"`
}

// ResponseMessage is one OpenAI assistant message.
type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Refusal   *string    `json:"refusal,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Usage is OpenAI token accounting.
type Usage struct {
	PromptTokens           int                     `json:"prompt_tokens"`
	CompletionTokens       int                     `json:"completion_tokens"`
	TotalTokens            int                     `json:"total_tokens"`
	CompletionTokenDetails *CompletionTokenDetails `json:"completion_tokens_details,omitempty"`
}

// CompletionTokenDetails contains OpenAI reasoning-token accounting.
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

// EncodeChat converts one canonical chat result to OpenAI wire values.
func EncodeChat(response inference.ChatResponse) ChatResponse {
	choices := make([]Choice, len(response.Choices))
	for index, choice := range response.Choices {
		choices[index] = Choice{
			Index: choice.Index,
			Message: ResponseMessage{
				Role: string(choice.Message.Role), Content: messageText(choice.Message),
				ToolCalls: encodeToolCalls(choice.Message.ToolCalls),
			},
			FinishReason: choice.FinishReason,
			LogProbs:     encodeLogProbs(choice.LogProbs),
		}
	}
	return ChatResponse{
		ID: response.ID, Object: "chat.completion", Created: response.CreatedUnix,
		Model: responseModel(response.Model, response.ModelUsed), Choices: choices,
		Usage: encodeUsage(response.Usage), SystemFingerprint: response.SystemFingerprint,
	}
}

// EmbeddingResponse is the OpenAI embeddings wire response.
type EmbeddingResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// Embedding is one OpenAI embedding vector.
type Embedding struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EncodeEmbedding converts one canonical embedding result to OpenAI wire values.
func EncodeEmbedding(response inference.EmbeddingResponse) EmbeddingResponse {
	data := make([]Embedding, len(response.Data))
	for index, embedding := range response.Data {
		data[index] = Embedding{Object: "embedding", Index: embedding.Index, Embedding: embedding.Vector}
	}
	return EmbeddingResponse{Object: "list", Data: data, Model: response.Model, Usage: encodeUsage(response.Usage)}
}

// StreamChunk is one OpenAI SSE data value.
type StreamChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []StreamChoice `json:"choices"`
	Usage             *Usage         `json:"usage,omitempty"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

// StreamChoice is one streamed OpenAI choice.
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
	LogProbs     *LogProbs    `json:"logprobs,omitempty"`
}

// MessageDelta is one streamed OpenAI message update.
type MessageDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// EncodeStream converts one canonical stream event to an OpenAI chunk.
func EncodeStream(event inference.StreamEvent) StreamChunk {
	chunk := StreamChunk{
		ID: event.ID, Object: "chat.completion.chunk", Created: event.CreatedUnix,
		Model: responseModel(event.Model, event.ModelUsed), Choices: make([]StreamChoice, 0, len(event.Deltas)),
		SystemFingerprint: event.SystemFingerprint,
	}
	if event.Usage != nil {
		usage := encodeUsage(*event.Usage)
		chunk.Usage = &usage
	}
	for _, delta := range event.Deltas {
		choice := StreamChoice{
			Index:    delta.Index,
			Delta:    MessageDelta{Role: string(delta.Role), Content: delta.Text, ToolCalls: encodeToolCalls(delta.ToolCalls)},
			LogProbs: encodeLogProbs(delta.LogProbs),
		}
		if delta.FinishReason != "" {
			choice.FinishReason = &delta.FinishReason
		}
		chunk.Choices = append(chunk.Choices, choice)
	}
	return chunk
}

func putExtension(target map[string]json.RawMessage, name string, value any) {
	if value == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err == nil && string(raw) != "null" {
		target[name] = raw
	}
}

// Model is one OpenAI model resource.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the OpenAI model-list response.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ErrorResponse is the OpenAI error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is one OpenAI API error.
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    string  `json:"code,omitempty"`
}

// WriteError writes one OpenAI error response.
func WriteError(w http.ResponseWriter, status int, errorType, message string, param *string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{Message: message, Type: errorType, Param: param}})
}

// WriteJSON writes one OpenAI JSON value.
func WriteJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	_ = WriteJSON(w, status, value)
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

func decodeMessage(wire Message, allowCacheControl bool) (inference.Message, error) {
	content, err := decodeContent(wire.Content, allowCacheControl)
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
		Role: inference.Role(wire.Role), Content: content, Name: wire.Name,
		ToolCalls: toolCalls, ToolCallID: wire.ToolCallID,
	}, nil
}

func decodeContent(raw json.RawMessage, allowCacheControl bool) ([]inference.ContentPart, error) {
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
	}
	_ = allowCacheControl
	return result, nil
}

func decodeTools(wire []Tool) ([]inference.Tool, error) {
	tools := make([]inference.Tool, len(wire))
	for index, tool := range wire {
		if tool.Type != functionToolType {
			return nil, fmt.Errorf("tools[%d].type must be function", index)
		}
		if tool.Function.Name == "" {
			return nil, fmt.Errorf("tools[%d].function.name is required", index)
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

func responseModel(model, modelUsed string) string {
	if model != "" {
		return model
	}
	return modelUsed
}
