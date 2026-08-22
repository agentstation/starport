// Package inference owns provider-neutral inference values and stream events.
package inference

import "encoding/json"

// Role identifies a message participant.
type Role string

const (
	// RoleSystem identifies a system instruction message.
	RoleSystem Role = "system"
	// RoleUser identifies a user message.
	RoleUser Role = "user"
	// RoleAssistant identifies a model response message.
	RoleAssistant Role = "assistant"
	// RoleTool identifies a tool result message.
	RoleTool Role = "tool"
)

// ContentKind identifies one message content modality.
type ContentKind string

const (
	// ContentText identifies plain text content.
	ContentText ContentKind = "text"
	// ContentImage identifies image content.
	ContentImage ContentKind = "image"
)

// Image describes an image input.
type Image struct {
	URL    string
	Detail string
}

// ContentPart is one typed part of a message.
type ContentPart struct {
	Kind         ContentKind
	Text         string
	Image        *Image
	CacheControl string
}

// Message is one provider-neutral conversation message.
type Message struct {
	Role       Role
	Content    []ContentPart
	Reasoning  string
	Name       string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Tool describes one callable function and its JSON Schema parameters.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one model-requested function call.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolChoiceMode selects how the model can call tools.
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide whether to call a tool.
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceNone prevents tool calls.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceRequired requires a tool call.
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceNamed requires one named tool.
	ToolChoiceNamed ToolChoiceMode = "named"
)

// ToolChoice is an explicit provider-neutral tool selection.
type ToolChoice struct {
	Mode ToolChoiceMode
	Name string
}

// OutputFormat identifies the required model output shape.
type OutputFormat string

const (
	// OutputText requests unstructured text output.
	OutputText OutputFormat = "text"
	// OutputJSONObject requests one JSON object.
	OutputJSONObject OutputFormat = "json_object"
	// OutputJSONSchema requests output that matches a JSON Schema.
	OutputJSONSchema OutputFormat = "json_schema"
)

// StructuredOutput describes a structured response contract.
type StructuredOutput struct {
	Format      OutputFormat
	Name        string
	Description string
	Schema      json.RawMessage
	Strict      bool
}

// ReasoningEffort identifies the requested reasoning depth.
type ReasoningEffort string

const (
	// ReasoningLow requests the lowest supported reasoning effort.
	ReasoningLow ReasoningEffort = "low"
	// ReasoningMedium requests medium reasoning effort.
	ReasoningMedium ReasoningEffort = "medium"
	// ReasoningHigh requests high reasoning effort.
	ReasoningHigh ReasoningEffort = "high"
)

// Reasoning configures reasoning-token behavior.
type Reasoning struct {
	Effort    ReasoningEffort
	MaxTokens *int
	Exclude   bool
}

// Sampling contains provider-neutral generation controls.
type Sampling struct {
	Temperature      *float32
	TopP             *float32
	CandidateCount   *int
	MaxTokens        *int
	Stop             []string
	PresencePenalty  *float32
	FrequencyPenalty *float32
	LogitBias        map[string]int
	Seed             *int
}

// StreamOptions configures stream event delivery.
type StreamOptions struct {
	IncludeUsage bool
}

// ChatRequest is the canonical chat inference request.
type ChatRequest struct {
	Model          string
	FallbackModels []string
	Messages       []Message
	Sampling       Sampling
	Tools          []Tool
	ToolChoice     ToolChoice
	// ParallelToolCalls permits or forbids several tool calls in one
	// assistant turn. Nil leaves the provider default in place.
	ParallelToolCalls *bool
	Output            StructuredOutput
	Reasoning         Reasoning
	Stream            bool
	StreamOptions     StreamOptions
	User              string
	Extensions        map[string]json.RawMessage
}

// Usage reports normalized token counts. InputTokens includes cache reads
// and cache writes; CacheReadTokens and CacheWriteTokens break out the
// cached portions for pricing.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	TotalTokens      int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int

	// Estimated marks counts the gateway synthesized with a tokenizer
	// because the provider reported no usage. Estimated counts never
	// appear on the wire as provider-reported facts; accounting records
	// carry the flag so operators can tell estimates from measurements.
	Estimated bool
}

// LogProb reports one token probability and its alternatives.
type LogProb struct {
	Token string
	Value float64
	Bytes []int
	Top   []TopLogProb
}

// TopLogProb reports one alternate token probability.
type TopLogProb struct {
	Token string
	Value float64
	Bytes []int
}

// Choice is one completed model choice.
type Choice struct {
	Index        int
	Message      Message
	FinishReason string
	LogProbs     []LogProb
}

// ChatResponse is the canonical completed chat response.
type ChatResponse struct {
	ID                string
	CreatedUnix       int64
	Model             string
	ModelUsed         string
	Choices           []Choice
	Usage             Usage
	SystemFingerprint string
}

// StreamEventKind identifies a normalized stream transition.
type StreamEventKind string

const (
	// StreamStart identifies the initial canonical stream event.
	StreamStart StreamEventKind = "start"
	// StreamDelta identifies a canonical content update.
	StreamDelta StreamEventKind = "delta"
	// StreamUsage identifies a canonical token-usage update.
	StreamUsage StreamEventKind = "usage"
	// StreamEnd identifies the terminal canonical stream event.
	StreamEnd StreamEventKind = "end"
)

// ChoiceDelta contains one streamed choice update.
type ChoiceDelta struct {
	Index        int
	Role         Role
	Text         string
	Reasoning    string
	ToolCalls    []ToolCall
	LogProbs     []LogProb
	FinishReason string
}

// StreamEvent is one typed provider-neutral stream transition.
type StreamEvent struct {
	Kind              StreamEventKind
	ID                string
	CreatedUnix       int64
	Model             string
	ModelUsed         string
	SystemFingerprint string
	Deltas            []ChoiceDelta
	Usage             *Usage
}

// EmbeddingInput is a normalized text or token input batch.
type EmbeddingInput struct {
	Texts    []string
	TokenIDs [][]int
}

// EmbeddingRequest is the canonical embedding request.
type EmbeddingRequest struct {
	Model          string
	Input          EmbeddingInput
	EncodingFormat string
	Dimensions     *int
	User           string
}

// Embedding is one normalized vector.
type Embedding struct {
	Index  int
	Vector []float32
}

// EmbeddingResponse is the canonical embedding response.
type EmbeddingResponse struct {
	Model string
	Data  []Embedding
	Usage Usage
}

// Clone returns an independent embedding response copy.
func (r EmbeddingResponse) Clone() EmbeddingResponse {
	clone := r
	clone.Data = make([]Embedding, len(r.Data))
	for i, embedding := range r.Data {
		clone.Data[i] = embedding
		clone.Data[i].Vector = append([]float32(nil), embedding.Vector...)
	}
	return clone
}

// Clone returns an independent request copy.
func (r ChatRequest) Clone() ChatRequest {
	clone := r
	clone.FallbackModels = append([]string(nil), r.FallbackModels...)
	clone.Messages = cloneMessages(r.Messages)
	clone.Sampling = r.Sampling.clone()
	clone.Tools = cloneTools(r.Tools)
	clone.ParallelToolCalls = clonePointer(r.ParallelToolCalls)
	clone.Output.Schema = append(json.RawMessage(nil), r.Output.Schema...)
	clone.Reasoning.MaxTokens = clonePointer(r.Reasoning.MaxTokens)
	clone.Extensions = cloneExtensions(r.Extensions)
	return clone
}

// Clone returns an independent response copy.
func (r ChatResponse) Clone() ChatResponse {
	clone := r
	clone.Choices = make([]Choice, len(r.Choices))
	for i, choice := range r.Choices {
		clone.Choices[i] = choice
		clone.Choices[i].Message = cloneMessage(choice.Message)
		clone.Choices[i].LogProbs = cloneLogProbs(choice.LogProbs)
	}
	return clone
}

// Clone returns an independent stream event copy.
func (e StreamEvent) Clone() StreamEvent {
	clone := e
	clone.Deltas = make([]ChoiceDelta, len(e.Deltas))
	for i, delta := range e.Deltas {
		clone.Deltas[i] = delta
		clone.Deltas[i].ToolCalls = append([]ToolCall(nil), delta.ToolCalls...)
		clone.Deltas[i].LogProbs = cloneLogProbs(delta.LogProbs)
	}
	clone.Usage = clonePointer(e.Usage)
	return clone
}

// Clone returns an independent embedding request copy.
func (r EmbeddingRequest) Clone() EmbeddingRequest {
	clone := r
	clone.Dimensions = clonePointer(r.Dimensions)
	clone.Input.Texts = append([]string(nil), r.Input.Texts...)
	clone.Input.TokenIDs = make([][]int, len(r.Input.TokenIDs))
	for i, tokens := range r.Input.TokenIDs {
		clone.Input.TokenIDs[i] = append([]int(nil), tokens...)
	}
	return clone
}

func (s Sampling) clone() Sampling {
	clone := s
	clone.Temperature = clonePointer(s.Temperature)
	clone.TopP = clonePointer(s.TopP)
	clone.CandidateCount = clonePointer(s.CandidateCount)
	clone.MaxTokens = clonePointer(s.MaxTokens)
	clone.Stop = append([]string(nil), s.Stop...)
	clone.PresencePenalty = clonePointer(s.PresencePenalty)
	clone.FrequencyPenalty = clonePointer(s.FrequencyPenalty)
	clone.Seed = clonePointer(s.Seed)
	if s.LogitBias != nil {
		clone.LogitBias = make(map[string]int, len(s.LogitBias))
		for token, bias := range s.LogitBias {
			clone.LogitBias[token] = bias
		}
	}
	return clone
}

func cloneMessages(messages []Message) []Message {
	clones := make([]Message, len(messages))
	for i, message := range messages {
		clones[i] = cloneMessage(message)
	}
	return clones
}

func cloneMessage(message Message) Message {
	clone := message
	clone.Content = make([]ContentPart, len(message.Content))
	for i, part := range message.Content {
		clone.Content[i] = part
		if part.Image != nil {
			image := *part.Image
			clone.Content[i].Image = &image
		}
	}
	clone.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	return clone
}

func cloneTools(tools []Tool) []Tool {
	clones := make([]Tool, len(tools))
	for i, tool := range tools {
		clones[i] = tool
		clones[i].Parameters = append(json.RawMessage(nil), tool.Parameters...)
	}
	return clones
}

func cloneLogProbs(logProbs []LogProb) []LogProb {
	clones := make([]LogProb, len(logProbs))
	for i, logProb := range logProbs {
		clones[i] = logProb
		clones[i].Bytes = append([]int(nil), logProb.Bytes...)
		clones[i].Top = make([]TopLogProb, len(logProb.Top))
		copy(clones[i].Top, logProb.Top)
		for j := range logProb.Top {
			clones[i].Top[j].Bytes = append([]int(nil), logProb.Top[j].Bytes...)
		}
	}
	return clones
}

func cloneExtensions(extensions map[string]json.RawMessage) map[string]json.RawMessage {
	if extensions == nil {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(extensions))
	for name, value := range extensions {
		clone[name] = append(json.RawMessage(nil), value...)
	}
	return clone
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
