// Package inference owns provider-neutral inference values and stream events.
package inference

import (
	"encoding/json"
	"errors"
)

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
	// ContentAudio identifies audio content.
	ContentAudio ContentKind = "audio"
	// ContentDocument identifies document content, which Starmap records as
	// the pdf modality.
	ContentDocument ContentKind = "document"
	// ContentVideo identifies video content.
	ContentVideo ContentKind = "video"
)

// ContentKinds lists every modality a canonical message part can carry.
// cloneMessage has one arm for each kind that owns a pointer, and the
// contract test walks this list, so a new kind cannot ship without the
// clone coverage that keeps one retry attempt from rewriting the next.
func ContentKinds() []ContentKind {
	return []ContentKind{
		ContentText,
		ContentImage,
		ContentAudio,
		ContentDocument,
		ContentVideo,
	}
}

// Modality names one payload family. A content kind describes one message
// part, while a modality describes what a request carries and what a model
// accepts, so a route decision compares like with like. Starmap records a
// document as the pdf modality, and the catalog boundary owns that
// translation.
type Modality string

const (
	// ModalityText is written or spoken language as characters.
	ModalityText Modality = "text"
	// ModalityImage is a still picture.
	ModalityImage Modality = "image"
	// ModalityAudio is recorded sound.
	ModalityAudio Modality = "audio"
	// ModalityDocument is a paged document, such as a PDF.
	ModalityDocument Modality = "document"
	// ModalityVideo is moving pictures.
	ModalityVideo Modality = "video"
)

// Image describes an image input.
type Image struct {
	URL    string
	Detail string
}

// AudioOutput asks the model to speak its answer. Voice names the provider's
// voice, and Format names the container the caller wants back, such as "wav"
// or "mp3". A provider that serves audio output requires both, so neither
// carries a gateway default: an unset field stays unset on the wire and the
// provider answers for it.
type AudioOutput struct {
	Voice  string
	Format string
}

// AudioChunk is one streamed piece of a spoken answer. Data holds the audio
// bytes for this chunk, and Transcript holds the text the model spoke in it.
// A provider sends either or both in one chunk, so neither field implies the
// other.
type AudioChunk struct {
	Data       []byte
	Transcript string
}

// Audio describes an audio input. A caller sends either a URL or inline
// Data, and Format names the container, such as "wav" or "mp3".
type Audio struct {
	URL    string
	Data   []byte
	Format string
}

// Document describes a document input, such as a PDF. Filename is separate
// from Format because both protocol families carry the caller's own name
// beside the bytes, and a parser reports page numbers against it.
//
// A caller supplies the bytes one of three ways: inline Data, a URL the
// gateway fetches, or FileID, which names a document this gateway already
// stores for the caller's own account. The three are exclusive. A part that
// named two of them would leave the answer to whichever one a codec happened
// to read first.
type Document struct {
	URL      string
	Data     []byte
	Format   string
	Filename string

	// FileID names a stored document this gateway holds for the requesting
	// account. It is the only document reference the gateway can resolve
	// without leaving the deployment, and the only one whose bytes cannot
	// change under a cached answer: a stored file is written once and
	// deleted, never rewritten, and its identifier is never reused.
	FileID string
}

// ErrDocumentSourceConflict reports a document part that names more than one
// source for the same bytes.
var ErrDocumentSourceConflict = errors.New("a document part names more than one source")

// Validate refuses a document that names more than one source.
//
// A part naming none is not this rule's concern: a codec that decoded an empty
// document part reports its own decode failure, and refusing here would turn
// that into a second, less specific message.
//
// The refusal names the rule rather than the fields that collided, because the
// two protocol families spell these sources differently on the wire. A codec
// wraps this error with the field path of the part it was decoding, which is
// what tells a caller where to look.
func (d Document) Validate() error {
	named := 0
	for _, source := range []bool{d.FileID != "", len(d.Data) > 0, d.URL != ""} {
		if source {
			named++
		}
	}
	if named > 1 {
		return ErrDocumentSourceConflict
	}
	return nil
}

// Video describes a video input. A caller sends either a URL or inline
// Data, and Format names the container, such as "mp4".
type Video struct {
	URL    string
	Data   []byte
	Format string
}

// ContentPart is one typed part of a message. Kind selects which payload
// pointer carries the part, and the others stay nil.
type ContentPart struct {
	Kind         ContentKind
	Text         string
	Image        *Image
	Audio        *Audio
	Document     *Document
	Video        *Video
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
	// OutputModalities names what the caller will accept back. Empty means
	// text, which is what every model served before this field existed.
	// A model that speaks its answer needs the caller to ask for audio, so
	// this is a request field and not a routing hint.
	//
	// The response cache derives its key by serializing this struct, so a
	// field added here changes the key of every request, including one that
	// never sets it. internal/response/cache owns that consequence and
	// answers for it by version, because a canonical type carries no
	// transport tag that could hide the field instead.
	OutputModalities []Modality
	// AudioOutput configures the spoken answer OutputModalities asks for.
	// Nil leaves the provider to choose, and a provider that requires a
	// voice refuses the turn itself rather than take a gateway default.
	AudioOutput   *AudioOutput
	Stream        bool
	StreamOptions StreamOptions
	User          string
	Extensions    map[string]json.RawMessage
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

	// AudioInputTokens and AudioOutputTokens count the audio a provider
	// metered at its own rate. Both are already inside InputTokens and
	// OutputTokens, the way CacheReadTokens is: a cost that adds them again
	// rather than reclassifying them bills the same audio twice.
	AudioInputTokens  int
	AudioOutputTokens int
	// GeneratedImages counts the pictures the answer carries. It is the one
	// output unit no token total can describe, because a provider prices a
	// generated image per image and reports no tokens for it.
	GeneratedImages int

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
	Index     int
	Role      Role
	Text      string
	Reasoning string
	// Audio carries one chunk of a spoken answer. OpenRouter serves audio
	// output through streaming alone, so a caller that never reads this
	// field never receives the answer at all.
	Audio *AudioChunk
	// Media carries generated parts that arrive whole rather than in
	// pieces. An image is the case that forces the field: a provider sends
	// the finished picture in one delta, so there is nothing to accumulate
	// and nothing Text could hold. Audio is the other case and keeps its
	// own field, because a spoken answer does arrive in pieces.
	Media        []ContentPart
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
	clone.OutputModalities = append([]Modality(nil), r.OutputModalities...)
	clone.AudioOutput = clonePointer(r.AudioOutput)
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
		clone.Deltas[i].Media = cloneContent(delta.Media)
		// Audio is the first pointer a delta carries. The struct copy above
		// aliases it, and the chunk owns a byte slice, so a replayed or
		// retried stream would share the bytes it is about to overwrite.
		if delta.Audio != nil {
			audio := *delta.Audio
			audio.Data = append([]byte(nil), delta.Audio.Data...)
			clone.Deltas[i].Audio = &audio
		}
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
	clone.Content = cloneContent(message.Content)
	clone.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	return clone
}

// cloneContent returns a part list that shares no memory with its source. A
// message holds one and so does a stream delta, and neither may hand a second
// reader the bytes the first is about to overwrite. A nil list clones to an
// empty one, which is what cloneMessage did before this helper existed and
// what the response cache key already hashes.
func cloneContent(parts []ContentPart) []ContentPart {
	clones := make([]ContentPart, len(parts))
	for i, part := range parts {
		clones[i] = cloneContentPart(part)
	}
	return clones
}

// cloneContentPart returns a part that shares no memory with its source.
// Every payload pointer needs an arm here: an aliased one would let a retry
// attempt rewrite the media the next attempt is about to send.
func cloneContentPart(part ContentPart) ContentPart {
	clone := part
	if part.Image != nil {
		image := *part.Image
		clone.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = append([]byte(nil), part.Audio.Data...)
		clone.Audio = &audio
	}
	if part.Document != nil {
		document := *part.Document
		document.Data = append([]byte(nil), part.Document.Data...)
		clone.Document = &document
	}
	if part.Video != nil {
		video := *part.Video
		video.Data = append([]byte(nil), part.Video.Data...)
		clone.Video = &video
	}
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
