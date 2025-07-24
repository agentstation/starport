package providers

import "time"

// Message represents a chat message
type Message struct {
	// Standard fields
	Role    string `json:"role"`              // "system", "user", "assistant", "tool"
	Content string `json:"content"`           // Message content
	Name    string `json:"name,omitempty"`    // Optional name for the message

	// Tool/Function calling
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`   // Tool calls made by assistant
	ToolCallID  string       `json:"tool_call_id,omitempty"` // ID when responding to tool call

	// Multimodal
	Images []string `json:"images,omitempty"` // Base64 encoded images or URLs
}

// ChatRequest represents a chat completion request with unified fields
type ChatRequest struct {
	// Model selection
	Model  string   `json:"model"`            // Primary model to use
	Models []string `json:"models,omitempty"` // Fallback models (OpenRouter style)

	// Messages
	Messages []Message `json:"messages"`

	// Generation parameters
	Temperature      float32 `json:"temperature,omitempty"`       // 0.0 to 2.0
	MaxTokens        int     `json:"max_tokens,omitempty"`        // Maximum tokens to generate
	TopP             float32 `json:"top_p,omitempty"`             // Nucleus sampling
	FrequencyPenalty float32 `json:"frequency_penalty,omitempty"` // -2.0 to 2.0
	PresencePenalty  float32 `json:"presence_penalty,omitempty"`  // -2.0 to 2.0
	Stop             []string `json:"stop,omitempty"`             // Stop sequences

	// Features
	Stream         bool                   `json:"stream,omitempty"`          // Enable streaming
	Tools          []Tool                 `json:"tools,omitempty"`           // Available tools
	ToolChoice     interface{}            `json:"tool_choice,omitempty"`     // "auto", "none", or specific tool
	ResponseFormat *ResponseFormat        `json:"response_format,omitempty"` // JSON mode
	Seed           *int                   `json:"seed,omitempty"`            // For reproducibility

	// OpenRouter extensions
	ProviderOrder      []string `json:"provider_order,omitempty"`       // Preferred providers
	ProviderIgnore     []string `json:"provider_ignore,omitempty"`      // Providers to skip
	ProviderAllow      []string `json:"provider_allow,omitempty"`       // Allowed providers
	ProviderPreferences map[string]interface{} `json:"provider_preferences,omitempty"`

	// Metadata
	User     string            `json:"user,omitempty"`     // User identifier
	Metadata map[string]string `json:"metadata,omitempty"` // Custom metadata
}

// ChatResponse represents a chat completion response with all fields
type ChatResponse struct {
	// OpenAI standard fields
	ID      string    `json:"id"`
	Object  string    `json:"object"` // "chat.completion"
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Choices []Choice  `json:"choices"`
	Usage   Usage     `json:"usage"`

	// OpenRouter extensions
	ModelUsed    string `json:"model_used,omitempty"`    // Actual model used
	ProviderUsed string `json:"provider_used,omitempty"` // Actual provider used

	// Starport additions
	CacheHit       bool              `json:"cache_hit,omitempty"`       // Response from cache
	ProcessingTime time.Duration     `json:"processing_time,omitempty"` // Request duration
	RoutingPath    []string          `json:"routing_path,omitempty"`    // Providers tried
	Metadata       map[string]string `json:"metadata,omitempty"`        // Response metadata
}

// Choice represents a completion choice
type Choice struct {
	Index        int         `json:"index"`
	Message      Message     `json:"message"`
	FinishReason string      `json:"finish_reason"` // "stop", "length", "tool_calls", etc.
	LogProbs     *LogProbs   `json:"logprobs,omitempty"`
}

// Usage represents token usage statistics
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	// Extended usage (some providers)
	PromptCachedTokens int `json:"prompt_cached_tokens,omitempty"`
	ReasoningTokens    int `json:"reasoning_tokens,omitempty"`
}

// ChatChunk represents a streaming chunk
type ChatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"` // "chat.completion.chunk"
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`

	// Usage may be included in final chunk
	Usage *Usage `json:"usage,omitempty"`
}

// ChunkChoice represents a streaming choice
type ChunkChoice struct {
	Index        int           `json:"index"`
	Delta        MessageDelta  `json:"delta"`
	FinishReason *string       `json:"finish_reason,omitempty"`
	LogProbs     *LogProbs     `json:"logprobs,omitempty"`
}

// MessageDelta represents incremental message content
type MessageDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Tool represents a function tool
type Tool struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef represents a function definition
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

// ToolCall represents a tool call made by the assistant
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ResponseFormat specifies the response format
type ResponseFormat struct {
	Type string `json:"type"` // "text" or "json_object"
}

// LogProbs represents log probability information
type LogProbs struct {
	Content []TokenLogProb `json:"content,omitempty"`
}

// TokenLogProb represents log probability for a token
type TokenLogProb struct {
	Token       string         `json:"token"`
	LogProb     float64        `json:"logprob"`
	Bytes       []int          `json:"bytes,omitempty"`
	TopLogProbs []TopLogProb   `json:"top_logprobs,omitempty"`
}

// TopLogProb represents top log probabilities
type TopLogProb struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

// EmbeddingsRequest represents an embeddings request
type EmbeddingsRequest struct {
	Input          interface{} `json:"input"`           // string or []string
	Model          string      `json:"model"`
	EncodingFormat string      `json:"encoding_format,omitempty"` // "float" or "base64"
	Dimensions     int         `json:"dimensions,omitempty"`      // For models that support it
	User           string      `json:"user,omitempty"`
}

// EmbeddingsResponse represents an embeddings response
type EmbeddingsResponse struct {
	Object string      `json:"object"` // "list"
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// Embedding represents a single embedding
type Embedding struct {
	Object    string    `json:"object"` // "embedding"
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// ImagesRequest represents an image generation request
type ImagesRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	N              int    `json:"n,omitempty"`               // Number of images
	Size           string `json:"size,omitempty"`            // "1024x1024", etc.
	Quality        string `json:"quality,omitempty"`         // "standard" or "hd"
	Style          string `json:"style,omitempty"`           // "vivid" or "natural"
	ResponseFormat string `json:"response_format,omitempty"` // "url" or "b64_json"
	User           string `json:"user,omitempty"`
}

// ImagesResponse represents an image generation response
type ImagesResponse struct {
	Created int64   `json:"created"`
	Data    []Image `json:"data"`
}

// Image represents a generated image
type Image struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// AudioRequest represents an audio transcription/translation request
type AudioRequest struct {
	File           []byte  `json:"file"`
	Model          string  `json:"model"`
	Language       string  `json:"language,omitempty"`
	Prompt         string  `json:"prompt,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"` // "json", "text", "srt", "vtt"
	Temperature    float32 `json:"temperature,omitempty"`
}

// AudioResponse represents an audio transcription/translation response
type AudioResponse struct {
	Text     string    `json:"text"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
}

// Segment represents a transcription segment
type Segment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

// ModerationsRequest represents a moderation request
type ModerationsRequest struct {
	Input interface{} `json:"input"` // string or []string
	Model string      `json:"model,omitempty"`
}

// ModerationsResponse represents a moderation response
type ModerationsResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// ModerationResult represents moderation results for one input
type ModerationResult struct {
	Flagged        bool                       `json:"flagged"`
	Categories     map[string]bool            `json:"categories"`
	CategoryScores map[string]float64         `json:"category_scores"`
}