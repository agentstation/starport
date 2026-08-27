package connectors

import (
	"encoding/json"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// Common role constants
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Provider wire constants keep shared OpenAI-compatible tokens consistent
// across connector implementations.
const (
	contentTypeText           = "text"
	contentTypeImageURL       = "image_url"
	finishReasonStop          = "stop"
	objectChatCompletion      = "chat.completion"
	objectChatCompletionChunk = "chat.completion.chunk"
	objectEmbedding           = "embedding"
	objectList                = "list"
	streamReadFailureReason   = "failed to read stream"
	toolTypeFunction          = "function"
	wireFieldMessages         = "messages"
	wireFieldStream           = "stream"
	wireFieldTemperature      = "temperature"
	wireModelToken            = "model"
	wireTypeToken             = "type"
)

// SSE stream constants
const (
	SSEDone = "[DONE]"
)

// StreamOptions configures streaming response behavior
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model             string          `json:"model"`
	Messages          []Message       `json:"messages"`
	Temperature       *float32        `json:"temperature,omitempty"`
	TopP              *float32        `json:"top_p,omitempty"`
	N                 *int            `json:"n,omitempty"`
	MaxTokens         *int            `json:"max_tokens,omitempty"`
	Stream            bool            `json:"stream,omitempty"`
	Stop              []string        `json:"stop,omitempty"`
	PresencePenalty   *float32        `json:"presence_penalty,omitempty"`
	FrequencyPenalty  *float32        `json:"frequency_penalty,omitempty"`
	LogitBias         map[string]int  `json:"logit_bias,omitempty"`
	User              string          `json:"user,omitempty"`
	Seed              *int            `json:"seed,omitempty"`
	Tools             []Tool          `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ResponseFormat    *ResponseFormat `json:"response_format,omitempty"`

	// OpenRouter-compatible model routing
	Models []string `json:"models,omitempty"` // Fallback model chain

	// Streaming options
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// OpenRouter reasoning configuration
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`

	// Modalities names what the caller will accept back. Absent means text.
	Modalities []string `json:"modalities,omitempty"`

	// Audio configures the spoken answer that Modalities asks for.
	Audio *AudioConfig `json:"audio,omitempty"`

	// ProviderOptions are provider-specific top-level wire extensions.
	ProviderOptions map[string]any `json:"-"`

	// Endpoint is the exact Starmap offering endpoint selected by the route plan.
	Endpoint InferenceEndpoint `json:"-"`

	// Credential is the request-selected inference material. Connector instances
	// never retain it.
	Credential credentials.Material `json:"-"`
}

// Message represents a chat message
type Message struct {
	Role       string         `json:"role"`
	Content    MessageContent `json:"content"`
	Reasoning  string         `json:"reasoning,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`

	// Images carries generated pictures. A provider returns them beside the
	// content rather than inside it, so they need their own field here.
	Images []GeneratedImage `json:"images,omitempty"`

	// Audio carries a spoken answer.
	Audio *GeneratedAudio `json:"audio,omitempty"`
}

// AudioConfig selects the voice and container for a spoken answer.
type AudioConfig struct {
	Voice  string `json:"voice,omitempty"`
	Format string `json:"format,omitempty"`
}

// GeneratedImage is one picture a model produced.
type GeneratedImage struct {
	Type     string    `json:"type"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	Index    int       `json:"index,omitempty"`
}

// GeneratedAudio is a spoken answer. Data is raw base64 with no data URL
// prefix, matching the audio input shape on the same wire.
type GeneratedAudio struct {
	Data       string `json:"data,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Format     string `json:"format,omitempty"`
}

// MessageContent can be a string or array of content parts for multimodal
type MessageContent any

// ContentPart represents a part of multimodal content
type ContentPart struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ImageURL     *ImageURL     `json:"image_url,omitempty"`
	InputAudio   *InputAudio   `json:"input_audio,omitempty"`
	File         *File         `json:"file,omitempty"`
	VideoURL     *VideoURL     `json:"video_url,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl represents cache control configuration for content parts
type CacheControl struct {
	Type string `json:"type"` // Currently only "ephemeral" is supported
}

// ImageURL represents an image in a message
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// InputAudio represents audio in a message. Data is raw base64 with no data
// URL prefix, which is why Format names the container beside it.
type InputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format,omitempty"`
}

// File represents a document in a message. FileData holds a data URL or a
// remote reference, and Filename is the caller's own name for it.
type File struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

// VideoURL represents a video in a message.
type VideoURL struct {
	URL string `json:"url"`
}

// Tool represents a function tool
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function represents a callable function
type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ResponseFormat specifies the format of the response
type ResponseFormat struct {
	Type       string              `json:"type"`
	JSONSchema *ResponseJSONSchema `json:"json_schema,omitempty"`
}

// ResponseJSONSchema describes an OpenAI-compatible structured output schema.
type ResponseJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      bool            `json:"strict,omitempty"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int       `json:"index"`
	Message      Message   `json:"message"`
	FinishReason string    `json:"finish_reason,omitempty"`
	LogProbs     *LogProbs `json:"logprobs,omitempty"`
}

// LogProbs represents log probabilities
type LogProbs struct {
	Content []LogProbItem `json:"content,omitempty"`
}

// LogProbItem represents a single log probability item
type LogProbItem struct {
	Token       string       `json:"token"`
	LogProb     float64      `json:"logprob"`
	Bytes       []int        `json:"bytes,omitempty"`
	TopLogProbs []TopLogProb `json:"top_logprobs,omitempty"`
}

// TopLogProb represents a top log probability
type TopLogProb struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

// Usage represents token usage information. PromptTokens uses OpenAI
// semantics: it includes cached prompt tokens. CacheWriteTokens has no
// OpenAI wire field; connectors whose providers report cache writes set it
// for internal accounting.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	CacheWriteTokens        int                      `json:"-"`
}

// PromptTokensDetails provides detailed prompt token counts
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// CompletionTokensDetails provides detailed token counts
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ChatStreamChunk represents a chunk in a streaming response
type ChatStreamChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []StreamChoice `json:"choices"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
	Usage             *Usage         `json:"usage,omitempty"`
}

// StreamChoice represents a choice in a streaming response
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason string       `json:"finish_reason,omitempty"`
	LogProbs     *LogProbs    `json:"logprobs,omitempty"`
}

// MessageDelta represents a message delta in streaming
type MessageDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Images arrive whole in one delta, because a provider sends a finished
	// picture rather than a growing one.
	Images []GeneratedImage `json:"images,omitempty"`

	// Audio arrives in pieces, each chunk holding its own base64 run.
	Audio *GeneratedAudio `json:"audio,omitempty"`
}

// EmbeddingsRequest represents an embeddings request
type EmbeddingsRequest struct {
	Model          string               `json:"model"`
	Input          any                  `json:"input"`
	EncodingFormat string               `json:"encoding_format,omitempty"`
	Dimensions     *int                 `json:"dimensions,omitempty"`
	User           string               `json:"user,omitempty"`
	Endpoint       InferenceEndpoint    `json:"-"`
	Credential     credentials.Material `json:"-"`
}

// InferenceEndpoint is a selected provider endpoint and wire protocol.
type InferenceEndpoint struct {
	Type catalogs.EndpointType
	URL  string
}

// EmbeddingsResponse represents an embeddings response
type EmbeddingsResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// Embedding represents a single embedding
type Embedding struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// ProviderConfig represents configuration for a specific provider
type ProviderConfig struct {
	// Common settings from internal/config/config.go
	BaseURL        string        `json:"base_url"`
	Timeout        time.Duration `json:"timeout"` // Maximum wait for response headers.
	MaxConnections int           `json:"max_connections"`

	// Enable flag for optional providers (e.g., Ollama)
	Enabled bool `json:"enabled"`
}

// Validate checks if the provider config is valid
func (c *ProviderConfig) Validate() error {
	if c.BaseURL == "" {
		return ErrInvalidConfig
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxConnections <= 0 {
		c.MaxConnections = 100
	}
	return nil
}

// UnmarshalMessageContent handles the polymorphic MessageContent field
func UnmarshalMessageContent(data []byte) (MessageContent, error) {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return str, nil
	}

	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err == nil {
		return parts, nil
	}

	return nil, ErrInvalidMessageContent
}

// ReasoningConfig represents OpenRouter-style reasoning configuration
type ReasoningConfig struct {
	Effort    string `json:"effort,omitempty"`     // "high", "medium", "low"
	MaxTokens *int   `json:"max_tokens,omitempty"` // Alternative to effort
	Exclude   bool   `json:"exclude,omitempty"`    // Exclude reasoning from response
}

// ParseMessageContent converts MessageContent to a slice of ContentPart
func ParseMessageContent(content MessageContent) ([]ContentPart, error) {
	if content == nil {
		return nil, nil
	}

	// Handle string content
	if str, ok := content.(string); ok {
		return []ContentPart{{Type: contentTypeText, Text: str}}, nil
	}

	// Handle already parsed content parts
	if parts, ok := content.([]ContentPart); ok {
		return parts, nil
	}

	// Handle []any from JSON unmarshaling
	if interfaces, ok := content.([]any); ok {
		parts := make([]ContentPart, 0, len(interfaces))
		for _, item := range interfaces {
			// Marshal and unmarshal to convert to ContentPart
			data, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			var part ContentPart
			if err := json.Unmarshal(data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return parts, nil
	}

	return nil, ErrInvalidMessageContent
}

// HasCacheControl checks if any content part has cache control
func HasCacheControl(content MessageContent) bool {
	parts, err := ParseMessageContent(content)
	if err != nil {
		return false
	}

	for _, part := range parts {
		if part.CacheControl != nil {
			return true
		}
	}
	return false
}

// StripCacheControl removes cache control from all content parts
func StripCacheControl(content MessageContent) (MessageContent, error) {
	parts, err := ParseMessageContent(content)
	if err != nil {
		return content, err
	}

	// If it was a string, return as-is
	if _, ok := content.(string); ok {
		return content, nil
	}

	// Remove cache control from parts
	cleanParts := make([]ContentPart, len(parts))
	for i, part := range parts {
		cleanPart := part
		cleanPart.CacheControl = nil
		cleanParts[i] = cleanPart
	}

	return cleanParts, nil
}
